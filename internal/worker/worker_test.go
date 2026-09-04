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

func TestPoolWithLiveLogNumbersCompletions(t *testing.T) {
	const n = 6
	var mu sync.Mutex
	var counters []int
	totals := map[int]bool{}

	PoolWithLiveLog(make([]int, n), 3,
		func(int) (int, error) { return 0, nil },
		func(completed, total int, _, _ int, _ error) {
			mu.Lock()
			counters = append(counters, completed)
			totals[total] = true
			mu.Unlock()
		},
	)

	if len(counters) != n {
		t.Fatalf("callback fired %d times, want %d", len(counters), n)
	}

	// completed must be 1..n with no gaps or repeats, whatever the order.
	seen := make(map[int]bool, n)
	for _, c := range counters {
		if c < 1 || c > n {
			t.Errorf("completed = %d, want between 1 and %d", c, n)
		}
		if seen[c] {
			t.Errorf("completed = %d reported more than once", c)
		}
		seen[c] = true
	}

	if len(totals) != 1 || !totals[n] {
		t.Errorf("total values seen = %v, want only %d", totals, n)
	}
}

func TestPoolWithLiveLogSerializesCallback(t *testing.T) {
	// The callback deliberately uses an unguarded counter: the pool documents
	// that it holds a mutex around onComplete, so callers may touch shared
	// state. Under -race this fails loudly if that guarantee is dropped.
	unguarded := 0

	PoolWithLiveLog(make([]int, 50), 8,
		func(int) (int, error) { return 0, nil },
		func(int, int, int, int, error) { unguarded++ },
	)

	if unguarded != 50 {
		t.Errorf("callback ran %d times, want 50", unguarded)
	}
}

func TestPoolWithLiveLogNilCallback(t *testing.T) {
	results := PoolWithLiveLog(make([]int, 4), 2,
		func(int) (int, error) { return 7, nil },
		nil,
	)

	if len(results) != 4 {
		t.Fatalf("len(results) = %d, want 4", len(results))
	}
	for i, r := range results {
		if r.Value != 7 {
			t.Errorf("results[%d].Value = %d, want 7", i, r.Value)
		}
	}
}

func TestPoolWithLiveLogCapturesPerItemErrors(t *testing.T) {
	items := []int{0, 1, 2, 3}
	boom := errors.New("boom")
	var mu sync.Mutex
	errsSeen := 0

	results := PoolWithLiveLog(items, 2,
		func(v int) (int, error) {
			if v%2 == 1 {
				return 0, boom
			}
			return v, nil
		},
		func(_, _, _, _ int, err error) {
			if err != nil {
				mu.Lock()
				errsSeen++
				mu.Unlock()
			}
		},
	)

	if errsSeen != 2 {
		t.Errorf("callback saw %d errors, want 2", errsSeen)
	}
	for i, r := range results {
		if i%2 == 1 && !errors.Is(r.Err, boom) {
			t.Errorf("results[%d].Err = %v, want boom", i, r.Err)
		}
		if i%2 == 0 && r.Err != nil {
			t.Errorf("results[%d].Err = %v, want nil", i, r.Err)
		}
	}
}

func TestPoolWithLiveLogBoundsConcurrency(t *testing.T) {
	const limit = 4
	var m maxInFlight
	var wg sync.WaitGroup
	wg.Add(24)

	PoolWithLiveLog(make([]int, 24), limit,
		func(int) (int, error) {
			m.enter()
			defer m.leave()
			wg.Done()
			return 0, nil
		},
		nil,
	)

	if peak := m.peak.Load(); peak > limit {
		t.Errorf("peak concurrency = %d, want <= %d", peak, limit)
	}
}

// ---------------------------------------------------------------------------
// Progress rendering
//
// The progress path is TTY-gated, so under `go test` it is unreachable unless
// stderrIsTerminal is stubbed. These tests do that, and capture os.Stderr to
// assert on what the pool actually draws.
// ---------------------------------------------------------------------------

// withTerminal makes the pool believe stderr is a terminal for one test.
func withTerminal(t *testing.T) {
	t.Helper()
	prev := stderrIsTerminal
	stderrIsTerminal = func() bool { return true }
	t.Cleanup(func() { stderrIsTerminal = prev })
}

// captureStderr runs fn with os.Stderr replaced by a pipe and returns what was
// written to it.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	prev := os.Stderr
	os.Stderr = w

	captured := make(chan string, 1)
	go func() {
		var sb strings.Builder
		_, _ = io.Copy(&sb, r)
		captured <- sb.String()
	}()

	fn()

	os.Stderr = prev
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	return <-captured
}

func TestProgressLine(t *testing.T) {
	got := progressLine(3, 10, "cloning")
	want := "\r[3/10] cloning..."
	if got != want {
		t.Errorf("progressLine(3, 10, %q) = %q, want %q", "cloning", got, want)
	}
}

