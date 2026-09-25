package unarchive

import (
	"context"
	"fmt"
	"io"
)

// blockSize is how much of the source one range read fetches. The decompressors
// read a few kilobytes at a time; answering each from a fetched block is what
// makes a 500 MB member about sixty range reads rather than a hundred thousand.
const blockSize = 8 << 20

// blockReader is an io.ReaderAt over a source's range reads, keeping the two
// most recent blocks. Two, because a zip read alternates between a member's
// data and the headers at its start, and a stream read crosses from one block
// into the next. It is used from one goroutine.
type blockReader struct {
	ctx    context.Context //nolint:containedctx // an io.ReaderAt carries no context of its own
	src    Source
	blocks [2]block
	next   int
}

type block struct {
	off  int64
	data []byte
}

func newBlockReader(ctx context.Context, src Source) *blockReader {
	r := &blockReader{ctx: ctx, src: src}
	for i := range r.blocks {
		r.blocks[i].off = -1
	}
	return r
}

// ReadAt reads len(p) bytes from off, answering io.EOF when the source ends
// first.
func (r *blockReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("negative offset %d: %w", off, ErrCorrupt)
	}
	n := 0
	for n < len(p) {
		at := off + int64(n)
		if at >= r.src.Size {
			return n, io.EOF
		}
		b, err := r.block(at - at%blockSize)
		if err != nil {
			return n, err
		}
		n += copy(p[n:], b.data[at-b.off:])
	}
	return n, nil
}

// block returns the block starting at off, fetching it when neither cached block
// is it.
func (r *blockReader) block(off int64) (block, error) {
	for _, b := range r.blocks {
		if b.off == off {
			return b, nil
		}
	}
	length := min(int64(blockSize), r.src.Size-off)
	data, err := r.src.Fetch(r.ctx, off, length)
	if err != nil {
		return block{}, fmt.Errorf("reading the archive at byte %d: %w", off, err)
	}
	if int64(len(data)) != length {
		return block{}, fmt.Errorf("storage answered %d bytes at byte %d, want %d: %w",
			len(data), off, length, ErrCorrupt)
	}
	b := block{off: off, data: data}
	r.blocks[r.next] = b
	r.next = (r.next + 1) % len(r.blocks)
	return b, nil
}
