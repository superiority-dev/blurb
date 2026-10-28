package readers

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

// TestStickyRead requires the reader's first error to come back from every
// later read, and [io.EOF] to pass through without being kept.
func TestStickyRead(t *testing.T) {
	sticky := &Sticky{R: iotest.TimeoutReader(bytes.NewReader([]byte("ab")))}
	p := make([]byte, 1)

	for i, want := range []error{nil, iotest.ErrTimeout, iotest.ErrTimeout} {
		if _, err := sticky.Read(p); !errors.Is(err, want) {
			t.Errorf("read %d: %v, want %v", i+1, err, want)
		}
	}

	ended := &Sticky{R: bytes.NewReader(nil)}
	if _, err := ended.Read(p); err != io.EOF || ended.Err() != nil {
		t.Errorf("at the end: %v, kept %v; want io.EOF, nothing kept", err, ended.Err())
	}
}

// TestCountingRead requires [Counting.N] to count every byte read through
// the reader.
func TestCountingRead(t *testing.T) {
	counting := &Counting{R: strings.NewReader("hello")}
	if _, err := io.Copy(io.Discard, counting); err != nil {
		t.Fatal(err)
	}

	if counting.N != 5 {
		t.Errorf("got %d, want 5", counting.N)
	}
}
