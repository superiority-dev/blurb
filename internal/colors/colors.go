// Package colors finds an image's average color and its dominant colors.
package colors

import (
	"cmp"
	"image"
	"image/color"
	"slices"
)

const maxDominantColors = 5

// binBits is the bits of red, green and blue that sort a pixel into a bin.
const binBits = 4

// Swatch is one dominant color and the share of the visible pixels it
// covers.
type Swatch struct {
	Color color.RGBA
	Share float64
}

// Average returns the mean color of img's visible pixels, each weighted by
// its alpha, as an opaque color; a fully transparent image gives black.
func Average(img *image.RGBA) color.RGBA {
	var r, g, b, a int64

	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			i := img.PixOffset(x, y)
			r += int64(img.Pix[i])
			g += int64(img.Pix[i+1])
			b += int64(img.Pix[i+2])
			a += int64(img.Pix[i+3])
		}
	}

	if a == 0 {
		return color.RGBA{A: 255}
	}

	return color.RGBA{R: unpremultiply(r, a), G: unpremultiply(g, a), B: unpremultiply(b, a), A: 255}
}

// Dominant returns the means of the up to 5 largest bins of img's visible
// pixels, largest first, ties in bin order, as opaque colors; a pixel's bin
// is the top 4 bits of its non-alpha-premultiplied red, green and blue.
func Dominant(img *image.RGBA) []Swatch {
	var (
		bins    [1 << (3 * binBits)]struct{ n, r, g, b int64 }
		visible int64
	)

	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			i := img.PixOffset(x, y)

			a := int64(img.Pix[i+3])
			if a == 0 {
				continue
			}

			r := unpremultiply(int64(img.Pix[i]), a)
			g := unpremultiply(int64(img.Pix[i+1]), a)
			b := unpremultiply(int64(img.Pix[i+2]), a)

			// Index by the top binBits bits of r, g and b, in that order.
			bin := &bins[int(r>>(8-binBits))<<(2*binBits)|int(g>>(8-binBits))<<binBits|int(b>>(8-binBits))]
			bin.n++
			bin.r += int64(r)
			bin.g += int64(g)
			bin.b += int64(b)
			visible++
		}
	}

	order := make([]int, 0, len(bins))

	for i := range bins {
		if bins[i].n > 0 {
			order = append(order, i)
		}
	}

	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(bins[b].n, bins[a].n) })
	order = order[:min(len(order), maxDominantColors)]

	out := make([]Swatch, 0, len(order))

	for _, i := range order {
		bin := bins[i]
		out = append(out, Swatch{
			Color: color.RGBA{
				R: uint8((bin.r + bin.n/2) / bin.n),
				G: uint8((bin.g + bin.n/2) / bin.n),
				B: uint8((bin.b + bin.n/2) / bin.n),
				A: 255,
			},
			Share: float64(bin.n) / float64(visible),
		})
	}

	return out
}

// unpremultiply scales channel, a premultiplied sum, over the alpha sum a to
// 0..255, rounded.
func unpremultiply(channel, a int64) uint8 {
	return uint8(min((channel*255+a/2)/a, 255))
}
