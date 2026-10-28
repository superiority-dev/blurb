package fetch

import (
	"context"
	"time"
)

// deadline cancels a fetch with [context.DeadlineExceeded] when its timer
// fires.
type deadline struct {
	timer  *time.Timer
	end    time.Time     // when the running timer fires
	left   time.Duration // what remained at the last pause
	paused bool
	cancel context.CancelCauseFunc
}

func newDeadline(timeout time.Duration, cancel context.CancelCauseFunc) *deadline {
	return &deadline{
		timer:  time.AfterFunc(timeout, func() { cancel(context.DeadlineExceeded) }),
		end:    time.Now().Add(timeout),
		cancel: cancel,
	}
}

func (d *deadline) pause() {
	if d.timer.Stop() {
		d.left = time.Until(d.end)
		d.paused = true
	}
}

func (d *deadline) resume() {
	if d.paused {
		d.paused = false
		d.end = time.Now().Add(d.left)
		d.timer.Reset(d.left)
	}
}

func (d *deadline) stop() {
	d.timer.Stop()
	d.cancel(nil)
}
