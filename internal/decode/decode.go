// Package decode decodes an image into the small picture its hashes are
// computed from.
package decode

import (
	"image"
	"io"

	"github.com/superiority-dev/blurb/internal/header"
	"github.com/superiority-dev/thumbhash"
	"golang.org/x/image/draw"
)

// maxSide is the longest side every image is scaled to: the largest a
// ThumbHash accepts.
const maxSide = thumbhash.MaxSize

// Small decodes the image r holds, which [header.Read] returned info for,
// scaled to fit inside [thumbhash.MaxSize] on a side and turned upright. The
// alpha result reports whether any of it may be transparent. It returns
// [header.DecodeError]'s error for an image that does not decode, and
// [header.ErrUnreadable] for one with no pixels.
func Small(r io.Reader, info header.Info) (small *image.RGBA, alpha bool, err error) {
	img, _, err := image.Decode(r)
	if err != nil {
		return nil, false, header.DecodeError(err, info.Format)
	}

	if img.Bounds().Empty() {
		return nil, false, header.ErrUnreadable
	}

	// info's sides are as displayed; the canvas is as stored.
	w, h := info.Width, info.Height
	if info.Orientation.SwapsSides() {
		w, h = h, w
	}

	// A GIF's first frame decodes at its place on the screen; an animated
	// WebP's decodes alone, at the origin.
	frame, canvas := img.Bounds(), image.Rect(0, 0, w, h)
	if !info.Frame.Empty() {
		frame = info.Frame
	}

	// A frame the canvas does not hold is drawn as the whole image.
	if !frame.In(canvas) {
		canvas = frame
	}

	return orient(shrink(img, frame, canvas), info.Orientation), hasAlpha(img) || frame != canvas, nil
}

// shrink scales canvas to fit inside maxSide by maxSide, keeping its aspect
// ratio, into an *image.RGBA, with img drawn at frame's place on it and
// transparent around it.
func shrink(img image.Image, frame, canvas image.Rectangle) *image.RGBA {
	w, h := maxSide, max(1, maxSide*canvas.Dy()/canvas.Dx())

	if canvas.Dy() > canvas.Dx() {
		w, h = max(1, maxSide*canvas.Dx()/canvas.Dy()), maxSide
	}

	small := image.NewRGBA(image.Rect(0, 0, w, h))
	onCanvas := frame.Sub(canvas.Min)
	dst := image.Rect(
		onCanvas.Min.X*w/canvas.Dx(),
		onCanvas.Min.Y*h/canvas.Dy(),
		onCanvas.Max.X*w/canvas.Dx(),
		onCanvas.Max.Y*h/canvas.Dy(),
	)
	draw.ApproxBiLinear.Scale(small, dst, img, img.Bounds(), draw.Src, nil)

	return small
}

// hasAlpha reports whether img has a type the registered decoders return
// for an image with an alpha channel, used or not: a paletted image when any
// palette entry is not opaque.
func hasAlpha(img image.Image) bool {
	switch m := img.(type) {
	case *image.NRGBA, *image.NRGBA64, *image.NYCbCrA, *image.Alpha, *image.Alpha16:
		return true
	case *image.Paletted:
		for _, c := range m.Palette {
			if _, _, _, a := c.RGBA(); a != 0xffff {
				return true
			}
		}
	}

	return false
}
