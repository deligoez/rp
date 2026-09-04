package worker

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// reverseFinisher builds fn bodies that force a deterministic completion order:
// item i waits for item i+1 to finish first, so the last item completes first
// and the first item completes last. That is the opposite of item order, which
// is what the ordering guarantees need to be tested against.
//
// It requires concurrency >= n, otherwise the wait deadlocks.
func reverseFinisher(n int) func(i int) {
	done := make([]chan struct{}, n)
	for i := range done {
		done[i] = make(chan struct{})
	}
	return func(i int) {
		if i+1 < n {
			<-done[i+1]
		}
		close(done[i])
	}
}

// maxInFlight tracks the peak number of concurrent calls.
type maxInFlight struct {
	current atomic.Int64
	peak    atomic.Int64
}

func (m *maxInFlight) enter() {
	now := m.current.Add(1)
	for {
		peak := m.peak.Load()
		if now <= peak || m.peak.CompareAndSwap(peak, now) {
			return
		}
	}
}

func (m *maxInFlight) leave() { m.current.Add(-1) }

// ---------------------------------------------------------------------------
// PoolWithProgress
// ---------------------------------------------------------------------------

func TestPoolWithProgressPreservesItemOrder(t *testing.T) {
	const n = 5
	items := []int{10, 20, 30, 40, 50}
	finish := reverseFinisher(n)

	results := PoolWithProgress(items, n, PoolOptions{}, func(v int) (int, error) {
		finish(v/10 - 1)
		return v * 2, nil
	})

	if len(results) != n {
		t.Fatalf("len(results) = %d, want %d", len(results), n)
	}
	for i, r := range results {
		if r.Index != i {
			t.Errorf("results[%d].Index = %d, want %d", i, r.Index, i)
		}
		if want := items[i] * 2; r.Value != want {
			t.Errorf("results[%d].Value = %d, want %d (completion order must not reorder results)", i, r.Value, want)
		}
	}
}

func TestPoolWithProgressCapturesPerItemErrors(t *testing.T) {
	items := []int{0, 1, 2, 3}
	boom := errors.New("boom")

	results := PoolWithProgress(items, 2, PoolOptions{}, func(v int) (string, error) {
		if v%2 == 1 {
			return "", boom
		}
		return fmt.Sprintf("ok-%d", v), nil
	})

	for i, r := range results {
		if i%2 == 1 {
			if !errors.Is(r.Err, boom) {
				t.Errorf("results[%d].Err = %v, want boom", i, r.Err)
			}
			continue
		}
		if r.Err != nil {
			t.Errorf("results[%d].Err = %v, want nil", i, r.Err)
		}
		if want := fmt.Sprintf("ok-%d", items[i]); r.Value != want {
			t.Errorf("results[%d].Value = %q, want %q (a failing item must not stop the others)", i, r.Value, want)
		}
	}
}

func TestPoolWithProgressBoundsConcurrency(t *testing.T) {
	const limit = 3
	var m maxInFlight
	var wg sync.WaitGroup
	wg.Add(20)

	// Every call blocks until all 20 have been started, so the pool cannot
	// finish one before the peak is observed.
	results := PoolWithProgress(make([]int, 20), limit, PoolOptions{}, func(int) (int, error) {
		m.enter()
		defer m.leave()
		wg.Done()
		return 0, nil
	})

	if len(results) != 20 {
		t.Fatalf("len(results) = %d, want 20", len(results))
	}
	if peak := m.peak.Load(); peak > limit {
		t.Errorf("peak concurrency = %d, want <= %d", peak, limit)
	}
}

func TestPoolWithProgressCallsEachItemExactlyOnce(t *testing.T) {
	items := []string{"a", "b", "c", "d"}
	var mu sync.Mutex
	seen := map[string]int{}

	PoolWithProgress(items, 2, PoolOptions{Verb: "testing"}, func(s string) (int, error) {
		mu.Lock()
		seen[s]++
		mu.Unlock()
		return 0, nil
	})

	for _, s := range items {
		if seen[s] != 1 {
			t.Errorf("fn called %d times for %q, want exactly 1", seen[s], s)
		}
	}
}

func TestPoolWithProgressEmptyItems(t *testing.T) {
	called := false
	results := PoolWithProgress(nil, 4, PoolOptions{Verb: "testing"}, func(int) (int, error) {
		called = true
		return 0, nil
	})

	if len(results) != 0 {
		t.Errorf("len(results) = %d, want 0", len(results))
	}
	if called {
		t.Error("fn must not be called for an empty item list")
	}
}

// ---------------------------------------------------------------------------
// PoolWithLiveLog
// ---------------------------------------------------------------------------

func TestPoolWithLiveLogPreservesItemOrder(t *testing.T) {
	const n = 5
	items := []int{10, 20, 30, 40, 50}
	finish := reverseFinisher(n)

	var mu sync.Mutex
	var completionOrder []int

	results := PoolWithLiveLog(items, n,
		func(v int) (int, error) {
			finish(v/10 - 1)
			return v * 2, nil
		},
		func(_, _ int, item, _ int, _ error) {
			mu.Lock()
			completionOrder = append(completionOrder, item)
			mu.Unlock()
		},
	)

	for i, r := range results {
		if want := items[i] * 2; r.Value != want || r.Index != i {
			t.Errorf("results[%d] = {Index:%d Value:%d}, want {Index:%d Value:%d}", i, r.Index, r.Value, i, want)
		}
	}

	// The fixture forces the reverse order, so this also proves the callback
	// really does fire in completion order rather than item order.
	want := []int{50, 40, 30, 20, 10}
	for i, got := range completionOrder {
		if got != want[i] {
			t.Errorf("completion order = %v, want %v", completionOrder, want)
			break
		}
	}
}

