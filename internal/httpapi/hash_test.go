package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/superiority-dev/blurb/internal/header"
	"github.com/superiority-dev/blurb/internal/queue"
	"github.com/superiority-dev/blurb/internal/testimage"
)

// TestHasherDecode requires the body's deadline to pause while the
// request waits for a decode slot, and to resume once it has one.
func TestHasherDecode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		decodeQueue, err := queue.New(1, queue.Options{MaxWaitingRequests: 1})
		if err != nil {
			t.Fatal(err)
		}

		release, err := decodeQueue.Acquire(context.Background(), 0)
		if err != nil {
			t.Fatal(err)
		}

		instruments, err := newMetrics(noop.Meter{}, decodeQueue, decodeQueue)
		if err != nil {
			t.Fatal(err)
		}

		body := &deadlineCounter{}
		rest := bytes.NewReader(testimage.GradientJPEG(t, 50, 40))
		done := make(chan error, 1)

		go func() {
			_, _, err := (&hasher{decodeQueue: decodeQueue, metrics: instruments}).decode(
				ctx,
				body,
				header.Info{Orientation: header.OrientationNormal},
				rest,
			)
			done <- err
		}()

		synctest.Wait()

		if body.paused.Load() != 1 || body.resumed.Load() != 0 {
			t.Errorf("while waiting: paused %d, resumed %d; want 1 and 0", body.paused.Load(), body.resumed.Load())
		}

		release()

		if err := <-done; err != nil {
			t.Fatal(err)
		}

		if body.paused.Load() != 1 || body.resumed.Load() != 1 {
			t.Errorf("once admitted: paused %d, resumed %d; want 1 and 1", body.paused.Load(), body.resumed.Load())
		}
	})
}

// TestAspect requires aspect to round to four decimals, halves up, and to
// marshal the same on every architecture.
func TestAspect(t *testing.T) {
	cases := []struct {
		width, height int
		want          string
	}{
		{width: 1920, height: 1080, want: "1.7778"},
		{width: 1080, height: 1920, want: "0.5625"},
		{width: 1, height: 1, want: "1"},
		{width: 3, height: 2, want: "1.5"},
		{width: 2, height: 3, want: "0.6667"},
		{width: 1, height: 3, want: "0.3333"},
		{width: 1, height: 32, want: "0.0313"},
		{width: 3, height: 32, want: "0.0938"},
		{width: 1, height: 20_000, want: "0.0001"},
		{width: 1, height: 20_001, want: "0"},
		{width: 20_000, height: 1, want: "20000"},
		{width: 1_000_000_000, height: 1, want: "1000000000"},
	}

	for _, tc := range cases {
		b, err := json.Marshal(aspect(tc.width, tc.height))
		if err != nil || string(b) != tc.want {
			t.Errorf("aspect(%d, %d) = %s, %v; want %s", tc.width, tc.height, b, err, tc.want)
		}
	}
}

func TestHex64(t *testing.T) {
	if got := hex64(1); got != "0000000000000001" {
		t.Errorf("hex64(1) = %q, want 16 digits", got)
	}
}

// deadlineCounter is a body that counts its PauseDeadline and ResumeDeadline
// calls.
type deadlineCounter struct {
	paused, resumed atomic.Int32
}

func (d *deadlineCounter) Read([]byte) (int, error) { return 0, io.EOF }

func (d *deadlineCounter) PauseDeadline() { d.paused.Add(1) }

func (d *deadlineCounter) ResumeDeadline() { d.resumed.Add(1) }
