package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/superiority-dev/blurb/internal/fetch"
	"github.com/superiority-dev/blurb/internal/header"
	"github.com/superiority-dev/blurb/internal/queue"
)

// Error codes, the machine-readable field of every error body.
const (
	codeBadRequest       = "bad_request"
	codeHostNotAllowed   = "host_not_allowed"
	codeNotFound         = "not_found"
	codeMethodNotAllowed = "method_not_allowed"
	codeTooLarge         = "too_large"
	codeUnsupported      = "unsupported_media_type"
	codeUnreadable       = "unreadable_image"
	codeInternal         = "internal"
	codeUpstreamFailed   = "upstream_failed"
	codeUpstreamNotFound = "upstream_not_found"
	codeUnavailable      = "unavailable"
	codeNotReady         = "not_ready"
	codeUpstreamTimeout  = "upstream_timeout"
	codeBodyTimeout      = "body_timeout"
)

// messageInternal is the message of every 500, which gives no detail.
const messageInternal = "internal error"

var (
	errBodyTimeout   = errors.New("httpapi: request body was not read in time")
	errBodyCut       = errors.New("httpapi: request body ended early")
	errBodyMalformed = errors.New("httpapi: request body is malformed")
)

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func notFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, codeNotFound, "no such route")
}

// methodNotAllowed returns a handler that answers 405 with an Allow header
// listing allow, the methods the route takes.
func methodNotAllowed(allow ...string) http.HandlerFunc {
	list := strings.Join(allow, ", ")

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", list)
		writeError(w, http.StatusMethodNotAllowed, codeMethodNotAllowed, r.Method+" is not allowed here; use "+list)
	}
}

// classify maps an error from the fetch or the hash to its status, code and
// message. The order matters: a body cut short by the timeout or the byte
// limit also fails to decode.
func classify(err error) (status int, code, message string) {
	var (
		se        *fetch.OriginStatusError
		maxBytes  *http.MaxBytesError
		feature   *header.UnsupportedFeatureError
		transport net.Error
	)

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, codeUpstreamTimeout, "the origin did not answer in time"
	case errors.As(err, &maxBytes):
		return http.StatusRequestEntityTooLarge, codeTooLarge, "the image is larger than the byte limit"
	case errors.Is(err, errTooManyPixels):
		return http.StatusRequestEntityTooLarge, codeTooLarge, "the image has more pixels than the limit"
	case errors.Is(err, queue.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, codeTooLarge,
			"the image needs more memory to decode than the service may use"
	case errors.Is(err, header.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, codeTooLarge, "the image header is too large"
	case errors.Is(err, errBodyTimeout):
		return http.StatusRequestTimeout, codeBodyTimeout, "the request body was not read in time"
	case errors.Is(err, errBodyCut):
		return http.StatusBadRequest, codeBadRequest, "the request body ended early"
	case errors.Is(err, errBodyMalformed):
		return http.StatusBadRequest, codeBadRequest, "the request body is malformed"
	case errors.Is(err, fetch.ErrOriginCut):
		return http.StatusBadGateway, codeUpstreamFailed, "the origin closed before sending the whole image"
	case errors.Is(err, fetch.ErrOriginMalformed):
		return http.StatusBadGateway, codeUpstreamFailed, "the origin sent a malformed body"
	case errors.Is(err, fetch.ErrBadURL):
		return http.StatusBadRequest, codeBadRequest, paramURL + " must be an http or https URL without credentials"
	case errors.Is(err, fetch.ErrHostNotAllowed):
		return http.StatusForbidden, codeHostNotAllowed, "the image host is not allowed"
	case errors.Is(err, fetch.ErrAddressNotAllowed):
		return http.StatusForbidden, codeHostNotAllowed, "the image host resolves to an address that is not allowed"
	case errors.As(err, &feature):
		return http.StatusUnsupportedMediaType, codeUnsupported,
			fmt.Sprintf("the image uses a %s feature that is not supported: %s", feature.Format, feature.Feature)
	case errors.Is(err, header.ErrUnsupportedFormat):
		return http.StatusUnsupportedMediaType, codeUnsupported,
			"the image format is not supported; JPEG, PNG, GIF and WebP are"
	case errors.Is(err, header.ErrUnreadable):
		return http.StatusUnprocessableEntity, codeUnreadable, "the image could not be decoded"
	case errors.As(err, &se) && (se.Status == http.StatusNotFound || se.Status == http.StatusGone):
		return http.StatusNotFound, codeUpstreamNotFound,
			fmt.Sprintf("the origin has no image at that URL; it answered %d", se.Status)
	case errors.As(err, &se):
		return http.StatusBadGateway, codeUpstreamFailed, fmt.Sprintf("the origin answered %d", se.Status)
	case errors.Is(err, fetch.ErrTooManyRedirects):
		return http.StatusBadGateway, codeUpstreamFailed, "the origin redirected too many times"
	case errors.Is(err, fetch.ErrBadRedirect):
		return http.StatusBadGateway, codeUpstreamFailed, "the origin redirected to a URL blurb does not fetch"
	case errors.Is(err, queue.ErrTimeout):
		return http.StatusServiceUnavailable, codeUnavailable, "the request waited too long for a decode; retry shortly"
	case errors.Is(err, queue.ErrBusy):
		return http.StatusServiceUnavailable, codeUnavailable, "too many requests are waiting; retry shortly"
	case errors.Is(err, context.Canceled):
		return http.StatusServiceUnavailable, codeUnavailable, "the request ended before it was served"
	case errors.As(err, &transport):
		return http.StatusBadGateway, codeUpstreamFailed, "the origin could not be reached"
	default:
		return http.StatusInternalServerError, codeInternal, messageInternal
	}
}
