package header

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/superiority-dev/blurb/internal/readers"
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
	exif := testimage.PNGChunk("eXIf", testimage.EXIFOrientation(6, binary.BigEndian))

	webp := testimage.WebPLossy()
	lossless := testimage.WebPLossless()

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
			input: testimage.JPEGPadTo(plain, 1<<20),
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
			input: testimage.JPEGWithEXIF(plain, 6, binary.BigEndian),
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
			name: "PNG, orientation 6 after 64 KiB of text",
			input: testimage.PNGWithChunks(
				plainPNG,
				testimage.PNGIHDREnd,
				testimage.PNGChunk("tEXt", make([]byte, 64<<10)),
				exif,
			),
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
			input: testimage.PNGWithChunks(grayPNG, testimage.PNGIHDREnd, testimage.PNGChunk("tRNS", []byte{0, 0})),
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
			input: testimage.PNGWithIHDR(plainPNG, 12, 1),
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
			// Read as a JPEG, the XMP chunk would be a frame header and an
			// APP0 segment.
			name: "WebP, lossless after JPEG markers in XMP",
			input: testimage.WebPFile(
				testimage.WebPVP8X(1<<2, 64, 48),
				testimage.WebPChunk("XMP ", []byte{0xff, 0xc0, 0, 2, 0xff, 0xe0, 0, 2}),
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
		{
			name: "WebP, lossless after 64 KiB of ICC profile",
			input: testimage.WebPFile(
				testimage.WebPVP8X(1<<5, 64, 48),
				testimage.WebPChunk("ICCP", make([]byte, 64<<10)),
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

// TestReadErrors requires [Read] to refuse each bad header with its error,
// named once in the message, and to return the reader's own error however many
// bytes came before it.
func TestReadErrors(t *testing.T) {
	errDown := errors.New("connection reset")
	jpegBytes := testimage.GradientJPEG(t, 30, 20)
	pastLimit := testimage.JPEGPadTo(jpegBytes, 1<<20+1)
	pngBytes := testimage.EncodePNG(t, testimage.Gradient(30, 20))
	lossy := testimage.WebPLossy()
	webpPastCanvas := testimage.WebPTwoFrames(lossy[12:], false, image.Pt(2, 0), image.Pt(64, 48))
	webpCut := testimage.WebPTwoFrames(lossy[12:], false, image.Point{}, image.Pt(64, 48))[:testimage.WebPFrameStart]
	lossless := testimage.WebPLossless()
	losslessPastLimit := testimage.WebPFile(
		testimage.WebPVP8X(0, 64, 48),
		testimage.WebPChunk("XMP ", make([]byte, 1<<20)),
		lossless[12:],
	)

	failAfter := func(b []byte) io.Reader {
		return io.MultiReader(bytes.NewReader(b), iotest.ErrReader(errDown))
	}

	cases := []struct {
		name string
		r    io.Reader
		want error
	}{
		{name: "empty", r: bytes.NewReader(nil), want: ErrUnreadable},
		{name: "not an image", r: bytes.NewReader([]byte("hello, world")), want: ErrUnsupportedFormat},
		{name: "header cut short", r: bytes.NewReader(jpegBytes[:40]), want: ErrUnreadable},
		{name: "header ending past 1 MiB", r: bytes.NewReader(pastLimit), want: ErrTooLarge},
		{name: "GIF screen of 0x0", r: bytes.NewReader(testimage.GIFEmptyScreen()), want: ErrUnreadable},
		{name: "PNG width 0", r: bytes.NewReader(testimage.PNGWithIHDR(pngBytes, 0, 0, 0, 0, 0)), want: ErrUnreadable},
		{name: "WebP first frame past its canvas", r: bytes.NewReader(webpPastCanvas), want: ErrUnreadable},
		{name: "WebP cut before its first frame", r: bytes.NewReader(webpCut), want: ErrUnreadable},
		{name: "lossless WebP cut inside its bitstream", r: bytes.NewReader(lossless[:30]), want: ErrUnreadable},
		{name: "lossless WebP bitstream past 1 MiB", r: bytes.NewReader(losslessPastLimit), want: ErrTooLarge},
		{name: "failing, no bytes", r: failAfter(nil), want: errDown},
		{name: "failing, PNG 4 bytes", r: failAfter(pngBytes[:4]), want: errDown},
		{name: "failing, JPEG 20 bytes", r: failAfter(jpegBytes[:20]), want: errDown},
		{name: "failing, PNG signature and IHDR", r: failAfter(pngBytes[:testimage.PNGIHDREnd]), want: errDown},
		{name: "failing with a whole JPEG's last bytes", r: iotest.DataErrReader(failAfter(jpegBytes)), want: errDown},
	}

	for _, tc := range cases {
		_, _, err := Read(tc.r)
		if !errors.Is(err, tc.want) || strings.Count(err.Error(), tc.want.Error()) != 1 {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}

// TestReadStops requires [Read] to refuse an image that uses a feature its
// decoder does not implement, reading at most 64 KiB past the image, with an
// [*UnsupportedFeatureError] naming the feature.
func TestReadStops(t *testing.T) {
	sof3 := append([]byte{0xff, 0xd8}, testimage.JPEGSegment(0xc3, []byte{8, 0, 16, 0, 16, 1, 1, 0x11, 0})...)

	// A CMYK 4:2:2 frame header ending at 1 MiB after APP2 segments of at most
	// 65,533 bytes, the longest payload a 16-bit segment length allows.
	cmyk422 := testimage.JPEGSegment(0xc0, []byte{8, 0, 16, 0, 16, 4, 1, 0x21, 0, 2, 0x11, 0, 3, 0x11, 0, 4, 0x21, 0})
	atLimit := []byte{0xff, 0xd8}

	for len(atLimit)+len(cmyk422) < 1<<20 {
		payload := min(1<<20-len(atLimit)-len(cmyk422)-4, 1<<16-3)
		atLimit = append(atLimit, testimage.JPEGSegment(0xe2, make([]byte, payload))...)
	}

	atLimit = append(atLimit, cmyk422...)

	cases := []struct {
		name  string
		input []byte
		want  UnsupportedFeatureError
	}{
		{
			name:  "lossless JPEG",
			input: sof3,
			want:  UnsupportedFeatureError{Format: formatJPEG, Feature: "unknown marker"},
		},
		{
			name:  "CMYK JPEG 4:2:2, header ending at 1 MiB",
			input: atLimit,
			want:  UnsupportedFeatureError{Format: formatJPEG, Feature: "luma/chroma subsampling ratio"},
		},
		{
			name:  "PNG bit depth 3",
			input: testimage.PNGWithIHDR(testimage.EncodePNG(t, testimage.Gradient(40, 30)), 8, 3),
			want:  UnsupportedFeatureError{Format: formatPNG, Feature: "bit depth 3, color type 2"},
		},
	}

	for _, tc := range cases {
		counter := &readers.Counting{R: io.MultiReader(bytes.NewReader(tc.input), bytes.NewReader(make([]byte, 2<<20)))}
		_, _, err := Read(counter)

		if counter.N > int64(len(tc.input))+64<<10 {
			t.Errorf("%s: read %d bytes, want at most 64 KiB past the image", tc.name, counter.N)
		}

		var feature *UnsupportedFeatureError
		if !errors.As(err, &feature) || *feature != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, err, &tc.want)
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

	// A lossless alpha bitstream of a predictor transform and one group, each
	// of one-symbol codes.
	predicted := testimage.WebPChunk("ALPH", []byte{1, 0x81, 0x88, 0x88, 0x40, 0x44, 0x44, 0x00})

	cases := []struct {
		name  string
		input []byte
		want  Info
	}{
		{
			name:  "lossy",
			input: testimage.WebPTwoFrames(lossy[12:], false, origin, canvas),
			want: Info{
				Format:      formatWebP,
				Width:       64,
				Height:      48,
				Orientation: OrientationNormal,
				DecodeBytes: wantLossy,
				Frame:       image.Rect(0, 0, 64, 48),
			},
		},
		{
			name:  "lossy with alpha",
			input: testimage.WebPTwoFrames(alpha[testimage.WebPChunksStart:], true, origin, canvas),
			want: Info{
				Format:      formatWebP,
				Width:       64,
				Height:      48,
				Orientation: OrientationNormal,
				DecodeBytes: wantLossy + 64*48*5 + 10931,
				Frame:       image.Rect(0, 0, 64, 48),
			},
		},
		{
			name:  "lossless",
			input: testimage.WebPTwoFrames(lossless[12:], false, origin, canvas),
			want: Info{
				Format:      formatWebP,
				Width:       64,
				Height:      48,
				Orientation: OrientationNormal,
				DecodeBytes: wantLossless,
				Frame:       image.Rect(0, 0, 64, 48),
			},
		},
		{
			// The predictor's sub-image is the frame's 16 by 12 tiles, not the
			// canvas's 25 by 20, at 4 bytes each: 768 bytes and 208 more. It
			// and the alpha each have a group of five one-node trees.
			name:  "lossy with alpha at (20,10) on 100x80",
			input: testimage.WebPTwoFrames(append(predicted, lossy[12:]...), true, image.Pt(20, 10), image.Pt(100, 80)),
			want: Info{
				Format:      formatWebP,
				Width:       100,
				Height:      80,
				Orientation: OrientationNormal,
				DecodeBytes: wantLossy + 64*48*5 + 976 + 2*(3366+5*26),
				Frame:       image.Rect(20, 10, 84, 58),
			},
		},
		{
			name:  "lossy at (20,10) on 100x80",
			input: testimage.WebPTwoFrames(lossy[12:], false, image.Pt(20, 10), image.Pt(100, 80)),
			want: Info{
				Format:      formatWebP,
				Width:       100,
				Height:      80,
				Orientation: OrientationNormal,
				DecodeBytes: wantLossy,
				Frame:       image.Rect(20, 10, 84, 58),
			},
		},
	}

	for _, tc := range cases {
		in := &readers.Counting{R: bytes.NewReader(tc.input)}

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
		}

		if _, err := io.Copy(io.Discard, rest); err != nil || in.N != int64(len(tc.input)) {
			t.Errorf("%s: read %d bytes of %d, %v; want them all", tc.name, in.N, len(tc.input), err)
		}
	}
}

// TestReadJPEG requires the reader [Read] returns for a JPEG to stop its decode
// at an APP0 segment after the first scan, and to fail every read after.
func TestReadJPEG(t *testing.T) {
	jpegBytes := testimage.GradientJPEG(t, 30, 20)
	eoi := len(jpegBytes) - 2
	lateAPP0 := slices.Concat(jpegBytes[:eoi], testimage.JPEGSegment(0xe0, []byte("JFIF\x00")), jpegBytes[eoi:])

	_, rest, err := Read(bytes.NewReader(lateAPP0))
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := image.Decode(rest); !errors.Is(err, errJPEGLateApp) {
		t.Errorf("got %v, want %v", err, errJPEGLateApp)
	}

	if _, err := rest.Read(make([]byte, 1)); err != errJPEGLateApp {
		t.Errorf("read again: got %v, want %v", err, errJPEGLateApp)
	}
}

// FuzzRead requires each error from [Read] to be one of the errors its doc
// names, each [Info] it returns to have a positive size and estimate, and the
// reader it returns to replay the whole input when [Info.Frame] is empty,
// unless it stops a JPEG.
func FuzzRead(f *testing.F) {
	src := testimage.Gradient(30, 20)
	jpegBytes := testimage.EncodeJPEG(f, src)

	for _, seed := range [][]byte{
		jpegBytes,
		testimage.JPEGWithEXIF(jpegBytes, 6, binary.LittleEndian),
		testimage.EncodePNG(f, src),
		testimage.EncodeGIF(f, src),
		testimage.GIFAnimated(),
		testimage.WebPLossy(),
		testimage.WebPLossyAlpha(),
		testimage.WebPLossless(),
		testimage.WebPExtendedLossless(),
		testimage.WebPAnimated(),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		info, rest, err := Read(bytes.NewReader(data))
		if err != nil {
			_, unsupported := errors.AsType[*UnsupportedFeatureError](err)
			if !unsupported && !errors.Is(err, ErrUnsupportedFormat) && !errors.Is(err, ErrUnreadable) &&
				!errors.Is(err, ErrTooLarge) {
				t.Fatalf("got %v, an error Read does not document", err)
			}

			return
		}

		if info.Width <= 0 || info.Height <= 0 || info.DecodeBytes <= 0 {
			t.Fatalf("got %+v, want a positive size and estimate", info)
		}

		replayed, err := io.ReadAll(rest)
		if errors.Is(err, errJPEGScans) || errors.Is(err, errJPEGLateApp) {
			return
		}

		if err != nil {
			t.Fatal(err)
		}

		if info.Frame.Empty() && !bytes.Equal(replayed, data) {
			t.Fatalf("rest replays %d bytes, want the input's %d", len(replayed), len(data))
		}
	})
}

// The want constants are the test images' [Info.DecodeBytes], each with the
// 1 MiB decoding overhead. Go's encoder writes a 4:2:0 JPEG, padded to 304 by
// 208 at 1.5 bytes a pixel; the PNG decodes to RGBA, twice that when
// interlaced, a gray one with a tRNS chunk to NRGBA, and the GIF to a paletted
// image. The lossy WebP decodes to 4:2:0 beside its 206-byte VP8 chunk, the
// lossless one at 4 bytes a pixel beside the 23,910 bytes x/image's decoder
// allocates for its bitstream, each allocation rounded up as heapBytes rounds
// it.
const (
	wantJPEG       = 304*208*3/2 + 1<<20
	wantPNG        = 300*200*4 + 1<<20
	wantInterlaced = 2*300*200*4 + 1<<20
	wantGIF        = 10*10 + 1<<20
	wantLossy      = 64*48*3/2 + 206 + 1<<20
	wantLossless   = 64*48*4 + 23910 + 1<<20
)
