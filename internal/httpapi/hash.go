package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/superiority-dev/blurb/internal/colors"
	"github.com/superiority-dev/blurb/internal/decode"
	"github.com/superiority-dev/blurb/internal/header"
	"github.com/superiority-dev/blurb/internal/lqip"
	"github.com/superiority-dev/blurb/internal/phash"
	"github.com/superiority-dev/blurb/internal/queue"
	"github.com/superiority-dev/blurb/internal/readers"
	"github.com/superiority-dev/blurhash"
	"github.com/superiority-dev/thumbhash"
)

// aspectScale keeps four decimals of an aspect ratio.
const aspectScale = 10_000

// errTooManyPixels is returned for an image whose width times height exceeds
// [Config.MaxPixels].
var errTooManyPixels = errors.New("httpapi: image has too many pixels")

type hasher struct {
	maxPixels                int64
	xComponents, yComponents int
	decodeQueue              *queue.Queue
	metrics                  *metrics
}

// hash computes what formats and extras ask for from the image in body. size is
// body's declared length, or -1 when it is unknown. hash drains body after the
// decode only when size is -1 or extras asks for the SHA-256. If hash fails
// after a read of body failed, it returns the read's error instead of its own.
func (h *hasher) hash(
	ctx context.Context,
	body io.Reader,
	size int64,
	formats formatSet,
	extras extraSet,
) (_ hashResponse, err error) {
	var format header.Format
	defer func() { h.metrics.result(ctx, format, err) }()

	src := &readers.Sticky{R: body}

	defer func() {
		if err != nil && src.Err() != nil {
			err = src.Err()
		}
	}()

	sum := sha256.New()
	in := &readers.Counting{R: src}

	var r io.Reader = in
	if extras.sha256 {
		r = io.TeeReader(in, sum)
	}

	info, rest, err := header.Read(r)
	if err != nil {
		return hashResponse{}, err
	}

	format = info.Format

	if int64(info.Width)*int64(info.Height) > h.maxPixels {
		return hashResponse{}, errTooManyPixels
	}

	small, alpha, err := h.decode(ctx, info, rest)
	if err != nil {
		return hashResponse{}, err
	}

	// The rest of the input counts toward its size and its digest.
	if size < 0 || extras.sha256 {
		if _, err := io.Copy(io.Discard, rest); err != nil {
			return hashResponse{}, err
		}
	}

	if size < 0 {
		size = in.N
	}

	res := hashResponse{
		Width:       info.Width,
		Height:      info.Height,
		Aspect:      aspect(info.Width, info.Height),
		Format:      info.Format,
		Bytes:       size,
		HasAlpha:    alpha,
		Orientation: info.Orientation,
		Color:       hexColor(colors.Average(small)),
	}

	if formats.blurHash {
		if res.BlurHash, err = blurhash.Encode(h.xComponents, h.yComponents, small); err != nil {
			return hashResponse{}, err
		}
	}

	if formats.thumbHash {
		if res.ThumbHash, err = thumbhash.Encode(small); err != nil {
			return hashResponse{}, err
		}
	}

	if extras.dominantColors {
		for _, dominant := range colors.Dominant(small) {
			res.DominantColors = append(res.DominantColors, swatch{
				Color: hexColor(dominant.Color),
				Share: dominant.Share,
			})
		}
	}

	if extras.pHash {
		hash, err := phash.Hash(small)
		if err != nil {
			return hashResponse{}, err
		}

		res.PHash = hex64(hash)
	}

	if extras.lqip {
		if res.LQIP, err = lqip.DataURI(small); err != nil {
			return hashResponse{}, err
		}
	}

	if extras.sha256 {
		res.SHA256 = hex.EncodeToString(sum.Sum(nil))
	}

	return res, nil
}

// decode waits for the decode queue, then decodes the image rest holds with
// [decode.Small].
func (h *hasher) decode(
	ctx context.Context,
	info header.Info,
	rest io.Reader,
) (small *image.RGBA, alpha bool, err error) {
	h.metrics.estimate.Record(ctx, info.DecodeBytes, metric.WithAttributes(attrFormat.String(string(info.Format))))

	start := time.Now()
	release, err := h.decodeQueue.Acquire(ctx, info.DecodeBytes)
	if err != nil {
		h.metrics.refuse(ctx, queueDecode, err)
		return nil, false, err
	}

	h.metrics.wait.Record(ctx, time.Since(start).Seconds())
	defer release()

	return decode.Small(rest, info)
}

// hashResponse is the response body of a successful /hash request.
type hashResponse struct {
	BlurHash       string             `json:"blurhash,omitempty"`
	ThumbHash      []byte             `json:"thumbhash,omitempty"`
	Width          int                `json:"width"`
	Height         int                `json:"height"`
	Aspect         float64            `json:"aspect"`
	Format         header.Format      `json:"format"`
	Bytes          int64              `json:"bytes"`
	HasAlpha       bool               `json:"has_alpha"`
	Orientation    header.Orientation `json:"orientation"`
	Color          string             `json:"color"`
	DominantColors []swatch           `json:"dominant_colors,omitempty"`
	PHash          string             `json:"phash,omitempty"`
	LQIP           string             `json:"lqip,omitempty"`
	SHA256         string             `json:"sha256,omitempty"`
}

// swatch is one dominant color in the response.
type swatch struct {
	Color string  `json:"color"`
	Share float64 `json:"share"`
}

// aspect returns width over height rounded to four decimals, halves up,
// computed in integers. height must be positive.
func aspect(width, height int) float64 {
	w, h := int64(width), int64(height)
	return float64((2*aspectScale*w+h)/(2*h)) / aspectScale
}

// hexColor returns c as #rrggbb.
func hexColor(c color.RGBA) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// hex64 returns hash as 16 hex digits.
func hex64(hash uint64) string {
	return fmt.Sprintf("%016x", hash)
}
