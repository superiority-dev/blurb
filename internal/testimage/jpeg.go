package testimage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"slices"
	"testing"
)

// GradientJPEG returns a w by h [Gradient] encoded by [EncodeJPEG].
func GradientJPEG(t testing.TB, w, h int) []byte {
	t.Helper()

	return EncodeJPEG(t, Gradient(w, h))
}

// EncodeJPEG returns img encoded as a JPEG of quality 90.
func EncodeJPEG(t testing.TB, img image.Image) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

// JPEGPadTo returns data with APP2 segments after its SOI, so that what
// [image.DecodeConfig] reads ends at byte end. Without JFIF's APP0 segment,
// which Go's encoder does not write, it reads up to the SOS segment's length.
// It panics if end falls before that length ends, or 1 to 3 bytes after it,
// where no segment fits. A marker is 2 bytes and a segment's head, the marker
// and its length, 4.
func JPEGPadTo(data []byte, end int) []byte {
	i := 2
	for data[i+1] != 0xda {
		i += 2 + int(binary.BigEndian.Uint16(data[i+2:]))
	}

	pad := end - (i + 4)
	if pad < 0 || pad > 0 && pad < 4 {
		panic(fmt.Sprintf("testimage: no APP2 segments end a JPEG at %d", end))
	}

	var segments []byte

	for pad > 0 {
		n := min(pad, 2+0xffff)
		if rest := pad - n; rest > 0 && rest < 4 {
			n -= 4
		}

		segments = append(segments, JPEGSegment(0xe2, make([]byte, n-4))...)
		pad -= n
	}

	return slices.Insert(slices.Clone(data), 2, segments...)
}

// JPEGWithEXIF returns data with an EXIF segment carrying orientation after
// its SOI.
func JPEGWithEXIF(data []byte, orientation uint16, order binary.ByteOrder) []byte {
	segment := JPEGSegment(0xe1, append([]byte("Exif\x00\x00"), EXIFOrientation(orientation, order)...))

	return slices.Insert(slices.Clone(data), 2, segment...)
}

// JPEGSegment returns one JPEG marker segment.
func JPEGSegment(marker byte, payload []byte) []byte {
	segment := []byte{0xff, marker, 0, 0}
	binary.BigEndian.PutUint16(segment[2:], uint16(len(payload)+2))

	return append(segment, payload...)
}
