package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"syscall"
	"testing"

	"github.com/superiority-dev/blurb/internal/fetch"
	"github.com/superiority-dev/blurb/internal/header"
	"github.com/superiority-dev/blurb/internal/queue"
)

// TestClassify requires each error to map to its status, code and message,
// and a decode failure caused by another error to map as its cause.
func TestClassify(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		status  int
		code    string
		message string
	}{
		{
			name:    "decode cut by the timeout",
			err:     errors.Join(header.ErrUnreadable, context.DeadlineExceeded),
			status:  http.StatusGatewayTimeout,
			code:    "upstream_timeout",
			message: "the origin did not answer in time",
		},
		{
			name:    "decode cut by the byte limit",
			err:     errors.Join(header.ErrUnreadable, &http.MaxBytesError{Limit: 1}),
			status:  http.StatusRequestEntityTooLarge,
			code:    "too_large",
			message: "the image is larger than the byte limit",
		},
		{
			name:    "too many pixels",
			err:     errTooManyPixels,
			status:  http.StatusRequestEntityTooLarge,
			code:    "too_large",
			message: "the image has more pixels than the limit",
		},
		{
			name:    "decode needs too much memory",
			err:     queue.ErrTooLarge,
			status:  http.StatusRequestEntityTooLarge,
			code:    "too_large",
			message: "the image needs more memory to decode than the service may use",
		},
		{
			name:    "header too large",
			err:     header.ErrTooLarge,
			status:  http.StatusRequestEntityTooLarge,
			code:    "too_large",
			message: "the image header is too large",
		},
		{
			name:    "decode of a request body cut short",
			err:     errors.Join(header.ErrUnreadable, errBodyCut),
			status:  http.StatusBadRequest,
			code:    "bad_request",
			message: "the request body ended early",
		},
		{
			name:    "decode of a malformed request body",
			err:     errors.Join(header.ErrUnreadable, errBodyMalformed),
			status:  http.StatusBadRequest,
			code:    "bad_request",
			message: "the request body is malformed",
		},
		{
			name:    "decode of an origin body cut short",
			err:     errors.Join(header.ErrUnreadable, fetch.ErrOriginCut),
			status:  http.StatusBadGateway,
			code:    "upstream_failed",
			message: "the origin closed before sending the whole image",
		},
		{
			name:    "decode of a malformed origin body",
			err:     errors.Join(header.ErrUnreadable, fetch.ErrOriginMalformed),
			status:  http.StatusBadGateway,
			code:    "upstream_failed",
			message: "the origin sent a malformed body",
		},
		{
			name:    "bad URL",
			err:     fetch.ErrBadURL,
			status:  http.StatusBadRequest,
			code:    "bad_request",
			message: "url must be an http or https URL without credentials",
		},
		{
			name:    "host not allowed",
			err:     fetch.ErrHostNotAllowed,
			status:  http.StatusForbidden,
			code:    "host_not_allowed",
			message: "the image host is not allowed",
		},
		{
			name:    "address not allowed",
			err:     fetch.ErrAddressNotAllowed,
			status:  http.StatusForbidden,
			code:    "host_not_allowed",
			message: "the image host resolves to an address that is not allowed",
		},
		{
			name:    "unsupported feature",
			err:     &header.UnsupportedFeatureError{Format: "jpeg", Feature: "precision"},
			status:  http.StatusUnsupportedMediaType,
			code:    "unsupported_media_type",
			message: "the image uses a jpeg feature that is not supported: precision",
		},
		{
			name:    "unsupported format",
			err:     header.ErrUnsupportedFormat,
			status:  http.StatusUnsupportedMediaType,
			code:    "unsupported_media_type",
			message: "the image format is not supported; JPEG, PNG, GIF and WebP are",
		},
		{
			name:    "unreadable",
			err:     header.ErrUnreadable,
			status:  http.StatusUnprocessableEntity,
			code:    "unreadable_image",
			message: "the image could not be decoded",
		},
		{
			name:    "origin not found",
			err:     &fetch.OriginStatusError{Status: http.StatusNotFound},
			status:  http.StatusNotFound,
			code:    "upstream_not_found",
			message: "the origin has no image at that URL; it answered 404",
		},
		{
			name:    "origin gone",
			err:     &fetch.OriginStatusError{Status: http.StatusGone},
			status:  http.StatusNotFound,
			code:    "upstream_not_found",
			message: "the origin has no image at that URL; it answered 410",
		},
		{
			name:    "origin failed",
			err:     &fetch.OriginStatusError{Status: http.StatusInternalServerError},
			status:  http.StatusBadGateway,
			code:    "upstream_failed",
			message: "the origin answered 500",
		},
		{
			name:    "too many redirects",
			err:     fetch.ErrTooManyRedirects,
			status:  http.StatusBadGateway,
			code:    "upstream_failed",
			message: "the origin redirected too many times",
		},
		{
			name:    "bad redirect",
			err:     fetch.ErrBadRedirect,
			status:  http.StatusBadGateway,
			code:    "upstream_failed",
			message: "the origin redirected to a URL blurb does not fetch",
		},
		{
			name:    "queue timeout",
			err:     queue.ErrTimeout,
			status:  http.StatusServiceUnavailable,
			code:    "unavailable",
			message: "the request waited too long for a decode; retry shortly",
		},
		{
			name:    "busy",
			err:     queue.ErrBusy,
			status:  http.StatusServiceUnavailable,
			code:    "unavailable",
			message: "too many requests are waiting; retry shortly",
		},
		{
			name:    "request ended",
			err:     context.Canceled,
			status:  http.StatusServiceUnavailable,
			code:    "unavailable",
			message: "the request ended before it was served",
		},
		{
			name:    "origin unreachable",
			err:     &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED},
			status:  http.StatusBadGateway,
			code:    "upstream_failed",
			message: "the origin could not be reached",
		},
		{
			name:    "anything else",
			err:     errors.New("boom"),
			status:  http.StatusInternalServerError,
			code:    "internal",
			message: messageInternal,
		},
	}

	for _, tc := range cases {
		status, code, message := classify(tc.err)
		if status != tc.status || code != tc.code || message != tc.message {
			t.Errorf("%s: %d %s %q, want %d %s %q", tc.name, status, code, message, tc.status, tc.code, tc.message)
		}
	}
}
