package header

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"io"
	"testing"
	"testing/iotest"

	"github.com/superiority-dev/blurb/internal/testimage"
)

// TestReadInfo requires [Read] to report each image's format, size as
// displayed, EXIF orientation and decode estimate, and its reader to replay
// the whole input.
func TestReadInfo(t *testing.T) {
	src := testimage.Gradient(300, 200)
	plain := testimage.EncodeJPEG(t, src)
	plainPNG := testimage.EncodePNG(t, src)
	grayPNG := testimage.EncodePNG(t, image.NewGray(image.Rect(0, 0, 300, 200)))
	exif := pngChunk("eXIf", exifData(6, binary.BigEndian))

	webp := testimage.WebPLossy()
	lossless := testimage.WebPLossless()

	// Go's encoder writes a 4:2:0 JPEG, padded to 304 by 208 at 1.5 bytes a
	// pixel; the PNG decodes to RGBA, twice that when interlaced, a gray one
	// with a tRNS chunk to NRGBA, and the GIF to a paletted image. The lossy
	// WebP decodes to 4:2:0 beside its 206-byte VP8 chunk, the lossless one at
	// 6 bytes a pixel.
	const (
		wantJPEG       = 304*208*3/2 + 1<<20
		wantPNG        = 300*200*4 + 1<<20
		wantInterlaced = 2*300*200*4 + 1<<20
		wantGIF        = 10*10 + 1<<20
		wantLossy      = 64*48*3/2 + 206 + 1<<20
		wantLossless   = 64*48*6 + 1<<20
	)

	cases := []struct {
		name  string
		input []byte
		want  Info
	}{
		{
			name:  "JPEG",
			input: plain,
			want: Info{
				Format:      formatJPEG,
				Width:       300,
				Height:      200,
				Orientation: OrientationNormal,
				DecodeBytes: wantJPEG,
			},
		},
		{
			name:  "JPEG, header ending at 1 MiB",
			input: jpegPadTo(plain, 1<<20),
			want: Info{
				Format:      formatJPEG,
				Width:       300,
				Height:      200,
				Orientation: OrientationNormal,
				DecodeBytes: wantJPEG,
			},
		},
		{
			name:  "JPEG, orientation 6",
			input: jpegWithEXIF(plain, 6, binary.BigEndian),
			want: Info{
				Format:      formatJPEG,
				Width:       200,
				Height:      300,
				Orientation: OrientationRotate90,
				DecodeBytes: wantJPEG,
			},
		},
		{
			name:  "PNG",
			input: plainPNG,
			want: Info{
				Format:      formatPNG,
				Width:       300,
				Height:      200,
				Orientation: OrientationNormal,
				DecodeBytes: wantPNG,
			},
		},
		{
			name:  "PNG, orientation 6 after 64 KiB of text",
			input: pngWithChunks(plainPNG, pngIHDREnd, pngChunk("tEXt", make([]byte, 64<<10)), exif),
			want: Info{
				Format:      formatPNG,
				Width:       200,
				Height:      300,
				Orientation: OrientationRotate90,
				DecodeBytes: wantPNG,
			},
		},
		{
			name:  "PNG, gray with tRNS",
			input: pngWithChunks(grayPNG, pngIHDREnd, pngChunk("tRNS", []byte{0, 0})),
			want: Info{
				Format:      formatPNG,
				Width:       300,
				Height:      200,
				Orientation: OrientationNormal,
				DecodeBytes: wantPNG,
			},
		},
		{
			name:  "PNG, interlaced",
			input: pngWithIHDR(plainPNG, 12, 1),
			want: Info{
				Format:      formatPNG,
				Width:       300,
				Height:      200,
				Orientation: OrientationNormal,
				DecodeBytes: wantInterlaced,
			},
		},
		{
			name:  "GIF",
			input: testimage.GIFZeroFrame(),
			want: Info{
				Format:      Format("gif"),
				Width:       10,
				Height:      10,
				Orientation: OrientationNormal,
				DecodeBytes: wantGIF,
			},
		},
		{
			name:  "WebP, lossy",
			input: webp,
			want: Info{
				Format:      formatWebP,
				Width:       64,
				Height:      48,
				Orientation: OrientationNormal,
				DecodeBytes: wantLossy,
			},
		},
		{
			name:  "WebP, lossless",
			input: lossless,
			want: Info{
				Format:      formatWebP,
				Width:       64,
				Height:      48,
				Orientation: OrientationNormal,
				DecodeBytes: wantLossless,
			},
		},
		{
			name: "WebP, lossless after 64 KiB of ICC profile",
			input: webpRIFF(
				webpChunk("VP8X", webpVP8X(1<<5, 64, 48)),
				webpChunk("ICCP", make([]byte, 64<<10)),
				lossless[12:],
			),
			want: Info{
				Format:      formatWebP,
				Width:       64,
				Height:      48,
				Orientation: OrientationNormal,
				DecodeBytes: wantLossless,
			},
		},
	}

	for _, tc := range cases {
		info, rest, err := Read(bytes.NewReader(tc.input))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		if info != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, info, tc.want)
		}

		if replayed, err := io.ReadAll(rest); err != nil || !bytes.Equal(replayed, tc.input) {
			t.Errorf("%s: replayed %d bytes, %v; want the input's %d", tc.name, len(replayed), err, len(tc.input))
		}
	}
}

func TestReadErrors(t *testing.T) {
	data := testimage.EncodeJPEG(t, testimage.Gradient(300, 200))

	cases := []struct {
		name  string
		input []byte
		want  error
	}{
		{
			name:  "empty",
			input: nil,
			want:  ErrUnreadable,
		},
		{
			name:  "not an image",
			input: []byte("hello, world"),
			want:  ErrUnsupportedFormat,
		},
		{
			name:  "header cut short",
			input: data[:40],
			want:  ErrUnreadable,
		},
		{
			name:  "header ending past 1 MiB",
			input: jpegPadTo(data, 1<<20+1),
			want:  ErrTooLarge,
		},
		{
			name:  "GIF screen of 0x0",
			input: emptyGIF,
			want:  ErrUnreadable,
		},
		{
			name: "WebP first frame past its canvas",
			input: animatedWebP(
				testimage.WebPLossy()[12:],
				false,
				image.Pt(2, 0),
				image.Pt(64, 48),
			),
			want: ErrUnreadable,
		},
	}

	for _, tc := range cases {
		if _, _, err := Read(bytes.NewReader(tc.input)); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}

// TestReadFails requires [Read] to return the reader's own error, however many
// bytes came before it.
func TestReadFails(t *testing.T) {
	errDown := errors.New("connection reset")
	jpegBytes := testimage.EncodeJPEG(t, testimage.Gradient(30, 20))
	pngBytes := testimage.EncodePNG(t, testimage.Gradient(30, 20))

	failAfter := func(b []byte) io.Reader {
		return io.MultiReader(bytes.NewReader(b), iotest.ErrReader(errDown))
	}

	cases := []struct {
		name string
		r    io.Reader
	}{
		{name: "no bytes", r: failAfter(nil)},
		{name: "PNG, 4 bytes", r: failAfter(pngBytes[:4])},
		{name: "JPEG, 20 bytes", r: failAfter(jpegBytes[:20])},
		{name: "PNG, signature and IHDR", r: failAfter(pngBytes[:pngIHDREnd])},
		{name: "JPEG whole, the error with its last bytes", r: iotest.DataErrReader(failAfter(jpegBytes))},
	}

	for _, tc := range cases {
		if _, _, err := Read(tc.r); !errors.Is(err, errDown) {
			t.Errorf("%s: got %v, want the reader's error", tc.name, err)
		}
	}
}

// TestReadStops requires [Read] to refuse an unsupported or invalid header
// within the first 64 KiB of its input.
func TestReadStops(t *testing.T) {
	sof3 := []byte{0xff, 0xd8}
	sof3 = append(sof3, jpegSegment(0xc3, []byte{8, 0, 16, 0, 16, 1, 1, 0x11, 0})...)

	png := testimage.EncodePNG(t, testimage.Gradient(40, 30))
	bitDepth3 := pngWithIHDR(png, 8, 3)
	widthZero := pngWithIHDR(png, 0, 0, 0, 0, 0)

	cases := []struct {
		name    string
		input   []byte
		feature *UnsupportedFeatureError
		err     error
	}{
		{
			name:  "lossless JPEG",
			input: sof3,
			feature: &UnsupportedFeatureError{
				Format:  formatJPEG,
				Feature: "unknown marker",
			},
		},
		{
			name:  "PNG bit depth 3",
			input: bitDepth3,
			feature: &UnsupportedFeatureError{
				Format:  formatPNG,
				Feature: "bit depth 3, color type 2",
			},
		},
		{
			name:  "PNG width 0",
			input: widthZero,
			err:   ErrUnreadable,
		},
	}

	for _, tc := range cases {
		counter := &countingReader{r: io.MultiReader(bytes.NewReader(tc.input), bytes.NewReader(make([]byte, 2<<20)))}
		_, _, err := Read(counter)

		if counter.n > 64<<10 {
			t.Errorf("%s: read %d bytes, want at most 64 KiB", tc.name, counter.n)
		}

		var feature *UnsupportedFeatureError
		if tc.feature != nil && (!errors.As(err, &feature) || *feature != *tc.feature) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.feature)
		}

		if tc.err != nil && !errors.Is(err, tc.err) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.err)
		}
	}
}

// TestReadAnimated requires [Read] to report an animated WebP's first frame,
// its estimate and its place on the canvas, and its reader to decode to that
// frame and then go on to the end of the input.
func TestReadAnimated(t *testing.T) {
	lossy := testimage.WebPLossy()
	alpha := testimage.WebPLossyAlpha()
	lossless := testimage.WebPLossless()
	origin, canvas := image.Point{}, image.Pt(64, 48)

	cases := []struct {
		name  string
		input []byte
		want  Info
	}{
		{
			name:  "lossy",
			input: animatedWebP(lossy[12:], false, origin, canvas),
			want: Info{
				Format:      formatWebP,
				Width:       64,
				Height:      48,
				Orientation: OrientationNormal,
				DecodeBytes: 64*48*3/2 + 206 + 1<<20,
				Frame:       image.Rect(0, 0, 64, 48),
			},
		},
		{
			name:  "lossy with alpha",
			input: animatedWebP(alpha[webpChunksStart:], true, origin, canvas),
			want: Info{
				Format:      formatWebP,
				Width:       64,
				Height:      48,
				Orientation: OrientationNormal,
				DecodeBytes: 64*48*3/2 + 206 + 64*48*5 + 1<<20,
				Frame:       image.Rect(0, 0, 64, 48),
			},
		},
		{
			name:  "lossless",
			input: animatedWebP(lossless[12:], false, origin, canvas),
			want: Info{
				Format:      formatWebP,
				Width:       64,
				Height:      48,
				Orientation: OrientationNormal,
				DecodeBytes: 64*48*6 + 1<<20,
				Frame:       image.Rect(0, 0, 64, 48),
			},
		},
		{
			name:  "lossy at (20,10) on 100x80",
			input: animatedWebP(lossy[12:], false, image.Pt(20, 10), image.Pt(100, 80)),
			want: Info{
				Format:      formatWebP,
				Width:       100,
				Height:      80,
				Orientation: OrientationNormal,
				DecodeBytes: 64*48*3/2 + 206 + 1<<20,
				Frame:       image.Rect(20, 10, 84, 58),
			},
		},
	}

	for _, tc := range cases {
		in := &countingReader{r: bytes.NewReader(tc.input)}

		info, rest, err := Read(in)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		if info != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, info, tc.want)
		}

		img, _, err := image.Decode(rest)
		if err != nil || img.Bounds() != image.Rect(0, 0, 64, 48) {
			t.Errorf("%s: decoded %v, %v; want the 64x48 first frame", tc.name, img, err)
			continue
		}

		if _, err := io.Copy(io.Discard, rest); err != nil || in.n != len(tc.input) {
			t.Errorf("%s: read %d bytes of %d, %v; want them all", tc.name, in.n, len(tc.input), err)
		}
	}

	// Cut before its first frame's bitstream, it is replayed as it is.
	cut := animatedWebP(lossy[12:], false, origin, canvas)[:webpFrameStart]

	_, rest, err := Read(bytes.NewReader(cut))
	if err != nil {
		t.Fatal(err)
	}

	if replayed, err := io.ReadAll(rest); err != nil || !bytes.Equal(replayed, cut) {
		t.Errorf("cut before the first frame: replayed %d bytes, %v; want the input's %d", len(replayed), err, len(cut))
	}
}

// TestStickyReader requires the reader's first error to come back from every
// later read, and io.EOF to pass through without being kept.
func TestStickyReader(t *testing.T) {
	sticky := &stickyReader{r: iotest.TimeoutReader(bytes.NewReader([]byte("ab")))}
	p := make([]byte, 1)

	for i, want := range []error{nil, iotest.ErrTimeout, iotest.ErrTimeout} {
		if _, err := sticky.Read(p); !errors.Is(err, want) {
			t.Errorf("read %d: %v, want %v", i+1, err, want)
		}
	}

	ended := &stickyReader{r: bytes.NewReader(nil)}
	if _, err := ended.Read(p); err != io.EOF || ended.err != nil {
		t.Errorf("at the end: %v, kept %v; want io.EOF, nothing kept", err, ended.err)
	}
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n

	return n, err
}

// emptyGIF is a GIF whose screen is 0x0, which image.DecodeConfig accepts.
var emptyGIF = []byte{'G', 'I', 'F', '8', '9', 'a', 0, 0, 0, 0, 0, 0, 0, 0x3b}
