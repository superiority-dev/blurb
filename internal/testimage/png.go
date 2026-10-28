package testimage

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"slices"
	"testing"
)

// PNGIHDREnd is where a PNG file's IHDR chunk ends: the signature, the chunk's
// length and type, its 13 bytes of data and its CRC.
const PNGIHDREnd = 8 + 8 + 13 + 4

// EncodePNG returns img encoded as a PNG.
func EncodePNG(t testing.TB, img image.Image) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

// PNGChunk returns one PNG chunk with its CRC.
func PNGChunk(kind string, data []byte) []byte {
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	chunk = append(chunk, kind...)
	chunk = append(chunk, data...)

	return binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
}

// PNGWithChunks returns a PNG file with chunks put in at offset at.
func PNGWithChunks(data []byte, at int, chunks ...[]byte) []byte {
	return slices.Insert(slices.Clone(data), at, slices.Concat(chunks...)...)
}

// PNGWithIHDR returns PNG data with its IHDR chunk's data from offset at
// replaced by field, and the chunk's CRC recomputed.
func PNGWithIHDR(data []byte, at int, field ...byte) []byte {
	out := slices.Clone(data)
	copy(out[8+8+at:], field)
	binary.BigEndian.PutUint32(out[8+8+13:], crc32.ChecksumIEEE(out[8+4:8+8+13]))

	return out
}
