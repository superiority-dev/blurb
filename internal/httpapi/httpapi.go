// Package httpapi serves the HTTP API and the health probes.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/metric"

	"github.com/superiority-dev/blurb/internal/fetch"
	"github.com/superiority-dev/blurb/internal/queue"
)

const (
	pathHealthz = "/healthz"
	pathReadyz  = "/readyz"
)

// operationHash is the span name otelhttp gives each request the API serves.
const operationHash = "hash"

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

	// BodyTimeout bounds the time the reads of a POST /hash body wait on the
	// client, summed over the body. It must be positive.
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
// while ready marks the service not ready. hashQueue admits each /hash
// request before it fetches or reads the image, and decodeQueue admits its
// decode. The metrics of every request but a probe, and of both queues, go to
// meters.
func NewHandler(
	cfg Config,
	fetcher *fetch.Fetcher,
	hashQueue, decodeQueue *queue.Queue,
	ready *Readiness,
	logger *slog.Logger,
	meters metric.MeterProvider,
) (http.Handler, error) {
	instruments, err := newMetrics(meters.Meter(meterName), hashQueue, decodeQueue)
	if err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}

	hashAPI := apiHandler(newAPI(cfg, fetcher, hashQueue, decodeQueue, instruments), logger)
	refuse := methodNotAllowed(http.MethodGet, http.MethodHead)

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+pathHealthz, healthz)
	mux.HandleFunc("GET "+pathReadyz, ready.readyz)
	mux.Handle(pathHealthz, refuse)
	mux.Handle(pathReadyz, refuse)
	mux.Handle("/", otelhttp.NewHandler(hashAPI, operationHash, otelhttp.WithMeterProvider(meters)))

	return mux, nil
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

// parseOptions returns what the formats and extras parameters ask for.
func parseOptions(values url.Values) (formats formatSet, extras extraSet, err error) {
	formats, err = parseFormats(values)
	if err != nil {
		return formatSet{}, extraSet{}, err
	}

	extras, err = parseExtras(values)
	if err != nil {
		return formatSet{}, extraSet{}, err
	}

	return formats, extras, nil
}

// param returns the value of the query parameter named by name, or an error
// when the query gives it more than once.
func param(values url.Values, name string) (string, error) {
	if n := len(values[name]); n > 1 {
		return "", fmt.Errorf("%s is given %d times; give it once", name, n)
	}

	return values.Get(name), nil
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
