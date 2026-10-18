package header

import (
	"image"
	"image/color"
	"image/color/palette"
	"math"
	"testing"
)

func TestModelDecodeBytes(t *testing.T) {
	cases := []struct {
		name  string
		model color.Model
		want  int64
	}{
		{name: "paletted", model: color.Palette(palette.Plan9), want: 1000 * 1000},
		{name: "unlisted model", model: color.YCbCrModel, want: 8 * 1000 * 1000},
	}

	for _, tc := range cases {
		cfg := image.Config{ColorModel: tc.model, Width: 1000, Height: 1000}
		if got, want := modelDecodeBytes(cfg), tc.want+1<<20; got != want {
			t.Errorf("%s: %d, want %d", tc.name, got, want)
		}
	}
}

func TestDecodeBytes(t *testing.T) {
	cases := []struct {
		name     string
		w, h     int
		perPixel float64
		want     int64
	}{
		{
			name:     "3 by 1 at half a byte a pixel, rounded up",
			w:        3,
			h:        1,
			perPixel: 0.5,
			want:     2 + 1<<20,
		},
		// 2^30 by 2^30 is 2^60 pixels, so these products fall 2^21 and 2^20
		// short of 2^63.
		{
			name:     "just under the int64 limit",
			w:        1 << 30,
			h:        1 << 30,
			perPixel: 8 - 1.0/(1<<39),
			want:     1<<63 - 1<<20,
		},
		{
			name:     "at the int64 limit",
			w:        1 << 30,
			h:        1 << 30,
			perPixel: 8 - 1.0/(1<<40),
			want:     math.MaxInt64,
		},
		{
			name:     "a PNG's largest sides at 8 bytes a pixel",
			w:        1<<31 - 1,
			h:        1<<31 - 1,
			perPixel: 8,
			want:     math.MaxInt64,
		},
	}

	for _, tc := range cases {
		if got := decodeBytes(tc.w, tc.h, tc.perPixel); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}
