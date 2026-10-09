package maphttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/maps"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
)

type objects struct {
	data  []byte
	reads [][2]int64
}

func (*objects) PutObjectStream(context.Context, string, string, io.Reader, string) (int64, error) {
	return 0, nil
}

func (o *objects) GetObjectRange(_ context.Context, _, _ string, offset, length int64) (body []byte, size int64, err error) {
	o.reads = append(o.reads, [2]int64{offset, length})
	end := min(offset+length, int64(len(o.data)))
	return o.data[offset:end], int64(len(o.data)), nil
}

func (*objects) DeleteObject(context.Context, string, string) error { return nil }

func (*objects) ListDirectory(context.Context, string, string) ([]s3adapter.ObjectEntry, bool, error) {
	return nil, false, nil
}

type service struct {
	state maps.State
	err   error
	objs  *objects
}

func (s *service) State(context.Context) (maps.State, error) { return s.state, s.err }

func (s *service) ServedArchive(_ context.Context, id string) (maps.Archive, maps.Objects, error) {
	if s.err != nil {
		return maps.Archive{}, nil, s.err
	}
	if id != "sf" {
		return maps.Archive{}, nil, maps.ErrUnavailable
	}
	return maps.Archive{Bucket: "b", Key: "k", Size: int64(len(s.objs.data)), ReadyAt: time.Now()}, s.objs, nil
}

func newMux(svc Service) *http.ServeMux {
	mux := http.NewServeMux()
	Mount(mux, svc)
	return mux
}

func TestArchiveIsServedByRange(t *testing.T) {
	data := make([]byte, 3<<20)
	for i := range data {
		data[i] = byte(i)
	}
	objs := &objects{data: data}
	mux := newMux(&service{objs: objs})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/portal/maps/sf.pmtiles", http.NoBody)
	req.Header.Set("Range", "bytes=100-199")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusPartialContent, rec.Code)
	assert.Equal(t, data[100:200], rec.Body.Bytes())
	assert.Equal(t, "bytes 100-199/3145728", rec.Header().Get("Content-Range"))
	assert.Equal(t, archiveCacheControl, rec.Header().Get("Cache-Control"))
	assert.NotEmpty(t, rec.Header().Get("ETag"))
	assert.Equal(t, "application/vnd.pmtiles", rec.Header().Get("Content-Type"))
	require.Len(t, objs.reads, 1)
	assert.Equal(t, [2]int64{100, 100}, objs.reads[0], "a small range reads only its bytes from the bucket, not a block")
}

func TestUnavailableArchivesAre404(t *testing.T) {
	mux := newMux(&service{objs: &objects{data: []byte("x")}})
	for _, p := range []string{"/portal/maps/la.pmtiles", "/portal/maps/sf.mbtiles"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, p, http.NoBody))
		assert.Equal(t, http.StatusNotFound, rec.Code, p)
	}
	rec := httptest.NewRecorder()
	newMux(&service{err: errors.New("db down")}).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/portal/maps/sf.pmtiles", http.NoBody))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "db down")
}

func TestRegionsAreListed(t *testing.T) {
	st := maps.State{Enabled: true, Regions: []maps.Ready{{ID: "sf", URL: maps.ArchivePath("sf")}}}
	rec := httptest.NewRecorder()
	newMux(&service{state: st}).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, maps.RegionsPath, http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code)
	var got maps.State
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, st, got)

	rec = httptest.NewRecorder()
	newMux(&service{err: errors.New("down")}).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, maps.RegionsPath, http.NoBody))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestMountWithoutAServiceMountsNothing(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, maps.RegionsPath, http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRangeEnd(t *testing.T) {
	for h, want := range map[string]int64{
		"bytes=0-16383":   16383,
		"bytes=10-":       -1,
		"bytes=-500":      -1,
		"bytes=0-1,4-5":   -1,
		"items=0-1":       -1,
		"bytes=0-x":       -1,
		"":                -1,
		"bytes=5-4":       4,
		"bytes=0-  12 ":   12,
		"bytes=0--1":      -1,
		"bytes=100-199  ": 199,
	} {
		assert.Equal(t, want, rangeEnd(h), h)
	}
}

func TestRegionsPathIsTheOneTheKnowledgePageNames(t *testing.T) {
	assert.Equal(t, maps.RegionsPath, regionsPath)
	assert.Equal(t, PathPrefix+"sf.pmtiles", maps.ArchivePath("sf"))
}
