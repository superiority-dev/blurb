package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	testHost     = "127.0.0.1"
	testTimeout  = 5 * time.Second
	testMaxBytes = 1 << 20 // 1 MiB
)

// TestNew requires [New] to refuse a timeout or maxBytes that is not more
// than zero.
func TestNew(t *testing.T) {
	cases := []struct {
		timeout  time.Duration
		maxBytes int64
	}{
		{timeout: 0, maxBytes: testMaxBytes},
		{timeout: -time.Second, maxBytes: testMaxBytes},
		{timeout: testTimeout, maxBytes: 0},
		{timeout: testTimeout, maxBytes: -1},
	}

	for _, tc := range cases {
		if _, err := New(tc.timeout, tc.maxBytes, Options{}); err == nil {
			t.Errorf("New(%v, %d) accepted", tc.timeout, tc.maxBytes)
		}
	}
}

func TestFetchBaseURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.URL.Path)
	}))
	defer srv.Close()

	base, err := url.Parse(srv.URL + "/images/")
	if err != nil {
		t.Fatal(err)
	}

	f := newFetcher(t, testTimeout, testMaxBytes, Options{BaseURL: base})

	if got := fetchAll(t, f, "a/b.jpg"); got != "/images/a/b.jpg" {
		t.Errorf("fetched %q, want /images/a/b.jpg", got)
	}
}

// TestFetchRefusesBeforeSending requires a host outside the allowlist,
// and a bad URL, to be refused before a request is sent.
func TestFetchRefusesBeforeSending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a refused fetch reached the server")
	}))
	defer srv.Close()

	f := newFetcher(t, testTimeout, testMaxBytes, Options{AllowedHosts: []string{"images.example.com"}})

	cases := []struct {
		rawURL string
		want   error
	}{
		{rawURL: srv.URL + "/a.jpg", want: ErrHostNotAllowed},
		{rawURL: "/relative.jpg", want: ErrBadURL},
		{rawURL: "http://user:pass@images.example.com/a.jpg", want: ErrBadURL},
		{rawURL: "http://[::1", want: ErrBadURL},
	}

	for _, tc := range cases {
		if _, err := f.Fetch(context.Background(), tc.rawURL); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.rawURL, err, tc.want)
		}
	}
}

// TestFetchPrivateAddress requires the dialer to refuse a loopback address
// when private addresses are not allowed, even for an allowed host.
func TestFetchPrivateAddress(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	f, err := New(testTimeout, testMaxBytes, Options{AllowedHosts: []string{testHost}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.Fetch(context.Background(), srv.URL+"/a.jpg"); !errors.Is(err, ErrAddressNotAllowed) {
		t.Errorf("got %v, want ErrAddressNotAllowed", err)
	}
}

// TestFetchRedirects requires each redirect's URL and host to be checked
// like the first request's, at most maxRedirects redirects to be followed,
// and none when [Options.FollowRedirects] is false.
func TestFetchRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/image", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") })
	mux.HandleFunc("/to-image", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/image", http.StatusFound) })
	mux.HandleFunc("/to-elsewhere", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://elsewhere.example/image", http.StatusFound)
	})
	mux.HandleFunc("/to-ftp", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "ftp://"+r.Host+"/image", http.StatusFound)
	})
	mux.HandleFunc("/via/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.TrimPrefix(r.URL.Path, "/via"), http.StatusFound)
	})
	mux.HandleFunc("/chain/", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/chain/"))
		if n == 0 {
			fmt.Fprint(w, "ok")
			return
		}

		http.Redirect(w, r, "/chain/"+strconv.Itoa(n-1), http.StatusFound)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	follow := newFetcher(t, testTimeout, testMaxBytes, Options{FollowRedirects: true})

	followed := []string{"/to-image", "/chain/5"}

	for _, path := range followed {
		if got := fetchAll(t, follow, srv.URL+path); got != "ok" {
			t.Errorf("%s: got %q, want ok", path, got)
		}
	}

	refused := map[string]error{
		"/to-elsewhere":     ErrHostNotAllowed,
		"/via/to-elsewhere": ErrHostNotAllowed,
		"/to-ftp":           ErrBadRedirect,
		"/via/to-ftp":       ErrBadRedirect,
		"/chain/6":          ErrTooManyRedirects,
	}

	for path, want := range refused {
		if _, err := follow.Fetch(context.Background(), srv.URL+path); !errors.Is(err, want) {
			t.Errorf("%s: got %v, want %v", path, err, want)
		}
	}

	var se *OriginStatusError

	_, err := newFetcher(t, testTimeout, testMaxBytes, Options{}).Fetch(context.Background(), srv.URL+"/to-image")
	if !errors.As(err, &se) || se.Status != http.StatusFound {
		t.Errorf("not following: got %v, want an OriginStatusError with 302", err)
	}
}

// TestFetchHeaderLimit requires response headers just under
// maxResponseHeaderBytes to be read and just over it to be refused.
func TestFetchHeaderLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/pad/"))
		w.Header().Set("X-Pad", strings.Repeat("a", n))
	}))
	defer srv.Close()

	const under, over = 60 << 10, 70 << 10 // 60 KiB and 70 KiB

	f := newFetcher(t, testTimeout, testMaxBytes, Options{})
	fetchAll(t, f, srv.URL+"/pad/"+strconv.Itoa(under))

	if _, err := f.Fetch(context.Background(), srv.URL+"/pad/"+strconv.Itoa(over)); err == nil {
		t.Error("70 KiB of response headers accepted")
	}
}

func TestFetchOriginStatusError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	var se *OriginStatusError

	_, err := newFetcher(t, testTimeout, testMaxBytes, Options{}).Fetch(context.Background(), srv.URL+"/missing.jpg")
	if !errors.As(err, &se) || se.Status != http.StatusNotFound {
		t.Errorf("got %v, want an OriginStatusError with 404", err)
	}
}

// TestFetchMaxBytes requires an image larger than maxBytes to fail by its
// Content-Length and, without one, while the body is read.
func TestFetchMaxBytes(t *testing.T) {
	const size = 100

	big := strings.Repeat("x", size)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chunked" {
			// Flushing early sends the body chunked, with no Content-Length.
			fmt.Fprint(w, big[:size/2])
			w.(http.Flusher).Flush()
			fmt.Fprint(w, big[size/2:])

			return
		}

		fmt.Fprint(w, big)
	}))
	defer srv.Close()

	exact := newFetcher(t, testTimeout, size, Options{})
	if got := fetchAll(t, exact, srv.URL+"/sized"); len(got) != size {
		t.Errorf("read %d bytes at maxBytes, want %d", len(got), size)
	}

	small := newFetcher(t, testTimeout, size-1, Options{})
	var tooLarge *http.MaxBytesError

	if _, err := small.Fetch(context.Background(), srv.URL+"/sized"); !errors.As(err, &tooLarge) || tooLarge.Limit != size-1 {
		t.Errorf("by Content-Length: got %v, want an *http.MaxBytesError with limit %d", err, size-1)
	}

	b, err := small.Fetch(context.Background(), srv.URL+"/chunked")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	n, err := io.Copy(io.Discard, b)
	if !errors.As(err, &tooLarge) || tooLarge.Limit != size-1 || n != size-1 {
		t.Errorf("while reading: %d bytes, %v; want %d and an *http.MaxBytesError", n, err, size-1)
	}
}

// TestFetchTimeout requires the timeout, also while the body is read, and a
// context deadline to end a fetch with an error matching
// [context.DeadlineExceeded].
func TestFetchTimeout(t *testing.T) {
	release := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow-body" {
			// The headers and part of the body go out; the rest never comes.
			fmt.Fprint(w, "partial")
			w.(http.Flusher).Flush()
		}

		<-release
	}))
	defer srv.Close()
	defer close(release)

	short := newFetcher(t, 50*time.Millisecond, testMaxBytes, Options{})
	long := newFetcher(t, time.Minute, testMaxBytes, Options{})

	cases := []struct {
		name     string
		f        *Fetcher
		deadline time.Duration
		path     string
	}{
		{name: "timeout", f: short, deadline: time.Minute, path: "/slow-headers"},
		{name: "timeout in the body", f: short, deadline: time.Minute, path: "/slow-body"},
		{name: "context deadline", f: long, deadline: 50 * time.Millisecond, path: "/slow-headers"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), tc.deadline)
			defer cancel()

			errc := make(chan error, 1)

			go func() {
				b, err := tc.f.Fetch(ctx, srv.URL+tc.path)
				if err == nil {
					_, err = io.ReadAll(b)
					b.Close()
				}
				errc <- err
			}()

			select {
			case err := <-errc:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("got %v, want context.DeadlineExceeded", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("still running after 5s: nothing ends the fetch")
			}
		})
	}
}

func TestFetchable(t *testing.T) {
	cases := []struct {
		rawURL string
		want   bool
	}{
		{rawURL: "https://images.example.com/a.jpg", want: true},
		{rawURL: "HTTP://images.example.com", want: true},
		{rawURL: "ftp://images.example.com/a.jpg", want: false},
		{rawURL: "https:///a.jpg", want: false},
		{rawURL: "https://user:pass@images.example.com/a.jpg", want: false},
		{rawURL: "/a.jpg", want: false},
	}

	for _, tc := range cases {
		u, err := url.Parse(tc.rawURL)
		if err != nil {
			t.Fatal(err)
		}

		if got := Fetchable(u); got != tc.want {
			t.Errorf("Fetchable(%q) = %t, want %t", tc.rawURL, got, tc.want)
		}
	}
}

// newFetcher returns [New]'s [Fetcher] and fails t on an error. It allows
// private addresses, and testHost when o names no hosts.
func newFetcher(t *testing.T, timeout time.Duration, maxBytes int64, o Options) *Fetcher {
	t.Helper()

	if o.AllowedHosts == nil {
		o.AllowedHosts = []string{testHost}
	}

	o.AllowPrivateAddresses = true

	f, err := New(timeout, maxBytes, o)
	if err != nil {
		t.Fatal(err)
	}

	return f
}

func fetchAll(t *testing.T, f *Fetcher, rawURL string) string {
	t.Helper()

	b, err := f.Fetch(context.Background(), rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	got, err := io.ReadAll(b)
	if err != nil {
		t.Fatal(err)
	}

	return string(got)
}
