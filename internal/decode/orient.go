package decode

import (
	"image"

	"github.com/superiority-dev/blurb/internal/header"
	"golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
)

// orient returns img, whose bounds start at the origin, flipped or rotated as
// orientation says to display it.
func orient(img *image.RGBA, orientation header.Orientation) *image.RGBA {
	w, h := float64(img.Rect.Dx()), float64(img.Rect.Dy())

	// m maps a stored pixel to its displayed place.
	var m f64.Aff3

	switch orientation {
	case header.OrientationFlipHorizontal:
		m = f64.Aff3{-1, 0, w, 0, 1, 0}
	case header.OrientationRotate180:
		m = f64.Aff3{-1, 0, w, 0, -1, h}
	case header.OrientationFlipVertical:
		m = f64.Aff3{1, 0, 0, 0, -1, h}
	case header.OrientationTranspose:
		m = f64.Aff3{0, 1, 0, 1, 0, 0}
	case header.OrientationRotate90:
		m = f64.Aff3{0, -1, h, 1, 0, 0}
	case header.OrientationTransverse:
		m = f64.Aff3{0, -1, h, -1, 0, w}
	case header.OrientationRotate270:
		m = f64.Aff3{0, 1, 0, -1, 0, w}
	default:
		return img
	}

	size := img.Rect.Size()
	if orientation.SwapsSides() {
		size.X, size.Y = size.Y, size.X
	}

	dst := image.NewRGBA(image.Rectangle{Max: size})
	draw.NearestNeighbor.Transform(dst, m, img, img.Rect, draw.Src, nil)

	return dst
}
