package cells

import (
	"slices"
	"testing"
)

// TestSpan requires lo to be i*size/n and hi to be (i+1)*size/n, rounded
// down, and a cell that would be empty to take one pixel.
func TestSpan(t *testing.T) {
	cases := []struct {
		n, size int
		want    [][2]int
	}{
		{n: 4, size: 10, want: [][2]int{{0, 2}, {2, 5}, {5, 7}, {7, 10}}},
		{n: 2, size: 2, want: [][2]int{{0, 1}, {1, 2}}},
		{n: 3, size: 2, want: [][2]int{{0, 1}, {0, 1}, {1, 2}}},
	}

	for _, tc := range cases {
		got := make([][2]int, tc.n)
		for i := range tc.n {
			got[i][0], got[i][1] = Span(i, tc.n, tc.size)
		}

		if !slices.Equal(got, tc.want) {
			t.Errorf("%d cells of %d pixels: got %v, want %v", tc.n, tc.size, got, tc.want)
		}
	}
}
