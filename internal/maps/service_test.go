package maps

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/pmtiles"
)

var sf = pmtiles.Bounds{MinLon: -122.52, MinLat: 37.70, MaxLon: -122.35, MaxLat: 37.83}

func addSF(t *testing.T, ts *testService) {
	t.Helper()
	_, err := ts.AddRegion(context.Background(), RegionInput{ID: "sf", Name: "San Francisco", Bounds: &sf}, "admin@example.com")
	require.NoError(t, err)
}

func TestPass_FetchesAQueuedRegionIntoTheBucket(t *testing.T) {
	src := newSourceServer(t, map[string][]byte{"20261007.pmtiles": testArchive(t, 2), "20261008.pmtiles": testArchive(t, 3)})
	ts := newTestService(t, src)
	addSF(t, ts)

	require.NoError(t, ts.Pass(context.Background()))

	r, err := ts.regions.Get(context.Background(), "sf")
	require.NoError(t, err)
	assert.Equal(t, StateReady, r.State)
	require.NotNil(t, r.Archive)
	assert.Equal(t, "2026-10-08", r.Archive.Build, "the build is dated by the OpenStreetMap replication time")
	assert.Equal(t, 3, r.Archive.MaxZoom, "the newest daily build was resolved, and it goes to zoom 3")
	assert.InDelta(t, -122.52, r.Archive.Bounds.MinLon, 1e-6)
	assert.Equal(t, r.Archive.Size, r.TotalBytes)
	assert.True(t, strings.HasPrefix(r.Archive.Key, "maps/regions/sf/2026-10-08-"), r.Archive.Key)

	stored, size, err := ts.objects.GetObjectRange(context.Background(), "bucket", r.Archive.Key, 0, pmtiles.RootFetchLen)
	require.NoError(t, err)
	assert.Equal(t, r.Archive.Size, size)
	h, err := pmtiles.ParseHeader(stored)
	require.NoError(t, err)
	assert.Equal(t, uint8(3), h.MaxZoom)
	assert.Equal(t, int32(-1225200000), h.MinLonE7, "the archive's header reports the requested bounds")
	assert.Equal(t, 1, ts.changed, "the knowledge page is refreshed once the region is ready")
}

func TestPass_RefreshReplacesTheArchiveOnlyOnceTheNewOneIsComplete(t *testing.T) {
	src := newSourceServer(t, map[string][]byte{"20261008.pmtiles": testArchive(t, 3)})
	ts := newTestService(t, src)
	addSF(t, ts)
	require.NoError(t, ts.Pass(context.Background()))
	first, _ := ts.regions.Get(context.Background(), "sf")

	_, err := ts.RefreshRegion(context.Background(), "sf")
	require.NoError(t, err)
	during, _ := ts.regions.Get(context.Background(), "sf")
	assert.Equal(t, StateQueued, during.State)
	assert.Equal(t, first.Archive.Key, during.Archive.Key, "a queued refresh keeps serving the archive it had")

	require.NoError(t, ts.Pass(context.Background()))
	after, _ := ts.regions.Get(context.Background(), "sf")
	assert.Equal(t, StateReady, after.State)
	assert.NotEqual(t, first.Archive.Key, after.Archive.Key)
	assert.Equal(t, []string{"bucket/" + after.Archive.Key}, ts.objects.keys("bucket/maps/regions/"),
		"the archive it replaced is removed")
}

func TestPass_AFailedFetchKeepsTheArchiveServing(t *testing.T) {
	src := newSourceServer(t, map[string][]byte{"20261008.pmtiles": testArchive(t, 3)})
	ts := newTestService(t, src)
	addSF(t, ts)
	require.NoError(t, ts.Pass(context.Background()))
	ready, _ := ts.regions.Get(context.Background(), "sf")

	ts.settings.s.SourceURL = "http://127.0.0.1:1/planet.pmtiles"
	_, err := ts.RefreshRegion(context.Background(), "sf")
	require.NoError(t, err)
	require.NoError(t, ts.Pass(context.Background()))

	r, _ := ts.regions.Get(context.Background(), "sf")
	assert.Equal(t, StateFailed, r.State)
	assert.Contains(t, r.Error, "reading the source archive", "the reason names what failed")
	require.NotNil(t, r.Archive)
	assert.Equal(t, ready.Archive.Key, r.Archive.Key)

	_, objects, err := ts.ServedArchive(context.Background(), "sf")
	require.NoError(t, err, "the region still serves")
	assert.NotNil(t, objects)
}

func TestPass_AFailedUploadRemovesWhatItWrote(t *testing.T) {
	src := newSourceServer(t, map[string][]byte{"20261008.pmtiles": testArchive(t, 3)})
	ts := newTestService(t, src)
	addSF(t, ts)
	ts.objects.putErr = errors.New("bucket refused")
	require.NoError(t, ts.Pass(context.Background()))

	r, _ := ts.regions.Get(context.Background(), "sf")
	assert.Equal(t, StateFailed, r.State)
	assert.Contains(t, r.Error, "bucket refused")
	assert.Nil(t, r.Archive)
	assert.Len(t, ts.objects.deleted, 1)
}

func TestPass_ASourceReplacedMidFetchFailsTheFetch(t *testing.T) {
	src := newSourceServer(t, map[string][]byte{"20261008.pmtiles": testArchive(t, 3)})
	ts := newTestService(t, src)
	addSF(t, ts)
	// The first read records the archive's tag; every later read asks for
	// that tag and the server now has another.
	src.Config.Handler = changeTagAfterFirst(src)
	require.NoError(t, ts.Pass(context.Background()))
	r, _ := ts.regions.Get(context.Background(), "sf")
	assert.Equal(t, StateFailed, r.State)
	assert.Contains(t, r.Error, "changed while it was being read")
}

func changeTagAfterFirst(src *sourceServer) http.Handler {
	inner := src.Config.Handler
	seen := 0
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".pmtiles") {
			seen++
			if seen > 1 {
				src.setETag(`"v2"`)
				if r.Header.Get("If-Match") != `"v2"` {
					w.WriteHeader(http.StatusPreconditionFailed)
					return
				}
			}
		}
		inner.ServeHTTP(w, r)
	})
}

func TestPass_ARegionOutsideTheSourceFailsSayingSo(t *testing.T) {
	src := newSourceServer(t, map[string][]byte{"20261008.pmtiles": testArchive(t, 3)})
	ts := newTestService(t, src)
	ts.settings.s.MaxZoom = 3
	_, err := ts.AddRegion(context.Background(), RegionInput{
		ID: "pole", Bounds: &pmtiles.Bounds{MinLon: 0, MinLat: 86, MaxLon: 1, MaxLat: 87},
	}, "a")
	require.NoError(t, err)
	require.NoError(t, ts.Pass(context.Background()))
	r, _ := ts.regions.Get(context.Background(), "pole")
	assert.Equal(t, StateFailed, r.State)
	assert.Equal(t, "the source archive holds nothing in this region at these zooms", r.Error)
}

func TestPass_DisabledDoesNothing(t *testing.T) {
	ts := newTestService(t, nil)
	ts.settings.s.Enabled = false
	addSF(t, ts)
	called := false
	ts.locker = func(context.Context) (func(), bool, error) { called = true; return func() {}, true, nil }
	require.NoError(t, ts.Pass(context.Background()))
	assert.False(t, called, "a disabled deployment takes no lock")
	r, _ := ts.regions.Get(context.Background(), "sf")
	assert.Equal(t, StateQueued, r.State)
}

func TestPass_ALockHeldElsewhereIsSkipped(t *testing.T) {
	ts := newTestService(t, nil)
	addSF(t, ts)
	ts.locker = func(context.Context) (func(), bool, error) { return nil, false, nil }
	require.ErrorIs(t, ts.Pass(context.Background()), bgloop.ErrSkipped)
	r, _ := ts.regions.Get(context.Background(), "sf")
	assert.Equal(t, StateQueued, r.State, "a replica without the lock fetches nothing")

	ts.locker = func(context.Context) (func(), bool, error) { return nil, false, errors.New("db down") }
	require.ErrorContains(t, ts.Pass(context.Background()), "db down")
}

func TestPass_RequeuesAFetchItsWorkerDied(t *testing.T) {
	src := newSourceServer(t, map[string][]byte{"20261008.pmtiles": testArchive(t, 3)})
	ts := newTestService(t, src)
	addSF(t, ts)
	ts.regions.rows["sf"].State = StateFetching
	require.NoError(t, ts.Pass(context.Background()))
	r, _ := ts.regions.Get(context.Background(), "sf")
	assert.Equal(t, StateReady, r.State)
}

func TestPass_ReportsWhatItCouldNotRead(t *testing.T) {
	ts := newTestService(t, nil)
	ts.settings.err = errors.New("settings unreadable")
	require.ErrorContains(t, ts.Pass(context.Background()), "settings unreadable")

	ts.settings.err = nil
	ts.d.Open = func(context.Context, Settings) (Bucket, error) { return Bucket{}, errors.New("no s3") }
	require.ErrorContains(t, ts.Pass(context.Background()), "no s3")

	ts2 := newTestService(t, nil)
	ts2.regions.failNext = errors.New("reset failed")
	require.ErrorContains(t, ts2.Pass(context.Background()), "reset failed")
}

func TestScanUploads_RecordsTheArchivesOperatorsPutInTheBucket(t *testing.T) {
	ts := newTestService(t, nil)
	ts.objects.objects["bucket/"+UploadPrefix+"metro.pmtiles"] = testArchive(t, 2)
	ts.objects.objects["bucket/"+UploadPrefix+"broken.pmtiles"] = []byte("not an archive")
	ts.objects.objects["bucket/"+UploadPrefix+"notes.txt"] = []byte("ignored")
	ts.objects.objects["bucket/"+UploadPrefix+"Bad Name.pmtiles"] = testArchive(t, 1)

	require.NoError(t, ts.Pass(context.Background()))
	regions, _ := ts.regions.List(context.Background())
	require.Len(t, regions, 2)
	byID := map[string]Region{}
	for _, r := range regions {
		byID[r.ID] = r
	}
	metro := byID["metro"]
	assert.Equal(t, StateReady, metro.State, "an uploaded archive is ready with no fetch")
	assert.Equal(t, OriginUpload, metro.Origin)
	require.NotNil(t, metro.Archive)
	assert.Equal(t, 2, metro.Archive.MaxZoom)
	assert.Equal(t, "2026-10-08", metro.Archive.Build)
	assert.Equal(t, StateFailed, byID["broken"].State)
	assert.Equal(t, "the file is not a PMTiles version 3 archive", byID["broken"].Error)
	assert.Equal(t, 1, ts.changed)

	// A second pass over the same files writes nothing new.
	require.NoError(t, ts.Pass(context.Background()))
	assert.Equal(t, 1, ts.changed)

	delete(ts.objects.objects, "bucket/"+UploadPrefix+"metro.pmtiles")
	require.NoError(t, ts.Pass(context.Background()))
	_, err := ts.regions.Get(context.Background(), "metro")
	require.ErrorIs(t, err, ErrRegionNotFound, "a removed file's region goes with it")
}

func TestScanUploads_LeavesAFetchedRegionOfTheSameName(t *testing.T) {
	src := newSourceServer(t, map[string][]byte{"20261008.pmtiles": testArchive(t, 3)})
	ts := newTestService(t, src)
	addSF(t, ts)
	require.NoError(t, ts.Pass(context.Background()))
	ts.objects.objects["bucket/"+UploadPrefix+"sf.pmtiles"] = testArchive(t, 1)
	require.NoError(t, ts.Pass(context.Background()))
	r, _ := ts.regions.Get(context.Background(), "sf")
	assert.Equal(t, OriginFetch, r.Origin)
}

func TestStartStop(t *testing.T) {
	ts := newTestService(t, nil)
	ts.settings.s.Enabled = false
	ts.Start(context.Background())
	ts.poke()
	ts.poke()
	ts.Stop()
	ts.Stop()
}

func TestNew_FillsDefaults(t *testing.T) {
	s := New(Deps{})
	assert.Equal(t, DefaultEvery, s.d.Every)
	assert.Equal(t, ProtomapsIndexURL, s.d.IndexURL)
	assert.Equal(t, ProtomapsBuildURL, s.d.BuildURL)
	assert.Equal(t, time.Second, s.d.RetryPause(1))
	s.d.Changed(context.Background())
}

func TestFailureReasonIsBounded(t *testing.T) {
	long := errors.New(strings.Repeat("é", maxReasonLen+10))
	assert.Len(t, []rune(failureReason(long)), maxReasonLen)
}

func TestSafeName(t *testing.T) {
	assert.Equal(t, "2026-10-08", safeName("2026-10-08"))
	assert.Equal(t, "a-b-c", safeName("a/b c"))
	assert.Equal(t, "build", safeName(""))
}

func TestUploadID(t *testing.T) {
	for key, want := range map[string]string{
		UploadPrefix + "metro.pmtiles":      "metro",
		UploadPrefix + "sub/metro.pmtiles":  "",
		UploadPrefix + "metro.mbtiles":      "",
		"elsewhere/metro.pmtiles":           "",
		UploadPrefix + "Upper Case.pmtiles": "",
	} {
		id, ok := uploadID(key)
		assert.Equal(t, want, id, key)
		assert.Equal(t, want != "", ok, key)
	}
}

// A listing that stopped short forgets no uploaded region: a file past the
// page is not gone.
func TestScanUploads_ATruncatedListingForgetsNothing(t *testing.T) {
	ts := newTestService(t, nil)
	ts.regions.rows["older"] = &Region{
		ID: "older", Origin: OriginUpload, State: StateReady,
		Archive: &Archive{Bucket: "bucket", Key: UploadPrefix + "older.pmtiles"},
	}
	ts.objects.objects["bucket/"+UploadPrefix+"metro.pmtiles"] = testArchive(t, 2)
	ts.objects.truncated = true

	require.ErrorIs(t, ts.scanUploads(context.Background(), Bucket{Objects: ts.objects, Name: "bucket"}), errUploadsTruncated)
	_, err := ts.regions.Get(context.Background(), "older")
	require.NoError(t, err, "a region whose file was past the listing is kept")
	metro, err := ts.regions.Get(context.Background(), "metro")
	require.NoError(t, err, "what the listing did return is recorded")
	assert.Equal(t, StateReady, metro.State)
	assert.Equal(t, 1, ts.changed)
}

// Stopping mid-fetch cancels it, removes what it wrote, and leaves the region
// fetching for the next pass to queue again, not failed.
func TestFetch_AStoppedFetchIsLeftForTheNextPass(t *testing.T) {
	src := newSourceServer(t, map[string][]byte{"20261008.pmtiles": testArchive(t, 3)})
	ts := newTestService(t, src)
	addSF(t, ts)
	ctx, cancel := context.WithCancel(context.Background())
	ts.objects.onPut = cancel
	require.ErrorIs(t, ts.Pass(ctx), context.Canceled)

	r, _ := ts.regions.Get(context.Background(), "sf")
	assert.Equal(t, StateFetching, r.State, "a stopped fetch is not recorded as failed")
	assert.Empty(t, r.Error)
	assert.Empty(t, ts.objects.keys("bucket/maps/regions/"), "the part written is removed")

	require.NoError(t, ts.Pass(context.Background()))
	r, _ = ts.regions.Get(context.Background(), "sf")
	assert.Equal(t, StateReady, r.State, "the next pass queues it again and finishes it")
}
