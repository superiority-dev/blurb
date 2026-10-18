// Package header reads an image's header without decoding the image.
package header

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"

	// Register the GIF decoder with image.DecodeConfig.
	_ "image/gif"

	// Register the WebP decoder with image.DecodeConfig.
	_ "golang.org/x/image/webp"
)

// maxBytes is the most of the input [Read] takes before giving up.
const maxBytes = 1 << 20 // 1 MiB

var (
	// ErrUnsupportedFormat is returned for input whose format no registered
	// decoder recognizes.
	ErrUnsupportedFormat = errors.New("header: unsupported image format")

	// ErrUnreadable is returned for input that is empty, or that claims a
	// supported format and does not decode.
	ErrUnreadable = errors.New("header: unreadable image")

	// ErrTooLarge is returned when the header does not end within the input's
	// first 1 MiB.
	ErrTooLarge = errors.New("header: image header too large")
)

// UnsupportedFeatureError is returned for an image in a format [Read] reads
// that uses a feature its decoder does not implement, such as a CMYK JPEG
// subsampled 4:2:2.
type UnsupportedFeatureError struct {
	Format Format

	// Feature is the decoder's name for what it does not implement, such as
	// "precision" for a JPEG that is not 8 bits a sample.
	Feature string
}

func (e *UnsupportedFeatureError) Error() string {
	return fmt.Sprintf("header: unsupported %s feature: %s", e.Format, e.Feature)
}

// Format is an image format, named as [image.DecodeConfig] names it.
type Format string

// The formats [Read] and the decode estimate treat apart.
const (
	formatJPEG Format = "jpeg"
	formatPNG  Format = "png"
	formatWebP Format = "webp"
)

// Info is what an image's header says. Its width and height are as the image
// is displayed.
type Info struct {
	Format        Format
	Width, Height int

	// Orientation is [OrientationNormal] when [Read] finds none.
	Orientation Orientation

	// DecodeBytes estimates the most memory decoding the image holds at
	// once.
	DecodeBytes int64

	// Frame is where an animation's first frame lies on its canvas when
	// [Read] has the decoder read that frame alone; it is empty otherwise.
	Frame image.Rectangle
}

// newInfo returns the [Info] for the image [image.DecodeConfig] read as cfg,
// whose header starts with buf and goes on in rest, and, for an animated
// WebP, the splice that makes it read as its first frame alone; or
// [ErrUnreadable] when cfg has no pixels.
func newInfo(cfg image.Config, format Format, buf []byte, rest io.Reader) (Info, *webpSplice, error) {
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return Info{}, nil, ErrUnreadable
	}

	var splice *webpSplice

	info := Info{Format: format, Width: cfg.Width, Height: cfg.Height, Orientation: OrientationNormal}

	switch format {
	case formatJPEG:
		frame, orientation := scanJPEG(buf)
		info.Orientation = orientation
		info.DecodeBytes = jpegDecodeBytes(cfg, frame)
	case formatPNG:
		orientation, transparent := scanPNG(io.MultiReader(bytes.NewReader(buf), rest))
		info.Orientation = orientation
		info.DecodeBytes = pngDecodeBytes(cfg, pngInterlaced(buf), transparent)
	case formatWebP:
		img := scanWebP(io.MultiReader(bytes.NewReader(buf), rest))
		info.DecodeBytes = webpDecodeBytes(cfg, img)

		if img.animated && img.kind != webpUnknown {
			if !img.bounds.In(image.Rect(0, 0, cfg.Width, cfg.Height)) {
				return Info{}, nil, ErrUnreadable
			}

			info.Frame, splice = img.bounds, firstFrameSplice(img)
		}
	default:
		info.DecodeBytes = modelDecodeBytes(cfg)
	}

	if info.Orientation.SwapsSides() {
		info.Width, info.Height = cfg.Height, cfg.Width
	}

	return info, splice, nil
}

// Read reads r's header, up to 1 MiB: as much as [image.DecodeConfig] needs
// and, for a PNG or a WebP, its chunks up to the image data. It returns what
// the header says and a reader that replays what was read before the rest of
// r; for an animated WebP, the reader reads as a WebP of the first frame
// alone, then goes on into the rest of r. It can return
// [ErrUnsupportedFormat], an [*UnsupportedFeatureError], [ErrTooLarge],
// [ErrUnreadable] or, when r fails, r's error.
func Read(r io.Reader) (Info, io.Reader, error) {
	var buf bytes.Buffer

	src := &stickyReader{r: r}
	in := io.TeeReader(io.LimitReader(src, maxBytes), &buf)

	var (
		info   Info
		splice *webpSplice
	)

	cfg, format, err := image.DecodeConfig(in)
	if err == nil {
		info, splice, err = newInfo(cfg, Format(format), buf.Bytes(), in)
	} else {
		err = configError(err, Format(format), buf.Len())
	}

	if src.err != nil {
		return Info{}, nil, src.err
	}

	if err != nil {
		return Info{}, nil, err
	}

	if splice != nil && splice.from <= int64(buf.Len()) {
		return info, io.MultiReader(bytes.NewReader(splice.head), bytes.NewReader(buf.Bytes()[splice.from:]), r), nil
	}

	return info, io.MultiReader(&buf, r), nil
}

// stickyReader reads through r; once r returns an error other than io.EOF,
// every later read returns it.
type stickyReader struct {
	r   io.Reader
	err error
}

func (s *stickyReader) Read(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}

	n, err := s.r.Read(p)
	if err != nil && err != io.EOF {
		s.err = err
	}

	return n, err
}

// configError returns the error [Read] reports when [image.DecodeConfig]
// fails with err, having read n bytes of an image in format.
func configError(err error, format Format, n int) error {
	feature, unsupported := unsupportedFeature(err)

	switch {
	case n == 0:
		return ErrUnreadable
	case unsupported:
		return &UnsupportedFeatureError{Format: format, Feature: feature}
	case errors.Is(err, image.ErrFormat):
		return ErrUnsupportedFormat
	case n >= maxBytes:
		return ErrTooLarge
	default:
		return fmt.Errorf("%w: %w", ErrUnreadable, err)
	}
}

// unsupportedFeature returns the feature a standard decoder reports it does
// not implement, when err is that report.
func unsupportedFeature(err error) (string, bool) {
	var (
		jpegErr jpeg.UnsupportedError
		pngErr  png.UnsupportedError
	)

	switch {
	case errors.As(err, &jpegErr):
		return string(jpegErr), true
	case errors.As(err, &pngErr):
		return string(pngErr), true
	default:
		return "", false
	}
}
