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

// PoolOptions controls optional behaviour of PoolWithProgress.
type PoolOptions struct {
	// Verb is the present-participle word shown in the progress line, e.g.
	// "cloning" or "syncing". An empty string disables progress output.
	Verb string
}

// stderrIsTerminal reports whether there is a terminal to draw progress on.
// It is a variable so tests can reach the progress path, which is otherwise
// dead under `go test` because stderr is a pipe there.
var stderrIsTerminal = func() bool { return term.IsTerminal(os.Stderr.Fd()) }

// stderrWidth reports the terminal's column count. Like stderrIsTerminal it is
// a variable so tests can drive both outcomes; a pipe has no width, so the
// error branch is all a test would otherwise ever see.
var stderrWidth = func() (int, error) {
	width, _, err := term.GetSize(os.Stderr.Fd())
	return width, err
}

// progressLine renders the in-place progress indicator. The leading carriage
// return rewinds to the start of the line so the indicator overwrites itself
// instead of scrolling.
func progressLine(done, total int, verb string) string {
	return fmt.Sprintf("\r[%d/%d] %s...", done, total, verb)
}

// clearLine renders the sequence that blanks a progress line of the given
// width. A width that could not be determined falls back to 80 columns.
func clearLine(width int) string {
	if width <= 0 {
		width = 80
	}
	return "\r" + strings.Repeat(" ", width) + "\r"
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

	showProgress := opts.Verb != "" && stderrIsTerminal()

	var done atomic.Int64

	printProgress := func() {
		fmt.Fprint(os.Stderr, progressLine(int(done.Load()), total, opts.Verb))
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
		width, err := stderrWidth()
		if err != nil {
			width = 0 // clearLine falls back to a conventional width
		}
		fmt.Fprint(os.Stderr, clearLine(width))
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
