package header

import "testing"

func TestOrientationSwapsSides(t *testing.T) {
	swaps := map[Orientation]bool{
		OrientationNormal:         false,
		OrientationFlipHorizontal: false,
		OrientationRotate180:      false,
		OrientationFlipVertical:   false,
		OrientationTranspose:      true,
		OrientationRotate90:       true,
		OrientationTransverse:     true,
		OrientationRotate270:      true,
		9:                         false,
	}

	for orientation, want := range swaps {
		if got := orientation.SwapsSides(); got != want {
			t.Errorf("%d: got %t, want %t", orientation, got, want)
		}
	}
}

func TestOrientationValid(t *testing.T) {
	// The EXIF orientations are 1 to 8.
	valid := map[Orientation]bool{
		0: false,
		1: true,
		8: true,
		9: false,
	}

	for orientation, want := range valid {
		if got := orientation.valid(); got != want {
			t.Errorf("%d: got %t, want %t", orientation, got, want)
		}
	}
}
