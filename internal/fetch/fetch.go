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
)

// OriginStatusError reports an origin's answer other than 200 OK.
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

	// FollowRedirects follows up to 5 redirects, checking each hop
	// as the first request is checked; when false, a redirect is returned as
	// an [*OriginStatusError].
	FollowRedirects bool

	// AllowPrivateAddresses lets fetches connect to any address, loopback and
	// private networks included.
	AllowPrivateAddresses bool
}

// Fetcher fetches images. It is safe for concurrent use.
type Fetcher struct {
	client    *http.Client
	allowlist allowlist
	baseURL   *url.URL
	maxBytes  int64
}

// New returns a [Fetcher]. timeout is the time limit for each fetch,
// including connection time, any redirects, and reading the body. maxBytes
// is the largest image a fetch reads. It returns an error when either is not
// more than zero.
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
		Timeout:       timeout,
	}

	return fetcher, nil
}

// Fetch resolves rawURL against [Options.BaseURL], refuses the URL unless it
// is [Fetchable] and its host is allowed, and returns the image's body.
// The body reads at most [New]'s maxBytes and must be closed. Fetch's error
// matches one of [ErrBadURL], [ErrBadRedirect], [ErrHostNotAllowed],
// [ErrAddressNotAllowed] and [ErrTooManyRedirects] under [errors.Is], is an
// [*OriginStatusError] or an [*http.MaxBytesError], or is a transport error.
// Reading the body past maxBytes gives an [*http.MaxBytesError]. When [New]'s
// timeout expires or ctx reaches its deadline, Fetch or a read of the body
// returns an error matching [context.DeadlineExceeded].
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	u, err := f.resolve(rawURL)
	if err != nil {
		return nil, err
	}

	if !f.allowlist.allows(u.Hostname()) {
		return nil, ErrHostNotAllowed
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, ErrBadURL
	}

	resp, err := f.client.Do(req)
	if err != nil {
		if errors.Is(err, ssrf.ErrNotAllowed) {
			return nil, ErrAddressNotAllowed
		}

		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &OriginStatusError{Status: resp.StatusCode}
	}

	if resp.ContentLength > f.maxBytes {
		resp.Body.Close()
		return nil, &http.MaxBytesError{Limit: f.maxBytes}
	}

	return http.MaxBytesReader(nil, resp.Body, f.maxBytes), nil
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

// Fetchable reports whether u is an absolute http or https URL with a host
// and no credentials.
func Fetchable(u *url.URL) bool {
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}
