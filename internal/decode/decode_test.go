package decode

import (
	"bytes"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"testing"

	"github.com/superiority-dev/blurb/internal/header"
	"github.com/superiority-dev/blurhash"
	"github.com/superiority-dev/thumbhash"

	"github.com/superiority-dev/blurb/internal/testimage"
)

// TestSmall requires each format's picture, and a portrait one, to give the
// BlurHash and ThumbHash pinned for it, with the alpha its decoded type
// carries.
func TestSmall(t *testing.T) {
	src := testimage.Gradient(300, 200)

	cases := []struct {
		name      string
		data      []byte
		alpha     bool
		blurHash  string
		thumbHash string
	}{
		{
			name:      "JPEG",
			data:      testimage.EncodeJPEG(t, src),
			alpha:     false,
			blurHash:  "L$HV6n2a$2SchlawjnfAgdfMfTfV",
			thumbHash: "df070a459a7276877085886798778777808308f777",
		},
		{
			name:      "PNG",
			data:      testimage.EncodePNG(t, src),
			alpha:     false,
			blurHash:  "L$HLl12a$2SchlawjnfAgdfMfTfV",
			thumbHash: "df070a459a7276777085886798778777808308f878",
		},
		{
			name:      "portrait JPEG",
			data:      testimage.EncodeJPEG(t, testimage.Gradient(200, 300)),
			alpha:     false,
			blurHash:  "L$HLl02U$6SghrawfMf8gZfdfTfl",
			thumbHash: "df070a451a7087727677856898877778838008f877",
		},
		{
			name:      "GIF",
			data:      encodeGIF(t, src),
			alpha:     false,
			blurHash:  "L#HV9v2a#+Schlawj-a%gdbCfAjy",
			thumbHash: "df070a459a7276777085886798778877808307f787",
		},
		{
			name:      "lossy WebP",
			data:      testimage.WebPLossy(),
			alpha:     false,
			blurHash:  "LsGlL+2e#=Sim2a%juf8gLfkfQfj",
			thumbHash: "dff70565967287877087788888787787708107f888",
		},
		{
			name:      "lossy WebP with alpha",
			data:      testimage.WebPLossyAlpha(),
			alpha:     true,
			blurHash:  "LRBCND6.w#Si;FFNsWWrr#SPjua}",
			thumbHash: "5e2786448e29818760788578072766806e7a78f88778888807",
		},
		{
			name:      "lossless WebP",
			data:      testimage.WebPLossless(),
			alpha:     true,
			blurHash:  "L%HetL2e$8Sihua%juf8gefRfQfQ",
			thumbHash: "dff705759a8287878088878888787888807108f877",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, rest, err := header.Read(bytes.NewReader(tc.data))
			if err != nil {
				t.Fatal(err)
			}

			small, alpha, err := Small(rest, info)
			if err != nil {
				t.Fatal(err)
			}

			blurHash, err := blurhash.Encode(4, 3, small)
			if err != nil {
				t.Fatal(err)
			}

			thumbHash, err := thumbhash.Encode(small)
			if err != nil {
				t.Fatal(err)
			}

			if blurHash != tc.blurHash || hex.EncodeToString(thumbHash) != tc.thumbHash || alpha != tc.alpha {
				t.Errorf(
					"got %q %x alpha %t, want %q %s alpha %t",
					blurHash,
					thumbHash,
					alpha,
					tc.blurHash,
					tc.thumbHash,
					tc.alpha,
				)
			}
		})
	}
}

// TestSmallOrientation requires the picture to be turned as the orientation
// says, a quarter turn swapping its sides, and the zero Info to leave it as
// decoded.
func TestSmallOrientation(t *testing.T) {
	data := testimage.EncodePNG(t, testimage.Gradient(300, 200))

	cases := []struct {
		info header.Info
		want image.Point
	}{
		{info: header.Info{Width: 300, Height: 200, Orientation: header.OrientationNormal}, want: image.Pt(100, 66)},
		{info: header.Info{Width: 200, Height: 300, Orientation: header.OrientationRotate90}, want: image.Pt(66, 100)},
		{info: header.Info{}, want: image.Pt(100, 66)},
	}

	for _, tc := range cases {
		small, alpha, err := Small(bytes.NewReader(data), tc.info)
		if err != nil {
			t.Fatal(err)
		}

		if got := small.Rect.Size(); got != tc.want || alpha {
			t.Errorf("orientation %d: %v alpha %t, want %v and no alpha", tc.info.Orientation, got, alpha, tc.want)
		}
	}
}

// TestSmallFirstFrame requires an animation's first frame to be drawn where
// it lies on its canvas, turned or not, transparent around it and reported
// as carrying alpha; with the zero Info, the frame is the whole picture.
func TestSmallFirstFrame(t *testing.T) {
	animatedGIF := testimage.GIFAnimated()

	cases := []struct {
		name   string
		data   []byte
		info   *header.Info
		size   image.Point
		opaque image.Rectangle
	}{
		{
			name: "WebP",
			data: testimage.WebPAnimated(),
			size: image.Pt(100, 75),
			// The frame at (24,16)-(40,32) of 64x48, on 100x75.
			opaque: image.Rect(37, 25, 62, 50),
		},
		{
			name: "GIF",
			data: animatedGIF,
			size: image.Pt(100, 75),
			// The frame at (8,8)-(24,24) of 64x48, on 100x75.
			opaque: image.Rect(12, 12, 37, 37),
		},
		{
			name: "WebP turned a quarter",
			data: testimage.WebPAnimated(),
			info: &header.Info{
				Width:       48,
				Height:      64,
				Orientation: header.OrientationRotate90,
				Frame:       image.Rect(24, 16, 40, 32),
			},
			size: image.Pt(75, 100),
			// The WebP row's frame, turned.
			opaque: image.Rect(25, 37, 50, 62),
		},
	}

	for _, tc := range cases {
		info, rest, err := header.Read(bytes.NewReader(tc.data))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		if tc.info != nil {
			info = *tc.info
		}

		small, alpha, err := Small(rest, info)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		opaque := opaqueBounds(small)
		if small.Rect.Size() != tc.size ||
			!alpha ||
			opaque != tc.opaque ||
			small.RGBAAt(0, 0) != (color.RGBA{}) {
			t.Errorf(
				"%s: %v alpha %t, opaque in %v, corner %v; want %v, alpha, opaque in %v, corner transparent",
				tc.name,
				small.Rect.Size(),
				alpha,
				opaque,
				small.RGBAAt(0, 0),
				tc.size,
				tc.opaque,
			)
		}
	}

	small, alpha, err := Small(bytes.NewReader(animatedGIF), header.Info{})
	if err != nil {
		t.Fatal(err)
	}

	if opaque := opaqueBounds(small); opaque != image.Rect(0, 0, 100, 100) || alpha {
		t.Errorf("zero Info: opaque within %v alpha %t, want all of 100x100 and no alpha", opaque, alpha)
	}
}

func TestSmallErrors(t *testing.T) {
	data := testimage.EncodeJPEG(t, testimage.Gradient(300, 200))

	cases := map[string][]byte{
		"zero-pixel frame": testimage.GIFZeroFrame(),
		"body cut short":   data[:len(data)/2],
		"not an image":     []byte("hello, world"),
	}

	for name, input := range cases {
		info := header.Info{Width: 300, Height: 200}
		if _, _, err := Small(bytes.NewReader(input), info); !errors.Is(err, header.ErrUnreadable) {
			t.Errorf("%s: got %v, want header.ErrUnreadable", name, err)
		}
	}
}

func TestHasAlpha(t *testing.T) {
	r := image.Rect(0, 0, 1, 1)
	opaque := image.NewPaletted(r, color.Palette{color.Black, color.White})
	seeThrough := image.NewPaletted(r, color.Palette{color.Black, color.Transparent})

	cases := map[string]struct {
		img  image.Image
		want bool
	}{
		"NRGBA":                 {img: image.NewNRGBA(r), want: true},
		"NRGBA64":               {img: image.NewNRGBA64(r), want: true},
		"NYCbCrA":               {img: image.NewNYCbCrA(r, image.YCbCrSubsampleRatio420), want: true},
		"paletted, transparent": {img: seeThrough, want: true},
		"paletted, opaque":      {img: opaque, want: false},
		"YCbCr":                 {img: image.NewYCbCr(r, image.YCbCrSubsampleRatio420), want: false},
		"RGBA":                  {img: image.NewRGBA(r), want: false},
		"Gray":                  {img: image.NewGray(r), want: false},
	}

	for name, tc := range cases {
		if got := hasAlpha(tc.img); got != tc.want {
			t.Errorf("%s: %t, want %t", name, got, tc.want)
		}
	}
}

// opaqueBounds is the smallest rectangle holding img's opaque pixels.
func opaqueBounds(img *image.RGBA) image.Rectangle {
	var r image.Rectangle
	for y := range img.Rect.Dy() {
		for x := range img.Rect.Dx() {
			if img.RGBAAt(x, y).A == 0xff {
				r = r.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}

	return r
}

func encodeGIF(t *testing.T, img image.Image) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}
