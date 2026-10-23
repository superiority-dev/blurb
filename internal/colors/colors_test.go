package colors

import (
	"image"
	"image/color"
	"slices"
	"testing"
)

func TestAverage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	img.SetRGBA(1, 0, color.RGBA{R: 255, A: 255})
	img.SetRGBA(0, 1, color.RGBA{B: 255, A: 255})

	if got, want := Average(img), (color.RGBA{R: 170, B: 85, A: 255}); got != want {
		t.Errorf("three opaque pixels and one transparent: %v, want %v", got, want)
	}

	if got := Average(image.NewRGBA(image.Rect(0, 0, 2, 2))); got != (color.RGBA{A: 255}) {
		t.Errorf("fully transparent: %v, want opaque black", got)
	}

	// 1 of red at alpha 2 is 127.5 straight, which rounds up.
	faint := image.NewRGBA(image.Rect(0, 0, 1, 1))
	faint.SetRGBA(0, 0, color.RGBA{R: 1, A: 2})

	if got := Average(faint); got != (color.RGBA{R: 128, A: 255}) {
		t.Errorf("red 1 at alpha 2: %v, want red 128, opaque", got)
	}

	if got := Average(redColumn()); got != (color.RGBA{R: 255, A: 255}) {
		t.Errorf("a red column of blue as a sub-image: %v, want red", got)
	}
}

func TestDominant(t *testing.T) {
	red := color.RGBA{R: 200, G: 10, B: 10, A: 255}
	blue := color.RGBA{R: 10, G: 10, B: 200, A: 255}

	img := image.NewRGBA(image.Rect(0, 0, 4, 1))
	for x := range 3 {
		img.SetRGBA(x, 0, red)
	}
	img.SetRGBA(3, 0, blue)

	if got := Dominant(img); !slices.Equal(got, []Swatch{{Color: red, Share: 0.75}, {Color: blue, Share: 0.25}}) {
		t.Errorf("got %+v, want red 0.75 then blue 0.25", got)
	}

	// A transparent pixel counts toward no color and no share.
	img.SetRGBA(3, 0, color.RGBA{})

	if got := Dominant(img); !slices.Equal(got, []Swatch{{Color: red, Share: 1}}) {
		t.Errorf("three red, one transparent: got %+v, want red alone, share 1", got)
	}

	// 100 of red at alpha 128 is 199.2 straight.
	faint := image.NewRGBA(image.Rect(0, 0, 1, 1))
	faint.SetRGBA(0, 0, color.RGBA{R: 100, A: 128})

	if got := Dominant(faint); !slices.Equal(got, []Swatch{{Color: color.RGBA{R: 199, A: 255}, Share: 1}}) {
		t.Errorf("red 100 at alpha 128: got %+v, want red 199, opaque, share 1", got)
	}

	// 200 and 201 of red share a bin and average to 200.5, which rounds up.
	reds := image.NewRGBA(image.Rect(0, 0, 2, 1))
	reds.SetRGBA(0, 0, color.RGBA{R: 200, A: 255})
	reds.SetRGBA(1, 0, color.RGBA{R: 201, A: 255})

	if got := Dominant(reds); !slices.Equal(got, []Swatch{{Color: color.RGBA{R: 201, A: 255}, Share: 1}}) {
		t.Errorf("red 200 and 201: got %+v, want red 201, share 1", got)
	}

	// At most five colors, a six-way tie broken by bin order.
	six := image.NewRGBA(image.Rect(0, 0, 6, 1))
	for x, gray := range []uint8{0, 50, 100, 150, 200, 250} {
		six.SetRGBA(x, 0, color.RGBA{R: gray, G: gray, B: gray, A: 255})
	}

	var grays []uint8
	for _, swatch := range Dominant(six) {
		grays = append(grays, swatch.Color.R)
	}

	if want := []uint8{0, 50, 100, 150, 200}; !slices.Equal(grays, want) {
		t.Errorf("six grays: %v, want %v", grays, want)
	}

	// A bin is the top four bits of red, green and blue.
	for gray, bins := range map[uint8]int{16: 2, 8: 1} {
		pair := image.NewRGBA(image.Rect(0, 0, 2, 1))
		pair.SetRGBA(0, 0, color.RGBA{A: 255})
		pair.SetRGBA(1, 0, color.RGBA{R: gray, G: gray, B: gray, A: 255})

		if got := Dominant(pair); len(got) != bins {
			t.Errorf("black and gray %d: %+v, want %d colors", gray, got, bins)
		}
	}

	if got := Dominant(redColumn()); !slices.Equal(got, []Swatch{{Color: color.RGBA{R: 255, A: 255}, Share: 1}}) {
		t.Errorf("a red column of blue as a sub-image: got %+v, want red alone, share 1", got)
	}
}

// redColumn returns the red pixels (1,1) and (1,2) of a 3x3 blue image, as
// a sub-image.
func redColumn() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 3, 3))
	for y := range img.Rect.Dy() {
		for x := range img.Rect.Dx() {
			img.SetRGBA(x, y, color.RGBA{B: 255, A: 255})
		}
	}
	img.SetRGBA(1, 1, color.RGBA{R: 255, A: 255})
	img.SetRGBA(1, 2, color.RGBA{R: 255, A: 255})

	return img.SubImage(image.Rect(1, 1, 2, 3)).(*image.RGBA)
}
