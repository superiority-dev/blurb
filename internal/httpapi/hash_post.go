package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// hashPost answers POST /hash, hashing the image in the request body.
func (a *api) hashPost(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()

	formats, extras, err := parseOptions(values)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
		return
	}

	if r.ContentLength > a.maxBodyBytes {
		fail(w, &http.MaxBytesError{Limit: a.maxBodyBytes})
		return
	}

	release, err := a.hashQueue.Acquire(r.Context(), 0)
	if err != nil {
		a.hasher.metrics.refuse(r.Context(), queueHash, err)
		fail(w, err)
		return
	}
	defer release()

	body := newClientBody(w, r, a.maxBodyBytes, a.bodyTimeout)

	res, err := a.hasher.hash(r.Context(), body, r.ContentLength, formats, extras)
	if err != nil {
		fail(w, err)
		return
	}

	writeJSON(w, http.StatusOK, noStore, res)
}

// clientBody is a request body read under one deadline for the whole body,
// which runs only inside its reads. Its read errors also match
// errBodyTimeout when the deadline passes, [context.Canceled] for any other
// network error, errBodyCut when the body ends early, and errBodyMalformed for
// any other error but an [*http.MaxBytesError].
type clientBody struct {
	r    io.Reader
	rc   *http.ResponseController
	left time.Duration // what remains of the deadline
}

// newClientBody returns r's body, limited to maxBytes, with a deadline of
// timeout.
func newClientBody(w http.ResponseWriter, r *http.Request, maxBytes int64, timeout time.Duration) *clientBody {
	return &clientBody{
		r:    http.MaxBytesReader(w, r.Body, maxBytes),
		rc:   http.NewResponseController(w),
		left: timeout,
	}
}

// Read sets the connection's read deadline to now plus the time the body has
// left. A writer that cannot set one leaves the body without a deadline.
func (c *clientBody) Read(p []byte) (int, error) {
	end := time.Now().Add(c.left)
	_ = c.rc.SetReadDeadline(end)
	n, err := c.r.Read(p)
	c.left = time.Until(end)

	if err == nil || err == io.EOF {
		return n, err
	}

	if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
		return n, err
	}

	class := errBodyMalformed
	if netErr, network := errors.AsType[net.Error](err); network && netErr.Timeout() {
		class = errBodyTimeout
	} else if network {
		class = context.Canceled
	} else if errors.Is(err, io.ErrUnexpectedEOF) {
		class = errBodyCut
	}

	return n, fmt.Errorf("%w: %w", class, err)
}
