package header

import (
	"encoding/binary"
	"testing"
)

func TestExifOrientation(t *testing.T) {
	// The orientation values, as per EXIF 2.32 section 4.6.4.
	values := map[uint16]Orientation{
		1: OrientationNormal,
		2: OrientationFlipHorizontal,
		3: OrientationRotate180,
		4: OrientationFlipVertical,
		5: OrientationTranspose,
		6: OrientationRotate90,
		7: OrientationTransverse,
		8: OrientationRotate270,
	}

	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for v, want := range values {
			if got, ok := exifOrientation(exifData(v, order)); !ok || got != want {
				t.Errorf("%v orientation %d: got %d, %t; want %d", order, v, got, ok, want)
			}
		}
	}

	if got, ok := exifOrientation(exifSecondEntry(6)); !ok || got != OrientationRotate90 {
		t.Errorf("orientation as the second entry: got %d, %t", got, ok)
	}

	pastEnd := exifData(6, binary.LittleEndian)
	binary.LittleEndian.PutUint32(pastEnd[4:], 1000)

	refused := map[string][]byte{
		"value 9":          exifData(9, binary.BigEndian),
		"not TIFF":         []byte("http://ns.adobe.com/xap/1.0/\x00<x:xmpmeta/>"),
		"header cut short": exifData(6, binary.BigEndian)[:3:3],
		"no orientation":   exifMakeOnly(),
		"cut short":        exifData(6, binary.BigEndian)[:20],
		"IFD past the end": pastEnd,
		"a LONG":           exifWithTypeCount(4, 1),
		"a BYTE":           exifWithTypeCount(1, 1),
		"an SSHORT":        exifWithTypeCount(8, 1),
		"a count of two":   exifWithTypeCount(3, 2),
		"a count of zero":  exifWithTypeCount(3, 0),
	}

	for name, seg := range refused {
		if got, ok := exifOrientation(seg); ok {
			t.Errorf("%s: got %d, want none", name, got)
		}
	}
}

// exifSecondEntry is EXIF data holding IFD0 with an entry tagged Make
// (0x010f), then the orientation.
func exifSecondEntry(orientation uint16) []byte {
	tiff := make([]byte, 8+2+2*12+4)
	copy(tiff, "II\x2a\x00")
	binary.LittleEndian.PutUint32(tiff[4:], 8)
	binary.LittleEndian.PutUint16(tiff[8:], 2)
	binary.LittleEndian.PutUint16(tiff[10:], 0x010f)
	binary.LittleEndian.PutUint16(tiff[22:], 0x0112)
	binary.LittleEndian.PutUint16(tiff[24:], 3)
	binary.LittleEndian.PutUint32(tiff[26:], 1)
	binary.LittleEndian.PutUint16(tiff[30:], orientation)

	return tiff
}

// exifWithTypeCount is little-endian EXIF data holding orientation 6 in an
// entry of TIFF type typ and count count.
func exifWithTypeCount(typ uint16, count uint32) []byte {
	tiff := exifData(6, binary.LittleEndian)
	binary.LittleEndian.PutUint16(tiff[12:], typ)
	binary.LittleEndian.PutUint32(tiff[14:], count)

	return tiff
}

// exifMakeOnly is EXIF data holding IFD0 with one entry tagged Make
// (0x010f) and no orientation.
func exifMakeOnly() []byte {
	tiff := exifData(6, binary.BigEndian)
	binary.BigEndian.PutUint16(tiff[10:], 0x010f)

	return tiff
}

// exifData is EXIF data holding IFD0 with one orientation entry.
func exifData(orientation uint16, order binary.ByteOrder) []byte {
	tiff := make([]byte, 8+2+12+4)

	if order == binary.LittleEndian {
		copy(tiff, "II\x2a\x00")
	} else {
		copy(tiff, "MM\x00\x2a")
	}

	order.PutUint32(tiff[4:], 8)
	order.PutUint16(tiff[8:], 1)
	order.PutUint16(tiff[10:], 0x0112)
	order.PutUint16(tiff[12:], 3)
	order.PutUint32(tiff[14:], 1)
	order.PutUint16(tiff[18:], orientation)

	return tiff
}
