// Package queue admits work by the number running at once and by the
// memory it is estimated to need, in arrival order, with a bounded wait.
package queue

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrBusy is returned when a request would have to wait and the number of
	// requests waiting is already at [Options.MaxWaitingRequests].
	ErrBusy = errors.New("queue: too many requests waiting")

	// ErrTimeout is returned when a request waits longer than
	// [Options.Timeout].
	ErrTimeout = errors.New("queue: waited too long")

	// ErrTooLarge is returned at once for a request estimated to need more
	// than the whole [Options.MemoryBudgetBytes].
	ErrTooLarge = errors.New("queue: needs more memory than the budget")
)

// Options holds a [Queue]'s settings beyond the number of requests it runs at
// once.
type Options struct {
	// MaxWaitingRequests limits the number of requests waiting; 0 means
	// none wait.
	MaxWaitingRequests int

	// Timeout bounds how long a request waits; 0 means no bound.
	Timeout time.Duration

	// MemoryBudgetBytes bounds the memory the running requests are estimated
	// to need together; 0 means no bound beyond the number running.
	MemoryBudgetBytes int64
}

// Queue admits requests in arrival order. It is safe for concurrent use.
type Queue struct {
	mu                 sync.Mutex
	slots              int
	bytes              int64
	capacity           int64
	maxWaitingRequests int
	timeout            time.Duration
	waiting            list.List
}

type waiter struct {
	n     int64
	ready chan struct{}
}

// New returns a [Queue] that runs up to concurrency requests at once. It
// returns an error when concurrency is not more than zero, or
// [Options.MaxWaitingRequests], [Options.Timeout] or
// [Options.MemoryBudgetBytes] is negative.
func New(concurrency int, opts Options) (*Queue, error) {
	switch {
	case concurrency <= 0:
		return nil, fmt.Errorf("concurrency must be more than zero, got %d", concurrency)
	case opts.MaxWaitingRequests < 0:
		return nil, fmt.Errorf("Options.MaxWaitingRequests must not be negative, got %d", opts.MaxWaitingRequests)
	case opts.Timeout < 0:
		return nil, fmt.Errorf("Options.Timeout must not be negative, got %v", opts.Timeout)
	case opts.MemoryBudgetBytes < 0:
		return nil, fmt.Errorf("Options.MemoryBudgetBytes must not be negative, got %d", opts.MemoryBudgetBytes)
	}

	return &Queue{
		slots:              concurrency,
		bytes:              opts.MemoryBudgetBytes,
		capacity:           opts.MemoryBudgetBytes,
		maxWaitingRequests: opts.MaxWaitingRequests,
		timeout:            opts.Timeout,
	}, nil
}

// Acquire waits until a request estimated to need n bytes may run, and
// returns the func that ends its run. It returns [ErrTooLarge], [ErrBusy] or
// [ErrTimeout], or ctx.Err() when ctx is done first.
func (q *Queue) Acquire(ctx context.Context, n int64) (release func(), err error) {
	if q.capacity == 0 {
		n = 0
	} else if n > q.capacity {
		return nil, ErrTooLarge
	}

	q.mu.Lock()

	if q.waiting.Len() == 0 && q.fits(n) {
		q.take(n)
		q.mu.Unlock()

		return func() { q.release(n) }, nil
	}

	if q.waiting.Len() >= q.maxWaitingRequests {
		q.mu.Unlock()
		return nil, ErrBusy
	}

	request := &waiter{n: n, ready: make(chan struct{})}
	e := q.waiting.PushBack(request)
	q.mu.Unlock()

	var expired <-chan time.Time

	if q.timeout > 0 {
		timer := time.NewTimer(q.timeout)
		defer timer.Stop()

		expired = timer.C
	}

	select {
	case <-request.ready:
		return func() { q.release(n) }, nil
	case <-ctx.Done():
		return nil, q.leave(e, n, ctx.Err())
	case <-expired:
		return nil, q.leave(e, n, ErrTimeout)
	}
}

// leave takes the waiter at e out of the queue, giving back the slot if it
// was granted meanwhile, and returns err.
func (q *Queue) leave(e *list.Element, n int64, err error) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	select {
	case <-e.Value.(*waiter).ready:
		q.give(n)
	default:
		q.waiting.Remove(e)
		q.wake()
	}

	return err
}

func (q *Queue) fits(n int64) bool { return q.slots > 0 && n <= q.bytes }

func (q *Queue) take(n int64) {
	q.slots--
	q.bytes -= n
}

func (q *Queue) give(n int64) {
	q.slots++
	q.bytes += n
	q.wake()
}

func (q *Queue) release(n int64) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.give(n)
}

// wake admits waiters from the front while they fit.
func (q *Queue) wake() {
	for e := q.waiting.Front(); e != nil; e = q.waiting.Front() {
		first := e.Value.(*waiter)
		if !q.fits(first.n) {
			return
		}

		q.take(first.n)
		q.waiting.Remove(e)
		close(first.ready)
	}
}
