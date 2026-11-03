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

	// lengthCode starts a normal code whose code length code has three
	// symbols: 18, coded 0, and the lengths 0 and 1, coded 1 0 and 1 1.
	lengthCode = "0 0:4 0:3 1:3 2:3 2:3 "

	// copyGreen is a green code whose one symbol is 256, the first copy:
	// max_symbol 3, then 138 and 118 zeros, then a length of 1.
	copyGreen = lengthCode + "1 0:3 1:2 0 127:7 0 107:7 1 1 "
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
		// 23,910 bytes is what x/image's decoder allocates for the fixture's
		// bitstream beside its pixels, each allocation rounded up as
		// heapBytes rounds it.
		{name: "the fixture", input: bitstream, want: 23910},
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
			// A green normal code of the one symbol 0 adds its 280 code
			// lengths, 1,120 bytes and 296 more, and the code length code's
			// tree of 5 nodes, 40 bytes and 26 more, with its 19 codes, 76
			// bytes and 35 more.
			name:   "a normal code",
			w:      1,
			h:      1,
			fields: "0 0 0 " + lengthCode + "1 0:3 0:2 1 1 1 0 " + strings.Repeat(oneSymbol, 4),
			want:   3366 + 5*26 + 1416 + 66 + 111,
		},
		{
			// A color cache of 2,048 colors, 8,192 bytes and 2,064 more,
			// beside one group.
			name:   "a color cache of 11 bits",
			w:      1,
			h:      1,
			fields: "0 1 11:4 0 " + oneSymbolGroup,
			want:   3366 + 5*26 + 10256,
		},
		{
			// Of 1,001 groups, the decoder keeps the two the map refers to:
			// 4,096 bytes of map and 1,040 more, 5,360 bytes of groups and
			// 1,356 more, and their ten trees. The map's own group has two
			// trees of 3 nodes and three of one, and the 1,001 places of the
			// kept groups take 2,002 bytes and 516 more.
			name:   "a group index of 1,000",
			w:      128,
			h:      128,
			fields: index1000,
			want:   5136 + 6716 + 10*26 + 3366 + 2*46 + 3*26 + 2518,
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
			want:   5136 + 6716 + 10*26 + 3366 + 2*46 + 3*26 + 2518,
		},
		{
			name:   "an invalid code in a kept group",
			w:      1,
			h:      1,
			fields: "0 0 0 " + invalidGroup,
			err:    errVP8L,
		},
		{
			name:   "a color cache of 0 bits",
			w:      1,
			h:      1,
			fields: "0 1 0:4 0 " + oneSymbolGroup,
			err:    errVP8L,
		},
		{
			name:   "a color cache of 12 bits",
			w:      1,
			h:      1,
			fields: "0 1 12:4 0 " + oneSymbolGroup,
			err:    errVP8L,
		},
		{
			// max_symbol 302, past green's alphabet of 280.
			name:   "a max_symbol past the alphabet",
			w:      1,
			h:      1,
			fields: "0 0 0 " + lengthCode + "1 4:3 300:10 " + strings.Repeat(oneSymbol, 4),
			err:    errVP8L,
		},
		{
			// Code length code 18 repeats a zero 11 + 127 times, past the
			// distance alphabet of 40.
			name:   "a repeat past the alphabet",
			w:      1,
			h:      1,
			fields: "0 0 0 " + strings.Repeat(oneSymbol, 4) + "0 0:4 0:3 1:3 0:3 0:3 0 127:7 ",
			err:    errVP8L,
		},
		{
			// A group map of 2 by 2 tiles whose green code is the one symbol
			// 256, a copy, at its first pixel.
			name:   "a copy at the first pixel",
			w:      8,
			h:      8,
			fields: "0 0 1 0:3 0 " + copyGreen + strings.Repeat(oneSymbol, 4) + oneSymbolGroup,
			err:    errVP8L,
		},
		{
			// A group map of 2 by 2 tiles, all group 5: the decoder keeps
			// only group 5 of the six, since 5 is not below the tile count.
			// 16 bytes of map and heapBytes's 20 more, the map's group and
			// its five trees, group 5 and its five trees, and the 6 places of
			// the kept groups, 12 bytes and 19 more.
			name:   "a group index past the tile count",
			w:      8,
			h:      8,
			fields: "0 0 1 0:3 0 1 0 1 5:8 " + strings.Repeat(oneSymbol, 4) + strings.Repeat(oneSymbolGroup, 6),
			want:   36 + 3366 + 5*26 + 3366 + 5*26 + 31,
		},
		{
			// As above, with a green normal code in group 0, which the map
			// does not refer to: the decoder still reads its 280 code lengths
			// and builds the code length code's tree.
			name: "a normal code in a group the map does not refer to",
			w:    8,
			h:    8,
			fields: "0 0 1 0:3 0 1 0 1 5:8 " + strings.Repeat(oneSymbol, 4) +
				lengthCode + "1 0:3 0:2 1 1 1 0 " + strings.Repeat(oneSymbol, 4) + strings.Repeat(oneSymbolGroup, 5),
			want: 36 + 3366 + 5*26 + 3366 + 5*26 + 31 + 1416 + 66 + 111,
		},
		{
			// A color table of 2 colors packs 8 pixels in one, so the 16 by 1
			// image decodes from 2 packed pixels: the table's 1,024 bytes and
			// 272 more with its group, the packed pixels' 8 bytes and 18 more,
			// and the image's group.
			name:   "a color table that packs pixels",
			w:      16,
			h:      1,
			fields: "1 3:2 1:8 0 " + oneSymbolGroup + "0 0 0 " + oneSymbolGroup,
			want:   1296 + 3366 + 5*26 + 26 + 3366 + 5*26,
		},
		{
			// A color table of 15 colors packs 2 pixels in one, so the 16 by 1
			// image decodes from 8 packed pixels, 32 bytes and 24 more.
			name:   "a color table that packs 2 pixels in one",
			w:      16,
			h:      1,
			fields: "1 3:2 14:8 0 " + oneSymbolGroup + "0 0 0 " + oneSymbolGroup,
			want:   1296 + 3366 + 5*26 + 56 + 3366 + 5*26,
		},
		{
			// The color table's sub-image of 2 colors with a color cache of 2
			// colors, 8 bytes and 18 more.
			name:   "a color cache in a sub-image",
			w:      16,
			h:      1,
			fields: "1 3:2 1:8 1 1:4 " + oneSymbolGroup + "0 0 0 " + oneSymbolGroup,
			want:   1296 + 3366 + 5*26 + 26 + 26 + 3366 + 5*26,
		},
		{
			// A 1 by 1 image of a 2-color table still decodes from a packed
			// pixel: its 4 bytes and 17 more.
			name:   "a color table that packs a 1-pixel width",
			w:      1,
			h:      1,
			fields: "1 3:2 1:8 0 " + oneSymbolGroup + "0 0 0 " + oneSymbolGroup,
			want:   1296 + 3366 + 5*26 + 21 + 3366 + 5*26,
		},
		{
			// A color table of 17 colors packs none, so no packed pixels.
			name:   "a color table that packs none",
			w:      16,
			h:      1,
			fields: "1 3:2 16:8 0 " + oneSymbolGroup + "0 0 0 " + oneSymbolGroup,
			want:   1296 + 3366 + 5*26 + 3366 + 5*26,
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

	if want := int64(3*(4096*4096*4+3366+5*26) + 3366 + 5*26); got != want || err != nil {
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
