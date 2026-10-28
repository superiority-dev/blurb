// Package cells divides a side of an image into cells.
package cells

// Span returns the source pixels lo <= x < hi that cell i of n covers along
// a side of size pixels: lo is i*size/n and hi is (i+1)*size/n, rounded
// down, and a cell that would be empty takes one pixel.
func Span(i, n, size int) (lo, hi int) {
	lo = i * size / n
	return lo, max((i+1)*size/n, lo+1)
}
