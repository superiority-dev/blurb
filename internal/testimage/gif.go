package testimage

import (
	"bytes"
	_ "embed"
	"image"
	"image/gif"
	"slices"
	"testing"
)

//go:embed testdata/animated.gif
var gifAnimated []byte

// EncodeGIF returns img encoded as a GIF.
func EncodeGIF(t testing.TB, img image.Image) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
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

// GIFEmptyScreen returns a GIF whose screen is 0x0, which
// [image.DecodeConfig] accepts.
func GIFEmptyScreen() []byte {
	return []byte{'G', 'I', 'F', '8', '9', 'a', 0, 0, 0, 0, 0, 0, 0, 0x3b}
}

// GIFAnimated returns an animated GIF: two frames on a 64x48 screen, the
// first a red 16x16 square at (8,8).
func GIFAnimated() []byte { return slices.Clone(gifAnimated) }
