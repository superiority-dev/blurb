package header

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"io"
	"slices"
	"testing"
	"testing/iotest"

	"github.com/superiority-dev/blurb/internal/testimage"
)

func TestJPEGReader(t *testing.T) {
	soi, eoi := []byte{0xff, 0xd8}, []byte{0xff, 0xd9}
	sof := testimage.JPEGSegment(0xc0, []byte{8, 0, 1, 0, 1, 1, 1, 0x11, 0})
	progressive := testimage.JPEGSegment(0xc2, []byte{8, 0, 1, 0, 1, 1, 1, 0x11, 0})

	// A scan of one component, its entropy data holding a stuffed 0xff and an
	// RST0 marker.
	scan := append(testimage.JPEGSegment(0xda, []byte{1, 1, 0, 0, 63, 0}), 0x12, 0xff, 0x00, 0x34, 0xff, 0xd0, 0x56)
	scans := func(n int) []byte { return bytes.Repeat(scan, n) }
	app0 := testimage.JPEGSegment(0xe0, []byte("JFIF\x00"))
	app14 := testimage.JPEGSegment(0xee, []byte("Adobe\x00\x00\x00\x00\x00\x00\x00"))
	fill := []byte{0xff, 0xff}

	// An APP1 segment of over 256 bytes whose payload holds SOS and APP0
	// segments of no payload.
	decoys := testimage.JPEGSegment(0xe1, bytes.Repeat([]byte{0xff, 0xda, 0, 2, 0xff, 0xe0, 0, 2}, 40))

	cases := []struct {
		name  string
		input []byte
		err   error
	}{
		{name: "32 scans", input: slices.Concat(soi, sof, scans(32), eoi)},
		{name: "33 scans", input: slices.Concat(soi, sof, scans(33), eoi), err: errJPEGScans},
		{name: "an APP0 and an APP14 before the frame header", input: slices.Concat(soi, app0, app14, sof, scan, eoi)},
		{name: "an APP0 after the frame header", input: slices.Concat(soi, sof, app0, scan, eoi), err: errJPEGLateApp},
		{
			name:  "an APP0 after a progressive frame header",
			input: slices.Concat(soi, progressive, app0, scan, eoi),
			err:   errJPEGLateApp,
		},
		{name: "an APP14 after the first scan", input: slices.Concat(soi, sof, scan, app14, eoi), err: errJPEGLateApp},
		{name: "an APP0 after fill bytes", input: slices.Concat(soi, sof, scan, fill, app0), err: errJPEGLateApp},
		{name: "codes inside a segment", input: slices.Concat(soi, decoys, sof, scan, decoys, eoi)},
		{name: "33 scans after the EOI", input: slices.Concat(soi, sof, scan, eoi, scans(33))},
	}

	for _, tc := range cases {
		for _, in := range []io.Reader{bytes.NewReader(tc.input), iotest.OneByteReader(bytes.NewReader(tc.input))} {
			got, err := io.ReadAll(&jpegReader{r: in})
			if !errors.Is(err, tc.err) || !bytes.HasPrefix(tc.input, got) || err != nil && len(got) == len(tc.input) {
				t.Errorf("%s: read %d of %d bytes, %v, want %v", tc.name, len(got), len(tc.input), err, tc.err)
			}
		}
	}
}

// TestScanJPEG requires scanJPEG to return the image's own frame header and
// the orientation in the first EXIF segment, however the segments end.
func TestScanJPEG(t *testing.T) {
	decoy := append([]byte("Exif\x00\x00"), testimage.EXIFOrientation(6, binary.LittleEndian)...)
	decoy = append(decoy, testimage.JPEGSegment(0xc0, []byte{8, 0, 1, 0, 1, 1, 1, 0x11, 0})...)

	// An APP1 segment whose identifier is not "Exif\x00\x00" is passed over,
	// even with EXIF data after it.
	notEXIF := append([]byte("EXIF\x00\x00"), testimage.EXIFOrientation(3, binary.LittleEndian)...)

	// Only the first EXIF segment counts.
	second := append([]byte("Exif\x00\x00"), testimage.EXIFOrientation(4, binary.LittleEndian)...)

	frame := func(marker byte, samplings ...byte) []byte {
		payload := []byte{8, 0, 16, 0, 16, byte(len(samplings))}
		for i, sampling := range samplings {
			payload = append(payload, byte(i+1), sampling, 0)
		}

		return testimage.JPEGSegment(marker, payload)
	}

	cases := []struct {
		name        string
		frame       []byte
		progressive bool
		samples     float64
		components  int
		mcu         [2]int
	}{
		{
			name:        "progressive 4:4:4",
			frame:       frame(0xc2, 0x11, 0x11, 0x11),
			progressive: true,
			samples:     3,
			components:  3,
			mcu:         [2]int{8, 8},
		},
		{
			name:        "progressive 4:2:0",
			frame:       frame(0xc2, 0x22, 0x11, 0x11),
			progressive: true,
			samples:     1.5,
			components:  3,
			mcu:         [2]int{16, 16},
		},
		{
			name:        "baseline 4:2:2",
			frame:       frame(0xc0, 0x21, 0x11, 0x11),
			progressive: false,
			samples:     2,
			components:  3,
			mcu:         [2]int{16, 8},
		},
		{
			name:        "baseline gray",
			frame:       frame(0xc0, 0x11),
			progressive: false,
			samples:     1,
			components:  1,
			mcu:         [2]int{8, 8},
		},
		{
			name:        "baseline gray 2x2",
			frame:       frame(0xc0, 0x22),
			progressive: false,
			samples:     1,
			components:  1,
			mcu:         [2]int{8, 8},
		},
		{
			name:        "progressive gray 2x1",
			frame:       frame(0xc2, 0x21),
			progressive: true,
			samples:     1,
			components:  1,
			mcu:         [2]int{8, 8},
		},
		{
			name:        "baseline CMYK",
			frame:       frame(0xc0, 0x11, 0x11, 0x11, 0x11),
			progressive: false,
			samples:     4,
			components:  4,
			mcu:         [2]int{8, 8},
		},
		{
			name:        "lossless, SOF15, the last frame marker",
			frame:       frame(0xcf, 0x11),
			progressive: false,
			samples:     1,
			components:  1,
			mcu:         [2]int{8, 8},
		},
		{
			name:        "baseline YCCK 2x2",
			frame:       frame(0xc0, 0x22, 0x11, 0x11, 0x22),
			progressive: false,
			samples:     2.5,
			components:  4,
			mcu:         [2]int{16, 16},
		},
	}

	// DHT, JPG and DAC sit among the frame markers; shaped like a frame,
	// they must still be passed over.
	var notFrames []byte
	for _, marker := range []byte{0xc4, 0xc8, 0xcc} {
		notFrames = append(notFrames, frame(marker, 0x44, 0x44)...)
	}

	// Past SOS or a stray byte comes a segment shaped like a frame, which the
	// walk must not reach.
	unreached := frame(0xc0, 0x44, 0x44)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := []byte{0xff, 0xd8}
			b = append(b, testimage.JPEGSegment(0xe1, notEXIF)...)
			b = append(b, testimage.JPEGSegment(0xe1, decoy)...)
			b = append(b, testimage.JPEGSegment(0xe1, second)...)
			// Fill bytes before a marker, as per T.81 section B.1.1.2.
			b = append(b, 0xff, 0xff)
			b = append(b, tc.frame...)
			b = append(b, notFrames...)

			endings := []struct {
				name string
				b    []byte
			}{
				{
					name: "SOS",
					b:    slices.Concat(b, []byte{0xff, 0xda, 0, 2}, unreached),
				},
				{
					name: "a stray byte",
					b:    slices.Concat(b, []byte{0}, unreached),
				},
				{
					name: "a segment's end",
					b:    b,
				},
				{
					name: "inside a segment",
					b:    b[:len(b)-1],
				},
			}

			for _, ending := range endings {
				got, orientation := scanJPEG(ending.b)
				if got == nil ||
					got.progressive != tc.progressive ||
					got.samples != tc.samples ||
					got.components != tc.components ||
					[2]int{got.mcuWidth, got.mcuHeight} != tc.mcu ||
					orientation != OrientationRotate90 {
					t.Errorf(
						"ending at %s: got %+v, orientation %d; "+
							"want progressive %t, samples %g, %d components, MCU %v, orientation 6",
						ending.name,
						got,
						orientation,
						tc.progressive,
						tc.samples,
						tc.components,
						tc.mcu,
					)
				}
			}
		})
	}
}

func TestJPEGDecodeBytes(t *testing.T) {
	const padded = 1008 * 1008

	cases := []struct {
		name  string
		model color.Model
		frame *jpegFrame
		want  int64
	}{
		{
			name: "progressive 4:4:4",
			frame: &jpegFrame{
				progressive: true,
				components:  3,
				samples:     3,
				mcuWidth:    8,
				mcuHeight:   8,
			},
			want: 15 * 1000 * 1000,
		},
		{
			name: "progressive 4:2:0",
			frame: &jpegFrame{
				progressive: true,
				components:  3,
				samples:     1.5,
				mcuWidth:    16,
				mcuHeight:   16,
			},
			want: 75 * padded / 10,
		},
		{
			name: "baseline 4:2:0",
			frame: &jpegFrame{
				progressive: false,
				components:  3,
				samples:     1.5,
				mcuWidth:    16,
				mcuHeight:   16,
			},
			want: 15 * padded / 10,
		},
		{
			name: "baseline CMYK",
			frame: &jpegFrame{
				progressive: false,
				components:  4,
				samples:     4,
				mcuWidth:    8,
				mcuHeight:   8,
			},
			want: 8 * 1000 * 1000,
		},
		{
			name: "baseline YCCK 2x2",
			frame: &jpegFrame{
				progressive: false,
				components:  4,
				samples:     2.5,
				mcuWidth:    16,
				mcuHeight:   16,
			},
			want: 65 * padded / 10,
		},
		{
			name: "progressive CMYK",
			frame: &jpegFrame{
				progressive: true,
				components:  4,
				samples:     4,
				mcuWidth:    8,
				mcuHeight:   8,
			},
			want: 24 * 1000 * 1000,
		},
		{
			name:  "baseline RGB",
			model: color.RGBAModel,
			frame: &jpegFrame{
				progressive: false,
				components:  3,
				samples:     3,
				mcuWidth:    8,
				mcuHeight:   8,
			},
			want: 7 * 1000 * 1000,
		},
		{
			name:  "no frame found",
			frame: nil,
			want:  24 * 1000 * 1000,
		},
	}

	for _, tc := range cases {
		cfg := image.Config{ColorModel: tc.model, Width: 1000, Height: 1000}
		if got, want := jpegDecodeBytes(cfg, tc.frame), tc.want+1<<20; got != want {
			t.Errorf("%s: %d, want %d", tc.name, got, want)
		}
	}
}
