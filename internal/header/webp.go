package header

import (
	"encoding/binary"
	"image"
	"image/color"
	"io"
)

// A WebP file, as per RFC 9649 sections 2.3 and 2.4: a RIFF header of
// webpHeaderLen bytes, then chunks, each a FourCC of webpFourCCLen bytes, its
// payload's size, a little-endian integer of webpSizeLen bytes, then the
// payload, padded to an even length. A VP8X payload, as per section 2.7, is
// webpVP8XLen bytes, with the alpha flag webpAlphaFlag; an ANMF payload, as
// per section 2.7.1.1, starts with a frame header of webpFrameHead bytes,
// then the frame's chunks. The frame header's 24-bit fields hold the frame's
// x and y in units of webpFrameUnit pixels, at webpFrameX and webpFrameY, and
// its width and height less one, at webpFrameWidth and webpFrameHeight.
const (
	webpHeaderLen   = 12
	webpFourCCLen   = 4
	webpSizeLen     = 4
	webpChunkHead   = webpFourCCLen + webpSizeLen
	webpVP8XLen     = 10
	webpAlphaFlag   = 1 << 4
	webpFrameHead   = 16
	webpFrameUnit   = 2
	webpFrameX      = 0
	webpFrameY      = 3
	webpFrameWidth  = 6
	webpFrameHeight = 9
)

// A VP8 frame, as per RFC 6386 sections 9.1 and 9.5: a first data partition
// whose size is a 19-bit field, so under webpFirstPartitionLimit bytes, then
// up to webpPartitions coefficient data partitions, each but the last with a
// 24-bit size, so under webpPartitionLimit bytes. x/image's decoder refuses a
// last one that large or larger, so it holds at most webpMaxFrame bytes of a
// frame.
const (
	webpFirstPartitionLimit = 1 << 19
	webpPartitions          = 8
	webpPartitionLimit      = 1 << 24
	webpMaxFrame            = webpFirstPartitionLimit + webpPartitions*webpPartitionLimit
)

// Terms of a WebP's decode estimate. A lossy WebP decodes into a 4:2:0 YCbCr
// image of whole macroblocks, webpYCbCr bytes a pixel, and holds its
// compressed frame meanwhile, at most webpMaxFrame bytes. Alpha adds a plane
// and, for lossless-compressed alpha, an NRGBA image it is decoded from,
// webpAlpha bytes a pixel together. A lossless WebP decodes into an NRGBA
// image, beside its packed pixels while it unpacks a palette, webpLossless
// bytes a pixel.
const (
	webpMacroblock = 16
	webpYCbCr      = 1.5
	webpAlpha      = 5
	webpLossless   = 6
)

// webpKind is the bitstream a WebP's image data is in.
type webpKind int

// The bitstreams, named by their chunks as per RFC 9649: VP8 lossy, VP8L
// lossless; webpUnknown is a walk that ended before either.
const (
	webpUnknown webpKind = iota
	webpVP8
	webpVP8L
)

// webpImage is what a WebP's chunks say of the image that decodes: the file's
// only image, or an animation's first frame.
type webpImage struct {
	kind webpKind

	// frame is a lossy image's VP8 chunk size, at most webpMaxFrame.
	frame int64

	// For an animation's first frame: where it lies on the canvas,
	// whether an ALPH chunk comes with it, and where its chunks start in
	// the file and how many bytes they take.
	animated      bool
	bounds        image.Rectangle
	alpha         bool
	start, length int64
}

// webpSplice represents a WebP file whose start is replaced: head, then the
// file's bytes from offset from.
type webpSplice struct {
	head []byte
	from int64
}

// scanWebP walks the chunks of the WebP file r holds up to the image data
// that decodes, an animation's first frame's in an animated file.
func scanWebP(r io.Reader) webpImage {
	var (
		img  webpImage
		head [webpChunkHead]byte
		pos  int64 = webpHeaderLen
	)

	if _, err := io.CopyN(io.Discard, r, webpHeaderLen); err != nil {
		return img
	}

	for {
		if _, err := io.ReadFull(r, head[:]); err != nil {
			return webpImage{animated: img.animated}
		}

		pos += webpChunkHead
		size := int64(binary.LittleEndian.Uint32(head[webpFourCCLen:]))

		switch string(head[:webpFourCCLen]) {
		// The fourth character is a space, as per RFC 9649 section 2.5.
		case "VP8 ":
			img.kind, img.frame = webpVP8, min(size, webpMaxFrame)
			return img
		case "VP8L":
			img.kind = webpVP8L
			return img
		case "ALPH":
			img.alpha = true
		case "ANMF":
			// A frame holds a bitstream, as per RFC 9649 section 2.7.1.1, so
			// a second frame before one is malformed.
			var frame [webpFrameHead]byte
			if img.animated || size < webpFrameHead {
				return webpImage{animated: true}
			}

			if _, err := io.ReadFull(r, frame[:]); err != nil {
				return webpImage{animated: true}
			}

			pos += webpFrameHead
			x, y := webpFrameUnit*uint24(frame[webpFrameX:]), webpFrameUnit*uint24(frame[webpFrameY:])
			img = webpImage{
				animated: true,
				bounds:   image.Rect(x, y, x+1+uint24(frame[webpFrameWidth:]), y+1+uint24(frame[webpFrameHeight:])),
				start:    pos,
				length:   size - webpFrameHead,
			}

			continue
		}

		if _, err := io.CopyN(io.Discard, r, size+size%2); err != nil {
			return webpImage{animated: img.animated}
		}

		pos += size + size%2
	}
}

// webpDecodeBytes estimates the memory decoding a WebP of cfg's size and color
// model holds, for its image img. An animation is charged for its first frame;
// an unknown bitstream as lossy with alpha and the largest frame, more than a
// lossless one.
func webpDecodeBytes(cfg image.Config, img webpImage) int64 {
	alpha := cfg.ColorModel != color.YCbCrModel
	if img.animated && img.kind != webpUnknown {
		cfg.Width, cfg.Height, alpha = img.bounds.Dx(), img.bounds.Dy(), img.alpha
	}

	switch img.kind {
	case webpVP8L:
		return decodeBytes(cfg.Width, cfg.Height, webpLossless)
	case webpVP8:
		return webpLossyBytes(cfg, img.frame, alpha)
	default:
		return webpLossyBytes(cfg, webpMaxFrame, true)
	}
}

// firstFrameSplice returns the splice that makes an animated WebP read as a
// WebP of its first frame img alone, which x/image decodes.
func firstFrameSplice(img webpImage) *webpSplice {
	var flags byte
	if img.alpha {
		flags = webpAlphaFlag
	}

	head := binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(webpFourCCLen+webpChunkHead+webpVP8XLen+img.length))
	head = append(head, "WEBP"...)
	head = append(head, "VP8X"...)
	head = binary.LittleEndian.AppendUint32(head, webpVP8XLen)
	head = append(head, flags, 0, 0, 0)
	head = appendUint24(head, img.bounds.Dx()-1)
	head = appendUint24(head, img.bounds.Dy()-1)

	return &webpSplice{head: head, from: img.start}
}

// webpLossyBytes estimates the memory decoding a lossy WebP of cfg's size
// holds, frame bytes of its compressed frame among them, with an alpha plane
// or without.
func webpLossyBytes(cfg image.Config, frame int64, alpha bool) int64 {
	mbCols := (cfg.Width + webpMacroblock - 1) / webpMacroblock
	mbRows := (cfg.Height + webpMacroblock - 1) / webpMacroblock

	n := decodeBytes(mbCols*webpMacroblock, mbRows*webpMacroblock, webpYCbCr) + frame
	if alpha {
		n += int64(cfg.Width) * int64(cfg.Height) * webpAlpha
	}

	return n
}

// uint24 reads a little-endian 24-bit field.
func uint24(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }

// appendUint24 appends v as a little-endian 24-bit field.
func appendUint24(b []byte, v int) []byte { return append(b, byte(v), byte(v>>8), byte(v>>16)) }
