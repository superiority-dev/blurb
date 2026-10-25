package lqip

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/superiority-dev/blurb/internal/testimage"
)

// TestDataURISize requires the placeholder to fit inside maxSide by maxSide
// pixels, keeping the aspect ratio, whichever side is longer.
func TestDataURISize(t *testing.T) {
	for _, tc := range []struct{ w, h, wantW, wantH int }{
		{w: 100, h: 75, wantW: 10, wantH: 8},
		{w: 75, h: 100, wantW: 8, wantH: 10},
		{w: 1, h: 100, wantW: 1, wantH: 10},
		{w: 100, h: 1, wantW: 10, wantH: 1},
		{w: 3, h: 1, wantW: 10, wantH: 3},
	} {
		img := decodeDataURI(t, testimage.Gradient(tc.w, tc.h))

		if b := img.Bounds(); b.Dx() != tc.wantW || b.Dy() != tc.wantH {
			t.Errorf("%dx%d: LQIP is %v, want %dx%d", tc.w, tc.h, b, tc.wantW, tc.wantH)
		}
	}
}

// TestDataURIPixels requires each placeholder pixel to average the pixels it
// covers, alpha included, in a picture that grows as well as one that
// shrinks.
func TestDataURIPixels(t *testing.T) {
	// 40x20 shrinks to 10x5, four by four pixels a cell: on the left, opaque
	// white beside transparent; on the right, opaque red.
	src := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := range 20 {
		for x := range 40 {
			switch {
			case x >= 20:
				src.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
			case (x+y)%2 == 0:
				src.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}
	}

	// 3x1 grows to 10x3: x 3 and 4 hold source pixels 0 and 1, since
	// 3*3/10 rounds down to 0 and 4*3/10 to 1.
	grows := image.NewRGBA(image.Rect(0, 0, 3, 1))
	grows.SetRGBA(0, 0, color.RGBA{A: 255})
	grows.SetRGBA(1, 0, color.RGBA{R: 100, A: 255})
	grows.SetRGBA(2, 0, color.RGBA{R: 200, A: 255})

	right := src.SubImage(image.Rect(20, 0, 40, 20)).(*image.RGBA)

	// 15x1 shrinks to 10x1: x 0 holds source pixel 0 alone, since 1*15/10
	// rounds down to 1.
	shrinks := image.NewRGBA(image.Rect(0, 0, 15, 1))
	for x := range 15 {
		shrinks.SetRGBA(x, 0, color.RGBA{R: 200, A: 255})
	}
	shrinks.SetRGBA(0, 0, color.RGBA{A: 255})

	for _, tc := range []struct {
		name string
		img  *image.RGBA
		x, y int
		want color.NRGBA
	}{
		{name: "40x20", img: src, x: 0, y: 0, want: color.NRGBA{R: 255, G: 255, B: 255, A: 128}},
		{name: "40x20", img: src, x: 9, y: 4, want: color.NRGBA{R: 255, A: 255}},
		{name: "its right half", img: right, x: 0, y: 0, want: color.NRGBA{R: 255, A: 255}},
		{name: "its right half", img: right, x: 0, y: 1, want: color.NRGBA{R: 255, A: 255}},
		{name: "3x1", img: grows, x: 3, y: 0, want: color.NRGBA{A: 255}},
		{name: "3x1", img: grows, x: 4, y: 0, want: color.NRGBA{R: 100, A: 255}},
		{name: "15x1", img: shrinks, x: 0, y: 0, want: color.NRGBA{A: 255}},
	} {
		got := color.NRGBAModel.Convert(decodeDataURI(t, tc.img).At(tc.x, tc.y))
		if got != tc.want {
			t.Errorf("%s: pixel (%d,%d) is %v, want %v", tc.name, tc.x, tc.y, got, tc.want)
		}
	}
}

func TestDataURIEmpty(t *testing.T) {
	if _, err := DataURI(image.NewRGBA(image.Rect(0, 0, 1, 0))); !errors.Is(err, ErrEmpty) {
		t.Errorf("1x0: error %v, want ErrEmpty", err)
	}
}

// decodeDataURI returns the image [DataURI] makes of img, and fails t when
// the result is not a PNG data URI.
func decodeDataURI(t *testing.T, img *image.RGBA) image.Image {
	t.Helper()

	uri, err := DataURI(img)
	if err != nil {
		t.Fatal(err)
	}

	b64, ok := strings.CutPrefix(uri, "data:image/png;base64,")
	if !ok {
		t.Fatalf("not a PNG data URI: %.40s", uri)
	}

	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	return decoded
}
