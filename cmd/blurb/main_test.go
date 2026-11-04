package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/superiority-dev/blurb/internal/testenv"
	"github.com/superiority-dev/blurb/internal/testimage"
	"github.com/superiority-dev/blurhash"
)

// TestRunServes requires the service to listen, report ready, apply its path
// prefix, pixel limit, BlurHash components, request id header and body
// deadline, serve the probes outside the prefix, and stop cleanly when its
// context ends, logging its shutdown timeout in nanoseconds.
func TestRunServes(t *testing.T) {
	// A soft memory limit of 64 MiB, as GOMEMLIMIT sets it, below any cgroup
	// limit a test machine sets.
	defer debug.SetMemoryLimit(debug.SetMemoryLimit(64 << 20))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logs := make(lines, 64)
	done := make(chan error, 1)

	go func() {
		done <- run(ctx, nil, testenv.Lookup(
			"BLURB_ADDR", "127.0.0.1:0",
			"BLURB_PATH_PREFIX", "/v1",
			"BLURB_REQUEST_ID_HEADER", "X-Amz-Cf-Id",
			"BLURB_ALLOWED_HOSTS", "images.example.com",
			"BLURB_MAX_MEGAPIXELS", "1",
			"BLURB_BLURHASH_COMPONENTS", "5x2",
			"BLURB_MAX_CONCURRENT_DECODES", "3",
			"BLURB_QUEUE_TIMEOUT", "2s",
			"BLURB_SHUTDOWN_TIMEOUT", "3s",
		), logs)
	}()

	addr, before := listening(t, logs, done)

	// MAX_WAITING_REQUESTS unset: four waiting per decode. The decodes get
	// three quarters of the limit, and the soft limit stays GOMEMLIMIT's.
	if len(before) == 0 ||
		before[0]["memory_limit_bytes"] != float64(64<<20) ||
		before[0]["memory_limit_source"] != "GOMEMLIMIT" ||
		before[0]["soft_memory_limit_bytes"] != float64(64<<20) ||
		before[0]["decode_memory_bytes"] != float64(48<<20) ||
		before[0]["max_concurrent_decodes"] != float64(3) ||
		before[0]["max_waiting_requests"] != float64(12) ||
		before[0]["queue_timeout_ns"] != float64(2*time.Second) {
		t.Errorf(
			"starting line %v, want memory_limit_bytes 64 MiB from GOMEMLIMIT, soft_memory_limit_bytes 64 MiB, "+
				"decode_memory_bytes 48 MiB, max_concurrent_decodes 3, max_waiting_requests 12 "+
				"and queue_timeout_ns 2s",
			before,
		)
	}

	resp, err := http.Get("http://" + addr + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	// An unread body can leave Shutdown waiting 5 s on a new connection.
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("readyz %d", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/hash", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Amz-Cf-Id", "cf-1")

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if got := resp.Header.Get("X-Amz-Cf-Id"); got != "cf-1" {
		t.Errorf("X-Amz-Cf-Id %q, want cf-1", got)
	}

	post := func(body io.Reader) (status int, hash string) {
		t.Helper()

		resp, err := http.Post("http://"+addr+"/v1/hash", "image/jpeg", body)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		var res struct {
			BlurHash string `json:"blurhash"`
		}

		_ = json.NewDecoder(resp.Body).Decode(&res)
		_, _ = io.Copy(io.Discard, resp.Body)

		return resp.StatusCode, res.BlurHash
	}

	if got, _ := post(bytes.NewReader(testimage.GradientJPEG(t, 1001, 1000))); got != http.StatusRequestEntityTooLarge {
		t.Errorf("1001 by 1000 under a 1 MP limit: %d, want 413", got)
	}

	// The body arrives in two parts, the second well inside FETCH_TIMEOUT.
	photo := testimage.GradientJPEG(t, 64, 48)
	pr, pw := io.Pipe()

	go func() {
		_, _ = pw.Write(photo[:len(photo)/2])
		time.Sleep(100 * time.Millisecond)
		_, _ = pw.Write(photo[len(photo)/2:])
		_ = pw.Close()
	}()

	status, hash := post(pr)
	if x, y, err := blurhash.Components(hash); status != http.StatusOK || err != nil || x != 5 || y != 2 {
		t.Errorf("64 by 48: %d, %q with %d by %d components (%v); want 200 and 5 by 2", status, hash, x, y, err)
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not stop")
	}

	var stopping map[string]any

	for len(logs) > 0 {
		var record map[string]any
		if err := json.Unmarshal([]byte(<-logs), &record); err == nil && record[slog.MessageKey] == "shutting down" {
			stopping = record
		}
	}

	if stopping["shutdown_timeout_ns"] != float64(3*time.Second) {
		t.Errorf("shutting down line %v, want shutdown_timeout_ns 3s", stopping)
	}
}

// TestRunDrains requires a stop to fail the readiness probe at once, by 503
// or by a refused connection, and to let an in-flight request finish before
// run returns.
func TestRunDrains(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	photo := testimage.GradientJPEG(t, 64, 48)

	var releaseOnce sync.Once

	unblock := func() { releaseOnce.Do(func() { close(release) }) }

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		fmt.Fprintf(w, "%s", photo)
	}))
	defer origin.Close()
	defer unblock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logs := make(lines, 64)
	done := make(chan error, 1)

	go func() {
		done <- run(ctx, nil, testenv.Lookup(
			"BLURB_ADDR", "127.0.0.1:0",
			"BLURB_ALLOWED_HOSTS", "127.0.0.1",
			"BLURB_ALLOW_PRIVATE_ADDRESSES", "true",
			"BLURB_MAX_MEGAPIXELS", "1",
			"BLURB_MAX_AGE", "1h",
		), logs)
	}()

	addr, _ := listening(t, logs, done)

	inflight := make(chan *http.Response, 1)

	go func() {
		resp, err := http.Get("http://" + addr + "/hash?url=" + url.QueryEscape(origin.URL+"/a.jpg"))
		if err != nil {
			inflight <- nil
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		inflight <- resp
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request did not reach the origin within 5s")
	}

	cancel()

	for deadline := time.Now().Add(5 * time.Second); ; {
		resp, err := http.Get("http://" + addr + "/readyz")
		if err != nil {
			break
		}

		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusServiceUnavailable {
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("readyz still answered 200 during the drain")
		}
	}

	unblock()

	resp := <-inflight
	if resp == nil {
		t.Fatal("in-flight request failed")
	}

	cc := resp.Header.Get("Cache-Control")
	if resp.StatusCode != http.StatusOK || cc != "public, max-age=3600, immutable" {
		t.Errorf("in-flight request: %d, Cache-Control %q; want 200 cached for an hour", resp.StatusCode, cc)
	}

	if err := <-done; err != nil {
		t.Errorf("run: %v", err)
	}
}

// TestRunBusy requires as many POSTs to be served at once as the decode queue
// can hold, and one more to answer 503 at once.
func TestRunBusy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logs := make(lines, 64)
	done := make(chan error, 1)

	go func() {
		done <- run(ctx, nil, testenv.Lookup(
			"BLURB_ADDR", "127.0.0.1:0",
			"BLURB_ALLOWED_HOSTS", "images.example.com",
			"BLURB_MAX_CONCURRENT_DECODES", "1",
			"BLURB_MAX_WAITING_REQUESTS", "1",
			"BLURB_QUEUE_TIMEOUT", "1s",
		), logs)
	}()

	addr, _ := listening(t, logs, done)
	photo := testimage.GradientJPEG(t, 64, 48)

	// A held POST gets its 100 Continue once its handler reads the body.
	var held []net.Conn

	for i := range 2 {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() {
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
		})

		fmt.Fprintf(conn, "POST /hash HTTP/1.1\r\nHost: blurb\r\nContent-Type: image/jpeg\r\n"+
			"Content-Length: %d\r\nExpect: 100-continue\r\n\r\n", len(photo))

		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusContinue {
			t.Fatalf("POST %d: got %d, want 100", i+1, resp.StatusCode)
		}

		held = append(held, conn)
	}

	resp, err := http.Post("http://"+addr+"/hash", "image/jpeg", bytes.NewReader(photo))
	if err != nil {
		t.Fatal(err)
	}

	var res struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	_ = json.NewDecoder(resp.Body).Decode(&res)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	const busy = "too many requests are in progress; retry shortly"
	if resp.StatusCode != http.StatusServiceUnavailable || res.Error.Message != busy {
		t.Errorf("POST 3: got %d %q, want 503 %q", resp.StatusCode, res.Error.Message, busy)
	}

	for i, conn := range held {
		if _, err := conn.Write(photo); err != nil {
			t.Fatal(err)
		}

		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("POST %d: got %d, want 200", i+1, resp.StatusCode)
		}
	}

	cancel()

	if err := <-done; err != nil {
		t.Errorf("run: %v", err)
	}
}

// TestRunMetrics requires the metrics of requests served before a stop to
// reach the OTLP collector when run returns.
func TestRunMetrics(t *testing.T) {
	bodies := make(chan []byte, 8)
	collector := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies <- b
	}))
	defer collector.Close()

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logs := make(lines, 64)
	done := make(chan error, 1)

	go func() {
		done <- run(ctx, nil, testenv.Lookup(
			"BLURB_ADDR", "127.0.0.1:0",
			"BLURB_ALLOWED_HOSTS", "images.example.com",
			"OTEL_METRICS_EXPORTER", "otlp",
		), logs)
	}()

	addr, _ := listening(t, logs, done)

	resp, err := http.Post("http://"+addr+"/hash", "image/jpeg", bytes.NewReader(testimage.GradientJPEG(t, 64, 48)))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST: got %d, want 200", resp.StatusCode)
	}

	cancel()

	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}

	var exported []byte
	for len(bodies) > 0 {
		exported = append(exported, <-bodies...)
	}

	for _, name := range []string{"blurb.hash.requests", "http.server.request.duration", "go.memory.used"} {
		if !bytes.Contains(exported, []byte(name)) {
			t.Errorf("the collector got no %s", name)
		}
	}
}

// TestRunAllowAll requires a warning when the allowlist is *.
func TestRunAllowAll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logs := make(lines, 64)
	done := make(chan error, 1)

	go func() {
		done <- run(ctx, nil, testenv.Lookup("BLURB_ADDR", "127.0.0.1:0", "BLURB_ALLOWED_HOSTS", "*"), logs)
	}()

	_, before := listening(t, logs, done)

	warned := false
	for _, record := range before {
		msg, _ := record[slog.MessageKey].(string)
		warn := record[slog.LevelKey] == slog.LevelWarn.String()
		warned = warned || (warn && strings.HasPrefix(msg, "the allowlist is *"))
	}

	if !warned {
		t.Errorf("lines before listening %v, want a warning for an allowlist of *", before)
	}

	cancel()

	if err := <-done; err != nil {
		t.Errorf("run: %v", err)
	}
}

func TestRunVersion(t *testing.T) {
	var out strings.Builder
	if err := run(context.Background(), []string{"-version"}, testenv.Lookup(), &out); err != nil {
		t.Fatal(err)
	}

	if out.String() != "blurb dev, commit unknown, built unknown\n" {
		t.Errorf("version output %q", out.String())
	}
}

// TestRunErrors requires an error that stops run to be its only output, one
// JSON line at ERROR, before and after the configured level applies.
func TestRunErrors(t *testing.T) {
	cases := []struct {
		args []string
		env  []string
		want string
	}{
		{args: []string{"-x"}, want: "flag provided but not defined: -x"},
		{want: "BLURB_ALLOWED_HOSTS is required"},
		{
			env:  []string{"BLURB_ADDR", "127.0.0.1:99999", "BLURB_ALLOWED_HOSTS", "*", "BLURB_LOG_LEVEL", "error"},
			want: "invalid port",
		},
		{
			env: []string{
				"BLURB_ALLOWED_HOSTS", "*", "BLURB_LOG_LEVEL", "error",
				"OTEL_METRICS_EXPORTER", "prometheus",
			},
			want: `OTEL_METRICS_EXPORTER must be otlp or none, got "prometheus"`,
		},
	}

	for _, tc := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

		var out strings.Builder
		err := run(ctx, tc.args, testenv.Lookup(tc.env...), &out)
		cancel()

		var record map[string]any
		if err == nil || json.Unmarshal([]byte(out.String()), &record) != nil ||
			record[slog.LevelKey] != slog.LevelError.String() ||
			!strings.Contains(fmt.Sprint(record["error"]), tc.want) {
			t.Errorf(
				"run(%q) with %q: %v, output %q; want only an ERROR line naming %q",
				tc.args,
				tc.env,
				err,
				out.String(),
				tc.want,
			)
		}
	}
}

func TestNewServer(t *testing.T) {
	s := newServer(http.NotFoundHandler(), slog.New(slog.DiscardHandler))
	if s.ReadHeaderTimeout != 10*time.Second || s.IdleTimeout != 2*time.Minute || s.ErrorLog == nil {
		t.Errorf(
			"got ReadHeaderTimeout %v, IdleTimeout %v, ErrorLog %v",
			s.ReadHeaderTimeout,
			s.IdleTimeout,
			s.ErrorLog,
		)
	}
}

// lines is a writer that hands each write, one log line, to a channel.
type lines chan string

func (l lines) Write(p []byte) (int, error) {
	l <- string(p)
	return len(p), nil
}

// listening reads run's log until it reports the address it listens on,
// and returns that address and the lines before it.
func listening(t *testing.T, logs lines, done chan error) (addr string, before []map[string]any) {
	t.Helper()

	for {
		var (
			record map[string]any
			line   string
		)

		select {
		case line = <-logs:
		case err := <-done:
			t.Fatalf("run returned before listening: %v", err)
		case <-time.After(10 * time.Second):
			t.Fatal("no listening line after 10s")
		}

		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("%v: %q", err, line)
		}

		if record[slog.MessageKey] == "listening" {
			addr, _ = record["addr"].(string)
			return addr, before
		}

		before = append(before, record)
	}
}
