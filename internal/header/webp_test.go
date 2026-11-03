package header

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"io"
	"testing"

	"github.com/superiority-dev/blurb/internal/testimage"
)

func TestScanWebP(t *testing.T) {
	lossy := testimage.WebPLossy()
	alpha := testimage.WebPLossyAlpha()
	lossless := testimage.WebPLossless()

	huge := testimage.WebPLossy()
	binary.LittleEndian.PutUint32(huge[12+4:], 1<<30)

	origin, canvas := image.Point{}, image.Pt(64, 48)

	lossyFrame := testimage.WebPFrame(lossy[12:], origin)
	alphaSize := int(binary.LittleEndian.Uint32(alpha[testimage.WebPChunksStart+4:]))
	alphaPayload := alpha[testimage.WebPChunksStart+8 : testimage.WebPChunksStart+8+alphaSize]

	cases := []struct {
		name  string
		input []byte
		want  webpImage
		err   error
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
				kind:     webpVP8,
				frame:    206,
				alpha:    true,
				lossless: 10931,
			},
		},
		{
			name:  "lossless",
			input: lossless,
			want: webpImage{
				kind:     webpVP8L,
				lossless: 23910,
			},
		},
		{
			name:  "lossless, after VP8X",
			input: testimage.WebPExtendedLossless(),
			want: webpImage{
				kind:     webpVP8L,
				lossless: 23910,
			},
		},
		{
			name:  "animated, lossy",
			input: testimage.WebPTwoFrames(lossy[12:], false, origin, canvas),
			want: webpImage{
				kind:     webpVP8,
				frame:    206,
				animated: true,
				bounds:   image.Rect(0, 0, 64, 48),
				start:    testimage.WebPFrameStart,
				length:   int64(len(lossy) - 12),
			},
		},
		{
			name:  "animated, lossy with alpha",
			input: testimage.WebPTwoFrames(alpha[testimage.WebPChunksStart:], true, origin, canvas),
			want: webpImage{
				kind:     webpVP8,
				frame:    206,
				animated: true,
				bounds:   image.Rect(0, 0, 64, 48),
				alpha:    true,
				start:    testimage.WebPFrameStart,
				length:   int64(len(alpha) - testimage.WebPChunksStart),
				lossless: 10931,
			},
		},
		{
			name:  "animated, lossless",
			input: testimage.WebPTwoFrames(lossless[12:], false, origin, canvas),
			want: webpImage{
				kind:     webpVP8L,
				animated: true,
				bounds:   image.Rect(0, 0, 64, 48),
				start:    testimage.WebPFrameStart,
				length:   int64(len(lossless) - 12),
				lossless: 23910,
			},
		},
		{
			name:  "animated, its first frame at (20,10)",
			input: testimage.WebPTwoFrames(lossy[12:], false, image.Pt(20, 10), image.Pt(100, 80)),
			want: webpImage{
				kind:     webpVP8,
				frame:    206,
				animated: true,
				bounds:   image.Rect(20, 10, 84, 58),
				start:    testimage.WebPFrameStart,
				length:   int64(len(lossy) - 12),
			},
		},
		{
			name: "animated, a first frame without a bitstream",
			input: testimage.WebPFile(
				testimage.WebPVP8X(1<<1, 64, 48),
				testimage.WebPFrame(nil, origin),
				lossyFrame,
			),
			err: ErrUnreadable,
		},
		{
			name: "animated, a first frame shorter than its frame header",
			input: testimage.WebPFile(
				testimage.WebPVP8X(1<<1, 64, 48),
				testimage.WebPChunk("ANMF", []byte{0, 0, 0, 0}),
				lossyFrame,
			),
			err: ErrUnreadable,
		},
		{
			name: "animated, an ALPH chunk before the first frame",
			input: testimage.WebPFile(
				testimage.WebPVP8X(1<<1, 64, 48),
				testimage.WebPChunk("ALPH", []byte{0}),
				lossyFrame,
			),
			want: webpImage{
				kind:     webpVP8,
				frame:    206,
				animated: true,
				bounds:   image.Rect(0, 0, 64, 48),
				start: int64(testimage.WebPFrameStart -
					len(testimage.WebPChunk("ANIM", make([]byte, 6))) +
					len(testimage.WebPChunk("ALPH", []byte{0}))),
				length: int64(len(lossy) - 12),
			},
		},
		{
			// Read as a chunk, the bytes past the bitstream would claim 4 GiB.
			name: "an ALPH chunk with bytes past its bitstream",
			input: testimage.WebPFile(
				testimage.WebPVP8X(1<<4, 64, 48),
				testimage.WebPChunk("ALPH", append(bytes.Clone(alphaPayload), bytes.Repeat([]byte{0xff}, 5000)...)),
				lossy[12:],
			),
			want: webpImage{
				kind:     webpVP8,
				frame:    206,
				alpha:    true,
				lossless: 10931,
			},
		},
		{
			// A lossless alpha bitstream whose subtract green transform comes
			// twice.
			name: "an ALPH chunk with an invalid bitstream",
			input: testimage.WebPFile(
				testimage.WebPVP8X(1<<4, 64, 48),
				testimage.WebPChunk("ALPH", []byte{1, 0b101101}),
				lossy[12:],
			),
			err: errVP8L,
		},
		{
			name: "after an odd chunk and its padding",
			input: testimage.WebPFile(
				testimage.WebPVP8X(0, 64, 48),
				testimage.WebPChunk("ICCP", []byte{1, 2, 3}),
				testimage.WebPChunk("VP8 ", make([]byte, 40)),
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
			err:   io.ErrUnexpectedEOF,
		},
		{
			name:  "animated, cut inside the first frame's header",
			input: testimage.WebPTwoFrames(lossy[12:], false, origin, canvas)[:testimage.WebPFrameStart-16+4],
			err:   io.ErrUnexpectedEOF,
		},
		{
			name: "animated, cut inside the first frame's ALPH",
			input: testimage.WebPTwoFrames(
				alpha[testimage.WebPChunksStart:],
				true,
				origin,
				canvas,
			)[:testimage.WebPFrameStart+8+2],
			err: io.EOF,
		},
		{
			name:  "cut inside the ALPH chunk, after its bitstream's trees",
			input: alpha[:testimage.WebPChunksStart+8+alphaSize-1],
			err:   io.EOF,
		},
	}

	for _, tc := range cases {
		got, err := scanWebP(bytes.NewReader(tc.input), canvas)
		if got != tc.want || !errors.Is(err, tc.err) {
			t.Errorf("%s: %+v, %v, want %+v, %v", tc.name, got, err, tc.want, tc.err)
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
				kind:     webpVP8,
				lossless: 10_000,
			},
			want: ycbcr + 5*1000*1000 + 10_000,
		},
		{
			name:  "lossless",
			model: color.NRGBAModel,
			img: webpImage{
				kind:     webpVP8L,
				lossless: 10_000,
			},
			want: 4*1000*1000 + 10_000,
		},
		{
			name:  "lossless after VP8X, reported as YCbCr",
			model: color.YCbCrModel,
			img: webpImage{
				kind:     webpVP8L,
				lossless: 10_000,
			},
			want: 4*1000*1000 + 10_000,
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
				lossless: 10_000,
			},
			want: 512*512*3/2 + 5*500*500 + 10_000,
		},
		{
			name:  "animated, a lossless first frame of 40x40",
			model: color.YCbCrModel,
			img: webpImage{
				kind:     webpVP8L,
				animated: true,
				bounds:   image.Rect(0, 0, 40, 40),
				lossless: 10_000,
			},
			want: 4*40*40 + 10_000,
		},
	}

	for _, tc := range cases {
		cfg := image.Config{ColorModel: tc.model, Width: 1000, Height: 1000}
		if got, want := webpDecodeBytes(cfg, tc.img), tc.want+1<<20; got != want {
			t.Errorf("%s: %d, want %d", tc.name, got, want)
		}
	}
}

func TestWebPAlphaBytes(t *testing.T) {
	alpha := testimage.WebPLossyAlpha()
	size := int(binary.LittleEndian.Uint32(alpha[testimage.WebPChunksStart+4:]))
	payload := alpha[testimage.WebPChunksStart+8 : testimage.WebPChunksStart+8+size]

	filtered := bytes.Clone(payload)
	filtered[0] |= 1 << 2

	unknown := bytes.Clone(payload)
	unknown[0] = unknown[0]&^3 | 2

	cases := []struct {
		name    string
		payload []byte
		want    int64
		err     error
	}{
		// 10,931 bytes is what x/image's decoder allocates for the alpha's
		// bitstream beside its pixels, each allocation rounded up as
		// heapBytes rounds it.
		{name: "lossless", payload: payload, want: 10931},
		{name: "lossless, filtered", payload: filtered, want: 10931},
		{name: "uncompressed", payload: append([]byte{0}, make([]byte, 64*48)...), want: 0},
		{name: "an unknown compression method", payload: unknown, want: 0},
		{name: "empty", payload: nil, err: io.EOF},
	}

	for _, tc := range cases {
		got, err := webpAlphaBytes(bytes.NewReader(tc.payload), image.Pt(64, 48))
		if got != tc.want || !errors.Is(err, tc.err) {
			t.Errorf("%s: %d, %v, want %d, %v", tc.name, got, err, tc.want, tc.err)
		}
	}
}
