// Package phash computes a perceptual hash of an image, which stays
// close for images that look alike.
package phash

import (
	"errors"
	"image"
	"slices"
)

// dctSide is the side of the square DCT [Hash] takes of the image.
const dctSide = 32

// hashSide is the side of the square of lowest frequencies [Hash] keeps.
const hashSide = 8

// ErrEmpty is returned for an image with no pixels.
var ErrEmpty = errors.New("phash: empty image")

// cosQ14 holds cos(m*pi/64) for m from 0 to 32, scaled by 2^14 and rounded:
// the DCT-II basis for a 32-point transform, written out so [Hash] is
// bit-identical across architectures.
var cosQ14 = [dctSide + 1]int64{
	16384, 16364, 16305, 16207, 16069, 15893, 15679, 15426,
	15137, 14811, 14449, 14053, 13623, 13160, 12665, 12140,
	11585, 11003, 10394, 9760, 9102, 8423, 7723, 7005,
	6270, 5520, 4756, 3981, 3196, 2404, 1606, 804,
	0,
}

// Hash returns a perceptual hash of img, bit-identical across architectures:
// one bit per coefficient of the 8 by 8 lowest frequencies of a 32 by 32 luma
// DCT, row by row, set when it is above the median of the non-DC coefficients.
// It can return [ErrEmpty].
func Hash(img *image.RGBA) (uint64, error) {
	if img.Rect.Empty() {
		return 0, ErrEmpty
	}

	grid := lumaGrid(img, dctSide, dctSide)

	var rows [dctSide][hashSide]int64

	for y := range dctSide {
		for k := range hashSide {
			for x := range dctSide {
				rows[y][k] += grid[y*dctSide+x] * dctBasis(k, x)
			}
		}
	}

	var coef [hashSide * hashSide]int64

	for ky := range hashSide {
		for kx := range hashSide {
			for y := range dctSide {
				coef[ky*hashSide+kx] += rows[y][kx] * dctBasis(ky, y)
			}
		}
	}

	ac := slices.Clone(coef[1:])
	slices.Sort(ac)
	median := ac[len(ac)/2]

	var hash uint64

	for _, coefficient := range coef {
		hash <<= 1
		if coefficient > median {
			hash |= 1
		}
	}

	return hash, nil
}

// lumaGrid returns img box-averaged to w by h cells in BT.601 luma, scaled
// by 1,000 and rounded, row by row.
func lumaGrid(img *image.RGBA, w, h int) []int64 {
	b := img.Rect
	out := make([]int64, w*h)

	for oy := range h {
		y0, y1 := cellSpan(oy, h, b.Dy())

		for ox := range w {
			x0, x1 := cellSpan(ox, w, b.Dx())

			var sum int64

			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					i := img.PixOffset(b.Min.X+x, b.Min.Y+y)
					sum += 299*int64(img.Pix[i]) + 587*int64(img.Pix[i+1]) + 114*int64(img.Pix[i+2])
				}
			}

			n := int64((y1 - y0) * (x1 - x0))
			out[oy*w+ox] = (sum + n/2) / n
		}
	}

	return out
}

// cellSpan is the range of source pixels cell i of n covers along a side
// of size pixels: from i*size/n to (i+1)*size/n, rounded down, never empty.
func cellSpan(i, n, size int) (lo, hi int) {
	lo = i * size / n
	return lo, max((i+1)*size/n, lo+1)
}

// dctBasis is cos(pi*k*(2x+1)/(2*dctSide)) scaled by 2^14, from cosQ14
// by the cosine's symmetries: its period is 4*dctSide in steps of
// pi/(2*dctSide), it is even about 2*dctSide, and odd about dctSide.
func dctBasis(k, x int) int64 {
	step := k * (2*x + 1) % (4 * dctSide)
	if step > 2*dctSide {
		step = 4*dctSide - step
	}

	if step > dctSide {
		return -cosQ14[2*dctSide-step]
	}

	return cosQ14[step]
}
