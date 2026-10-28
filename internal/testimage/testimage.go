// Package testimage holds the test images and image file builders blurb's
// packages share.
package testimage

import (
	"image"
	"image/color"
)

// Gradient returns a w by h image whose red rises from left to right, green
// from top to bottom, and blue with x+y, modulo 256.
func Gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, color.RGBA{R: uint8(255 * x / w), G: uint8(255 * y / h), B: uint8(x + y), A: 255})
		}
	}

	return img
}
