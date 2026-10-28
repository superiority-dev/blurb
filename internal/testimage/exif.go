package testimage

import (
	"encoding/binary"
	"slices"
)

// The TIFF headers that start EXIF data, in each byte order.
const (
	tiffLittleEndian = "II\x2a\x00"
	tiffBigEndian    = "MM\x00\x2a"
)

// EXIFMakeOnly returns big-endian EXIF data holding IFD0 with one entry
// tagged Make (0x010f) and no orientation.
func EXIFMakeOnly() []byte {
	tiff := EXIFOrientation(6, binary.BigEndian)
	binary.BigEndian.PutUint16(tiff[10:], 0x010f)

	return tiff
}

// EXIFSecondEntry returns little-endian EXIF data holding IFD0 with an entry
// tagged Make (0x010f), then the orientation.
func EXIFSecondEntry(orientation uint16) []byte {
	entry := make([]byte, 12)
	binary.LittleEndian.PutUint16(entry, 0x010f)

	tiff := EXIFOrientation(orientation, binary.LittleEndian)
	binary.LittleEndian.PutUint16(tiff[8:], 2)

	return slices.Insert(tiff, 10, entry...)
}

// EXIFWithTypeCount returns little-endian EXIF data holding orientation 6 in
// an entry of TIFF type typ and count count.
func EXIFWithTypeCount(typ uint16, count uint32) []byte {
	tiff := EXIFOrientation(6, binary.LittleEndian)
	binary.LittleEndian.PutUint16(tiff[12:], typ)
	binary.LittleEndian.PutUint32(tiff[14:], count)

	return tiff
}

// EXIFOrientation returns EXIF data holding IFD0 with one orientation entry.
func EXIFOrientation(orientation uint16, order binary.ByteOrder) []byte {
	tiff := make([]byte, 8+2+12+4)

	if order == binary.LittleEndian {
		copy(tiff, tiffLittleEndian)
	} else {
		copy(tiff, tiffBigEndian)
	}

	order.PutUint32(tiff[4:], 8)
	order.PutUint16(tiff[8:], 1)
	order.PutUint16(tiff[10:], 0x0112)
	order.PutUint16(tiff[12:], 3)
	order.PutUint32(tiff[14:], 1)
	order.PutUint16(tiff[18:], orientation)

	return tiff
}
