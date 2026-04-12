package worker

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/charmbracelet/x/term"
)

// Result holds the outcome of processing a single item.
type Result[T any] struct {
	Index int
	Value T
	Err   error
}

// Pool runs fn for each item in items with the given concurrency.
// Results are returned in the same order as items.
// Errors in individual workers do not stop other workers.
func Pool[T any, R any](items []T, concurrency int, fn func(T) (R, error)) []Result[R] {
	return PoolWithProgress(items, concurrency, PoolOptions{}, fn)
}

// PoolOptions controls optional behaviour of PoolWithProgress.
type PoolOptions struct {
	// Verb is the present-participle word shown in the progress line, e.g.
	// "cloning" or "syncing". An empty string disables progress output.
	Verb string
}

// PoolWithProgress runs fn for each item in items with the given concurrency
// and optionally streams a live progress indicator to stderr.
//
// Progress is only printed when opts.Verb is non-empty and stderr is a TTY.
// The indicator is written in-place using a carriage-return so it does not
// scroll the terminal. After all work is done the line is cleared.
//
// Results are returned in the same order as items.
// Errors in individual workers do not stop other workers.
func PoolWithProgress[T any, R any](items []T, concurrency int, opts PoolOptions, fn func(T) (R, error)) []Result[R] {
	results := make([]Result[R], len(items))
	total := len(items)

	showProgress := opts.Verb != "" && term.IsTerminal(os.Stderr.Fd())

	var done atomic.Int64

	printProgress := func() {
		n := done.Load()
		line := fmt.Sprintf("\r[%d/%d] %s...", n, total, opts.Verb)
		fmt.Fprint(os.Stderr, line)
	}

	if showProgress && total > 0 {
		printProgress()
	}

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, item := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, it T) {
			defer wg.Done()
			defer func() { <-sem }()
			val, err := fn(it)
			results[idx] = Result[R]{Index: idx, Value: val, Err: err}
			if showProgress {
				done.Add(1)
				printProgress()
			}
		}(i, item)
	}

	wg.Wait()

	if showProgress && total > 0 {
		// Clear the progress line.
		width, _, err := term.GetSize(os.Stderr.Fd())
		if err != nil || width <= 0 {
			width = 80
		}
		fmt.Fprint(os.Stderr, "\r"+strings.Repeat(" ", width)+"\r")
	}
	return results
}

// PoolWithLiveLog runs fn for each item with the given concurrency and
// invokes onComplete after each item finishes, in completion order. It
// does NOT print the legacy single-line progress bar — callers are
// expected to emit their own per-item lines via onComplete.
//
// Results are returned in the same order as items (like PoolWithProgress).
// onComplete is called under an internal mutex, so callers can safely
// write to stdout/stderr without interleaving.
func PoolWithLiveLog[T any, R any](
	items []T,
	concurrency int,
	fn func(T) (R, error),
	onComplete func(completed, total int, item T, result R, err error),
) []Result[R] {
	results := make([]Result[R], len(items))
	total := len(items)

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	completed := 0

	for i, item := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, it T) {
			defer wg.Done()
			defer func() { <-sem }()
			val, err := fn(it)
			results[idx] = Result[R]{Index: idx, Value: val, Err: err}
			if onComplete != nil {
				mu.Lock()
				completed++
				n := completed
				onComplete(n, total, it, val, err)
				mu.Unlock()
			}
		}(i, item)
	}

	wg.Wait()
	return results
}
