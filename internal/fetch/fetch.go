// Package fetch gets images over HTTP and HTTPS from allowed hosts only.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/superiority-dev/blurb/internal/ssrf"
)

const (
	maxRedirects           = 5
	maxResponseHeaderBytes = 64 << 10 // 64 KiB
)

var (
	// ErrBadURL is returned for a URL that does not parse, is relative while
	// [Options.BaseURL] is nil, or is not [Fetchable].
	ErrBadURL = errors.New("fetch: bad image URL")

	// ErrBadRedirect is returned when the origin redirects to a URL that is
	// not [Fetchable].
	ErrBadRedirect = errors.New("fetch: bad redirect URL")

	// ErrHostNotAllowed is returned when the URL's host, or a redirect's, is
	// not in the allowlist.
	ErrHostNotAllowed = errors.New("fetch: host not allowed")

	// ErrAddressNotAllowed is returned when [Options.AllowPrivateAddresses] is
	// false and an allowed host's address is one [ssrf.Control] refuses, such
	// as 127.0.0.1, 10.0.0.5 or a 6to4 address (2002::/16).
	ErrAddressNotAllowed = errors.New("fetch: address not allowed")

	// ErrTooManyRedirects is returned after more than 5 redirects.
	ErrTooManyRedirects = errors.New("fetch: too many redirects")

	// ErrOriginCut is returned when the body of a fetched image ends before
	// all of it arrived.
	ErrOriginCut = errors.New("fetch: origin body ended early")

	// ErrOriginMalformed is returned when the body of a fetched image is
	// malformed, such as broken chunked encoding.
	ErrOriginMalformed = errors.New("fetch: origin body is malformed")
)

// OriginStatusError is returned when the origin answers with a status other
// than 200 OK.
type OriginStatusError struct {
	Status int
}

func (e *OriginStatusError) Error() string {
	return fmt.Sprintf("fetch: origin answered %d", e.Status)
}

// Options holds a [Fetcher]'s optional settings.
type Options struct {
	// AllowedHosts are the hosts images may be fetched from: a host name or IP
	// address, "*.example.com" for every host name under example.com, or "*"
	// for any host.
	AllowedHosts []string

	// BaseURL resolves relative URLs; nil means URLs must be absolute.
	BaseURL *url.URL

	// FollowRedirects, if true, makes [Fetcher.Fetch] follow up to 5 redirects,
	// each checked as the first request is; if false, a redirect is returned as
	// an [*OriginStatusError].
	FollowRedirects bool

	// AllowPrivateAddresses lets fetches connect to any address, loopback and
	// private networks included.
	AllowPrivateAddresses bool
}

// Fetcher is an HTTP client for images on the allowed hosts. It is safe for
// concurrent use. A Fetcher must be created with [New].
type Fetcher struct {
	client    *http.Client
	allowlist allowlist
	baseURL   *url.URL
	timeout   time.Duration
	maxBytes  int64
}

// New returns a [Fetcher]. timeout is the time limit for each fetch, including
// connection time, any redirects, and reading the body. maxBytes is the largest
// image a fetch reads. It returns an error unless both are positive.
func New(timeout time.Duration, maxBytes int64, opts Options) (*Fetcher, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("timeout must be more than zero, got %v", timeout)
	}

	if maxBytes <= 0 {
		return nil, fmt.Errorf("maxBytes must be more than zero, got %d", maxBytes)
	}

	fetcher := &Fetcher{
		allowlist: newAllowlist(opts.AllowedHosts),
		baseURL:   opts.BaseURL,
		timeout:   timeout,
		maxBytes:  maxBytes,
	}

	dialer := &net.Dialer{}
	if !opts.AllowPrivateAddresses {
		dialer.Control = ssrf.Control
	}

	// No proxy: the address check must see the origin's address.
	transport := &http.Transport{
		DialContext:            dialer.DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           100,
		IdleConnTimeout:        90 * time.Second,
		MaxResponseHeaderBytes: maxResponseHeaderBytes,
	}

	fetcher.client = &http.Client{
		Transport:     transport,
		CheckRedirect: fetcher.checkRedirect(opts.FollowRedirects),
	}

	return fetcher, nil
}

// Fetch gets the image at rawURL, resolved against [Options.BaseURL]. The
// image result is the response body: it must be closed, a read past [New]'s
// maxBytes returns an [*http.MaxBytesError], and its PauseDeadline and
// ResumeDeadline methods pause and resume [New]'s timeout. The declared
// result is the response's Content-Length, or -1 when it is unknown.
//
// The error matches one of [ErrBadURL], [ErrBadRedirect], [ErrHostNotAllowed],
// [ErrAddressNotAllowed] and [ErrTooManyRedirects] under [errors.Is], or is
// an [*OriginStatusError], an [*http.MaxBytesError] or a transport error.
// When the timeout or ctx's deadline passes, Fetch or a body read returns an
// error matching [context.DeadlineExceeded]. A body read can also return an
// error matching [ErrOriginCut] or [ErrOriginMalformed].
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (image io.ReadCloser, declared int64, err error) {
	u, err := f.resolve(rawURL)
	if err != nil {
		return nil, 0, err
	}

	if !f.allowlist.allows(u.Hostname()) {
		return nil, 0, ErrHostNotAllowed
	}

	ctx, cancel := context.WithCancelCause(ctx)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		cancel(nil)
		return nil, 0, ErrBadURL
	}

	dl := newDeadline(f.timeout, cancel)

	resp, err := f.client.Do(req)
	if err != nil {
		dl.stop()

		if errors.Is(err, ssrf.ErrNotAllowed) {
			return nil, 0, ErrAddressNotAllowed
		}

		return nil, 0, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		dl.stop()

		return nil, 0, &OriginStatusError{Status: resp.StatusCode}
	}

	if resp.ContentLength > f.maxBytes {
		resp.Body.Close()
		dl.stop()

		return nil, 0, &http.MaxBytesError{Limit: f.maxBytes}
	}

	limited := http.MaxBytesReader(nil, resp.Body, f.maxBytes)

	return &body{r: limited, dl: dl}, resp.ContentLength, nil
}

func (f *Fetcher) resolve(rawURL string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, ErrBadURL
	}

	if !u.IsAbs() {
		if f.baseURL == nil {
			return nil, ErrBadURL
		}

		u = f.baseURL.ResolveReference(u)
	}

	if !Fetchable(u) {
		return nil, ErrBadURL
	}

	return u, nil
}

func (f *Fetcher) checkRedirect(follow bool) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if !follow {
			return http.ErrUseLastResponse
		}

		if len(via) > maxRedirects {
			return ErrTooManyRedirects
		}

		if !Fetchable(req.URL) {
			return ErrBadRedirect
		}

		if !f.allowlist.allows(req.URL.Hostname()) {
			return ErrHostNotAllowed
		}

		return nil
	}
}

// body holds a fetch's response body and its deadline; closing it ends the
// fetch.
type body struct {
	r  io.ReadCloser
	dl *deadline
}

func (b *body) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err == nil || err == io.EOF {
		return n, err
	}

	_, transport := errors.AsType[net.Error](err)
	_, tooLarge := errors.AsType[*http.MaxBytesError](err)

	if transport || tooLarge || errors.Is(err, context.Canceled) {
		return n, err
	}

	class := ErrOriginMalformed
	if errors.Is(err, io.ErrUnexpectedEOF) {
		class = ErrOriginCut
	}

	return n, fmt.Errorf("%w: %w", class, err)
}

// PauseDeadline stops counting time against the timeout until
// ResumeDeadline, unless the timeout has already run out.
func (b *body) PauseDeadline() { b.dl.pause() }

// ResumeDeadline starts counting time against the timeout again, from where
// PauseDeadline stopped.
func (b *body) ResumeDeadline() { b.dl.resume() }

func (b *body) Close() error {
	defer b.dl.stop()
	return b.r.Close()
}

// Fetchable reports whether u is an absolute http or https URL with a host
// and no credentials.
func Fetchable(u *url.URL) bool {
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}
