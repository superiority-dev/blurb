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

// TestBodyDeadline requires a body's deadline to run only inside its reads:
// time between reads does not count, and reads that wait on the origin for
// longer than the timeout in all are cut.
func TestBodyDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		origin := &slowReader{}
		fetchedBody := &body{
			r:  io.NopCloser(origin),
			dl: newDeadline(100*time.Millisecond, cancel),
		}
		fetchedBody.dl.pause()

		for _, wait := range []time.Duration{60 * time.Millisecond, 39 * time.Millisecond} {
			time.Sleep(time.Second)
			origin.wait = wait

			if _, err := fetchedBody.Read(make([]byte, 1)); err != nil {
				t.Fatal(err)
			}
		}

		synctest.Wait()

		if ctx.Err() != nil {
			t.Fatal("timed out after reads that waited 99ms in all, with a 100ms timeout")
		}

		origin.wait = 2 * time.Millisecond
		_, _ = fetchedBody.Read(make([]byte, 1))
		synctest.Wait()

		if context.Cause(ctx) != context.DeadlineExceeded {
			t.Fatalf("after reads that waited 101ms: cause %v, want context.DeadlineExceeded", context.Cause(ctx))
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

// slowReader is an origin whose every read waits for wait, then returns one
// byte.
type slowReader struct {
	wait time.Duration
}

func (s *slowReader) Read(p []byte) (int, error) {
	time.Sleep(s.wait)
	p[0] = 'x'

	return 1, nil
}
