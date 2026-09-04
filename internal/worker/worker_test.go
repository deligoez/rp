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

