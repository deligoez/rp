package worker

import "sync"

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
	results := make([]Result[R], len(items))

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
		}(i, item)
	}

	wg.Wait()
	return results
}
