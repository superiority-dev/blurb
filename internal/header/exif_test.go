package header

import (
	"encoding/binary"
	"testing"

	"github.com/superiority-dev/blurb/internal/testimage"
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
			if got, ok := exifOrientation(testimage.EXIFOrientation(v, order)); !ok || got != want {
				t.Errorf("%v orientation %d: got %d, %t; want %d", order, v, got, ok, want)
			}
		}
	}

	if got, ok := exifOrientation(testimage.EXIFSecondEntry(6)); !ok || got != OrientationRotate90 {
		t.Errorf("orientation as the second entry: got %d, %t", got, ok)
	}

	pastEnd := testimage.EXIFOrientation(6, binary.LittleEndian)
	binary.LittleEndian.PutUint32(pastEnd[4:], 1000)

	refused := map[string][]byte{
		"value 9":          testimage.EXIFOrientation(9, binary.BigEndian),
		"not TIFF":         []byte("http://ns.adobe.com/xap/1.0/\x00<x:xmpmeta/>"),
		"header cut short": testimage.EXIFOrientation(6, binary.BigEndian)[:3:3],
		"no orientation":   testimage.EXIFMakeOnly(),
		"cut short":        testimage.EXIFOrientation(6, binary.BigEndian)[:20],
		"IFD past the end": pastEnd,
		"a LONG":           testimage.EXIFWithTypeCount(4, 1),
		"a BYTE":           testimage.EXIFWithTypeCount(1, 1),
		"an SSHORT":        testimage.EXIFWithTypeCount(8, 1),
		"a count of two":   testimage.EXIFWithTypeCount(3, 2),
		"a count of zero":  testimage.EXIFWithTypeCount(3, 0),
	}

	for name, seg := range refused {
		if got, ok := exifOrientation(seg); ok {
			t.Errorf("%s: got %d, want none", name, got)
		}
	}
}
