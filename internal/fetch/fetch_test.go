package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/iotest"
	"time"
)

// TestNew requires [New] to refuse a timeout or maxBytes that is not positive.
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

	fetcher := newFetcher(t, testTimeout, testMaxBytes, Options{BaseURL: base})

	if got := fetchAll(t, fetcher, "a/b.jpg"); got != "/images/a/b.jpg" {
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

	fetcher := newFetcher(t, testTimeout, testMaxBytes, Options{AllowedHosts: []string{"images.example.com"}})

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
		if _, _, err := fetcher.Fetch(context.Background(), tc.rawURL); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.rawURL, err, tc.want)
		}
	}
}

// TestFetchPrivateAddress requires the dialer to refuse a loopback address
// when private addresses are not allowed, even for an allowed host.
func TestFetchPrivateAddress(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	fetcher, err := New(testTimeout, testMaxBytes, Options{AllowedHosts: []string{testHost}})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := fetcher.Fetch(context.Background(), srv.URL+"/a.jpg"); !errors.Is(err, ErrAddressNotAllowed) {
		t.Errorf("got %v, want ErrAddressNotAllowed", err)
	}
}

// TestFetchStatus requires every origin status but 200 to be an
// [OriginStatusError] with that status, a partial or empty answer included.
func TestFetchStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		if err != nil {
			t.Error(err)
		}

		w.WriteHeader(status)
		fmt.Fprint(w, "part of an image")
	}))
	defer srv.Close()

	fetcher := newFetcher(t, testTimeout, testMaxBytes, Options{})
	statuses := []int{
		http.StatusPartialContent,
		http.StatusNoContent,
		http.StatusNotFound,
		http.StatusInternalServerError,
	}

	for _, status := range statuses {
		var se *OriginStatusError

		_, _, err := fetcher.Fetch(context.Background(), srv.URL+"/"+strconv.Itoa(status))
		if !errors.As(err, &se) || se.Status != status {
			t.Errorf("origin %d: got %v, want an OriginStatusError with %d", status, err, status)
		}
	}
}

// TestFetchRedirects requires each redirect's URL and host to be checked
// like the first request's, at most maxRedirects redirects to be followed,
// and none when [Options.FollowRedirects] is false.
func TestFetchRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/image", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("/to-image", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/image", http.StatusFound)
	})
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
		if _, _, err := follow.Fetch(context.Background(), srv.URL+path); !errors.Is(err, want) {
			t.Errorf("%s: got %v, want %v", path, err, want)
		}
	}

	var se *OriginStatusError

	_, _, err := newFetcher(t, testTimeout, testMaxBytes, Options{}).Fetch(context.Background(), srv.URL+"/to-image")
	if !errors.As(err, &se) || se.Status != http.StatusFound {
		t.Errorf("not following: got %v, want an OriginStatusError with 302", err)
	}
}

// TestFetchHeaderLimit requires response headers 1 KiB under
// maxResponseHeaderBytes to be read and 1 KiB over it to be refused.
func TestFetchHeaderLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/pad/"))
		w.Header().Set("X-Pad", strings.Repeat("a", n))
	}))
	defer srv.Close()

	const under, over = 63 << 10, 65 << 10

	fetcher := newFetcher(t, testTimeout, testMaxBytes, Options{})
	fetchAll(t, fetcher, srv.URL+"/pad/"+strconv.Itoa(under))

	if _, _, err := fetcher.Fetch(context.Background(), srv.URL+"/pad/"+strconv.Itoa(over)); err == nil {
		t.Error("65 KiB of response headers accepted")
	}
}

// TestFetchMaxBytes requires an image larger than maxBytes to fail, by its
// Content-Length when it declares one and while the body is read otherwise,
// and the declared length to be the Content-Length, or -1 without one.
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

	sized, declared, err := exact.Fetch(context.Background(), srv.URL+"/sized")
	if err != nil {
		t.Fatal(err)
	}
	defer sized.Close()

	if n, err := io.Copy(io.Discard, sized); err != nil || n != size || declared != size {
		t.Errorf("at maxBytes: %d bytes, %v, declared %d; want %d, nil and %d", n, err, declared, size, size)
	}

	small := newFetcher(t, testTimeout, size-1, Options{})
	var tooLarge *http.MaxBytesError

	if _, _, err := small.Fetch(context.Background(), srv.URL+"/sized"); !errors.As(err, &tooLarge) ||
		tooLarge.Limit != size-1 {
		t.Errorf("by Content-Length: got %v, want an *http.MaxBytesError with limit %d", err, size-1)
	}

	chunked, declared, err := small.Fetch(context.Background(), srv.URL+"/chunked")
	if err != nil {
		t.Fatal(err)
	}
	defer chunked.Close()

	n, err := io.Copy(io.Discard, chunked)
	if !errors.As(err, &tooLarge) || tooLarge.Limit != size-1 || n != size-1 || declared != -1 {
		t.Errorf("while reading: %d bytes, %v, declared %d; want %d, an *http.MaxBytesError and -1",
			n, err, declared, size-1)
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
		fetcher  *Fetcher
		deadline time.Duration
		path     string
	}{
		{name: "timeout", fetcher: short, deadline: time.Minute, path: "/slow-headers"},
		{name: "timeout in the body", fetcher: short, deadline: time.Minute, path: "/slow-body"},
		{name: "context deadline", fetcher: long, deadline: 50 * time.Millisecond, path: "/slow-headers"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), tc.deadline)
			defer cancel()

			errc := make(chan error, 1)

			go func() {
				image, _, err := tc.fetcher.Fetch(ctx, srv.URL+tc.path)
				if err == nil {
					_, err = io.ReadAll(image)
					image.Close()
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

// TestFetchSlowReader requires the time between a fetched body's reads not to
// count against the timeout.
func TestFetchSlowReader(t *testing.T) {
	// Larger than the socket buffers, so most of it is read after the wait.
	image := bytes.Repeat([]byte("x"), slowReaderBodyBytes)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(image)
	}))
	defer srv.Close()

	fetcher := newFetcher(t, 50*time.Millisecond, slowReaderBodyBytes, Options{})

	fetched, _, err := fetcher.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer fetched.Close()

	time.Sleep(200 * time.Millisecond)

	b, err := io.ReadAll(fetched)
	if err != nil || !bytes.Equal(b, image) {
		t.Errorf("read 200ms after a fetch with a 50ms timeout: %d bytes, %v; want %d", len(b), err, len(image))
	}
}

// TestFetchBodyErrors requires a body that ends before its Content-Length to
// fail with [ErrOriginCut], and one with broken chunked encoding with
// [ErrOriginMalformed].
func TestFetchBodyErrors(t *testing.T) {
	cases := []struct {
		name     string
		response string
		want     error
	}{
		{
			name:     "cut",
			response: "HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nabc",
			want:     ErrOriginCut,
		},
		{
			name:     "bad chunk size",
			response: "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\nzz\r\n",
			want:     ErrOriginMalformed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				conn, rw, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}

				fmt.Fprint(rw, tc.response)

				if err := errors.Join(rw.Flush(), conn.Close()); err != nil {
					t.Error(err)
				}
			}))
			defer srv.Close()

			fetcher := newFetcher(t, testTimeout, testMaxBytes, Options{})

			image, _, err := fetcher.Fetch(context.Background(), srv.URL+"/a.jpg")
			if err != nil {
				t.Fatal(err)
			}
			defer image.Close()

			if _, err := io.ReadAll(image); !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// TestBodyRead requires a read error to also match [ErrOriginCut] when the
// body ended early and [ErrOriginMalformed] otherwise, except a network error,
// a cancellation, a passed deadline or an [*http.MaxBytesError], which match
// neither.
func TestBodyRead(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		class error
	}{
		{name: "cut", err: io.ErrUnexpectedEOF, class: ErrOriginCut},
		{name: "framing", err: errors.New("invalid byte in chunk length"), class: ErrOriginMalformed},
		{name: "network", err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}},
		{name: "canceled", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "too large", err: &http.MaxBytesError{Limit: testMaxBytes}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fetchedBody := &body{r: io.NopCloser(iotest.ErrReader(tc.err)), dl: newDeadline(time.Hour, func(error) {})}
			_, err := fetchedBody.Read(make([]byte, 1))
			if !errors.Is(err, tc.err) {
				t.Fatalf("got %v, want it to wrap %v", err, tc.err)
			}

			for _, class := range []error{ErrOriginCut, ErrOriginMalformed} {
				if want := class == tc.class; errors.Is(err, class) != want {
					t.Errorf("got %v; matching %v is %t, want %t", err, class, !want, want)
				}
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

const (
	testHost     = "127.0.0.1"
	testTimeout  = 5 * time.Second
	testMaxBytes = 1 << 20 // 1 MiB

	slowReaderBodyBytes = 16 << 20 // 16 MiB
)

// newFetcher returns [New]'s [Fetcher] and fails t on an error. It allows
// private addresses, and testHost when opts names no hosts.
func newFetcher(t *testing.T, timeout time.Duration, maxBytes int64, opts Options) *Fetcher {
	t.Helper()

	if opts.AllowedHosts == nil {
		opts.AllowedHosts = []string{testHost}
	}

	opts.AllowPrivateAddresses = true

	fetcher, err := New(timeout, maxBytes, opts)
	if err != nil {
		t.Fatal(err)
	}

	return fetcher
}

func fetchAll(t *testing.T, fetcher *Fetcher, rawURL string) string {
	t.Helper()

	image, _, err := fetcher.Fetch(context.Background(), rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()

	got, err := io.ReadAll(image)
	if err != nil {
		t.Fatal(err)
	}

	return string(got)
}
