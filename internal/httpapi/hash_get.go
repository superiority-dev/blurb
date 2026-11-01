package httpapi

import "net/http"

// hashGet answers GET /hash, hashing the image at the url parameter.
func (a *api) hashGet(w http.ResponseWriter, r *http.Request) {
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

	rawURL, err := param(values, paramURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
		return
	}

	if rawURL == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, paramURL+" is required")
		return
	}

	release, err := a.hashQueue.Acquire(r.Context(), 0)
	if err != nil {
		a.hasher.metrics.refuse(r.Context(), queueHash, err)
		fail(w, err)
		return
	}
	defer release()

	body, size, err := a.fetcher.Fetch(r.Context(), rawURL)
	if err != nil {
		fail(w, err)
		return
	}
	defer body.Close()

	res, err := a.hasher.hash(r.Context(), body, size, formats, extras)
	if err != nil {
		fail(w, err)
		return
	}

	writeJSON(w, http.StatusOK, a.maxAge, res)
}
