// Package testimage holds the test images blurb's packages share.
package testimage

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"slices"
	"testing"
)

var (
	//go:embed testdata/animated.gif
	gifAnimated []byte

	//go:embed testdata/lossy.webp
	webpLossy []byte

	//go:embed testdata/lossy-alpha.webp
	webpLossyAlpha []byte

	//go:embed testdata/lossless.webp
	webpLossless []byte

	//go:embed testdata/animated.webp
	webpAnimated []byte
)

// Gradient returns a w by h image whose red rises from left to right, green
// from top to bottom, and blue with x+y, modulo 256.
func Gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, color.RGBA{R: uint8(255 * x / w), G: uint8(255 * y / h), B: uint8((x + y) % 256), A: 255})
		}
	}

	return img
}

// EncodeJPEG returns img encoded as a JPEG of quality 90.
func EncodeJPEG(t testing.TB, img image.Image) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

// EncodePNG returns img encoded as a PNG.
func EncodePNG(t testing.TB, img image.Image) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

// GIFZeroFrame returns a GIF whose 10x10 screen holds one 0x0 frame.
func GIFZeroFrame() []byte {
	return []byte{
		'G', 'I', 'F', '8', '9', 'a', 10, 0, 10, 0, 0, 0, 0,
		0x2c, 0, 0, 0, 0, 0, 0, 0, 0, 0x80,
		0, 0, 0, 255, 255, 255,
		2, 1, 0x2c, 0, 0x3b,
	}
}

// GIFAnimated returns an animated GIF: two frames on a 64x48 screen, the
// first a red 16x16 square at (8,8).
func GIFAnimated() []byte { return slices.Clone(gifAnimated) }

// WebPLossy returns a 64x48 color gradient as a lossy WebP: one 206-byte VP8
// chunk.
func WebPLossy() []byte { return slices.Clone(webpLossy) }

// WebPLossyAlpha returns [WebPLossy]'s image with alpha falling from top to
// bottom, as a lossy WebP: VP8X, ALPH and a 206-byte VP8 chunk.
func WebPLossyAlpha() []byte { return slices.Clone(webpLossyAlpha) }

// WebPLossless returns [WebPLossy]'s image as a lossless WebP: one VP8L chunk.
func WebPLossless() []byte { return slices.Clone(webpLossless) }

// WebPAnimated returns an animated WebP: two lossless frames on a 64x48
// canvas, the first 16x16 at (24,16).
func WebPAnimated() []byte { return slices.Clone(webpAnimated) }
