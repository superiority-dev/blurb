package header

import (
	"encoding/binary"
	"image"
	"image/color"
	"io"
)

// A WebP file, as per RFC 9649 sections 2.3 and 2.4: a RIFF header, then
// chunks, each a FourCC, its payload's size and the payload, padded to an
// even length.
const (
	webpHeaderLen = 12
	webpFourCCLen = 4

	// webpSizeLen is the length of a chunk's payload size, a little-endian
	// integer.
	webpSizeLen = 4

	webpChunkHead = webpFourCCLen + webpSizeLen

	// webpVP8XLen is the length of a VP8X payload, as per section 2.7.
	webpVP8XLen = 10

	// webpAlphaFlag is the VP8X flag set when any frame holds alpha.
	webpAlphaFlag = 1 << 4

	// webpFrameHead is the length of the frame header an ANMF payload starts
	// with, as per section 2.7.1.1; the frame's chunks follow it.
	webpFrameHead = 16

	// webpFrameUnit is the frame header's unit of x and y, in pixels.
	webpFrameUnit = 2

	// webpFrameX is the offset in the frame header of the frame's x, a
	// 24-bit field.
	webpFrameX = 0

	// webpFrameY is the offset in the frame header of the frame's y, a
	// 24-bit field.
	webpFrameY = 3

	// webpFrameWidth is the offset in the frame header of the frame's width
	// less one, a 24-bit field.
	webpFrameWidth = 6

	// webpFrameHeight is the offset in the frame header of the frame's
	// height less one, a 24-bit field.
	webpFrameHeight = 9
)

// A VP8 frame's partitions, as per RFC 6386 sections 9.1 and 9.5.
const (
	// webpFirstPartitionLimit is what the first data partition stays under:
	// its size is a 19-bit field.
	webpFirstPartitionLimit = 1 << 19 // 512 KiB

	// webpPartitions is the most coefficient data partitions a frame has.
	webpPartitions = 8

	// webpPartitionLimit is what each coefficient data partition but the
	// last stays under: its size is a 24-bit field. x/image's decoder
	// refuses a last one that large or larger.
	webpPartitionLimit = 1 << 24 // 16 MiB

	// webpMaxFrame is the most bytes of a frame x/image's decoder holds.
	webpMaxFrame = webpFirstPartitionLimit + webpPartitions*webpPartitionLimit
)

// Terms of a WebP's decode estimate.
const (
	// webpMacroblock is the side of a macroblock, in pixels; a lossy WebP
	// decodes into an image of whole macroblocks.
	webpMacroblock = 16

	// webpYCbCr is the bytes per pixel of the 4:2:0 YCbCr image a lossy WebP
	// decodes into, while it holds its compressed frame.
	webpYCbCr = 1.5

	// webpAlpha is the bytes per pixel alpha adds: a plane and, for
	// lossless-compressed alpha, an NRGBA image it is decoded from.
	webpAlpha = 5

	// webpLossless is the bytes per pixel of a lossless WebP: an NRGBA
	// image, beside its packed pixels while it unpacks a palette.
	webpLossless = 6
)

// webpKind is the bitstream a WebP's image data is in.
type webpKind int

// The bitstreams, named by their chunks as per RFC 9649.
const (
	// webpUnknown is the kind of a walk that ended before it found a
	// bitstream.
	webpUnknown webpKind = iota

	// webpVP8 is lossy.
	webpVP8

	// webpVP8L is lossless.
	webpVP8L
)

// webpImage is what a WebP's chunks say of the image that decodes: the file's
// only image, or an animation's first frame.
type webpImage struct {
	kind webpKind

	// frame is a lossy image's VP8 chunk size, at most webpMaxFrame.
	frame int64

	// animated reports whether the image is an animation's first frame.
	animated bool

	// bounds is where an animation's first frame lies on the canvas.
	bounds image.Rectangle

	// alpha reports whether an ALPH chunk comes with an animation's first
	// frame.
	alpha bool

	// start and length are where an animation's first frame's chunks start
	// in the file and how many bytes they take.
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
