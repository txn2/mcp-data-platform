package tableregister

import (
	"cmp"
	"context"
	"errors"
	"io"

	"github.com/txn2/mcp-data-platform/internal/tableparquet"
	"github.com/txn2/mcp-data-platform/internal/tabletype"
)

// parquetColumns reads the columns a Parquet file's footer declares, through
// ranged reads of the object rather than the whole of it (#1833). A read the
// store failed is the platform's failure; anything the footer says that no
// table can be declared over -- bytes that are not Parquet, a type nothing
// reads back exactly, two columns one apart by case -- is a refusal naming it.
func parquetColumns(ctx context.Context, objects ObjectReader, src Source) ([]Column, error) {
	ra := &rangeReader{ctx: ctx, objects: objects, bucket: src.Bucket, key: src.HeadKey}
	size, err := ra.size()
	if err != nil || size < 0 {
		// The probe cannot always answer: a store refuses a ranged read of an
		// empty object outright, and one that satisfies a range may report no
		// total, which arrives negative. Reading the object whole answers
		// both, and an empty one is refused by name below.
		body, _, getErr := objects.GetObject(ctx, src.Bucket, src.HeadKey)
		if getErr != nil {
			return nil, failedf("reading the file", cmp.Or(err, getErr))
		}
		size = int64(len(body))
	}
	cols, err := tableparquet.Columns(ra, size)
	switch {
	case ra.err != nil:
		return nil, failedf("reading the file", ra.err)
	case errors.Is(err, tableparquet.ErrNotParquet):
		return nil, refusedf("the file is named or typed as Parquet, but %s", err.Error())
	case err != nil:
		return nil, refusedf("%s", err.Error())
	}
	return declaredColumns(cols), nil
}

// declaredColumns renders inferred columns as a registration records them.
func declaredColumns(cols []tabletype.Column) []Column {
	out := make([]Column, 0, len(cols))
	for _, c := range cols {
		out = append(out, Column{Name: c.Name, Type: c.Type.SQL()})
	}
	return out
}

// rangeReader is an io.ReaderAt over an object's ranged reads. The first
// failure the store returns is kept, so the caller can tell a read that failed
// from a file the footer parse refused.
type rangeReader struct {
	ctx     context.Context //nolint:containedctx // an io.ReaderAt carries no context of its own
	objects ObjectReader
	bucket  string
	key     string
	err     error
}

// sizeProbe is how much of the object the size is learned from: the magic the
// footer read asks for anyway.
const sizeProbe = 4

// size learns the object's whole size from one small ranged read.
func (r *rangeReader) size() (int64, error) {
	_, size, err := r.objects.GetObjectRange(r.ctx, r.bucket, r.key, 0, sizeProbe)
	return size, err //nolint:wrapcheck // named by the caller
}

// ReadAt reads len(p) bytes at off.
func (r *rangeReader) ReadAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	body, _, err := r.objects.GetObjectRange(r.ctx, r.bucket, r.key, off, int64(len(p)))
	if err != nil {
		if r.err == nil {
			r.err = err
		}
		return 0, err //nolint:wrapcheck // kept on r.err and named by the caller
	}
	n := copy(p, body)
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
