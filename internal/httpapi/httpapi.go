// Package httpapi serves the HTTP API and the health probes.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/superiority-dev/blurb/internal/fetch"
	"github.com/superiority-dev/blurb/internal/queue"
)

const (
	pathHealthz = "/healthz"
	pathReadyz  = "/readyz"
)

// noStore is the maxAge that sends Cache-Control: no-store.
const noStore time.Duration = 0

// Config configures the handler [NewHandler] returns.
type Config struct {
	MaxPixels int64

	// XComponents is the number of horizontal BlurHash components.
	XComponents int

	// YComponents is the number of vertical BlurHash components.
	YComponents int

	// MaxBodyBytes bounds the body POST /hash accepts.
	MaxBodyBytes int64

	// BodyTimeout bounds the time to read the body of POST /hash, which the
	// decode reads as it goes; a wait for a decode slot does not count. It
	// must be positive.
	BodyTimeout time.Duration

	// PathPrefix, such as /v1, is prepended to /hash but not to the probes.
	PathPrefix string

	// MaxAge is the Cache-Control max-age sent with a hash of a fetched image;
	// 0 sends no-store.
	MaxAge time.Duration

	// RequestIDHeader names the header a request id is read from and sent
	// back in.
	RequestIDHeader string
}

// NewHandler returns a handler that serves /hash under cfg.PathPrefix and
// the probes /healthz and /readyz at the root. Every request but a probe is
// logged to logger, as is any panic it recovers from. /readyz answers 503
// while ready marks the service not ready.
func NewHandler(
	cfg Config,
	fetcher *fetch.Fetcher,
	decodeQueue *queue.Queue,
	ready *Readiness,
	logger *slog.Logger,
) http.Handler {
	refuse := methodNotAllowed(http.MethodGet, http.MethodHead)

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+pathHealthz, healthz)
	mux.HandleFunc("GET "+pathReadyz, ready.readyz)
	mux.Handle(pathHealthz, refuse)
	mux.Handle(pathReadyz, refuse)
	mux.Handle("/", apiHandler(newAPI(cfg, fetcher, decodeQueue), logger))

	return mux
}

// apiHandler returns a handler that serves /hash under [Config.PathPrefix]
// and answers any other path 404. Every request is logged to logger, as is any
// panic it recovers from.
func apiHandler(hashAPI *api, logger *slog.Logger) http.Handler {
	hashRoute := hashAPI.pathPrefix + "/hash"

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+hashRoute, hashAPI.hashGet)
	mux.HandleFunc("POST "+hashRoute, hashAPI.hashPost)
	mux.Handle(hashRoute, methodNotAllowed(http.MethodGet, http.MethodHead, http.MethodPost))
	mux.HandleFunc("/", notFound)

	return apiChain(hashAPI.requestIDHeader, logger)(mux)
}

// fail writes the error response for err, with Retry-After for [queue.ErrBusy]
// and [queue.ErrTimeout]. For a 5xx it notes err as the cause logRequests logs.
func fail(w http.ResponseWriter, err error) {
	status, code, message := classify(err)
	if errors.Is(err, queue.ErrBusy) || errors.Is(err, queue.ErrTimeout) {
		w.Header().Set("Retry-After", "1")
	}

	if rec, ok := w.(interface{ noteCause(error) }); ok && status >= http.StatusInternalServerError {
		rec.noteCause(err)
	}

	writeError(w, status, code, message)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, noStore, errorBody{Error: errorDetail{Code: code, Message: message}})
}

// writeJSON writes the JSON encoding of v, followed by a newline, with status,
// Content-Type: application/json, a Cache-Control for maxAge, and
// X-Content-Type-Options: nosniff. If encoding v fails, writeJSON writes that
// error with fail.
func writeJSON(w http.ResponseWriter, status int, maxAge time.Duration, v any) {
	doc, err := json.Marshal(v)
	if err != nil {
		fail(w, err)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", cacheControl(maxAge))
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(append(doc, '\n'))
}

// cacheControl returns the Cache-Control value for maxAge, no-store when it
// is 0.
func cacheControl(maxAge time.Duration) string {
	if maxAge == noStore {
		return "no-store"
	}

	return fmt.Sprintf("public, max-age=%d, immutable", maxAge/time.Second)
}
