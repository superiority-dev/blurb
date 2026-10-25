package decode

import (
	"image"
	"slices"
	"testing"

	"github.com/superiority-dev/blurb/internal/header"
)

// TestOrient requires each orientation to turn a 3x2 picture holding 1 to 6
// into the picture EXIF defines for it.
func TestOrient(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for i := range 6 {
		src.Pix[4*i] = uint8(i + 1)
	}

	cases := []struct {
		orientation header.Orientation
		size        image.Point
		pixels      []uint8
	}{
		{orientation: header.OrientationNormal, size: image.Pt(3, 2), pixels: []uint8{1, 2, 3, 4, 5, 6}},
		{orientation: header.OrientationFlipHorizontal, size: image.Pt(3, 2), pixels: []uint8{3, 2, 1, 6, 5, 4}},
		{orientation: header.OrientationRotate180, size: image.Pt(3, 2), pixels: []uint8{6, 5, 4, 3, 2, 1}},
		{orientation: header.OrientationFlipVertical, size: image.Pt(3, 2), pixels: []uint8{4, 5, 6, 1, 2, 3}},
		{orientation: header.OrientationTranspose, size: image.Pt(2, 3), pixels: []uint8{1, 4, 2, 5, 3, 6}},
		{orientation: header.OrientationRotate90, size: image.Pt(2, 3), pixels: []uint8{4, 1, 5, 2, 6, 3}},
		{orientation: header.OrientationTransverse, size: image.Pt(2, 3), pixels: []uint8{6, 3, 5, 2, 4, 1}},
		{orientation: header.OrientationRotate270, size: image.Pt(2, 3), pixels: []uint8{3, 6, 2, 5, 1, 4}},
	}

	for _, tc := range cases {
		got := orient(src, tc.orientation)

		var pixels []uint8
		for i := 0; i < len(got.Pix); i += 4 {
			pixels = append(pixels, got.Pix[i])
		}

		if got.Rect.Size() != tc.size || !slices.Equal(pixels, tc.pixels) {
			t.Errorf("orientation %d: %v %v, want %v %v", tc.orientation, got.Rect.Size(), pixels, tc.size, tc.pixels)
		}
	}
}
