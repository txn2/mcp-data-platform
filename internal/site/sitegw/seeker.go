package sitegw

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// rangeChunk is how much one read of a large file fetches.
const rangeChunk = int64(4 << 20)

// open is a seekable view of an object: its bytes when it is small enough to
// hold, a disk copy when it is not, and ranges of the object itself past what
// is cached at all.
func (g *Gateway) open(ctx context.Context, cacheKey, objectKey string, size int64) (io.ReadSeeker, func(), error) {
	noop := func() {}
	// Past what memory holds, with no disk to hold it, ranges of the object
	// are what serve it: fetching it whole per request would cost a full read
	// for every Range.
	if size <= maxDiskItem && (size <= maxMemoryItem || g.cache.dir != "") {
		if f := g.cache.readDisk(cacheKey); f != nil && size > maxMemoryItem {
			return f, func() { _ = f.Close() }, nil
		} else if f != nil {
			_ = f.Close()
		}
		body, err := g.bytes(ctx, cacheKey, objectKey)
		if err != nil {
			return nil, noop, err
		}
		return bytes.NewReader(body), noop, nil
	}
	return &objectSeeker{ctx: ctx, objects: g.deps.Objects, bucket: g.deps.Bucket, key: objectKey, size: size}, noop, nil
}

// objectSeeker reads an object through ranged GETs, for a file too large to
// cache: a media file played from the middle costs only the part played.
type objectSeeker struct {
	ctx     context.Context //nolint:containedctx // scoped to one request's ServeContent call
	objects Objects
	bucket  string
	key     string
	size    int64
	off     int64
	buf     []byte
	bufOff  int64
}

// Seek moves the read position.
func (s *objectSeeker) Seek(offset int64, whence int) (int64, error) {
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = s.off + offset
	case io.SeekEnd:
		next = s.size + offset
	default:
		return 0, errors.New("invalid whence")
	}
	if next < 0 {
		return 0, errors.New("negative position")
	}
	s.off = next
	return next, nil
}

// Read serves from the held chunk, fetching the next one when the position
// has left it.
func (s *objectSeeker) Read(p []byte) (int, error) {
	if s.off >= s.size {
		return 0, io.EOF
	}
	if s.off < s.bufOff || s.off >= s.bufOff+int64(len(s.buf)) {
		length := min(rangeChunk, s.size-s.off)
		body, _, err := s.objects.GetObjectRange(s.ctx, s.bucket, s.key, s.off, length)
		if err != nil {
			return 0, fmt.Errorf("reading a range: %w", err)
		}
		if len(body) == 0 {
			return 0, io.ErrUnexpectedEOF
		}
		s.buf, s.bufOff = body, s.off
	}
	n := copy(p, s.buf[s.off-s.bufOff:])
	s.off += int64(n)
	return n, nil
}

// serveBytes answers from body, with ranges and conditional requests.
func serveBytes(w http.ResponseWriter, r *http.Request, body []byte) {
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
}
