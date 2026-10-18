package header

import "encoding/binary"

// exifTagOrientation is the EXIF orientation tag, as per EXIF 2.32 section
// 4.6.4, whose value is one tiffShort.
const exifTagOrientation = 0x0112

// TIFF's layout, as per TIFF 6.0 section 2: a header of tiffHeaderLen bytes
// that starts with the byte order and 42 in it, tiffLEHeader or tiffBEHeader,
// and whose last four, from tiffIFDOffset, give IFD0's offset; an IFD's entry
// count of tiffIFDCountLen bytes, then entries of tiffIFDEntryLen bytes whose
// type, count and value start at tiffEntryType, tiffEntryCount and
// tiffEntryValue. The type tiffShort is a 2-byte integer, which sits in the
// value's first two bytes.
const (
	tiffLEHeader    = "II\x2a\x00"
	tiffBEHeader    = "MM\x00\x2a"
	tiffHeaderLen   = 8
	tiffIFDOffset   = 4
	tiffShort       = 3
	tiffIFDCountLen = 2
	tiffIFDEntryLen = 12
	tiffEntryType   = 2
	tiffEntryCount  = 4
	tiffEntryValue  = 8
)

// exifOrientation reads the orientation tag from IFD0 of EXIF data, which
// starts with its TIFF header. It reports false when it finds no valid
// orientation.
func exifOrientation(tiff []byte) (Orientation, bool) {
	order, ok := tiffByteOrder(tiff)
	if !ok || len(tiff) < tiffHeaderLen {
		return 0, false
	}

	ifd := int(order.Uint32(tiff[tiffIFDOffset:]))
	if ifd < tiffHeaderLen || ifd > len(tiff)-tiffIFDCountLen {
		return 0, false
	}

	n := int(order.Uint16(tiff[ifd:]))

	for i := range n {
		entry := ifd + tiffIFDCountLen + tiffIFDEntryLen*i
		if entry+tiffIFDEntryLen > len(tiff) {
			return 0, false
		}

		if order.Uint16(tiff[entry:]) != exifTagOrientation {
			continue
		}

		if order.Uint16(tiff[entry+tiffEntryType:]) != tiffShort || order.Uint32(tiff[entry+tiffEntryCount:]) != 1 {
			return 0, false
		}

		orientation := Orientation(order.Uint16(tiff[entry+tiffEntryValue:]))
		if !orientation.valid() {
			return 0, false
		}

		return orientation, true
	}

	return 0, false
}

// tiffByteOrder returns the byte order the TIFF header that starts tiff
// names, or false when tiff does not start with one.
func tiffByteOrder(tiff []byte) (binary.ByteOrder, bool) {
	if len(tiff) < len(tiffLEHeader) {
		return nil, false
	}

	switch string(tiff[:len(tiffLEHeader)]) {
	case tiffLEHeader:
		return binary.LittleEndian, true
	case tiffBEHeader:
		return binary.BigEndian, true
	default:
		return nil, false
	}
}
