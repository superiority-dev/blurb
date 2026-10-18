package header

import (
	"encoding/binary"
	"image"
	"slices"
	"testing"
)

// TestScanJPEG requires scanJPEG to return the image's own frame header and
// the orientation in the first EXIF segment, however the segments end.
func TestScanJPEG(t *testing.T) {
	decoy := append([]byte("Exif\x00\x00"), exifData(6, binary.LittleEndian)...)
	decoy = append(decoy, jpegSegment(0xc0, []byte{8, 0, 1, 0, 1, 1, 1, 0x11, 0})...)

	// An APP1 segment whose identifier is not "Exif\x00\x00" is passed over,
	// even with EXIF data after it.
	notEXIF := append([]byte("EXIF\x00\x00"), exifData(3, binary.LittleEndian)...)

	// Only the first EXIF segment counts.
	second := append([]byte("Exif\x00\x00"), exifData(4, binary.LittleEndian)...)

	frame := func(marker byte, samplings ...byte) []byte {
		payload := []byte{8, 0, 16, 0, 16, byte(len(samplings))}
		for i, sampling := range samplings {
			payload = append(payload, byte(i+1), sampling, 0)
		}

		return jpegSegment(marker, payload)
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
			b = append(b, jpegSegment(0xe1, notEXIF)...)
			b = append(b, jpegSegment(0xe1, decoy)...)
			b = append(b, jpegSegment(0xe1, second)...)
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
			name:  "no frame found",
			frame: nil,
			want:  24 * 1000 * 1000,
		},
	}

	cfg := image.Config{Width: 1000, Height: 1000}
	for _, tc := range cases {
		if got, want := jpegDecodeBytes(cfg, tc.frame), tc.want+1<<20; got != want {
			t.Errorf("%s: %d, want %d", tc.name, got, want)
		}
	}
}

// jpegWithEXIF puts an EXIF segment carrying orientation after a JPEG's SOI.
func jpegWithEXIF(data []byte, orientation uint16, order binary.ByteOrder) []byte {
	out := append([]byte{}, data[:2]...)
	out = append(out, jpegSegment(0xe1, append([]byte("Exif\x00\x00"), exifData(orientation, order)...))...)

	return append(out, data[2:]...)
}

// jpegPadTo puts APP2 segments after a JPEG's SOI so that what
// [image.DecodeConfig] reads ends at byte end. Without JFIF's APP0 segment,
// which Go's encoder does not write, it reads up to the SOS segment's length.
// A marker is 2 bytes and a segment's head, the marker and its length, 4.
func jpegPadTo(data []byte, end int) []byte {
	i := 2
	for data[i+1] != 0xda {
		i += 2 + int(binary.BigEndian.Uint16(data[i+2:]))
	}

	out := append([]byte{}, data[:2]...)

	for pad := end - (i + 4); pad > 0; {
		n := min(pad, 2+0xffff)
		if rest := pad - n; rest > 0 && rest < 4 {
			n -= 4
		}

		out = append(out, jpegSegment(0xe2, make([]byte, n-4))...)
		pad -= n
	}

	return append(out, data[2:]...)
}

// jpegSegment is one JPEG marker segment.
func jpegSegment(marker byte, payload []byte) []byte {
	segment := []byte{0xff, marker, 0, 0}
	binary.BigEndian.PutUint16(segment[2:], uint16(len(payload)+2))

	return append(segment, payload...)
}
