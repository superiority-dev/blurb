package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"sync/atomic"
	"time"
)

const (
	logKeyRequestID = "request_id"
	logKeyError     = "error"
)

// maxRequestIDLen is the longest request id taken from a client.
const maxRequestIDLen = 128

var (
	requestIDPrefix = newRequestIDPrefix()
	requestCount    atomic.Uint64
)

type middleware func(http.Handler) http.Handler

type requestIDKey struct{}

// statusRecorder keeps the status a handler wrote, whether it has written
// the header, and the cause of a failure the handler noted.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	cause       error
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.wroteHeader = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusRecorder) noteCause(err error) { s.cause = err }

// apiChain returns the middleware every API request passes through. Every line
// it logs carries the request id, and a panic is logged at error level.
func apiChain(requestIDHeader string, logger *slog.Logger) middleware {
	return chain(requestID(requestIDHeader), logRequests(logger), recoverPanics(logger))
}

// chain applies middlewares so that the first one listed runs first.
func chain(middlewares ...middleware) middleware {
	return func(h http.Handler) http.Handler {
		for _, wrap := range slices.Backward(middlewares) {
			h = wrap(h)
		}

		return h
	}
}

// requestID gives each request an id: the client's value of header when it is
// 1 to maxRequestIDLen visible ASCII characters, otherwise a new one. It sets
// the id in header on the response and stores it in the request's context.
func requestID(header string) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(header)
			if !validRequestID(id) {
				id = requestIDPrefix + strconv.FormatUint(requestCount.Add(1), 10)
			}

			w.Header().Set(header, id)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
		})
	}
}

func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLen {
		return false
	}

	for i := range len(id) {
		if id[i] <= ' ' || id[i] > '~' {
			return false
		}
	}

	return true
}

func newRequestIDPrefix() string {
	var b [6]byte
	rand.Read(b[:])

	return hex.EncodeToString(b[:]) + "-"
}

// logRequests writes one line per request: at the level its status calls
// for, with the cause a handler noted, or, when a panic aborted the response,
// at error level with the error "response aborted".
func logRequests(logger *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			returned := false

			defer func() {
				attrs := []slog.Attr{
					slog.String("method", r.Method),
					slog.String("route", r.Pattern),
					slog.Int("status", rec.status),
					slog.Duration("duration_ns", time.Since(start)),
					slog.String(logKeyRequestID, requestIDFrom(r.Context())),
				}
				level := statusLevel(rec.status)

				switch {
				case !returned:
					attrs = append(attrs, slog.String(logKeyError, "response aborted"))
					level = slog.LevelError
				case rec.cause != nil:
					attrs = append(attrs, slog.String(logKeyError, rec.cause.Error()))
				}

				logger.LogAttrs(r.Context(), level, "request", attrs...)
			}()

			next.ServeHTTP(rec, r)
			returned = true
		})
	}
}

// statusLevel returns the log level for a request that ended with status: error
// for the service's own failure, warn for an origin's, and info for the rest,
// including the 503 of a full queue or of a client that left.
func statusLevel(status int) slog.Level {
	switch status {
	case http.StatusInternalServerError:
		return slog.LevelError
	case http.StatusBadGateway, http.StatusGatewayTimeout:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// recoverPanics logs a panic in a handler, then answers 500, or, once the
// handler has written the header, panics with [http.ErrAbortHandler] so the
// client sees an interrupted response. It lets a panic with
// [http.ErrAbortHandler] through unlogged.
func recoverPanics(logger *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				e := recover()
				if e == nil {
					return
				}

				if e == http.ErrAbortHandler {
					panic(e)
				}

				logger.ErrorContext(
					r.Context(),
					"panic",
					slog.Any("panic_value", e),
					slog.String(logKeyRequestID, requestIDFrom(r.Context())),
				)

				if rec, ok := w.(*statusRecorder); ok && rec.wroteHeader {
					panic(http.ErrAbortHandler)
				}

				writeError(w, http.StatusInternalServerError, codeInternal, messageInternal)
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// requestIDFrom returns the id requestID put in ctx, or "".
func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}
