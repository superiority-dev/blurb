package header

import (
	"errors"
	"io"
)

// A lossless bitstream, as per RFC 9649 section 3.
const (
	// vp8lSignature is the first byte of a bitstream's header, as per section
	// 3.4.
	vp8lSignature = 0x2f

	vp8lSignatureBits = 8

	// vp8lSizeBits is the bits of the header's width and height, each one
	// less than the side.
	vp8lSizeBits = 14

	// vp8lHeaderTail is the bits of the header after the sizes: the alpha
	// hint and the version.
	vp8lHeaderTail = 1 + 3

	// vp8lTransformBits is the bits of a transform's type, as per section
	// 3.5.
	vp8lTransformBits = 2

	// vp8lTileBits is the bits of a transform's or the group map's tile size,
	// as a power of two less vp8lMinTileBits.
	vp8lTileBits = 3

	// vp8lMinTileBits is the smallest tile size, as a power of two.
	vp8lMinTileBits = 2

	// vp8lColorBits is the bits of a color table's size, less one, as per
	// section 3.5.4.
	vp8lColorBits = 8

	// vp8lCacheBits is the bits of a color cache's size, as a power of two,
	// as per section 3.6.2.3.
	vp8lCacheBits = 4

	// vp8lMaxCacheBits is the largest color cache, as a power of two.
	vp8lMaxCacheBits = 11
)

// The transforms, as per RFC 9649 section 3.5.
const (
	vp8lPredictor = iota
	vp8lColor
	vp8lSubtractGreen
	vp8lColorIndexing
	vp8lTransforms
)

// The prefix codes, as per RFC 9649 section 3.7.
const (
	// vp8lLiterals is the alphabet of a literal's red, blue and alpha, and
	// the first part of the green alphabet.
	vp8lLiterals = 256

	// vp8lLengths is the part of the green alphabet that codes LZ77
	// lengths, as per section 3.6.2.2.
	vp8lLengths = 24

	// vp8lDistances is the alphabet of LZ77 distances.
	vp8lDistances = 40

	// vp8lDirectParams is the count of LZ77 length and distance symbols
	// that code their value with no extra bits.
	vp8lDirectParams = 4

	// vp8lCodes is the count of prefix codes in a group: green, red, blue,
	// alpha and distance.
	vp8lCodes = 5

	// vp8lIndexRedShift is where a group map pixel's red sits in its group
	// index, above green, as per section 3.7.2.2.2.
	vp8lIndexRedShift = 8

	// vp8lMaxGreen is the largest green alphabet, with the largest color
	// cache.
	vp8lMaxGreen = vp8lLiterals + vp8lLengths + 1<<vp8lMaxCacheBits

	// vp8lMaxCodeLength is the longest code.
	vp8lMaxCodeLength = 15

	// vp8lCodeLengthCodes is the alphabet of the code that codes a normal
	// code's lengths, as per section 3.7.2.1.2.
	vp8lCodeLengthCodes = 19

	// vp8lCodeCountBits is the bits of the count of code length code
	// lengths, less vp8lMinCodeCount.
	vp8lCodeCountBits = 4

	// vp8lMinCodeCount is the fewest code length code lengths.
	vp8lMinCodeCount = 4

	// vp8lCodeLengthBits is the bits of one code length code length.
	vp8lCodeLengthBits = 3

	// vp8lRepeatCode is the first code length code that repeats lengths.
	vp8lRepeatCode = 16

	// vp8lFirstPrevious is the length code 16 repeats until a length other
	// than 0 is read.
	vp8lFirstPrevious = 8

	// vp8lMaxSymbolSizeBits is the bits of the field n that gives max_symbol
	// a width of 2 + 2*n bits, as per section 3.7.2.1.2.
	vp8lMaxSymbolSizeBits = 3

	// vp8lSimpleSymbols is the most symbols a simple code has.
	vp8lSimpleSymbols = 2

	// vp8lSimpleSymbolBits is the bits of a simple code's second symbol, and
	// of its first unless a flag makes that one a single bit.
	vp8lSimpleSymbolBits = 8
)

// What x/image's decoder holds for a lossless bitstream.
const (
	// vp8lMaxGroups is the most groups of prefix codes it accepts.
	vp8lMaxGroups = 2600

	// vp8lMappedIndex is the group index from which it keeps only the
	// groups the group map refers to: it does when the map's largest index
	// is this or more, or is at least the map's tile count.
	vp8lMappedIndex = 1000

	// vp8lPixelBytes is the bytes per pixel of a sub-image.
	vp8lPixelBytes = 4

	// vp8lColorTableBytes is the bytes of a color table, which holds 256
	// colors whatever its size.
	vp8lColorTableBytes = 256 * vp8lPixelBytes

	// vp8lNodeBytes is the bytes of a Huffman tree node; a tree of n symbols
	// holds 2n-1.
	vp8lNodeBytes = 8

	// vp8lGroupBytes is the bytes of a group's five trees beside their
	// nodes: each a slice header, 24 bytes on a 64-bit platform, and a table
	// of 128 uint32.
	vp8lGroupBytes = vp8lCodes * (24 + 128*4)
)

// The heap's rounding of an allocation, as per the Go runtime's size classes.
const (
	// heapMaxSmall is the largest allocation a size class holds.
	heapMaxSmall = 32 << 10

	// heapPage is the unit a larger allocation is rounded up to.
	heapPage = 8 << 10
)

// vp8lCodeLengthOrder is the order a normal code's code length code lengths
// come in, as per RFC 9649 section 3.7.2.1.2.
var vp8lCodeLengthOrder = [vp8lCodeLengthCodes]int{17, 18, 0, 1, 2, 3, 4, 5, 16, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

// The extra bits and the offset of the repeat count of code length codes 16,
// 17 and 18.
var (
	vp8lRepeatBits    = [...]uint{2, 3, 7}
	vp8lRepeatOffsets = [...]int{3, 3, 11}
)

// vp8lAlphabets is the alphabet of each code of a group, the green one
// without its color cache.
var vp8lAlphabets = [vp8lCodes]int{vp8lLiterals + vp8lLengths, vp8lLiterals, vp8lLiterals, vp8lLiterals, vp8lDistances}

// errVP8L is the error a [vp8lReader] holds for a bitstream x/image's decoder
// rejects.
var errVP8L = errors.New("header: invalid lossless bitstream")

// vp8lReader reads a lossless bitstream's bits, least significant first. Its
// first error sticks, and each read after it returns zero.
type vp8lReader struct {
	r     io.ByteReader
	bits  uint64
	nBits uint
	err   error

	// lengths holds the code lengths of the code being read.
	lengths [vp8lMaxGreen]uint8
}

// read returns the next n bits.
func (r *vp8lReader) read(n uint) int {
	for r.nBits < n {
		if r.err != nil {
			return 0
		}

		c, err := r.r.ReadByte()
		if err != nil {
			r.err = err
			return 0
		}

		r.bits |= uint64(c) << r.nBits
		r.nBits += 8
	}

	v := int(r.bits & (1<<n - 1))
	r.bits >>= n
	r.nBits -= n

	return v
}

// readSubImage reads past a sub-image of w by h pixels, as per RFC 9649
// section 3.6.1, and returns the largest group index its pixels hold. It sets
// used[i] for each group index i below len(used) that a pixel may hold.
func (r *vp8lReader) readSubImage(w, h int64, used []bool) int {
	var group [vp8lCodes]vp8lCode

	r.readGroup(&group, r.readCacheBits(), true)

	return r.skipPixels(&group, w*h, used)
}

// readGroup reads a group's codes into group, green's alphabet widened by a
// color cache of 1<<cacheBits colors unless cacheBits is 0. It fails on an
// invalid code only when kept, since the decoder builds only kept groups'
// trees.
func (r *vp8lReader) readGroup(group *[vp8lCodes]vp8lCode, cacheBits uint, kept bool) {
	for i, alphabet := range vp8lAlphabets {
		if i == 0 && cacheBits > 0 {
			alphabet += 1 << cacheBits
		}

		if !r.readCode(&group[i], alphabet) && kept {
			r.fail()
		}
	}
}

// readCacheBits returns the size of a color cache, as a power of two, or 0 for
// none.
func (r *vp8lReader) readCacheBits() uint {
	if r.read(1) == 0 {
		return 0
	}

	bits := uint(r.read(vp8lCacheBits))
	if bits < 1 || bits > vp8lMaxCacheBits {
		r.fail()
	}

	return bits
}

// readCode reads a prefix code of alphabet symbols into code, as per RFC 9649
// section 3.7.2.1, and reports whether the code is valid, as x/image's decoder
// requires of each code it builds a Huffman tree of.
func (r *vp8lReader) readCode(code *vp8lCode, alphabet int) bool {
	if r.read(1) == 1 {
		var symbols [vp8lSimpleSymbols]int

		n := 1 + r.read(1)

		bits := uint(1)
		if r.read(1) == 1 {
			bits = vp8lSimpleSymbolBits
		}

		symbols[0] = r.read(bits)

		if n == vp8lSimpleSymbols {
			symbols[1] = r.read(vp8lSimpleSymbolBits)
		}

		code.simple(symbols[:n])

		return symbols[0] < alphabet && symbols[1] < alphabet
	}

	var (
		codeLengths [vp8lCodeLengthCodes]uint8
		lengthCode  vp8lCode
	)

	for _, symbol := range vp8lCodeLengthOrder[:vp8lMinCodeCount+r.read(vp8lCodeCountBits)] {
		codeLengths[symbol] = uint8(r.read(vp8lCodeLengthBits))
	}

	if !lengthCode.build(codeLengths[:]) {
		r.fail()
		return false
	}

	lengths := r.lengths[:alphabet]
	clear(lengths)

	reads := alphabet
	if r.read(1) == 1 {
		reads = 2 + r.read(2+2*uint(r.read(vp8lMaxSymbolSizeBits)))
		if reads > alphabet {
			r.fail()
		}
	}

	previous := uint8(vp8lFirstPrevious)
	for symbol := 0; symbol < alphabet && reads > 0 && r.err == nil; reads-- {
		length := lengthCode.next(r)
		if length < vp8lRepeatCode {
			lengths[symbol] = uint8(length)
			symbol++

			if length != 0 {
				previous = uint8(length)
			}

			continue
		}

		repeat := vp8lRepeatOffsets[length-vp8lRepeatCode] + r.read(vp8lRepeatBits[length-vp8lRepeatCode])
		if symbol+repeat > alphabet {
			r.fail()
			break
		}

		value := uint8(0)
		if length == vp8lRepeatCode {
			value = previous
		}

		for range repeat {
			lengths[symbol] = value
			symbol++
		}
	}

	return code.build(lengths)
}

// skipPixels reads past n pixels coded by group, as per RFC 9649 section
// 3.6.2, and returns the largest group index their literals hold. It sets used
// as [vp8lReader.readSubImage] does, and used[0] for a color cache lookup.
func (r *vp8lReader) skipPixels(group *[vp8lCodes]vp8lCode, n int64, used []bool) int {
	green, red, blue, alpha, distance := &group[0], &group[1], &group[2], &group[3], &group[4]

	maxIndex := 0
	for pixel := int64(0); pixel < n && r.err == nil; {
		// A step that reads no bits repeats until the image ends.
		constant := green.constant()

		symbol := green.next(r)
		switch {
		case symbol < vp8lLiterals:
			index := red.next(r)<<vp8lIndexRedShift | symbol
			blue.next(r)
			alpha.next(r)

			maxIndex = max(maxIndex, index)
			if index < len(used) {
				used[index] = true
			}

			pixel++
			constant = constant && red.constant() && blue.constant() && alpha.constant()
		case symbol < vp8lLiterals+vp8lLengths:
			// A copy needs an earlier pixel.
			if pixel == 0 {
				r.fail()
			}

			pixel += int64(r.lz77(symbol - vp8lLiterals))
			r.lz77(distance.next(r))

			constant = false
		default:
			if used != nil {
				used[0] = true
			}

			pixel++
		}

		if constant {
			break
		}
	}

	return maxIndex
}

// lz77 returns the LZ77 length or distance that symbol codes, reading its
// extra bits, as per RFC 9649 section 3.6.2.2.
func (r *vp8lReader) lz77(symbol int) int {
	if symbol < vp8lDirectParams {
		return symbol + 1
	}

	extra := uint(symbol-2) >> 1
	offset := (2 + symbol&1) << extra

	return offset + r.read(extra) + 1
}

// fail makes errVP8L r's error, unless it has one.
func (r *vp8lReader) fail() {
	if r.err == nil {
		r.err = errVP8L
	}
}

// vp8lCode is a canonical prefix code, as per RFC 9649 section 3.7.2.1.
type vp8lCode struct {
	// counts holds how many codes each length has.
	counts [vp8lMaxCodeLength + 1]int

	// symbols holds the symbols in the order of their codes; a code of one
	// symbol reads no bits.
	symbols []int

	// n is the count of symbols with a length.
	n int
}

// simple sets c to the code of a simple code's one or two symbols.
func (c *vp8lCode) simple(symbols []int) {
	*c = vp8lCode{symbols: append(c.symbols[:0], symbols...), n: len(symbols)}
	c.counts[1] = len(symbols)
}

// build sets c to the canonical code of the given lengths, and reports whether
// it is valid: one symbol, or a complete code.
func (c *vp8lCode) build(lengths []uint8) bool {
	*c = vp8lCode{symbols: c.symbols[:0]}

	for _, length := range lengths {
		c.counts[length]++
	}

	c.n, c.counts[0] = len(lengths)-c.counts[0], 0

	var offsets [vp8lMaxCodeLength + 1]int
	for length := 2; length <= vp8lMaxCodeLength; length++ {
		offsets[length] = offsets[length-1] + c.counts[length-1]
	}

	c.symbols = append(c.symbols, make([]int, c.n)...)
	for symbol, length := range lengths {
		if length > 0 {
			c.symbols[offsets[length]] = symbol
			offsets[length]++
		}
	}

	if c.n == 1 {
		return true
	}

	left := 1
	for _, count := range c.counts[1:] {
		left = left<<1 - count
	}

	return c.n > 0 && left == 0
}

// constant reports whether c reads no bits.
func (c *vp8lCode) constant() bool { return len(c.symbols) == 1 }

// next returns the next symbol r holds.
func (c *vp8lCode) next(r *vp8lReader) int {
	if c.constant() {
		return c.symbols[0]
	}

	code, first, index := 0, 0, 0
	for _, count := range c.counts[1:] {
		code |= r.read(1)
		if code-first < count {
			return c.symbols[index+code-first]
		}

		index += count
		first = (first + count) << 1
		code <<= 1
	}

	r.fail()

	return 0
}

// vp8lImageBytes returns the memory [vp8lBytes] estimates for the bitstream
// of a VP8L chunk, read from r, which starts with the bitstream's header.
func vp8lImageBytes(r io.ByteReader) (bytes int64, err error) {
	header := vp8lReader{r: r}
	if header.read(vp8lSignatureBits) != vp8lSignature {
		header.fail()
	}

	w, h := 1+header.read(vp8lSizeBits), 1+header.read(vp8lSizeBits)
	header.read(vp8lHeaderTail)

	if header.err != nil {
		return 0, header.err
	}

	return vp8lBytes(r, w, h)
}

// vp8lBytes estimates the memory x/image's decoder holds beside its pixels for
// a w by h lossless bitstream, read from r where its transforms start: the
// transforms' sub-images, the group map and the Huffman trees. The error is
// r's when r ends before the last tree, or errVP8L.
func vp8lBytes(r io.ByteReader, w, h int) (bytes int64, err error) {
	var (
		n    int64
		seen [vp8lTransforms]bool
	)

	stream := &vp8lReader{r: r}
	width := int64(w)

	for stream.read(1) == 1 && stream.err == nil {
		transform := stream.read(vp8lTransformBits)
		if seen[transform] {
			stream.fail()
			break
		}

		seen[transform] = true

		switch transform {
		case vp8lPredictor, vp8lColor:
			bits := vp8lMinTileBits + uint(stream.read(vp8lTileBits))
			tileW, tileH := vp8lTiles(width, bits), vp8lTiles(int64(h), bits)
			stream.readSubImage(tileW, tileH, nil)
			n += heapBytes(tileW * tileH * vp8lPixelBytes)
		case vp8lColorIndexing:
			colors := 1 + stream.read(vp8lColorBits)
			stream.readSubImage(int64(colors), 1, nil)
			n += heapBytes(vp8lColorTableBytes)
			width = vp8lTiles(width, vp8lPackBits(colors))
		}
	}

	var (
		used   [vp8lMaxGroups]bool
		mapped bool
	)

	cacheBits := stream.readCacheBits()
	maxIndex := 0

	if stream.read(1) == 1 {
		bits := vp8lMinTileBits + uint(stream.read(vp8lTileBits))
		tileW, tileH := vp8lTiles(width, bits), vp8lTiles(int64(h), bits)

		maxIndex = stream.readSubImage(tileW, tileH, used[:])
		if maxIndex >= vp8lMaxGroups {
			stream.fail()
		}

		mapped = maxIndex >= vp8lMappedIndex || int64(maxIndex) >= tileW*tileH
		n += heapBytes(tileW * tileH * vp8lPixelBytes)
	}

	var (
		group  [vp8lCodes]vp8lCode
		groups int64
	)

	for i := 0; i <= maxIndex && stream.err == nil; i++ {
		kept := !mapped || used[i]
		stream.readGroup(&group, cacheBits, kept)

		if !kept || stream.err != nil {
			continue
		}

		groups++
		for _, code := range group {
			n += heapBytes(int64(2*code.n-1) * vp8lNodeBytes)
		}
	}

	if stream.err != nil {
		return 0, stream.err
	}

	return n + heapBytes(groups*vp8lGroupBytes), nil
}

// vp8lTiles returns the count of tiles of 1<<bits pixels that cover size
// pixels.
func vp8lTiles(size int64, bits uint) int64 { return (size + 1<<bits - 1) >> bits }

// vp8lPackBits returns how many pixels a color table of the given colors packs
// into one, as a power of two, as per RFC 9649 section 3.5.4.
func vp8lPackBits(colors int) uint {
	switch {
	case colors <= 2:
		return 3
	case colors <= 4:
		return 2
	case colors <= 16:
		return 1
	default:
		return 0
	}
}

// heapBytes returns the most memory an allocation of n bytes takes from the
// heap: a size class holds at most a quarter more and 16 bytes, and a larger
// allocation is rounded up to whole pages.
func heapBytes(n int64) int64 {
	if n <= heapMaxSmall {
		return n + n/4 + 16
	}

	return (n + heapPage - 1) / heapPage * heapPage
}
