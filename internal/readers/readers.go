// Package readers holds the [io.Reader] wrappers blurb's packages share.
package readers

import "io"

// Sticky reads through [Sticky.R]; once it returns an error other than
// [io.EOF], every later read returns that error.
type Sticky struct {
	R   io.Reader
	err error
}

// Read reads from [Sticky.R], or returns the error it returned before.
func (s *Sticky) Read(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}

	n, err := s.R.Read(p)
	if err != nil && err != io.EOF {
		s.err = err
	}

	return n, err
}

// Err returns the first error other than [io.EOF] that [Sticky.R] returned,
// or nil.
func (s *Sticky) Err() error { return s.err }

// Counting reads through [Counting.R] and adds the bytes read to
// [Counting.N].
type Counting struct {
	R io.Reader
	N int64
}

// Read reads from [Counting.R] and adds the bytes read to [Counting.N].
func (c *Counting) Read(p []byte) (int, error) {
	n, err := c.R.Read(p)
	c.N += int64(n)

	return n, err
}
