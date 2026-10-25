// Package lqip makes a low-quality image placeholder: an image scaled to fit
// inside 10 by 10 pixels, as a PNG data URI.
package lqip

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
)

// maxSide bounds the placeholder's width and height, in pixels.
const maxSide = 10

// ErrEmpty is returned for an image with no pixels.
var ErrEmpty = errors.New("lqip: empty image")

// DataURI returns img box-averaged to fit inside 10 by 10 pixels, alpha kept,
// as a PNG data URI. It can return [ErrEmpty].
func DataURI(img *image.RGBA) (string, error) {
	b := img.Rect
	if b.Empty() {
		return "", ErrEmpty
	}

	w, h := maxSide, max(1, (maxSide*b.Dy()+b.Dx()/2)/b.Dx())

	if b.Dy() > b.Dx() {
		w, h = max(1, (maxSide*b.Dx()+b.Dy()/2)/b.Dy()), maxSide
	}

	small := image.NewRGBA(image.Rect(0, 0, w, h))

	for oy := range h {
		y0, y1 := cellSpan(oy, h, b.Dy())

		for ox := range w {
			x0, x1 := cellSpan(ox, w, b.Dx())

			var sum [4]int

			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					i := img.PixOffset(b.Min.X+x, b.Min.Y+y)
					for channel := range sum {
						sum[channel] += int(img.Pix[i+channel])
					}
				}
			}

			n := (y1 - y0) * (x1 - x0)
			i := small.PixOffset(ox, oy)

			for channel := range sum {
				small.Pix[i+channel] = uint8((sum[channel] + n/2) / n)
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, small); err != nil {
		return "", err
	}

	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// cellSpan is the range of source pixels cell i of n covers along a side
// of size pixels: from i*size/n to (i+1)*size/n, rounded down, never empty.
func cellSpan(i, n, size int) (lo, hi int) {
	lo = i * size / n
	return lo, max((i+1)*size/n, lo+1)
}
