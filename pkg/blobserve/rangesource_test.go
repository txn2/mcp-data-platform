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

// TestServe_RangeReadsOnlyTheRange is the point of the source: a range request
// over a large object must not read the object whole.
func TestServe_RangeReadsOnlyTheRange(t *testing.T) {
	data := body(t, 8<<20) // 8 MiB
	store := &rangeStore{data: data}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/content", http.NoBody)
	req.Header.Set("Range", "bytes=5242880-5243391") // 512 bytes, 5 MiB in
	rec := httptest.NewRecorder()
	require.True(t, blobserve.ServeRanged(rec, req, blobserve.Options{
		Name: "rows.parquet", ContentType: "application/vnd.apache.parquet",
		ModTime: time.Unix(1700000000, 0),
	}, blobserve.Object{Store: store, Bucket: "b", Key: "k", Size: int64(len(data))}))

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

// failStore refuses every range read, the way a missing object or an
// unavailable bucket does.
type failStore struct{ reads int }

func (s *failStore) GetObjectRange(
	_ context.Context, _, _ string, _, _ int64,
) (body []byte, size int64, err error) {
	s.reads++
	return nil, 0, errors.New("not found")
}

func rangedRequest(method string, header map[string]string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), method, "/content", http.NoBody)
	for k, v := range header {
		r.Header.Set(k, v)
	}
	return r
}

func TestServeRanged_WholeGetStreamsWithoutReadingTheFirstBlockTwice(t *testing.T) {
	data := body(t, (2<<20)+17)
	store := &rangeStore{data: data}
	rec := httptest.NewRecorder()
	served := blobserve.ServeRanged(rec, rangedRequest(http.MethodGet, nil), blobserve.Options{
		Name: "clip.mp4", ContentType: "video/mp4", ModTime: time.Unix(1700000000, 0),
	}, blobserve.Object{Store: store, Bucket: "b", Key: "k", Size: int64(len(data))})

	require.True(t, served)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, data, rec.Body.Bytes())
	assert.Len(t, store.reads, 3, "the primed first block is the one the response sends")
	assert.NotEmpty(t, rec.Header().Get("ETag"), "a streamed body carries a validator")
}

func TestServeRanged_WholeGetOfAnUnreadableObjectWritesNothing(t *testing.T) {
	store := &failStore{}
	rec := httptest.NewRecorder()
	served := blobserve.ServeRanged(rec, rangedRequest(http.MethodGet, nil), blobserve.Options{
		Name: "clip.mp4", ContentType: "video/mp4",
	}, blobserve.Object{Store: store, Bucket: "b", Key: "k", Size: 100})

	assert.False(t, served, "the caller's own path answers a missing object")
	assert.Empty(t, rec.Header(), "no header written before the caller answers")
	assert.Equal(t, 1, store.reads)
}

func TestServeRanged_HeadProbesOneByte(t *testing.T) {
	store := &rangeStore{data: body(t, 4096)}
	rec := httptest.NewRecorder()
	require.True(t, blobserve.ServeRanged(rec, rangedRequest(http.MethodHead, nil), blobserve.Options{
		Name: "clip.mp4", ContentType: "video/mp4",
	}, blobserve.Object{Store: store, Bucket: "b", Key: "k", Size: 4096}))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "4096", rec.Header().Get("Content-Length"))
	assert.Equal(t, []int64{1}, store.reads, "the size is learned from one byte")
}

// TestServeRanged_ServesTheObjectTheStoreHolds: an object replaced in place by
// a writer that never touched the record (#1833's vendor drop) is served at
// the size the store reports, not the record's, whole and by range.
func TestServeRanged_ServesTheObjectTheStoreHolds(t *testing.T) {
	data := body(t, 3000)
	for name, recorded := range map[string]int64{"record too small": 10, "record too large": 1 << 30, "record unknown": 0} {
		t.Run(name, func(t *testing.T) {
			obj := blobserve.Object{Store: &rangeStore{data: data}, Bucket: "b", Key: "k", Size: recorded}
			rec := httptest.NewRecorder()
			require.True(t, blobserve.ServeRanged(rec, rangedRequest(http.MethodGet, nil), blobserve.Options{Name: "f.bin", ContentType: "application/octet-stream"}, obj))
			assert.Equal(t, data, rec.Body.Bytes())

			part := httptest.NewRecorder()
			require.True(t, blobserve.ServeRanged(part, rangedRequest(http.MethodGet, map[string]string{"Range": "bytes=2990-"}), blobserve.Options{Name: "f.bin", ContentType: "application/octet-stream"}, obj))
			assert.Equal(t, http.StatusPartialContent, part.Code)
			assert.Equal(t, "bytes 2990-2999/3000", part.Header().Get("Content-Range"))
			assert.Equal(t, data[2990:], part.Body.Bytes())
		})
	}
}

func TestServeRanged_WithoutARangeStoreServesNothing(t *testing.T) {
	rec := httptest.NewRecorder()
	assert.False(t, blobserve.ServeRanged(rec, rangedRequest(http.MethodGet, nil), blobserve.Options{}, blobserve.Object{Store: &wholeStore{}}))
	assert.Empty(t, rec.Header())
}

func TestServeRanged_ConditionalGetAnswers304(t *testing.T) {
	data := body(t, 4096)
	obj := blobserve.Object{Store: &rangeStore{data: data}, Bucket: "b", Key: "k", Size: 4096}
	opts := blobserve.Options{Name: "clip.mp4", ContentType: "video/mp4", ModTime: time.Unix(1700000000, 0)}

	first := httptest.NewRecorder()
	require.True(t, blobserve.ServeRanged(first, rangedRequest(http.MethodGet, nil), opts, obj))
	tag := first.Header().Get("ETag")
	require.NotEmpty(t, tag)

	again := &rangeStore{data: data}
	obj.Store = again
	rec := httptest.NewRecorder()
	require.True(t, blobserve.ServeRanged(rec, rangedRequest(http.MethodGet, map[string]string{"If-None-Match": tag}), opts, obj))
	assert.Equal(t, http.StatusNotModified, rec.Code)
	assert.Empty(t, rec.Body.Bytes())
}

func TestObjectETag_MovesWithEveryPartOfTheIdentity(t *testing.T) {
	at := time.Unix(1700000000, 0)
	base := blobserve.Object{Bucket: "b", Key: "k", Size: 10}
	tag := blobserve.ObjectETag(base, at)
	assert.Equal(t, tag, blobserve.ObjectETag(base, at), "stable for one object")
	for name, other := range map[string]string{
		"bucket": blobserve.ObjectETag(blobserve.Object{Bucket: "c", Key: "k", Size: 10}, at),
		"key":    blobserve.ObjectETag(blobserve.Object{Bucket: "b", Key: "j", Size: 10}, at),
		"size":   blobserve.ObjectETag(blobserve.Object{Bucket: "b", Key: "k", Size: 11}, at),
		"time":   blobserve.ObjectETag(base, at.Add(time.Second)),
	} {
		assert.NotEqual(t, tag, other, name)
	}
}
