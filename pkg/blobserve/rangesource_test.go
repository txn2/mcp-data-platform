package blobserve_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/blobserve"
)

// rangeStore is a store that can read ranges, and counts what was asked for.
type rangeStore struct {
	data  []byte
	reads []int64 // the length of each range read
}

func (s *rangeStore) GetObjectRange(
	_ context.Context, _, _ string, offset, length int64,
) (body []byte, size int64, err error) {
	s.reads = append(s.reads, length)
	end := min(offset+length, int64(len(s.data)))
	if offset >= int64(len(s.data)) {
		return nil, int64(len(s.data)), nil
	}
	return s.data[offset:end], int64(len(s.data)), nil
}

// wholeStore cannot read ranges.
type wholeStore struct{}

func body(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}

func TestRangeSourceFor_OnlyWhenTheStoreCanReadRanges(t *testing.T) {
	store := &rangeStore{data: body(t, 100)}
	assert.NotNil(t, blobserve.RangeSourceFor(context.Background(), store, "b", "k", 100))
	assert.Nil(t, blobserve.RangeSourceFor(context.Background(), &wholeStore{}, "b", "k", 100),
		"a store with no ranged read is served whole")
	assert.Nil(t, blobserve.RangeSourceFor(context.Background(), store, "b", "k", 0),
		"an unknown size cannot be seeked")
}

// TestServe_RangeReadsOnlyTheRange is the point of the source: a range request
// over a large object must not read the object whole.
func TestServe_RangeReadsOnlyTheRange(t *testing.T) {
	data := body(t, 8<<20) // 8 MiB
	store := &rangeStore{data: data}
	src := blobserve.RangeSourceFor(context.Background(), store, "b", "k", int64(len(data)))
	require.NotNil(t, src)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/content", http.NoBody)
	req.Header.Set("Range", "bytes=5242880-5243391") // 512 bytes, 5 MiB in
	rec := httptest.NewRecorder()
	blobserve.Serve(rec, req, blobserve.Options{
		Name: "rows.parquet", ContentType: "application/vnd.apache.parquet",
		ModTime: time.Unix(1700000000, 0), Source: src,
	})

	assert.Equal(t, http.StatusPartialContent, rec.Code)
	assert.Equal(t, data[5242880:5243392], rec.Body.Bytes())
	assert.Equal(t, "bytes 5242880-5243391/8388608", rec.Header().Get("Content-Range"))
	var total int64
	for _, n := range store.reads {
		total += n
	}
	assert.Less(t, total, int64(2<<20), "the whole object was read to serve 512 bytes")
	assert.Equal(t, "bytes", rec.Header().Get("Accept-Ranges"))
}

func TestServe_SourceServesTheWholeObjectInBlocks(t *testing.T) {
	data := body(t, 3<<20)
	store := &rangeStore{data: data}
	rec := httptest.NewRecorder()
	blobserve.Serve(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/content", http.NoBody), blobserve.Options{
		Name: "rows.parquet", ContentType: "application/vnd.apache.parquet",
		Source: blobserve.NewRangeSource(int64(len(data)), func(offset, length int64) ([]byte, error) {
			b, _, err := store.GetObjectRange(context.Background(), "b", "k", offset, length)
			return b, err
		}),
	})
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, data, rec.Body.Bytes())
	assert.Len(t, store.reads, 3, "one read per megabyte block")
}

func TestRangeSource_SeekAndReadEdges(t *testing.T) {
	data := body(t, 10)
	src := blobserve.NewRangeSource(int64(len(data)), func(offset, length int64) ([]byte, error) {
		return data[offset : offset+length], nil
	})

	end, err := src.Seek(0, io.SeekEnd)
	require.NoError(t, err)
	assert.Equal(t, int64(10), end)
	_, err = src.Read(make([]byte, 1))
	assert.ErrorIs(t, err, io.EOF, "a read past the end is EOF")

	at, err := src.Seek(-4, io.SeekEnd)
	require.NoError(t, err)
	assert.Equal(t, int64(6), at)
	buf := make([]byte, 4)
	n, err := src.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, data[6:], buf[:n])

	_, err = src.Seek(0, io.SeekStart)
	require.NoError(t, err)
	n, err = src.Read(nil)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "an empty buffer reads nothing and asks the store for nothing")

	_, err = src.Seek(0, io.SeekCurrent)
	require.NoError(t, err)
	_, err = src.Seek(-1, io.SeekStart)
	assert.Error(t, err, "a seek before the start is refused")
	_, err = src.Seek(0, 99)
	assert.Error(t, err, "an unknown whence is refused")
}

func TestRangeSource_ReadFailureIsReported(t *testing.T) {
	want := errors.New("the store said no")
	src := blobserve.NewRangeSource(10, func(_, _ int64) ([]byte, error) { return nil, want })
	_, err := src.Read(make([]byte, 4))
	assert.ErrorIs(t, err, want)

	empty := blobserve.NewRangeSource(10, func(_, _ int64) ([]byte, error) { return nil, nil })
	_, err = empty.Read(make([]byte, 4))
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF, "a range answered with nothing must not spin")
}
