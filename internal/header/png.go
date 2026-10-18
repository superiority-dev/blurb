package header

import (
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"io"
)

// A PNG file, as per W3C PNG-3 sections 5.2 and 5.3: a signature of
// pngSignatureLen bytes, then chunks, each its data's length and its type,
// the data, and a CRC over the type and the data.
const (
	pngSignatureLen   = 8
	pngChunkLengthLen = 4
	pngChunkTypeLen   = 4
	pngChunkHead      = pngChunkLengthLen + pngChunkTypeLen
	pngChunkCRCLen    = 4
)

// The interlace method, as per PNG-3 section 11.2.1: pngIHDRInterlace bytes
// into the IHDR chunk's data, after the width, height, bit depth, color type,
// compression method and filter method. pngInterlaceByte is its offset in the
// file, after the signature and the IHDR chunk's head; pngAdam7 is the method
// that interlaces.
const (
	pngIHDRInterlace = 12
	pngInterlaceByte = pngSignatureLen + pngChunkHead + pngIHDRInterlace
	pngAdam7         = 1
)

// pngInterlaceFactor multiplies an interlaced PNG's image: it decodes each
// Adam7 pass into its own image beside the result.
const pngInterlaceFactor = 2

// pngInterlaced reports whether the PNG file b is Adam7 interlaced.
func pngInterlaced(b []byte) bool {
	return len(b) > pngInterlaceByte && b[pngInterlaceByte] == pngAdam7
}

// scanPNG walks the chunks of the PNG file r holds by their lengths up to
// the first IDAT, which PNG-3 puts after any eXIf or tRNS chunk, and returns
// the orientation in the first eXIf chunk whose CRC and TIFF header are
// valid, and whether a tRNS chunk is present. A walk that ends before IDAT
// reports one.
func scanPNG(r io.Reader) (Orientation, bool) {
	orientation := OrientationNormal

	if _, err := io.CopyN(io.Discard, r, pngSignatureLen); err != nil {
		return orientation, true
	}

	var (
		head        [pngChunkHead]byte
		sawEXIf     bool
		transparent bool
	)

	for {
		if _, err := io.ReadFull(r, head[:]); err != nil {
			return orientation, true
		}

		size := int64(binary.BigEndian.Uint32(head[:pngChunkLengthLen]))

		switch string(head[pngChunkLengthLen:]) {
		case "IDAT":
			return orientation, transparent
		case "tRNS":
			transparent = true
		case "eXIf":
			if sawEXIf {
				break
			}

			data, err := io.ReadAll(io.LimitReader(r, size+pngChunkCRCLen))
			if err != nil || int64(len(data)) != size+pngChunkCRCLen {
				return orientation, true
			}

			crc := crc32.Update(crc32.ChecksumIEEE(head[pngChunkLengthLen:]), crc32.IEEETable, data[:size])
			if _, ok := tiffByteOrder(data[:size]); !ok || crc != binary.BigEndian.Uint32(data[size:]) {
				continue
			}

			sawEXIf = true

			if fromEXIF, ok := exifOrientation(data[:size]); ok {
				orientation = fromEXIF
			}

			continue
		}

		if _, err := io.CopyN(io.Discard, r, size+pngChunkCRCLen); err != nil {
			return orientation, true
		}
	}
}

// pngDecodeBytes estimates the memory decoding a PNG of cfg's size and color
// model holds. A gray PNG with a tRNS chunk decodes to NRGBA or NRGBA64, as
// image/png decodes it.
func pngDecodeBytes(cfg image.Config, interlaced, transparent bool) int64 {
	model := cfg.ColorModel
	if transparent {
		switch model {
		case color.GrayModel:
			model = color.NRGBAModel
		case color.Gray16Model:
			model = color.NRGBA64Model
		}
	}

	perPixel := modelBytes(model)
	if interlaced {
		perPixel *= pngInterlaceFactor
	}

	return decodeBytes(cfg.Width, cfg.Height, perPixel)
}
