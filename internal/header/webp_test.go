package header

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
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
			input: testimage.WebPExtendedLossless(),
			want: webpImage{
				kind: webpVP8L,
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
			want: webpImage{
				animated: true,
			},
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
			want:  webpImage{},
		},
		{
			name:  "animated, cut inside the first frame's header",
			input: testimage.WebPTwoFrames(lossy[12:], false, origin, canvas)[:testimage.WebPFrameStart-16+4],
			want: webpImage{
				animated: true,
			},
		},
		{
			name: "animated, cut inside the first frame's ALPH",
			input: testimage.WebPTwoFrames(
				alpha[testimage.WebPChunksStart:],
				true,
				origin,
				canvas,
			)[:testimage.WebPFrameStart+8+2],
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
