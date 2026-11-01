package httpapi

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/superiority-dev/blurb/internal/fetch"
	"github.com/superiority-dev/blurb/internal/queue"
)

const (
	paramURL     = "url"
	paramFormats = "formats"
	paramExtras  = "extras"
)

// listSeparator separates the items of the formats and extras lists.
const listSeparator = ","

var defaultFormats = formatSet{blurHash: true, thumbHash: true}

type api struct {
	pathPrefix      string
	requestIDHeader string
	maxBodyBytes    int64
	bodyTimeout     time.Duration
	maxAge          time.Duration
	fetcher         *fetch.Fetcher
	hashQueue       *queue.Queue
	hasher          hasher
}

func newAPI(cfg Config, fetcher *fetch.Fetcher, hashQueue, decodeQueue *queue.Queue, instruments *metrics) *api {
	return &api{
		pathPrefix:      cfg.PathPrefix,
		requestIDHeader: cfg.RequestIDHeader,
		maxBodyBytes:    cfg.MaxBodyBytes,
		bodyTimeout:     cfg.BodyTimeout,
		maxAge:          cfg.MaxAge,
		fetcher:         fetcher,
		hashQueue:       hashQueue,
		hasher: hasher{
			maxPixels:   cfg.MaxPixels,
			xComponents: cfg.XComponents,
			yComponents: cfg.YComponents,
			decodeQueue: decodeQueue,
			metrics:     instruments,
		},
	}
}

type formatSet struct {
	blurHash, thumbHash bool
}

// parseFormats returns the hashes the formats parameter, a comma-separated
// list, asks for, or defaultFormats when it is empty or not given.
func parseFormats(values url.Values) (formatSet, error) {
	list, err := param(values, paramFormats)
	if err != nil {
		return formatSet{}, err
	}

	if list == "" {
		return defaultFormats, nil
	}

	var formats formatSet

	err = setEach(paramFormats, list, "blurhash, thumbhash or both", map[string]*bool{
		"blurhash":  &formats.blurHash,
		"thumbhash": &formats.thumbHash,
	})
	if err != nil {
		return formatSet{}, err
	}

	return formats, nil
}

type extraSet struct {
	dominantColors, pHash, lqip, sha256 bool
}

// parseExtras returns the extra values the extras parameter, a comma-separated
// list, asks for, or none when it is empty or not given.
func parseExtras(values url.Values) (extraSet, error) {
	list, err := param(values, paramExtras)
	if err != nil || list == "" {
		return extraSet{}, err
	}

	var extras extraSet

	err = setEach(paramExtras, list, "any of dominant_colors, phash, lqip and sha256", map[string]*bool{
		"dominant_colors": &extras.dominantColors,
		"phash":           &extras.pHash,
		"lqip":            &extras.lqip,
		"sha256":          &extras.sha256,
	})
	if err != nil {
		return extraSet{}, err
	}

	return extras, nil
}

// param returns the value of the query parameter named by name, or an error
// when the query gives it more than once.
func param(values url.Values, name string) (string, error) {
	if n := len(values[name]); n > 1 {
		return "", fmt.Errorf("%s is given %d times; give it once", name, n)
	}

	return values.Get(name), nil
}

// setEach sets the flag of each item of list. For an item without a flag it
// returns an error saying what the query parameter named by name takes.
func setEach(name, list, takes string, flags map[string]*bool) error {
	for item := range strings.SplitSeq(list, listSeparator) {
		flag, ok := flags[strings.TrimSpace(item)]
		if !ok {
			return fmt.Errorf("%s takes %s, got %q", name, takes, list)
		}

		*flag = true
	}

	return nil
}
