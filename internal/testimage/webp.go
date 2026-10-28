package testimage

import (
	_ "embed"
	"encoding/binary"
	"image"
	"slices"
)

const (
	// WebPChunksStart is where an extended WebP's chunks start: after its
	// RIFF header and VP8X chunk.
	WebPChunksStart = 12 + 8 + 10

	// WebPFrameStart is where [WebPTwoFrames] puts its first copy of frame:
	// after the RIFF header, the VP8X and ANIM chunks, and the first ANMF
	// chunk's head and frame header.
	WebPFrameStart = WebPChunksStart + 8 + 6 + 8 + 16
)

var (
	//go:embed testdata/lossy.webp
	webpLossy []byte

	//go:embed testdata/lossy-alpha.webp
	webpLossyAlpha []byte

	//go:embed testdata/lossless.webp
	webpLossless []byte

	//go:embed testdata/animated.webp
	webpAnimated []byte
)

// WebPExtendedLossless returns the VP8L chunk of [WebPLossless] behind a VP8X
// chunk.
func WebPExtendedLossless() []byte {
	return WebPFile(WebPVP8X(0, 64, 48), WebPLossless()[12:])
}

// WebPLossy returns a 64x48 color gradient as a lossy WebP: one 206-byte VP8
// chunk.
func WebPLossy() []byte { return slices.Clone(webpLossy) }

// WebPLossyAlpha returns [WebPLossy]'s image with alpha falling from top to
// bottom, as a lossy WebP: VP8X, ALPH and a 206-byte VP8 chunk.
func WebPLossyAlpha() []byte { return slices.Clone(webpLossyAlpha) }

// WebPLossless returns [WebPLossy]'s image as a lossless WebP: one VP8L chunk.
func WebPLossless() []byte { return slices.Clone(webpLossless) }

// WebPAnimated returns an animated WebP: two lossless frames on a 64x48
// canvas, the first 16x16 at (24,16).
func WebPAnimated() []byte { return slices.Clone(webpAnimated) }

// WebPTwoFrames returns an animated WebP on a canvas of canvas's size with two
// frames at point at, each holding frame, the chunks of a 64x48 image. Its
// VP8X chunk has the alpha flag set if alpha is true.
func WebPTwoFrames(frame []byte, alpha bool, at, canvas image.Point) []byte {
	flags := byte(1 << 1)
	if alpha {
		flags |= 1 << 4
	}

	anmf := WebPFrame(frame, at)

	return WebPFile(WebPVP8X(flags, canvas.X, canvas.Y), WebPChunk("ANIM", make([]byte, 6)), anmf, anmf)
}

// WebPFrame returns an ANMF chunk holding the header of a 64x48 frame at point
// at, then frame.
func WebPFrame(frame []byte, at image.Point) []byte {
	payload := append(make([]byte, 16), frame...)
	putUint24(payload[0:], at.X/2)
	putUint24(payload[3:], at.Y/2)
	putUint24(payload[6:], 63)
	putUint24(payload[9:], 47)

	return WebPChunk("ANMF", payload)
}

// WebPFile returns a WebP file holding chunks.
func WebPFile(chunks ...[]byte) []byte {
	b := append([]byte("RIFF\x00\x00\x00\x00WEBP"), slices.Concat(chunks...)...)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))

	return b
}

// WebPVP8X returns a VP8X chunk holding flags, then a canvas of w by h, each
// side less one in 24 bits.
func WebPVP8X(flags byte, w, h int) []byte {
	payload := []byte{flags, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	putUint24(payload[4:], w-1)
	putUint24(payload[7:], h-1)

	return WebPChunk("VP8X", payload)
}

// WebPChunk returns one RIFF chunk, padded to an even length.
func WebPChunk(fourCC string, payload []byte) []byte {
	chunk := binary.LittleEndian.AppendUint32([]byte(fourCC), uint32(len(payload)))
	chunk = append(chunk, payload...)

	if len(payload)%2 == 1 {
		chunk = append(chunk, 0)
	}

	return chunk
}

// putUint24 writes v into b as a little-endian 24-bit field, as
// binary.LittleEndian.PutUint32 does for 32 bits.
func putUint24(b []byte, v int) {
	b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16)
}
