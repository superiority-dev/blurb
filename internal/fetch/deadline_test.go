package fetch

import (
	"context"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// TestDeadline requires the deadline to cancel with [context.DeadlineExceeded]
// after its timeout, pause to stop it, resume to go on with the time left and
// to do nothing without a pause, and stop to cancel without that cause.
func TestDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		dl := newDeadline(100*time.Millisecond, cancel)

		dl.resume()
		time.Sleep(40 * time.Millisecond)
		dl.pause()
		time.Sleep(time.Second)
		dl.resume()
		time.Sleep(20 * time.Millisecond)
		dl.pause()
		time.Sleep(time.Second)
		dl.resume()
		time.Sleep(39 * time.Millisecond)
		synctest.Wait()

		if ctx.Err() != nil {
			t.Fatal("fired after 99ms of running time with a 100ms timeout")
		}

		time.Sleep(2 * time.Millisecond)
		synctest.Wait()

		if context.Cause(ctx) != context.DeadlineExceeded {
			t.Fatalf("after 101ms of running time: cause %v, want context.DeadlineExceeded", context.Cause(ctx))
		}

		ctx, cancel = context.WithCancelCause(context.Background())
		newDeadline(100*time.Millisecond, cancel).stop()
		time.Sleep(time.Second)
		synctest.Wait()

		if context.Cause(ctx) != context.Canceled {
			t.Errorf("stopped: cause %v, want context.Canceled", context.Cause(ctx))
		}
	})
}

// TestBodyPauseDeadline requires PauseDeadline to stop a body's deadline
// and ResumeDeadline to go on with the time that was left, not a new
// timeout.
func TestBodyPauseDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		fetchedBody := &body{
			r:  io.NopCloser(strings.NewReader("image")),
			dl: newDeadline(100*time.Millisecond, cancel),
		}

		time.Sleep(70 * time.Millisecond)
		fetchedBody.PauseDeadline()
		time.Sleep(time.Second)
		synctest.Wait()

		if ctx.Err() != nil {
			t.Fatal("timed out while paused")
		}

		fetchedBody.ResumeDeadline()
		time.Sleep(29 * time.Millisecond)
		synctest.Wait()

		if ctx.Err() != nil {
			t.Fatal("timed out after 99ms of running time")
		}

		time.Sleep(2 * time.Millisecond)
		synctest.Wait()

		if ctx.Err() == nil {
			t.Fatal("not timed out after 101ms of running time")
		}
	})
}

// TestBodyClose requires Close to cancel the fetch at once.
func TestBodyClose(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	fetchedBody := &body{
		r:  io.NopCloser(strings.NewReader("image")),
		dl: newDeadline(time.Hour, cancel),
	}

	if err := fetchedBody.Close(); err != nil {
		t.Fatal(err)
	}

	if context.Cause(ctx) != context.Canceled {
		t.Errorf("after Close: cause %v, want context.Canceled", context.Cause(ctx))
	}
}
