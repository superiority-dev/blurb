package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/superiority-dev/blurb/internal/fetch"
)

const prefix = "BLURB_"

// megapixelDecimals is the most decimals MAX_MEGAPIXELS takes; the sixth is a
// single pixel.
const megapixelDecimals = 6

// byteUnits are the suffixes MAX_DOWNLOAD_BYTES accepts, longest first.
var byteUnits = []struct {
	suffix string
	shift  uint
}{
	{suffix: "GiB", shift: 30},
	{suffix: "MiB", shift: 20},
	{suffix: "KiB", shift: 10},
	{suffix: "B", shift: 0},
}

// reader holds the lookup of BLURB_ variables and every problem found in them.
type reader struct {
	lookup func(string) (string, bool)
	errs   []error
}

func (r *reader) get(name string) (string, bool) {
	v, ok := r.lookup(prefix + name)
	if !ok || strings.TrimSpace(v) == "" {
		return "", false
	}

	return strings.TrimSpace(v), true
}

func (r *reader) fail(name, msg string) {
	r.errs = append(r.errs, fmt.Errorf("%s%s %s", prefix, name, msg))
}

func (r *reader) str(name, def string) string {
	if v, ok := r.get(name); ok {
		return v
	}

	return def
}

func (r *reader) pathPrefix(name string) string {
	v, ok := r.get(name)
	if !ok {
		return ""
	}

	segs := strings.Split(v, "/")
	valid := segs[0] == "" && !strings.ContainsFunc(v, func(c rune) bool { return c != '/' && !unreserved(c) })

	for _, seg := range segs[1:] {
		valid = valid && seg != "" && seg != "." && seg != ".."
	}

	if !valid {
		r.fail(name, fmt.Sprintf(
			`must be a path such as /v1, segments of letters, digits and "-._~", not "." or "..", got %q`,
			v,
		))
		return ""
	}

	return v
}

func (r *reader) headerName(name, def string) string {
	v, ok := r.get(name)
	if !ok {
		return def
	}

	if strings.ContainsFunc(v, func(c rune) bool { return !tchar(c) }) {
		r.fail(name, fmt.Sprintf("must be a header name such as X-Request-Id, got %q", v))
		return ""
	}

	return v
}

func (r *reader) allowlist(name string) []string {
	if _, ok := r.get(name); !ok {
		r.fail(name, "is required: name the hosts images may be fetched from")
		return nil
	}

	return r.list(name)
}

func (r *reader) list(name string) []string {
	v, ok := r.get(name)
	if !ok {
		return nil
	}

	var out []string

	for item := range strings.SplitSeq(v, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			r.fail(name, fmt.Sprintf("has an empty entry: %q", v))
			return nil
		}

		out = append(out, item)
	}

	return out
}

func (r *reader) url(name string) *url.URL {
	v, ok := r.get(name)
	if !ok {
		return nil
	}

	u, err := url.Parse(v)
	if err != nil || !fetch.Fetchable(u) {
		r.fail(name, fmt.Sprintf("must be an absolute http or https URL without credentials, got %q", v))
		return nil
	}

	return u
}

func (r *reader) bool(name string, def bool) bool {
	v, ok := r.get(name)
	if !ok {
		return def
	}

	b, err := strconv.ParseBool(v)
	if err != nil {
		r.fail(name, fmt.Sprintf("must be true or false, got %q", v))
		return def
	}

	return b
}

func (r *reader) duration(name string, def time.Duration) time.Duration {
	v, ok := r.get(name)
	if !ok {
		return def
	}

	d, err := time.ParseDuration(v)
	if err != nil {
		r.fail(name, fmt.Sprintf("must be a duration such as 5s or 500ms, got %q", v))
		return def
	}

	return d
}

func (r *reader) positiveDuration(name string, def time.Duration) time.Duration {
	d := r.duration(name, def)
	r.mustBePositive(name, d > 0, d)

	return d
}

func (r *reader) seconds(name string, def, limit time.Duration) time.Duration {
	d := r.duration(name, def)
	if d < 0 || d > limit || d%time.Second != 0 {
		r.fail(name, fmt.Sprintf("must be whole seconds from 0s to %s, got %s", limit, d))
		return def
	}

	return d
}

func (r *reader) int64(name string, def int64) int64 {
	v, ok := r.get(name)
	if !ok {
		return def
	}

	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		r.fail(name, fmt.Sprintf("must be a whole number, got %q", v))
		return def
	}

	return n
}

func (r *reader) count(name string, def, limit int64) int64 {
	if _, ok := r.get(name); !ok {
		return def
	}

	n := r.int64(name, 0)
	if n < 0 || n > limit {
		r.fail(name, fmt.Sprintf("must be between 0 and %d, got %d", limit, n))
	}

	return n
}

func (r *reader) megapixels(name string, def int64) int64 {
	v, ok := r.get(name)
	if !ok {
		return def
	}

	whole, frac, dot := strings.Cut(v, ".")
	px, valid := int64(0), (!dot || frac != "") && len(frac) <= megapixelDecimals

	if valid {
		megapixels, werr := strconv.ParseUint(whole, 10, 32)
		pixels, ferr := strconv.ParseUint(frac+strings.Repeat("0", megapixelDecimals-len(frac)), 10, 32)
		px, valid = int64(megapixels)*megapixel+int64(pixels), werr == nil && ferr == nil
	}

	if !valid || px < 1 || px > maxPixelsLimit {
		lowest := "0." + strings.Repeat("0", megapixelDecimals-1) + "1"
		r.fail(name, fmt.Sprintf("must be megapixels such as 50 or 12.5, at most %d decimals, from %s to %d, got %q",
			megapixelDecimals, lowest, maxPixelsLimit/megapixel, v))

		return def
	}

	return px
}

func (r *reader) bytes(name string, def int64) int64 {
	v, ok := r.get(name)
	if !ok {
		return def
	}

	num, shift := v, uint(0)

	for _, unit := range byteUnits {
		if before, found := strings.CutSuffix(v, unit.suffix); found {
			num, shift = strings.TrimSpace(before), unit.shift
			break
		}
	}

	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil || n < 0 || n > maxBytesLimit>>shift {
		r.fail(name, fmt.Sprintf(
			"must be a byte count such as 134217728 or 128MiB, at most %d, got %q",
			int64(maxBytesLimit),
			v,
		))
		return def
	}

	r.mustBePositive(name, n > 0, v)

	return n << shift
}

func (r *reader) components(name string, defX, defY int) (xComponents, yComponents int) {
	v, ok := r.get(name)
	if !ok {
		return defX, defY
	}

	xs, ys, _ := strings.Cut(v, "x")
	xComponents, xerr := strconv.Atoi(xs)
	yComponents, yerr := strconv.Atoi(ys)

	if xerr != nil || yerr != nil ||
		xComponents < 1 || xComponents > maxComponents ||
		yComponents < 1 || yComponents > maxComponents {
		r.fail(name, fmt.Sprintf("must be two counts from 1 to %d written XxY, such as 4x3, got %q", maxComponents, v))
		return defX, defY
	}

	return xComponents, yComponents
}

func (r *reader) level(name string, def slog.Level) slog.Level {
	v, ok := r.get(name)
	if !ok {
		return def
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(v)); err != nil {
		r.fail(name, fmt.Sprintf("must be debug, info, warn or error, got %q", v))
		return def
	}

	return level
}

func (r *reader) mustBePositive(name string, positive bool, got any) {
	if !positive {
		r.fail(name, fmt.Sprintf("must be more than zero, got %v", got))
	}
}

// unreserved reports whether c is in RFC 3986's unreserved set, which a URL
// path carries as it is.
func unreserved(c rune) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.ContainsRune("-._~", c)
}

// tchar reports whether c may appear in a header name, as per RFC 9110
// section 5.6.2.
func tchar(c rune) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
		strings.ContainsRune("!#$%&'*+-.^_`|~", c)
}
