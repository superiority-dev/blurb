package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/superiority-dev/blurb/internal/fetch"
	"github.com/superiority-dev/blurb/internal/queue"
)

// TestNewHandler requires one handler to serve the API's routes and the probes,
// and to log a line for each request but a probe.
func TestNewHandler(t *testing.T) {
	logger, logs := jsonLogger()
	hashAPI := testAPI(t)
	h := NewHandler(testConfig(), hashAPI.fetcher, hashAPI.hasher.decodeQueue, testReady(), logger)

	for _, rt := range apiRoutes {
		checkRoute(t, h, rt)
	}

	for _, rt := range probeRoutes {
		checkRoute(t, h, rt)
	}

	if n, want := strings.Count(logs.String(), `"msg":"request"`), len(apiRoutes); n != want {
		t.Errorf(`%d "request" log lines, want %d`, n, want)
	}
}

// TestNewHandlerConfig requires the prefix on the API's routes alone, and the
// request id to be read from [Config.RequestIDHeader] and sent back there.
func TestNewHandlerConfig(t *testing.T) {
	hashAPI := testAPI(t)
	cfg := testConfig()
	cfg.PathPrefix = "/v1"
	cfg.RequestIDHeader = "X-Amz-Cf-Id"
	h := NewHandler(cfg, hashAPI.fetcher, hashAPI.hasher.decodeQueue, testReady(), discardLogger)

	for _, rt := range prefixedRoutes {
		checkRoute(t, h, rt)
	}

	for _, rt := range probeRoutes {
		checkRoute(t, h, rt)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/hash", nil)
	req.Header.Set("X-Amz-Cf-Id", "cf-1")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Amz-Cf-Id"); got != "cf-1" {
		t.Errorf("X-Amz-Cf-Id %q, want cf-1", got)
	}
}

// TestReadiness requires readyz to follow the readiness flag and healthz not
// to, every answer being JSON marked no-store.
func TestReadiness(t *testing.T) {
	hashAPI := testAPI(t)
	ready := &Readiness{}
	h := NewHandler(testConfig(), hashAPI.fetcher, hashAPI.hasher.decodeQueue, ready, discardLogger)

	cases := []struct {
		ready  bool
		path   string
		status int
		body   string
	}{
		{
			ready:  false,
			path:   "/readyz",
			status: http.StatusServiceUnavailable,
			body:   `{"error":{"code":"not_ready","message":"the service is not ready"}}`,
		},
		{ready: false, path: "/healthz", status: http.StatusOK, body: `{"status":"ok"}`},
		{ready: true, path: "/readyz", status: http.StatusOK, body: `{"status":"ok"}`},
		{ready: true, path: "/healthz", status: http.StatusOK, body: `{"status":"ok"}`},
	}

	for _, tc := range cases {
		ready.Set(tc.ready)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

		if rec.Code != tc.status {
			t.Errorf("ready %t, %s: status %d, want %d", tc.ready, tc.path, rec.Code, tc.status)
		}

		if got := rec.Body.String(); got != tc.body+"\n" {
			t.Errorf("ready %t, %s: body %q, want %s", tc.ready, tc.path, got, tc.body)
		}

		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("ready %t, %s: Cache-Control %q, want no-store", tc.ready, tc.path, cc)
		}

		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("ready %t, %s: Content-Type %q, want application/json", tc.ready, tc.path, ct)
		}
	}
}

// TestFail requires a full queue and a wait past the queue timeout each
// to answer 503 with Retry-After.
func TestFail(t *testing.T) {
	for _, err := range []error{queue.ErrBusy, queue.ErrTimeout} {
		rec := httptest.NewRecorder()
		fail(rec, err)

		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" {
			t.Errorf("%v: got %d, Retry-After %q; want 503 and 1", err, rec.Code, rec.Header().Get("Retry-After"))
		}
	}
}

func TestCacheControl(t *testing.T) {
	cases := []struct {
		maxAge time.Duration
		want   string
	}{
		{maxAge: 0, want: "no-store"},
		{maxAge: time.Second, want: "public, max-age=1, immutable"},
		{maxAge: 8760 * time.Hour, want: "public, max-age=31536000, immutable"},
	}

	for _, tc := range cases {
		if got := cacheControl(tc.maxAge); got != tc.want {
			t.Errorf("cacheControl(%v) = %q, want %q", tc.maxAge, got, tc.want)
		}
	}
}

// maxTestBody is the largest image testAPI lets a fetch or a request body
// carry.
const maxTestBody = 256 << 10 // 256 KiB

var discardLogger = slog.New(slog.DiscardHandler)

// testAPI returns an api that fetches from 127.0.0.1 with a short timeout.
func testAPI(t *testing.T) *api {
	t.Helper()

	fetcher, err := fetch.New(300*time.Millisecond, maxTestBody, fetch.Options{
		AllowedHosts:          []string{"127.0.0.1"},
		AllowPrivateAddresses: true,
		FollowRedirects:       true,
	})
	if err != nil {
		t.Fatal(err)
	}

	decodeQueue, err := queue.New(2, queue.Options{})
	if err != nil {
		t.Fatal(err)
	}

	return newAPI(testConfig(), fetcher, decodeQueue)
}

func testConfig() Config {
	return Config{
		MaxPixels:       1 << 20,
		XComponents:     4,
		YComponents:     3,
		MaxBodyBytes:    maxTestBody,
		MaxAge:          time.Hour,
		RequestIDHeader: "X-Request-Id",
	}
}

func testReady() *Readiness {
	ready := &Readiness{}
	ready.Set(true)

	return ready
}

func serve(h http.Handler, method, target string, body []byte) *httptest.ResponseRecorder {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, r))

	return rec
}

// checkRepeated requires rec to be a 400 naming the parameter given twice.
func checkRepeated(t *testing.T, rec *httptest.ResponseRecorder, name string) {
	t.Helper()

	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusBadRequest ||
		body.Error.Code != "bad_request" || !strings.HasPrefix(body.Error.Message, name+" is given 2 times") {
		t.Errorf("%s twice: got %d %s, want 400 naming it", name, rec.Code, rec.Body.String())
	}
}

// jsonLogger returns a logger that writes JSON lines to the buffer it also
// returns.
func jsonLogger() (*slog.Logger, *bytes.Buffer) {
	var logs bytes.Buffer

	return slog.New(slog.NewJSONHandler(&logs, nil)), &logs
}

// checkLog requires logs to hold one JSON line per entry of want, with the
// values that entry names, and returns the lines. A nil value requires the
// key to be absent.
func checkLog(t *testing.T, logs *bytes.Buffer, want ...map[string]any) []map[string]any {
	t.Helper()

	raw := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(raw) != len(want) {
		t.Fatalf("log %q, want %d lines", logs.String(), len(want))
	}

	lines := make([]map[string]any, len(raw))
	for i, w := range want {
		if err := json.Unmarshal([]byte(raw[i]), &lines[i]); err != nil {
			t.Fatalf("line %d: %v: %q", i+1, err, raw[i])
		}

		for key, value := range w {
			if lines[i][key] != value {
				t.Errorf("line %d: %s %v, want %v: %q", i+1, key, lines[i][key], value, raw[i])
			}
		}
	}

	return lines
}

type route struct {
	method, path string
	status       int
	code         string
	allow        string
}

var apiRoutes = []route{
	{
		method: http.MethodGet,
		path:   "/hash",
		status: http.StatusBadRequest,
		code:   "bad_request",
		allow:  "",
	},
	{
		method: http.MethodHead,
		path:   "/hash",
		status: http.StatusBadRequest,
		code:   "bad_request",
		allow:  "",
	},
	{
		method: http.MethodPost,
		path:   "/hash",
		status: http.StatusUnprocessableEntity,
		code:   "unreadable_image",
		allow:  "",
	},
	{
		method: http.MethodDelete,
		path:   "/hash",
		status: http.StatusMethodNotAllowed,
		code:   "method_not_allowed",
		allow:  "GET, HEAD, POST",
	},
	{
		method: http.MethodGet,
		path:   "/v1/hash",
		status: http.StatusNotFound,
		code:   "not_found",
		allow:  "",
	},
	{
		method: http.MethodGet,
		path:   "/debug/pprof/",
		status: http.StatusNotFound,
		code:   "not_found",
		allow:  "",
	},
}

var prefixedRoutes = []route{
	{
		method: http.MethodGet,
		path:   "/v1/hash",
		status: http.StatusBadRequest,
		code:   "bad_request",
		allow:  "",
	},
	{
		method: http.MethodPost,
		path:   "/v1/hash",
		status: http.StatusUnprocessableEntity,
		code:   "unreadable_image",
		allow:  "",
	},
	{
		method: http.MethodHead,
		path:   "/v1/hash",
		status: http.StatusBadRequest,
		code:   "bad_request",
		allow:  "",
	},
	{
		method: http.MethodGet,
		path:   "/hash",
		status: http.StatusNotFound,
		code:   "not_found",
		allow:  "",
	},
	{
		method: http.MethodGet,
		path:   "/v1/healthz",
		status: http.StatusNotFound,
		code:   "not_found",
		allow:  "",
	},
}

var probeRoutes = []route{
	{
		method: http.MethodGet,
		path:   "/healthz",
		status: http.StatusOK,
		code:   "",
		allow:  "",
	},
	{
		method: http.MethodHead,
		path:   "/healthz",
		status: http.StatusOK,
		code:   "",
		allow:  "",
	},
	{
		method: http.MethodGet,
		path:   "/readyz",
		status: http.StatusOK,
		code:   "",
		allow:  "",
	},
	{
		method: http.MethodHead,
		path:   "/readyz",
		status: http.StatusOK,
		code:   "",
		allow:  "",
	},
	{
		method: http.MethodPost,
		path:   "/readyz",
		status: http.StatusMethodNotAllowed,
		code:   "method_not_allowed",
		allow:  "GET, HEAD",
	},
}

// checkRoute sends rt's request to h and requires rt's status and Allow
// header, nosniff, and, when rt names an error code, a JSON error body with
// that code and a message.
func checkRoute(t *testing.T, h http.Handler, rt route) {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(rt.method, rt.path, nil))

	if rec.Code != rt.status {
		t.Errorf("%s %s: status %d, want %d", rt.method, rt.path, rec.Code, rt.status)
	}

	if got := rec.Header().Get("Allow"); got != rt.allow {
		t.Errorf("%s %s: Allow %q, want %q", rt.method, rt.path, got, rt.allow)
	}

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("%s %s: X-Content-Type-Options %q, want nosniff", rt.method, rt.path, got)
	}

	if rt.code == "" {
		return
	}

	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil ||
		body.Error.Code != rt.code ||
		body.Error.Message == "" {
		t.Errorf("%s %s: body %q, want error code %q", rt.method, rt.path, rec.Body.String(), rt.code)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("%s %s: Content-Type %q", rt.method, rt.path, ct)
	}
}
