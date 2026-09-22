package blobserve

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// ReadRange reads length bytes of an object at offset. It is the one thing a
// RangeSource needs of a store, named here so a caller passes a closure over
// whichever client it holds rather than this package learning about S3.
type ReadRange func(offset, length int64) ([]byte, error)

// RangeReader is the ranged read a blob store may offer. A store that has it
// can answer a byte range without reading the object whole; one that does not
// is served from Data as before, so this is asked for by type assertion rather
// than added to every store's interface.
type RangeReader interface {
	GetObjectRange(ctx context.Context, bucket, key string, offset, length int64) (body []byte, size int64, err error)
}

// blockSize is how much of the object one ranged read fetches.
//
// http.ServeContent copies a range through a 32 KiB buffer, so reading exactly
// what each copy asks for would turn one 50 MB range into sixteen hundred
// requests. Reading a block at a time bounds the requests to the range's size
// over this, and the bytes fetched beyond a small range to one block.
const blockSize = 1 << 20

// RangeSource serves an object of a known size by reading only the blocks the
// response needs. It satisfies Options.Source.
//
// A viewer that reads one object many times by byte range -- the Parquet
// viewer reads the footer, then a row group at a time (#1833) -- otherwise
// costs one whole-object read per request, which for a large file is the file
// again for every few kilobytes served.
type RangeSource struct {
	size  int64
	read  ReadRange
	pos   int64
	block []byte // the bytes held, covering [start, start+len(block))
	start int64
}

// NewRangeSource returns a source over an object of size bytes.
func NewRangeSource(size int64, read ReadRange) *RangeSource {
	return &RangeSource{size: size, read: read}
}

// RangeSourceFor returns a source over an object when store can read ranges,
// or nil when it cannot or the size is unknown, in which case the caller
// serves the object's bytes as it did before.
func RangeSourceFor(ctx context.Context, store any, bucket, key string, size int64) *RangeSource {
	reader, ok := store.(RangeReader)
	if !ok || size <= 0 {
		return nil
	}
	return NewRangeSource(size, func(offset, length int64) ([]byte, error) {
		body, _, err := reader.GetObjectRange(ctx, bucket, key, offset, length)
		return body, err //nolint:wrapcheck // named by RangeSource.Read
	})
}

// Read fills p from the current position, fetching a block when the bytes it
// holds do not cover it.
func (s *RangeSource) Read(p []byte) (int, error) {
	if s.pos >= s.size {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if err := s.fill(); err != nil {
		return 0, err
	}
	n := copy(p, s.block[s.pos-s.start:])
	s.pos += int64(n)
	return n, nil
}

// fill makes the held block cover the current position.
func (s *RangeSource) fill() error {
	if s.pos >= s.start && s.pos < s.start+int64(len(s.block)) {
		return nil
	}
	length := min(int64(blockSize), s.size-s.pos)
	body, err := s.read(s.pos, length)
	if err != nil {
		return fmt.Errorf("reading bytes %d-%d: %w", s.pos, s.pos+length-1, err)
	}
	if len(body) == 0 {
		// A store that answered the range with nothing would otherwise spin:
		// a reader returning (0, nil) forever never finishes.
		return io.ErrUnexpectedEOF
	}
	s.block, s.start = body, s.pos
	return nil
}

// Seek moves the read position. http.ServeContent seeks to the end to learn
// the size and to the start of the range it answers.
func (s *RangeSource) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = s.pos + offset
	case io.SeekEnd:
		abs = s.size + offset
	default:
		return 0, errors.New("invalid whence")
	}
	if abs < 0 {
		return 0, errors.New("seek before the start of the object")
	}
	s.pos = abs
	return abs, nil
}
