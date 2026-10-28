package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/superiority-dev/blurb/internal/fetch"
	"github.com/superiority-dev/blurb/internal/queue"
)

func TestRequestID(t *testing.T) {
	cases := []struct {
		in   string
		kept bool
	}{
		{in: "abc-123", kept: true},
		{in: "", kept: false},
		{in: "has space", kept: false},
		{in: "bad\nline", kept: false},
		{in: strings.Repeat("x", 128), kept: true},
		{in: strings.Repeat("x", 129), kept: false},
	}

	for _, header := range []string{"X-Request-Id", "X-Amz-Cf-Id"} {
		for _, tc := range cases {
			var seen string

			h := requestID(header)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				seen = requestIDFrom(r.Context())
			}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(header, tc.in)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			got := rec.Header().Get(header)
			if got != seen {
				t.Errorf("%s %q: response %q, context %q; want the same", header, tc.in, got, seen)
			}

			if kept := got == tc.in; kept != tc.kept {
				t.Errorf("%s %q: response %q, kept %t; want %t", header, tc.in, got, kept, tc.kept)
			}

			if generated := strings.HasPrefix(got, requestIDPrefix); generated == tc.kept {
				t.Errorf("%s %q: response %q, generated %t; want %t", header, tc.in, got, generated, !tc.kept)
			}
		}
	}
}

func TestLogRequests(t *testing.T) {
	logger, logs := jsonLogger()
	h := apiHandler(testAPI(t), logger)

	req := httptest.NewRequest(http.MethodDelete, "/hash", nil)
	req.Header.Set("X-Request-Id", "req-1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	lines := checkLog(t, logs, map[string]any{
		"msg":        "request",
		"method":     "DELETE",
		"route":      "/hash",
		"status":     float64(http.StatusMethodNotAllowed),
		"request_id": "req-1",
	})

	if lines[0]["took"] == nil {
		t.Errorf("log %q, want a took", logs.String())
	}
}

// TestLogRequestsLevels requires one line per request, at error level for
// a 500, warn for an origin's 502 or 504 and info for a 503 or a 404, with
// the cause on a 5xx's line.
func TestLogRequestsLevels(t *testing.T) {
	cases := []struct {
		err   error
		level string
		cause any
	}{
		{err: errors.New("boom"), level: "ERROR", cause: "boom"},
		{
			err:   &fetch.OriginStatusError{Status: http.StatusInternalServerError},
			level: "WARN",
			cause: "fetch: origin answered 500",
		},
		{err: context.DeadlineExceeded, level: "WARN", cause: context.DeadlineExceeded.Error()},
		{err: queue.ErrBusy, level: "INFO", cause: queue.ErrBusy.Error()},
		{err: queue.ErrTimeout, level: "INFO", cause: queue.ErrTimeout.Error()},
		{err: &fetch.OriginStatusError{Status: http.StatusNotFound}, level: "INFO", cause: nil},
	}

	for _, tc := range cases {
		logger, logs := jsonLogger()
		h := apiChain("X-Request-Id", logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fail(w, tc.err)
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/hash", nil))

		checkLog(t, logs, map[string]any{"msg": "request", "level": tc.level, "error": tc.cause})
	}
}

// TestRecoverPanicsLogged requires a panic in an API handler to answer 500
// and to log a line for the panic and one for the request, each with the
// request id.
func TestRecoverPanicsLogged(t *testing.T) {
	logger, logs := jsonLogger()
	h := apiChain("X-Request-Id", logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "req-7")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil ||
		rec.Code != http.StatusInternalServerError ||
		body.Error.Code != "internal" ||
		body.Error.Message != "internal error" {
		t.Errorf("got %d %q, want a 500 with code internal and message internal error", rec.Code, rec.Body.String())
	}

	checkLog(t, logs,
		map[string]any{"msg": "panic", "value": "boom", "request_id": "req-7"},
		map[string]any{"msg": "request", "status": float64(http.StatusInternalServerError), "request_id": "req-7"},
	)
}

// TestRecoverPanicsWritten requires a panic after the handler has written the
// header to end in [http.ErrAbortHandler], with nothing added to what the
// handler wrote, and to log a line for the panic and an error line for the
// request.
func TestRecoverPanicsWritten(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{name: "header", status: http.StatusAccepted, body: ""},
		{name: "body", status: 0, body: "partial"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logger, logs := jsonLogger()
			h := apiChain("X-Request-Id", logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}

				if tc.body != "" {
					_, _ = w.Write([]byte(tc.body))
				}

				panic("boom")
			}))

			rec := httptest.NewRecorder()

			defer func() {
				if e := recover(); e != http.ErrAbortHandler {
					t.Errorf("recovered %v, want http.ErrAbortHandler", e)
				}

				if rec.Body.String() != tc.body {
					t.Errorf("body %q, want %q", rec.Body.String(), tc.body)
				}

				checkLog(t, logs,
					map[string]any{"msg": "panic"},
					map[string]any{"msg": "request", "level": "ERROR", "error": "response aborted"},
				)
			}()

			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		})
	}
}

// TestRecoverPanicsAbortHandler requires [http.ErrAbortHandler] to be
// panicked again, so the server aborts the response and logs no stack trace.
func TestRecoverPanicsAbortHandler(t *testing.T) {
	h := recoverPanics(slog.New(slog.DiscardHandler))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		if e := recover(); e != http.ErrAbortHandler {
			t.Errorf("recovered %v, want http.ErrAbortHandler", e)
		}
	}()

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}
