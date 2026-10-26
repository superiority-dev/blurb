package header

import (
	"bytes"
	"encoding/binary"
	"image"
)

// jpegPrefix is the byte every JPEG marker follows.
const jpegPrefix = 0xff

// JPEG markers, as per ITU-T T.81 table B.1.
const (
	jpegSOI   = 0xd8
	jpegSOS   = 0xda
	jpegAPP1  = 0xe1
	jpegSOF0  = 0xc0
	jpegSOF2  = 0xc2
	jpegSOF15 = 0xcf
)

// JPEG markers among jpegSOF0 to jpegSOF15 that are not frame markers.
const (
	jpegDHT = 0xc4
	jpegJPG = 0xc8
	jpegDAC = 0xcc
)

// A segment, as per T.81 section B.1.1.
const (
	jpegMarkerLen = 2

	// jpegLengthLen is the length of a segment's length field, which counts
	// its own two bytes and the payload's.
	jpegLengthLen = 2

	// jpegSegmentHead is the marker and the length field a segment starts
	// with.
	jpegSegmentHead = jpegMarkerLen + jpegLengthLen
)

// The frame header, as per T.81 section B.2.2.
const (
	// jpegFrameComponents is the offset of the component count.
	jpegFrameComponents = 5

	// jpegFrameFirstComponent is the offset of the first component.
	jpegFrameFirstComponent = 6

	jpegFrameComponentLen = 3

	// jpegComponentSampling is the offset in a component of its sampling
	// factors, horizontal in the high jpegSamplingBits bits and vertical in
	// the low.
	jpegComponentSampling = 1

	// jpegSamplingBits is the width of one sampling factor, in bits.
	jpegSamplingBits = 4

	// jpegDCTBlock is the side of a DCT block, in pixels.
	jpegDCTBlock = 8
)

// Terms of a JPEG's decode estimate.
const (
	// jpegProgressiveFactor multiplies a progressive JPEG's samples: it keeps
	// every coefficient as an int32 until the last scan, then converts.
	jpegProgressiveFactor = 5

	// jpegCMYKComponents is a CMYK or YCCK JPEG's component count.
	jpegCMYKComponents = 4

	// jpegCMYKOutput is the bytes per pixel of the image a CMYK or YCCK JPEG
	// is converted into, beside its stored samples, however they are
	// subsampled.
	jpegCMYKOutput = 4

	// jpegUnknown is used when the frame header was not found: four
	// components, unsubsampled and progressive.
	jpegUnknown = jpegCMYKComponents*jpegProgressiveFactor + jpegCMYKOutput
)

// jpegEXIFIdentifier is the EXIF identifier code, which starts the payload of
// an APP1 segment that holds EXIF data.
var jpegEXIFIdentifier = []byte("Exif\x00\x00")

// jpegFrame is a JPEG's frame header.
type jpegFrame struct {
	progressive bool

	// components is the frame's component count: 1 for gray, 3 for
	// YCbCr, 4 for CMYK or YCCK.
	components int

	// samples is the stored samples per pixel, summed over components.
	samples float64

	// mcuWidth and mcuHeight are the pixels one MCU covers; the decoder
	// pads the image to whole MCUs.
	mcuWidth, mcuHeight int
}

// scanJPEG walks a JPEG's segments by their lengths up to the first scan, so
// an EXIF thumbnail's own frame header is passed over, and returns the frame
// header and the orientation in the first EXIF segment.
func scanJPEG(b []byte) (*jpegFrame, Orientation) {
	orientation := OrientationNormal

	if len(b) < jpegMarkerLen || b[0] != jpegPrefix || b[1] != jpegSOI {
		return nil, orientation
	}

	var (
		frame   *jpegFrame
		sawEXIF bool
	)

	for i := jpegMarkerLen; i+jpegSegmentHead <= len(b); {
		if b[i] != jpegPrefix {
			return frame, orientation
		}

		marker := b[i+1]
		if marker == jpegPrefix {
			i++
			continue
		}

		if marker == jpegSOS {
			return frame, orientation
		}

		size := int(binary.BigEndian.Uint16(b[i+jpegMarkerLen:]))
		end := i + jpegMarkerLen + size

		if size < jpegLengthLen || end > len(b) {
			return frame, orientation
		}

		seg := b[i+jpegSegmentHead : end]

		switch {
		case marker == jpegAPP1 && !sawEXIF && bytes.HasPrefix(seg, jpegEXIFIdentifier):
			sawEXIF = true
			if fromEXIF, ok := exifOrientation(seg[len(jpegEXIFIdentifier):]); ok {
				orientation = fromEXIF
			}
		case marker >= jpegSOF0 && marker <= jpegSOF15 &&
			marker != jpegDHT && marker != jpegJPG && marker != jpegDAC:
			frame = parseJPEGFrame(marker, seg)
		}

		i = end
	}

	return frame, orientation
}

// parseJPEGFrame reads a frame header's component count and sampling factors.
// It returns nil for a header cut short, without components, or whose
// horizontal or vertical factors are all zero.
func parseJPEGFrame(marker byte, seg []byte) *jpegFrame {
	if len(seg) < jpegFrameFirstComponent {
		return nil
	}

	n := int(seg[jpegFrameComponents])
	if n == 0 || len(seg) < jpegFrameFirstComponent+jpegFrameComponentLen*n {
		return nil
	}

	var hmax, vmax int

	for i := range n {
		h, v := jpegSampling(seg, i)
		hmax, vmax = max(hmax, h), max(vmax, v)
	}

	if hmax == 0 || vmax == 0 {
		return nil
	}

	frame := &jpegFrame{
		progressive: marker == jpegSOF2,
		components:  n,
		mcuWidth:    jpegDCTBlock * hmax,
		mcuHeight:   jpegDCTBlock * vmax,
	}

	// One component is one data unit per MCU, whatever its sampling
	// factors, as per T.81 section 4.8.2.
	if n == 1 {
		frame.mcuWidth, frame.mcuHeight = jpegDCTBlock, jpegDCTBlock
	}

	for i := range n {
		h, v := jpegSampling(seg, i)
		frame.samples += float64(h*v) / float64(hmax*vmax)
	}

	return frame
}

// jpegSampling returns component i's horizontal and vertical sampling
// factors from a frame header.
func jpegSampling(seg []byte, i int) (h, v int) {
	hv := seg[jpegFrameFirstComponent+jpegFrameComponentLen*i+jpegComponentSampling]
	return int(hv >> jpegSamplingBits), int(hv & (1<<jpegSamplingBits - 1))
}

// jpegDecodeBytes estimates the memory decoding a JPEG of cfg's size holds,
// from frame, its frame header; without one it assumes jpegUnknown bytes a
// pixel.
func jpegDecodeBytes(cfg image.Config, frame *jpegFrame) int64 {
	if frame == nil {
		return decodeBytes(cfg.Width, cfg.Height, jpegUnknown)
	}

	mcuCols := (cfg.Width + frame.mcuWidth - 1) / frame.mcuWidth
	mcuRows := (cfg.Height + frame.mcuHeight - 1) / frame.mcuHeight

	perPixel := frame.samples
	if frame.progressive {
		perPixel *= jpegProgressiveFactor
	}

	if frame.components == jpegCMYKComponents {
		perPixel += jpegCMYKOutput
	}

	return decodeBytes(mcuCols*frame.mcuWidth, mcuRows*frame.mcuHeight, perPixel)
}
