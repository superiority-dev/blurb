package header

// Orientation is an EXIF orientation, as per EXIF 2.32 section 4.6.4: the
// transformation that displays the stored image correctly.
type Orientation int

// The eight orientations. Rotations are clockwise; [OrientationTranspose]
// flips across the top-left to bottom-right diagonal, [OrientationTransverse]
// across the top-right to bottom-left one.
const (
	OrientationNormal Orientation = iota + 1
	OrientationFlipHorizontal
	OrientationRotate180
	OrientationFlipVertical
	OrientationTranspose
	OrientationRotate90
	OrientationTransverse
	OrientationRotate270
)

// SwapsSides reports whether o swaps the image's width and height.
func (o Orientation) SwapsSides() bool { return o >= OrientationTranspose && o <= OrientationRotate270 }

// valid reports whether o is one of the eight orientations.
func (o Orientation) valid() bool { return o >= OrientationNormal && o <= OrientationRotate270 }
