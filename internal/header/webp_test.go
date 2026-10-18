package header

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"

	"github.com/superiority-dev/blurb/internal/testimage"
)

// An extended WebP's chunks start after its RIFF header and VP8X chunk, and
// an animation's first frame after the ANIM chunk and the ANMF chunk's head
// and frame header too.
const (
	webpChunksStart = 12 + 8 + 10
	webpFrameStart  = webpChunksStart + 8 + 6 + 8 + 16
)

func TestScanWebP(t *testing.T) {
	lossy := testimage.WebPLossy()
	alpha := testimage.WebPLossyAlpha()
	lossless := testimage.WebPLossless()

	huge := append([]byte{}, lossy...)
	binary.LittleEndian.PutUint32(huge[12+4:], 1<<30)

	origin, canvas := image.Point{}, image.Pt(64, 48)

	// head is a 64x48 first frame's header, at the origin.
	head := make([]byte, 16)
	putUint24(head[6:], 63)
	putUint24(head[9:], 47)
	lossyFrame := append(append([]byte{}, head...), lossy[12:]...)

	cases := []struct {
		name  string
		input []byte
		want  webpImage
	}{
		{
			name:  "lossy",
			input: lossy,
			want: webpImage{
				kind:  webpVP8,
				frame: 206,
			},
		},
		{
			name:  "lossy with alpha, after VP8X and ALPH",
			input: alpha,
			want: webpImage{
				kind:  webpVP8,
				frame: 206,
				alpha: true,
			},
		},
		{
			name:  "lossless",
			input: lossless,
			want: webpImage{
				kind: webpVP8L,
			},
		},
		{
			name:  "lossless, after VP8X",
			input: webpExtendedLossless(),
			want: webpImage{
				kind: webpVP8L,
			},
		},
		{
			name:  "animated, lossy",
			input: animatedWebP(lossy[12:], false, origin, canvas),
			want: webpImage{
				kind:     webpVP8,
				frame:    206,
				animated: true,
				bounds:   image.Rect(0, 0, 64, 48),
				start:    webpFrameStart,
				length:   int64(len(lossy) - 12),
			},
		},
		{
			name:  "animated, lossy with alpha",
			input: animatedWebP(alpha[webpChunksStart:], true, origin, canvas),
			want: webpImage{
				kind:     webpVP8,
				frame:    206,
				animated: true,
				bounds:   image.Rect(0, 0, 64, 48),
				alpha:    true,
				start:    webpFrameStart,
				length:   int64(len(alpha) - webpChunksStart),
			},
		},
		{
			name:  "animated, lossless",
			input: animatedWebP(lossless[12:], false, origin, canvas),
			want: webpImage{
				kind:     webpVP8L,
				animated: true,
				bounds:   image.Rect(0, 0, 64, 48),
				start:    webpFrameStart,
				length:   int64(len(lossless) - 12),
			},
		},
		{
			name:  "animated, its first frame at (20,10)",
			input: animatedWebP(lossy[12:], false, image.Pt(20, 10), image.Pt(100, 80)),
			want: webpImage{
				kind:     webpVP8,
				frame:    206,
				animated: true,
				bounds:   image.Rect(20, 10, 84, 58),
				start:    webpFrameStart,
				length:   int64(len(lossy) - 12),
			},
		},
		{
			name: "animated, a first frame without a bitstream",
			input: webpRIFF(
				webpChunk("VP8X", webpVP8X(1<<1, 64, 48)),
				webpChunk("ANMF", head),
				webpChunk("ANMF", lossyFrame),
			),
			want: webpImage{
				animated: true,
			},
		},
		{
			name: "animated, an ALPH chunk before the first frame",
			input: webpRIFF(
				webpChunk("VP8X", webpVP8X(1<<1, 64, 48)),
				webpChunk("ALPH", []byte{0}),
				webpChunk("ANMF", lossyFrame),
			),
			want: webpImage{
				kind:     webpVP8,
				frame:    206,
				animated: true,
				bounds:   image.Rect(0, 0, 64, 48),
				start: int64(webpFrameStart -
					len(webpChunk("ANIM", make([]byte, 6))) +
					len(webpChunk("ALPH", []byte{0}))),
				length: int64(len(lossy) - 12),
			},
		},
		{
			name: "after an odd chunk and its padding",
			input: webpRIFF(
				webpChunk("VP8X", webpVP8X(0, 64, 48)),
				webpChunk("ICCP", []byte{1, 2, 3}),
				webpChunk("VP8 ", make([]byte, 40)),
			),
			want: webpImage{
				kind:  webpVP8,
				frame: 40,
			},
		},
		{
			name:  "frame larger than the decoder holds",
			input: huge,
			want: webpImage{
				kind:  webpVP8,
				frame: 8<<24 + 1<<19,
			},
		},
		{
			name:  "cut before the frame",
			input: lossy[:12+4],
			want:  webpImage{},
		},
		{
			name:  "animated, cut inside the first frame's header",
			input: animatedWebP(lossy[12:], false, origin, canvas)[:webpFrameStart-16+4],
			want: webpImage{
				animated: true,
			},
		},
		{
			name:  "animated, cut inside the first frame's ALPH",
			input: animatedWebP(alpha[webpChunksStart:], true, origin, canvas)[:webpFrameStart+8+2],
			want: webpImage{
				animated: true,
			},
		},
	}

	for _, tc := range cases {
		if got := scanWebP(bytes.NewReader(tc.input)); got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestWebPDecodeBytes(t *testing.T) {
	const ycbcr = 1008 * 1008 * 3 / 2

	cases := []struct {
		name  string
		model color.Model
		img   webpImage
		want  int64
	}{
		{
			name:  "lossy",
			model: color.YCbCrModel,
			img: webpImage{
				kind: webpVP8,
			},
			want: ycbcr,
		},
		{
			name:  "lossy, with its frame",
			model: color.YCbCrModel,
			img: webpImage{
				kind:  webpVP8,
				frame: 3_000_000,
			},
			want: ycbcr + 3_000_000,
		},
		{
			name:  "lossy with alpha",
			model: color.NYCbCrAModel,
			img: webpImage{
				kind: webpVP8,
			},
			want: ycbcr + 5*1000*1000,
		},
		{
			name:  "lossless",
			model: color.NRGBAModel,
			img: webpImage{
				kind: webpVP8L,
			},
			want: 6 * 1000 * 1000,
		},
		{
			name:  "lossless after VP8X, reported as YCbCr",
			model: color.YCbCrModel,
			img: webpImage{
				kind: webpVP8L,
			},
			want: 6 * 1000 * 1000,
		},
		{
			name:  "unknown",
			model: color.YCbCrModel,
			img:   webpImage{},
			want:  ycbcr + 1<<19 + 8<<24 + 5*1000*1000,
		},
		{
			name:  "animated, a lossy first frame of 500x500",
			model: color.NYCbCrAModel,
			img: webpImage{
				kind:     webpVP8,
				frame:    1000,
				animated: true,
				bounds:   image.Rect(0, 0, 500, 500),
			},
			want: 512*512*3/2 + 1000,
		},
		{
			name:  "animated, a lossy first frame with alpha",
			model: color.YCbCrModel,
			img: webpImage{
				kind:     webpVP8,
				animated: true,
				bounds:   image.Rect(0, 0, 500, 500),
				alpha:    true,
			},
			want: 512*512*3/2 + 5*500*500,
		},
		{
			name:  "animated, the first frame not reached",
			model: color.YCbCrModel,
			img: webpImage{
				animated: true,
			},
			want: ycbcr + 1<<19 + 8<<24 + 5*1000*1000,
		},
	}

	for _, tc := range cases {
		cfg := image.Config{ColorModel: tc.model, Width: 1000, Height: 1000}
		if got, want := webpDecodeBytes(cfg, tc.img), tc.want+1<<20; got != want {
			t.Errorf("%s: %d, want %d", tc.name, got, want)
		}
	}
}

// webpExtendedLossless is the VP8L chunk of testimage.WebPLossless behind a
// VP8X chunk.
func webpExtendedLossless() []byte {
	return webpRIFF(webpChunk("VP8X", webpVP8X(0, 64, 48)), testimage.WebPLossless()[12:])
}

// animatedWebP is an animated WebP on a canvas of canvas's size with two
// frames at at, each holding frame, the chunks of a 64x48 image.
func animatedWebP(frame []byte, alpha bool, at, canvas image.Point) []byte {
	flags := byte(1 << 1)
	if alpha {
		flags |= 1 << 4
	}

	anmf := append(make([]byte, 16), frame...)
	putUint24(anmf[0:], at.X/2)
	putUint24(anmf[3:], at.Y/2)
	putUint24(anmf[6:], 63)
	putUint24(anmf[9:], 47)

	return webpRIFF(
		webpChunk("VP8X", webpVP8X(flags, canvas.X, canvas.Y)),
		webpChunk("ANIM", make([]byte, 6)),
		webpChunk("ANMF", anmf),
		webpChunk("ANMF", anmf),
	)
}

// webpRIFF is a WebP file holding chunks.
func webpRIFF(chunks ...[]byte) []byte {
	b := []byte("RIFF\x00\x00\x00\x00WEBP")
	for _, chunk := range chunks {
		b = append(b, chunk...)
	}

	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))

	return b
}

// webpChunk is one RIFF chunk, padded to an even length.
func webpChunk(fourCC string, payload []byte) []byte {
	chunk := binary.LittleEndian.AppendUint32([]byte(fourCC), uint32(len(payload)))
	chunk = append(chunk, payload...)

	if len(payload)%2 == 1 {
		chunk = append(chunk, 0)
	}

	return chunk
}

// webpVP8X is a VP8X chunk's payload: flags, then a canvas of w by h, each side
// less one in 24 bits.
func webpVP8X(flags byte, w, h int) []byte {
	b := []byte{flags, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	putUint24(b[4:], w-1)
	putUint24(b[7:], h-1)

	return b
}

// putUint24 writes v into b as a little-endian 24-bit field, as
// binary.LittleEndian.PutUint32 does for 32 bits.
func putUint24(b []byte, v int) {
	b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16)
}
