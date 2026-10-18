package header

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
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
	exif := pngChunk("eXIf", exifData(6, binary.BigEndian))
	trns := pngChunk("tRNS", []byte{0, 0, 0, 0, 0, 0})
	text := pngChunk("tEXt", []byte("Comment\x00hello"))

	badCRC := slices.Clone(exif)
	badCRC[len(badCRC)-1] ^= 1

	noTIFF := pngChunk("eXIf", append([]byte("Exif\x00\x00"), exifData(6, binary.BigEndian)...))

	cases := []struct {
		name        string
		input       []byte
		orientation Orientation
		transparent bool
	}{
		{
			name:        "eXIf after IHDR",
			input:       pngWithChunks(plain, pngIHDREnd, exif),
			orientation: OrientationRotate90,
		},
		{
			name:        "eXIf after another chunk",
			input:       pngWithChunks(plain, pngIHDREnd, text, exif),
			orientation: OrientationRotate90,
		},
		{
			name:        "no eXIf",
			input:       plain,
			orientation: OrientationNormal,
		},
		{
			name:        "eXIf after IDAT",
			input:       pngWithChunks(plain, len(plain)-12, exif),
			orientation: OrientationNormal,
		},
		{
			name: "a second eXIf",
			input: pngWithChunks(
				plain,
				pngIHDREnd,
				exif,
				pngChunk("eXIf", exifData(3, binary.BigEndian)),
			),
			orientation: OrientationRotate90,
		},
		{
			name:        "CRC wrong",
			input:       pngWithChunks(plain, pngIHDREnd, badCRC),
			orientation: OrientationNormal,
		},
		{
			name:        "CRC wrong, then a valid eXIf",
			input:       pngWithChunks(plain, pngIHDREnd, badCRC, exif),
			orientation: OrientationRotate90,
		},
		{
			name:        "JPEG's identifier first",
			input:       pngWithChunks(plain, pngIHDREnd, noTIFF),
			orientation: OrientationNormal,
		},
		{
			name:        "JPEG's identifier first, then a valid eXIf",
			input:       pngWithChunks(plain, pngIHDREnd, noTIFF, exif),
			orientation: OrientationRotate90,
		},
		{
			name:        "a valid eXIf without orientation, then another",
			input:       pngWithChunks(plain, pngIHDREnd, pngChunk("eXIf", exifMakeOnly()), exif),
			orientation: OrientationNormal,
		},
		{
			name:        "tRNS",
			input:       pngWithChunks(plain, pngIHDREnd, trns),
			orientation: OrientationNormal,
			transparent: true,
		},
		{
			name:        "tRNS after eXIf",
			input:       pngWithChunks(plain, pngIHDREnd, exif, trns),
			orientation: OrientationRotate90,
			transparent: true,
		},
		{
			name:        "cut before IDAT",
			input:       plain[:pngIHDREnd],
			orientation: OrientationNormal,
			transparent: true,
		},
		{
			name:        "length 4 GiB, no data",
			input:       pngWithChunks(plain, pngIHDREnd, []byte{0xff, 0xff, 0xff, 0xff, 'e', 'X', 'I', 'f'}),
			orientation: OrientationNormal,
			transparent: true,
		},
		{
			name:        "cut inside eXIf",
			input:       pngWithChunks(plain, pngIHDREnd, exif)[:pngIHDREnd+8+4],
			orientation: OrientationNormal,
			transparent: true,
		},
		{
			name:        "cut inside the CRC",
			input:       pngWithChunks(plain, pngIHDREnd, exif)[:pngIHDREnd+len(exif)-2],
			orientation: OrientationNormal,
			transparent: true,
		},
		{
			name:        "eXIf, then cut inside the next chunk",
			input:       pngWithChunks(plain, pngIHDREnd, exif, text)[:pngIHDREnd+len(exif)+8+2],
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

// pngIHDREnd is where a PNG file's IHDR chunk ends: the signature, the chunk's
// length and type, its 13 bytes of data and its CRC.
const pngIHDREnd = 8 + 8 + 13 + 4

// pngChunk is one PNG chunk with its CRC.
func pngChunk(kind string, data []byte) []byte {
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	chunk = append(chunk, kind...)
	chunk = append(chunk, data...)

	return binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
}

// pngWithChunks puts chunks into a PNG file at offset at.
func pngWithChunks(data []byte, at int, chunks ...[]byte) []byte {
	return slices.Insert(slices.Clone(data), at, slices.Concat(chunks...)...)
}

// pngWithIHDR is PNG data with its IHDR chunk's data from offset at replaced by
// field, and the chunk's CRC recomputed.
func pngWithIHDR(data []byte, at int, field ...byte) []byte {
	out := slices.Clone(data)
	copy(out[8+8+at:], field)
	binary.BigEndian.PutUint32(out[8+8+13:], crc32.ChecksumIEEE(out[8+4:8+8+13]))

	return out
}
