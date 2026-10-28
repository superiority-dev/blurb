package header

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"slices"
	"testing"

	"github.com/superiority-dev/blurb/internal/testimage"
)

// TestScanPNG requires scanPNG to report, from the chunks before the image
// data, the orientation in the first eXIf with a valid CRC and TIFF header,
// and a tRNS chunk, which a walk that ends early also reports.
func TestScanPNG(t *testing.T) {
	plain := testimage.EncodePNG(t, testimage.Gradient(40, 30))
	exif := testimage.PNGChunk("eXIf", testimage.EXIFOrientation(6, binary.BigEndian))
	trns := testimage.PNGChunk("tRNS", []byte{0, 0, 0, 0, 0, 0})
	text := testimage.PNGChunk("tEXt", []byte("Comment\x00hello"))

	badCRC := slices.Clone(exif)
	badCRC[len(badCRC)-1] ^= 1

	noTIFF := testimage.PNGChunk(
		"eXIf",
		append([]byte("Exif\x00\x00"), testimage.EXIFOrientation(6, binary.BigEndian)...),
	)

	afterIHDR := func(chunks ...[]byte) []byte {
		return testimage.PNGWithChunks(plain, testimage.PNGIHDREnd, chunks...)
	}

	cases := []struct {
		name        string
		input       []byte
		orientation Orientation
		transparent bool
	}{
		{
			name:        "eXIf after IHDR",
			input:       afterIHDR(exif),
			orientation: OrientationRotate90,
		},
		{
			name:        "eXIf after another chunk",
			input:       afterIHDR(text, exif),
			orientation: OrientationRotate90,
		},
		{
			name:        "no eXIf",
			input:       plain,
			orientation: OrientationNormal,
		},
		{
			name:        "eXIf after IDAT",
			input:       testimage.PNGWithChunks(plain, len(plain)-12, exif),
			orientation: OrientationNormal,
		},
		{
			name:        "a second eXIf",
			input:       afterIHDR(exif, testimage.PNGChunk("eXIf", testimage.EXIFOrientation(3, binary.BigEndian))),
			orientation: OrientationRotate90,
		},
		{
			name:        "CRC wrong",
			input:       afterIHDR(badCRC),
			orientation: OrientationNormal,
		},
		{
			name:        "CRC wrong, then a valid eXIf",
			input:       afterIHDR(badCRC, exif),
			orientation: OrientationRotate90,
		},
		{
			name:        "JPEG's identifier first",
			input:       afterIHDR(noTIFF),
			orientation: OrientationNormal,
		},
		{
			name:        "JPEG's identifier first, then a valid eXIf",
			input:       afterIHDR(noTIFF, exif),
			orientation: OrientationRotate90,
		},
		{
			name:        "a valid eXIf without orientation, then another",
			input:       afterIHDR(testimage.PNGChunk("eXIf", testimage.EXIFMakeOnly()), exif),
			orientation: OrientationNormal,
		},
		{
			name:        "tRNS",
			input:       afterIHDR(trns),
			orientation: OrientationNormal,
			transparent: true,
		},
		{
			name:        "tRNS after eXIf",
			input:       afterIHDR(exif, trns),
			orientation: OrientationRotate90,
			transparent: true,
		},
		{
			name:        "cut before IDAT",
			input:       plain[:testimage.PNGIHDREnd],
			orientation: OrientationNormal,
			transparent: true,
		},
		{
			name:        "length 4 GiB, no data",
			input:       afterIHDR([]byte{0xff, 0xff, 0xff, 0xff, 'e', 'X', 'I', 'f'}),
			orientation: OrientationNormal,
			transparent: true,
		},
		{
			name:        "cut inside eXIf",
			input:       afterIHDR(exif)[:testimage.PNGIHDREnd+8+4],
			orientation: OrientationNormal,
			transparent: true,
		},
		{
			name:        "cut inside the CRC",
			input:       afterIHDR(exif)[:testimage.PNGIHDREnd+len(exif)-2],
			orientation: OrientationNormal,
			transparent: true,
		},
		{
			name:        "eXIf, then cut inside the next chunk",
			input:       afterIHDR(exif, text)[:testimage.PNGIHDREnd+len(exif)+8+2],
			orientation: OrientationRotate90,
			transparent: true,
		},
	}

	for _, tc := range cases {
		orientation, transparent := scanPNG(bytes.NewReader(tc.input))
		if orientation != tc.orientation || transparent != tc.transparent {
			t.Errorf(
				"%s: got %d, tRNS %t; want %d, tRNS %t",
				tc.name,
				orientation,
				transparent,
				tc.orientation,
				tc.transparent,
			)
		}
	}
}

func TestPNGDecodeBytes(t *testing.T) {
	cases := []struct {
		name        string
		model       color.Model
		interlaced  bool
		transparent bool
		want        int64
	}{
		{name: "8-bit gray", model: color.GrayModel, want: 1000 * 1000},
		{name: "16-bit gray", model: color.Gray16Model, want: 2 * 1000 * 1000},
		{name: "8-bit gray with tRNS", model: color.GrayModel, transparent: true, want: 4 * 1000 * 1000},
		{name: "16-bit gray with tRNS", model: color.Gray16Model, transparent: true, want: 8 * 1000 * 1000},
		{name: "8-bit RGB with tRNS", model: color.RGBAModel, transparent: true, want: 4 * 1000 * 1000},
		{name: "16-bit", model: color.NRGBA64Model, want: 8 * 1000 * 1000},
		{name: "8-bit", model: color.NRGBAModel, want: 4 * 1000 * 1000},
		{name: "8-bit interlaced", model: color.NRGBAModel, interlaced: true, want: 8 * 1000 * 1000},
	}

	for _, tc := range cases {
		cfg := image.Config{ColorModel: tc.model, Width: 1000, Height: 1000}
		if got, want := pngDecodeBytes(cfg, tc.interlaced, tc.transparent), tc.want+1<<20; got != want {
			t.Errorf("%s: %d, want %d", tc.name, got, want)
		}
	}
}
