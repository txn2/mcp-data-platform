package maps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/pmtiles"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
)

// memSettings is the settings section held in memory.
type memSettings struct {
	mu  sync.Mutex
	s   Settings
	err error
}

func (m *memSettings) Get(context.Context) (Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.s, m.err
}

func (m *memSettings) Set(_ context.Context, s Settings, author string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	s.UpdatedBy, s.UpdatedAt = author, time.Now()
	m.s = s
	return nil
}

// memRegions is the map_regions table held in memory, with the store
// contract PostgresRegions keeps: a requeue refuses a region being fetched,
// Finish and Fail act only on a region being fetched, an upload never
// replaces a fetched region, and a region has an archive once one was ready.
type memRegions struct {
	mu       sync.Mutex
	rows     map[string]*Region
	failNext error
}

func newMemRegions() *memRegions { return &memRegions{rows: map[string]*Region{}} }

func (m *memRegions) takeFailure() error {
	err := m.failNext
	m.failNext = nil
	return err
}

func clone(r *Region) *Region {
	c := *r
	if r.Archive != nil {
		a := *r.Archive
		c.Archive = &a
	}
	return &c
}

func (m *memRegions) List(context.Context) ([]Region, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.takeFailure(); err != nil {
		return nil, err
	}
	out := make([]Region, 0, len(m.rows))
	for _, r := range m.rows {
		out = append(out, *clone(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *memRegions) Get(_ context.Context, id string) (*Region, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.takeFailure(); err != nil {
		return nil, err
	}
	r, ok := m.rows[id]
	if !ok {
		return nil, ErrRegionNotFound
	}
	return clone(r), nil
}

func (m *memRegions) Create(_ context.Context, r Region) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.takeFailure(); err != nil {
		return err
	}
	if _, ok := m.rows[r.ID]; ok {
		return ErrRegionExists
	}
	r.Origin, r.State, r.RequestedAt = OriginFetch, StateQueued, time.Now()
	m.rows[r.ID] = &r
	return nil
}

func (m *memRegions) Delete(_ context.Context, id string) (*Region, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return nil, ErrRegionNotFound
	}
	delete(m.rows, id)
	return r, nil
}

func (m *memRegions) Requeue(_ context.Context, id string, maxZoom int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return ErrRegionNotFound
	}
	if r.Origin != OriginFetch || r.State == StateFetching {
		return ErrRegionBusy
	}
	r.State, r.MaxZoom, r.Error, r.ProgressBytes, r.TotalBytes = StateQueued, maxZoom, "", 0, 0
	r.RequestedAt = time.Now()
	return nil
}

func (m *memRegions) ResetStale(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.takeFailure(); err != nil {
		return err
	}
	for _, r := range m.rows {
		if r.State == StateFetching {
			r.State, r.ProgressBytes = StateQueued, 0
		}
	}
	return nil
}

func (m *memRegions) ClaimNext(context.Context) (*Region, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.takeFailure(); err != nil {
		return nil, err
	}
	var next *Region
	for _, r := range m.rows {
		if r.State == StateQueued && r.Origin == OriginFetch && (next == nil || r.RequestedAt.Before(next.RequestedAt)) {
			next = r
		}
	}
	if next == nil {
		return nil, nil //nolint:nilnil // the store's own answer for nothing queued
	}
	next.State, next.Error, next.ProgressBytes, next.TotalBytes = StateFetching, "", 0, 0
	return clone(next), nil
}

func (m *memRegions) Progress(_ context.Context, id string, done, total int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rows[id]; ok && r.State == StateFetching {
		r.ProgressBytes, r.TotalBytes = done, total
	}
	return nil
}

func (m *memRegions) Finish(_ context.Context, id string, a Archive) (*Archive, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.takeFailure(); err != nil {
		return nil, false, err
	}
	r, ok := m.rows[id]
	if !ok || r.State != StateFetching {
		return nil, false, nil
	}
	prev := r.Archive
	a.ReadyAt = time.Now()
	r.State, r.Archive, r.ProgressBytes, r.TotalBytes = StateReady, &a, a.Size, a.Size
	return prev, true, nil
}

func (m *memRegions) Fail(_ context.Context, id, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rows[id]; ok && r.State == StateFetching {
		r.State, r.Error = StateFailed, reason
	}
	return nil
}

func (m *memRegions) PutUpload(_ context.Context, r Region) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if prev, ok := m.rows[r.ID]; ok && prev.Origin != OriginUpload {
		return nil
	}
	r.Origin = OriginUpload
	if r.State != StateReady {
		r.Archive = nil
	}
	m.rows[r.ID] = &r
	return nil
}

func (m *memRegions) DeleteUploadsExcept(_ context.Context, keep []string) ([]Region, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := map[string]bool{}
	for _, k := range keep {
		kept[k] = true
	}
	out := make([]Region, 0)
	for id, r := range m.rows {
		key := UploadPrefix + id + ".pmtiles"
		if r.Origin == OriginUpload && !kept[key] {
			out = append(out, *r)
			delete(m.rows, id)
		}
	}
	return out, nil
}

// memObjects is a bucket held in memory.
type memObjects struct {
	mu        sync.Mutex
	objects   map[string][]byte
	putErr    error
	deleted   []string
	truncated bool
	// onPut, when set, runs before a put reads its body.
	onPut func()
}

func newMemObjects() *memObjects { return &memObjects{objects: map[string][]byte{}} }

func (o *memObjects) PutObjectStream(_ context.Context, bucket, key string, body io.Reader, _ string) (int64, error) {
	if o.onPut != nil {
		o.onPut()
	}
	b, err := io.ReadAll(body)
	if err != nil {
		return 0, fmt.Errorf("reading the upload: %w", err)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.putErr != nil {
		return 0, o.putErr
	}
	o.objects[bucket+"/"+key] = b
	return int64(len(b)), nil
}

func (o *memObjects) GetObjectRange(_ context.Context, bucket, key string, offset, length int64) (body []byte, size int64, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	b, ok := o.objects[bucket+"/"+key]
	if !ok {
		return nil, 0, errors.New("no such key")
	}
	end := min(offset+length, int64(len(b)))
	if offset >= end {
		return nil, int64(len(b)), nil
	}
	return b[offset:end], int64(len(b)), nil
}

func (o *memObjects) DeleteObject(_ context.Context, bucket, key string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.objects, bucket+"/"+key)
	o.deleted = append(o.deleted, bucket+"/"+key)
	return nil
}

func (o *memObjects) ListDirectory(_ context.Context, bucket, prefix string) ([]s3adapter.ObjectEntry, bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []s3adapter.ObjectEntry
	for k, b := range o.objects {
		key, ok := strings.CutPrefix(k, bucket+"/")
		if !ok || !strings.HasPrefix(key, prefix) || strings.Contains(strings.TrimPrefix(key, prefix), "/") {
			continue
		}
		out = append(out, s3adapter.ObjectEntry{Key: key, Size: int64(len(b))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, o.truncated, nil
}

func (o *memObjects) keys(prefix string) []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []string
	for k := range o.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// testArchive builds a small clustered archive holding every tile from zoom
// 0 to maxZ, each one's content naming it, with OpenStreetMap metadata.
func testArchive(t *testing.T, maxZ uint8) []byte {
	t.Helper()
	type tile struct {
		id   uint64
		data []byte
	}
	var tiles []tile
	for z := uint8(0); z <= maxZ; z++ {
		for x := uint32(0); x < 1<<z; x++ {
			for y := uint32(0); y < 1<<z; y++ {
				tiles = append(tiles, tile{pmtiles.ZxyToID(z, x, y), fmt.Appendf(nil, "tile %d/%d/%d", z, x, y)})
			}
		}
	}
	sort.Slice(tiles, func(i, j int) bool { return tiles[i].id < tiles[j].id })
	var data bytes.Buffer
	entries := make([]pmtiles.Entry, 0, len(tiles))
	for _, tl := range tiles {
		entries = append(entries, pmtiles.Entry{
			TileID: tl.id, Offset: uint64(data.Len()), Length: uint32(len(tl.data)), RunLength: 1, //nolint:gosec // a buffer length is never negative
		})
		_, _ = data.Write(tl.data)
	}
	root, err := pmtiles.SerializeDirectory(entries, pmtiles.CompressionNone)
	require.NoError(t, err)
	meta := []byte(`{"planetiler:osm:osmosisreplicationtime":"2026-10-08T04:00:00Z","attribution":"OpenStreetMap"}`)
	h := pmtiles.Header{
		RootOffset: pmtiles.HeaderLen, RootLength: uint64(len(root)),
		MetadataOffset: pmtiles.HeaderLen + uint64(len(root)), MetadataLength: uint64(len(meta)),
		TileEntries: uint64(len(entries)), TileContents: uint64(len(entries)), AddressedTiles: uint64(len(entries)),
		Clustered: true, InternalCompression: pmtiles.CompressionNone, TileType: pmtiles.TileTypeMVT,
		MaxZoom: maxZ, MinLonE7: -1800000000, MinLatE7: -850000000, MaxLonE7: 1800000000, MaxLatE7: 850000000,
	}
	h.LeafOffset = h.MetadataOffset + h.MetadataLength
	h.TileDataOffset = h.LeafOffset
	h.TileDataLength = uint64(data.Len()) //nolint:gosec // a buffer length is never negative
	return append(append(append(h.Bytes(), root...), meta...), data.Bytes()...)
}

// sourceServer serves archives by range at /<name> and a Protomaps-style
// build index at /builds.json listing them.
type sourceServer struct {
	*httptest.Server
	mu       sync.Mutex
	archives map[string][]byte
	etag     string
	reads    int
}

func newSourceServer(t *testing.T, archives map[string][]byte) *sourceServer {
	t.Helper()
	s := &sourceServer{archives: archives, etag: `"v1"`}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.reads++
		etag := s.etag
		s.mu.Unlock()
		if r.URL.Path == "/builds.json" {
			keys := make([]string, 0, len(s.archives))
			for k := range s.archives {
				keys = append(keys, fmt.Sprintf(`{"key":%q}`, k))
			}
			_, _ = io.WriteString(w, "["+strings.Join(keys, ",")+`,{"key":"v4.15.2.pmtiles"}]`)
			return
		}
		b, ok := s.archives[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", etag)
		http.ServeContent(w, r, "a.pmtiles", time.Time{}, bytes.NewReader(b))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *sourceServer) setETag(e string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.etag = e
}

// testService is a service over in-memory stores, a bucket and a source
// server, holding the fetch lock whenever it asks.
type testService struct {
	*Service
	settings *memSettings
	regions  *memRegions
	objects  *memObjects
	changed  int
}

func newTestService(t *testing.T, src *sourceServer) *testService {
	t.Helper()
	ts := &testService{
		settings: &memSettings{s: Settings{Enabled: true, MaxZoom: 3}},
		regions:  newMemRegions(),
		objects:  newMemObjects(),
	}
	index, base := "", ""
	if src != nil {
		index, base = src.URL+"/builds.json", src.URL+"/"
	}
	ts.Service = New(Deps{
		Settings: ts.settings, Regions: ts.regions,
		Open: func(context.Context, Settings) (Bucket, error) {
			return Bucket{Objects: ts.objects, Name: "bucket"}, nil
		},
		Client:   http.DefaultClient,
		Changed:  func(context.Context) { ts.changed++ },
		IndexURL: index, BuildURL: base,
		RetryPause: func(int) time.Duration { return 0 },
	})
	ts.locker = func(context.Context) (func(), bool, error) { return func() {}, true, nil }
	return ts
}
