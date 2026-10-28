package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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

	body := clientBody{r: http.MaxBytesReader(w, r.Body, a.maxBodyBytes)}

	res, err := a.hasher.hash(r.Context(), body, r.ContentLength, formats, extras)
	if err != nil {
		fail(w, err)
		return
	}

	writeJSON(w, http.StatusOK, noStore, res)
}

// clientBody is a request body. Its read errors also match [context.Canceled]
// for a network error, errBodyCut when the body ends early, and
// errBodyMalformed for any other error but an [*http.MaxBytesError].
type clientBody struct {
	r io.Reader
}

func (c clientBody) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if err == nil || err == io.EOF {
		return n, err
	}

	if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
		return n, err
	}

	class := errBodyMalformed
	if _, network := errors.AsType[net.Error](err); network {
		class = context.Canceled
	} else if errors.Is(err, io.ErrUnexpectedEOF) {
		class = errBodyCut
	}

	return n, fmt.Errorf("%w: %w", class, err)
}
