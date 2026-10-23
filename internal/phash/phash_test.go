package phash

import (
	"errors"
	"image"
	"image/color"
	"math"
	"math/bits"
	"slices"
	"testing"

	"github.com/superiority-dev/blurb/internal/testimage"
)

// TestHash requires a flat image to set the DC bit alone, a uniform change
// of brightness to move only the DC term, a gradient to give the bits the
// DCT's definition gives, and an image with no pixels to be refused.
func TestHash(t *testing.T) {
	flat := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := range flat.Pix {
		flat.Pix[i] = 100
	}

	if got := hash(t, flat); got != 1<<63 {
		t.Errorf("flat: %016x, want only the DC bit", got)
	}

	low := testimage.Gradient(100, 75)
	for i := range low.Pix {
		if i%4 != 3 {
			low.Pix[i] /= 2
		}
	}

	high := image.NewRGBA(low.Rect)
	for i, v := range low.Pix {
		high.Pix[i] = v
		if i%4 != 3 {
			high.Pix[i] = v + 20
		}
	}

	if lowHash, highHash := hash(t, low), hash(t, high); lowHash != highHash {
		t.Errorf("a uniform +20 moved pHash from %016x to %016x", lowHash, highHash)
	}

	if got := hash(t, testimage.Gradient(400, 300)); got != 0x88240d5ef11bfe59 {
		t.Errorf("400x300 gradient: %016x, want 88240d5ef11bfe59", got)
	}

	if _, err := Hash(image.NewRGBA(image.Rect(0, 0, 0, 1))); !errors.Is(err, ErrEmpty) {
		t.Errorf("0x1: error %v, want ErrEmpty", err)
	}
}

// TestHashMedian requires the threshold to be the median of the 63 AC
// coefficients, so an image whose coefficients differ sets exactly 31 of
// their bits.
func TestHashMedian(t *testing.T) {
	if n := bits.OnesCount64(hash(t, testimage.Gradient(400, 300)) &^ (1 << 63)); n != 31 {
		t.Errorf("%d AC bits set, want 31", n)
	}
}

// TestLumaGrid requires BT.601 luma, 0.299 R + 0.587 G + 0.114 B, scaled by
// 1,000, averaged over each cell and rounded.
func TestLumaGrid(t *testing.T) {
	primaries := image.NewRGBA(image.Rect(0, 0, 3, 1))
	primaries.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	primaries.SetRGBA(1, 0, color.RGBA{G: 255, A: 255})
	primaries.SetRGBA(2, 0, color.RGBA{B: 255, A: 255})

	if got := lumaGrid(primaries, 3, 1); !slices.Equal(got, []int64{76245, 149685, 29070}) {
		t.Errorf("red, green, blue: %v, want 76245, 149685, 29070", got)
	}

	// 299 and 0 average to 149.5, which rounds up.
	halves := image.NewRGBA(image.Rect(0, 0, 2, 1))
	halves.SetRGBA(0, 0, color.RGBA{R: 1, A: 255})

	if got := lumaGrid(halves, 1, 1); !slices.Equal(got, []int64{150}) {
		t.Errorf("red 1 beside black in one cell: %v, want 150", got)
	}

	// 3 pixels into 2 cells: pixel 0 in the first, pixels 1 and 2 in the
	// second.
	edges := image.NewRGBA(image.Rect(0, 0, 3, 1))
	edges.SetRGBA(1, 0, color.RGBA{R: 10, A: 255})
	edges.SetRGBA(2, 0, color.RGBA{R: 20, A: 255})

	if got := lumaGrid(edges, 2, 1); !slices.Equal(got, []int64{0, 4485}) {
		t.Errorf("red 0, 10, 20 into 2 cells: %v, want 0, 4485", got)
	}

	column := image.NewRGBA(image.Rect(0, 0, 3, 3))
	column.SetRGBA(1, 1, color.RGBA{R: 20, A: 255})
	column.SetRGBA(1, 2, color.RGBA{R: 20, A: 255})

	if got := lumaGrid(column.SubImage(image.Rect(1, 1, 2, 3)).(*image.RGBA), 1, 1); !slices.Equal(got, []int64{5980}) {
		t.Errorf("red 20 at (1,1) and (1,2) as a sub-image: %v, want 5980", got)
	}
}

// TestDCTBasis requires the integer basis to stay within one unit of the
// cosine it stands for.
func TestDCTBasis(t *testing.T) {
	for k := range 8 {
		for x := range 32 {
			want := math.Cos(math.Pi*float64(k)*(2*float64(x)+1)/64) * 16384
			if got := float64(dctBasis(k, x)); math.Abs(got-want) > 1 {
				t.Errorf("dctBasis(%d, %d) = %v, want %v", k, x, got, want)
			}
		}
	}
}

// hash returns [Hash] of img, failing t on an error.
func hash(t *testing.T, img *image.RGBA) uint64 {
	t.Helper()

	h, err := Hash(img)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	return h
}
