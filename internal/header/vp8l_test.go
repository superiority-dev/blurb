package header

import (
	"bytes"
	"errors"
	"io"
	"math"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/superiority-dev/blurb/internal/testimage"
)

// Prefix codes, as vp8lStream fields.
const (
	// oneSymbol is a simple code of the one symbol 0, which reads no bits.
	oneSymbol = "1 0 0 0:1 "

	// oneSymbolGroup is a group of five oneSymbol codes.
	oneSymbolGroup = oneSymbol + oneSymbol + oneSymbol + oneSymbol + oneSymbol

	// invalidGroup is a group of four oneSymbol codes and a distance code
	// whose one symbol, 200, is past its alphabet of 40.
	invalidGroup = oneSymbol + oneSymbol + oneSymbol + oneSymbol + "1 0 1 200:8 "
)

func TestVP8LImageBytes(t *testing.T) {
	bitstream := testimage.WebPLossless()[20:]

	wrongSignature := bytes.Clone(bitstream)
	wrongSignature[0]--

	cases := []struct {
		name  string
		input []byte
		want  int64
		err   error
	}{
		// 4,293 bytes is what x/image's decoder allocates for the fixture's
		// sub-images and Huffman trees, each allocation rounded up as
		// heapBytes rounds it.
		{name: "the fixture", input: bitstream, want: 4293},
		{name: "a wrong signature", input: wrongSignature, err: errVP8L},
		{name: "cut inside the header", input: bitstream[:3], err: io.EOF},
		{name: "cut inside the trees", input: bitstream[:10], err: io.EOF},
	}

	for _, tc := range cases {
		got, err := vp8lImageBytes(bytes.NewReader(tc.input))
		if got != tc.want || !errors.Is(err, tc.err) {
			t.Errorf("%s: %d, %v, want %d, %v", tc.name, got, err, tc.want, tc.err)
		}
	}
}

func TestVP8LBytes(t *testing.T) {
	// A 128 by 128 image whose group map of 32 by 32 tiles holds index 1,000
	// in its first tile and 0 in the others.
	index1000 := "0 " + // no transforms
		"0 " + // no color cache
		"1 0:3 " + // a group map of 4 by 4 pixel tiles
		"0 1 1 1 0:8 232:8 " + // its codes: no cache, green 0 or 232,
		"1 1 1 0:8 3:8 " + oneSymbol + oneSymbol + oneSymbol + // red 0 or 3
		"1 1 " + strings.Repeat("0 0 ", 1023) + // its tiles: 3*256 + 232, then 0
		strings.Repeat(oneSymbolGroup, 1001) // groups 0 to 1,000

	cases := []struct {
		name   string
		w, h   int
		fields string
		want   int64
		err    error
	}{
		{
			// A group's trees beside their nodes, 2,680 bytes and a quarter
			// and 16 more, and five trees of one node, 8 bytes and 18 more.
			name:   "one group",
			w:      1,
			h:      1,
			fields: "0 0 0 " + oneSymbolGroup,
			want:   3366 + 5*26,
		},
		{
			// Of 1,001 groups, the decoder keeps the two the map refers to:
			// 4,096 bytes of map and 1,040 more, 5,360 bytes of groups and
			// 1,356 more, and their ten trees.
			name:   "a group index of 1,000",
			w:      128,
			h:      128,
			fields: index1000,
			want:   5136 + 6716 + 10*26,
		},
		{
			name:   "a group index of 2,600",
			w:      128,
			h:      128,
			fields: strings.Replace(index1000, "232:8 1 1 1 0:8 3:8", "40:8 1 1 1 0:8 10:8", 1),
			err:    errVP8L,
		},
		{
			// Group 1's distance code is invalid, and the decoder does not
			// build its tree, since the map does not refer to group 1.
			name:   "an invalid code in a group the map does not refer to",
			w:      128,
			h:      128,
			fields: strings.Replace(index1000, oneSymbolGroup+oneSymbolGroup, oneSymbolGroup+invalidGroup, 1),
			want:   5136 + 6716 + 10*26,
		},
		{
			name:   "an invalid code in a kept group",
			w:      1,
			h:      1,
			fields: "0 0 0 " + invalidGroup,
			err:    errVP8L,
		},
		{
			name:   "cut before the last tree",
			w:      1,
			h:      1,
			fields: "0 0 0 " + strings.Repeat(oneSymbol, 4),
			err:    io.EOF,
		},
	}

	for _, tc := range cases {
		got, err := vp8lBytes(bytes.NewReader(vp8lStream(t, tc.fields)), tc.w, tc.h)
		if got != tc.want || !errors.Is(err, tc.err) {
			t.Errorf("%s: %d, %v, want %d, %v", tc.name, got, err, tc.want, tc.err)
		}
	}
}

// TestVP8LBytesConstant requires sub-images whose codes read no bits to parse
// in time set by their bits, not their pixels: three of 4,096 by 4,096 pixels
// within 100 ms.
func TestVP8LBytesConstant(t *testing.T) {
	fields := "1 0:2 0:3 0 " + oneSymbolGroup + "1 1:2 0:3 0 " + oneSymbolGroup + "0 0 1 0:3 0 " + oneSymbolGroup +
		oneSymbolGroup

	start := time.Now()
	got, err := vp8lBytes(bytes.NewReader(vp8lStream(t, fields)), 16384, 16384)

	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("parsed in %v, want 100ms at most", elapsed)
	}

	if want := int64(3*4096*4096*4 + 3366 + 5*26); got != want || err != nil {
		t.Errorf("%d, %v, want %d", got, err, want)
	}
}

// TestHeapBytes requires heapBytes to hold at least what the runtime takes for
// an allocation of each size up to beyond its largest size class.
func TestHeapBytes(t *testing.T) {
	var before, after runtime.MemStats

	for n := 1; n <= 5*heapPage; n += 7 {
		took := uint64(math.MaxUint64)

		// The least of three, since another goroutine may allocate meanwhile.
		for range 3 {
			runtime.ReadMemStats(&before)
			heapSink = make([]byte, n)
			runtime.ReadMemStats(&after)

			took = min(took, after.TotalAlloc-before.TotalAlloc)
		}

		if got := heapBytes(int64(n)); uint64(got) < took {
			t.Errorf("heapBytes(%d) = %d, want at least %d", n, got, took)
		}
	}
}

// heapSink keeps TestHeapBytes's allocations on the heap.
var heapSink []byte

// vp8lStream returns the bitstream of fields, each a bit, 0 or 1, or a value of
// n bits written as value:n, in the order a reader reads them, the least
// significant bit of each first.
func vp8lStream(t *testing.T, fields string) []byte {
	t.Helper()

	var (
		stream []byte
		bits   uint
	)

	for field := range strings.FieldsSeq(fields) {
		value, width, ok := strings.Cut(field, ":")
		if !ok {
			width = "1"
		}

		v, err := strconv.Atoi(value)
		if err != nil {
			t.Fatal(err)
		}

		n, err := strconv.Atoi(width)
		if err != nil || v >= 1<<n {
			t.Fatalf("field %q does not fit", field)
		}

		for i := range n {
			if bits%8 == 0 {
				stream = append(stream, 0)
			}

			stream[len(stream)-1] |= byte(v>>i&1) << (bits % 8)
			bits++
		}
	}

	return stream
}
