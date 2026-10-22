package queue

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// TestNew requires [New] to refuse a concurrency not more than zero and a
// negative [Options.MaxWaitingRequests], [Options.Timeout] or
// [Options.MemoryBudgetBytes], and to accept zero for those three.
func TestNew(t *testing.T) {
	cases := []struct {
		concurrency int
		opts        Options
	}{
		{concurrency: 0, opts: Options{}},
		{concurrency: 1, opts: Options{MaxWaitingRequests: -1}},
		{concurrency: 1, opts: Options{Timeout: -1}},
		{concurrency: 1, opts: Options{MemoryBudgetBytes: -1}},
	}

	for _, tc := range cases {
		if _, err := New(tc.concurrency, tc.opts); err == nil {
			t.Errorf("New(%d, %+v) accepted", tc.concurrency, tc.opts)
		}
	}

	if _, err := New(1, Options{}); err != nil {
		t.Errorf("New with MaxWaitingRequests, Timeout and MemoryBudgetBytes at zero: %v", err)
	}
}

// TestAcquireSlots requires requests to be admitted by the number running,
// whatever their size when there is no memory budget.
func TestAcquireSlots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		queue := newQueue(t, 1, Options{MaxWaitingRequests: 8})

		release, err := queue.Acquire(ctx, 1<<40)
		if err != nil {
			t.Fatal(err)
		}

		admitted := make(chan struct{})

		go func() {
			release, err := queue.Acquire(ctx, 1)
			if err == nil {
				close(admitted)
				release()
			}
		}()

		synctest.Wait()

		select {
		case <-admitted:
			t.Fatal("second request admitted while the only slot was taken")
		default:
		}

		release()
		synctest.Wait()

		select {
		case <-admitted:
		default:
			t.Fatal("second request not admitted after release")
		}

		if queue.slots != 1 {
			t.Errorf("%d slots after both released, want 1", queue.slots)
		}
	})
}

// TestAcquireBytes requires requests to be admitted by memory in arrival
// order, so a small one that would fit does not pass a large one waiting.
func TestAcquireBytes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		queue := newQueue(t, 10, Options{MaxWaitingRequests: 8, MemoryBudgetBytes: 100})

		releaseFirst, err := queue.Acquire(ctx, 55)
		if err != nil {
			t.Fatal(err)
		}

		releaseSecond, err := queue.Acquire(ctx, 5)
		if err != nil {
			t.Fatal(err)
		}

		admitted := make(chan int64, 2)

		for _, n := range []int64{50, 30} {
			go func() {
				if _, err := queue.Acquire(ctx, n); err == nil {
					admitted <- n
				}
			}()

			synctest.Wait()
		}

		if len(admitted) != 0 {
			t.Errorf("%d admitted while 60 of 100 were taken and 50 waited first", len(admitted))
		}

		releaseSecond()
		synctest.Wait()

		if len(admitted) != 0 {
			t.Errorf("%d admitted while 55 of 100 were taken and 50 waited first", len(admitted))
		}

		releaseFirst()
		synctest.Wait()

		if len(admitted) != 2 {
			t.Fatalf("%d admitted after every release, want 50 and 30", len(admitted))
		}

		if got := <-admitted + <-admitted; got != 80 {
			t.Errorf("admitted %d bytes after release, want 50 and 30", got)
		}
	})
}

// TestAcquireTooLarge requires a request larger than the whole budget to
// be refused at once rather than wait.
func TestAcquireTooLarge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		queue := newQueue(t, 1, Options{MaxWaitingRequests: 8, MemoryBudgetBytes: 100})
		if _, err := queue.Acquire(ctx, 101); !errors.Is(err, ErrTooLarge) {
			t.Errorf("got %v, want ErrTooLarge", err)
		}
	})
}

// TestAcquireCancel requires a waiter whose context ends to leave the
// queue and give back nothing it did not take.
func TestAcquireCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		queue := newQueue(t, 1, Options{MaxWaitingRequests: 8, MemoryBudgetBytes: 100})

		release, err := queue.Acquire(ctx, 100)
		if err != nil {
			t.Fatal(err)
		}

		waiterCtx, cancelWaiter := context.WithCancel(ctx)
		errc := make(chan error, 1)

		go func() {
			_, err := queue.Acquire(waiterCtx, 10)
			errc <- err
		}()

		synctest.Wait()
		cancelWaiter()

		if err := <-errc; !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}

		release()

		if queue.slots != 1 || queue.bytes != 100 || queue.waiting.Len() != 0 {
			t.Errorf(
				"after cancel and release: %d slots, %d bytes, %d waiting; want 1, 100, 0",
				queue.slots,
				queue.bytes,
				queue.waiting.Len(),
			)
		}
	})
}

// TestAcquireBusy requires a full queue to refuse at once.
func TestAcquireBusy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		queue := newQueue(t, 1, Options{MaxWaitingRequests: 1})

		release, err := queue.Acquire(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer release()

		go func() { _, _ = queue.Acquire(ctx, 0) }()
		synctest.Wait()

		if _, err := queue.Acquire(ctx, 0); !errors.Is(err, ErrBusy) {
			t.Errorf("got %v, want ErrBusy with one waiting and a queue of one", err)
		}
	})
}

// TestAcquireNoWaiting requires a [Options.MaxWaitingRequests] of zero to
// refuse at once when no slot is free.
func TestAcquireNoWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		queue := newQueue(t, 1, Options{MaxWaitingRequests: 0})

		release, err := queue.Acquire(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer release()

		if _, err := queue.Acquire(ctx, 0); !errors.Is(err, ErrBusy) {
			t.Errorf("got %v, want ErrBusy with no queue", err)
		}
	})
}

// TestAcquireTimeout requires a waiter to give up with
// [ErrTimeout] when its wait reaches the timeout, and to leave the queue.
func TestAcquireTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		queue := newQueue(t, 1, Options{MaxWaitingRequests: 1, Timeout: time.Second})

		release, err := queue.Acquire(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer release()

		start := time.Now()
		_, err = queue.Acquire(ctx, 0)
		if !errors.Is(err, ErrTimeout) || time.Since(start) != time.Second {
			t.Errorf("got %v after %v, want ErrTimeout after 1s", err, time.Since(start))
		}

		if n := queue.waiting.Len(); n != 0 {
			t.Errorf("%d still waiting, want none", n)
		}
	})
}

// TestAcquireTimeoutWakes requires a waiter that gives up at the head
// of the queue to let in the one behind it that fits.
func TestAcquireTimeoutWakes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		queue := newQueue(t, 2, Options{MaxWaitingRequests: 2, Timeout: time.Second, MemoryBudgetBytes: 100})

		release, err := queue.Acquire(ctx, 60)
		if err != nil {
			t.Fatal(err)
		}
		defer release()

		// 50 bytes do not fit beside 60 of 100, so this waits at the head.
		go func() { _, _ = queue.Acquire(ctx, 50) }()

		time.Sleep(100 * time.Millisecond)

		start := time.Now()

		releaseSecond, err := queue.Acquire(ctx, 30)
		if err != nil || time.Since(start) != 900*time.Millisecond {
			t.Fatalf("got %v after %v, want the slot when the head gives up, after 900ms", err, time.Since(start))
		}

		releaseSecond()
	})
}

// TestAcquireCancelGranted requires a waiter that is granted a slot after
// its context ends, but before it returns, to give the slot back.
func TestAcquireCancelGranted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		// The release may grant the slot before the waiter leaves, or after.
		for round := range 64 {
			queue := newQueue(t, 1, Options{MaxWaitingRequests: 1})

			release, err := queue.Acquire(ctx, 0)
			if err != nil {
				t.Fatal(err)
			}

			waiterCtx, cancelWaiter := context.WithCancel(ctx)
			got := make(chan func(), 1)

			go func() {
				release, _ := queue.Acquire(waiterCtx, 0)
				got <- release
			}()

			synctest.Wait()
			cancelWaiter()
			release()

			if release := <-got; release != nil {
				release()
			}

			if queue.slots != 1 || queue.waiting.Len() != 0 {
				t.Fatalf(
					"round %d: %d slots, %d waiting after everything ended; want 1, 0",
					round,
					queue.slots,
					queue.waiting.Len(),
				)
			}
		}
	})
}

// newQueue returns [New]'s [Queue] and fails t on an error.
func newQueue(t *testing.T, concurrency int, opts Options) *Queue {
	t.Helper()

	queue, err := New(concurrency, opts)
	if err != nil {
		t.Fatal(err)
	}

	return queue
}
