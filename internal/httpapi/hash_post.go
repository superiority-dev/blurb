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

	formats, err := parseFormats(values)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
		return
	}

	extras, err := parseExtras(values)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
		return
	}

	if r.ContentLength > a.maxBodyBytes {
		fail(w, &http.MaxBytesError{Limit: a.maxBodyBytes})
		return
	}

	body := newClientBody(w, r, a.maxBodyBytes, a.bodyTimeout)

	res, err := a.hasher.hash(r.Context(), body, r.ContentLength, formats, extras)
	if err != nil {
		fail(w, err)
		return
	}

	writeJSON(w, http.StatusOK, noStore, res)
}

// clientBody is a request body read under one deadline for the whole body,
// which stops while the request waits for a decode. Its read errors also match
// errBodyTimeout when the deadline passes, [context.Canceled] for any other
// network error, errBodyCut when the body ends early, and errBodyMalformed for
// any other error but an [*http.MaxBytesError].
type clientBody struct {
	r    io.Reader
	rc   *http.ResponseController
	end  time.Time     // the connection's read deadline
	left time.Duration // what remained at the last pause
}

// newClientBody returns r's body, limited to maxBytes, with its deadline
// timeout from now.
func newClientBody(w http.ResponseWriter, r *http.Request, maxBytes int64, timeout time.Duration) *clientBody {
	c := &clientBody{
		r:   http.MaxBytesReader(w, r.Body, maxBytes),
		rc:  http.NewResponseController(w),
		end: time.Now().Add(timeout),
	}
	c.setDeadline()

	return c
}

func (c *clientBody) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
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

// PauseDeadline stops the deadline until [clientBody.ResumeDeadline]. Nothing
// reads the body meanwhile, so the connection's deadline is left as it is.
func (c *clientBody) PauseDeadline() {
	c.left = time.Until(c.end)
}

// ResumeDeadline restarts the deadline with what remained at the pause.
func (c *clientBody) ResumeDeadline() {
	c.end = time.Now().Add(c.left)
	c.setDeadline()
}

// setDeadline sets the connection's read deadline to end. A writer that
// cannot set one leaves the body without a deadline.
func (c *clientBody) setDeadline() {
	_ = c.rc.SetReadDeadline(c.end)
}
