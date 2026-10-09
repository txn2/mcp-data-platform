//go:build integration

package maps

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestRegionStoreRealDB runs a region through the states the fetch moves it
// through, against the migrated map_regions table.
func TestRegionStoreRealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := NewPostgresRegions(db)

	require.NoError(t, s.Create(ctx, Region{ID: "sf", Name: "San Francisco", Bounds: sf, MaxZoom: 13, CreatedBy: "a"}))
	require.ErrorIs(t, s.Create(ctx, Region{ID: "sf", Name: "again", Bounds: sf, MaxZoom: 13}), ErrRegionExists)

	claimed, err := s.ClaimNext(ctx)
	require.NoError(t, err)
	require.Equal(t, "sf", claimed.ID)
	assert.Equal(t, StateFetching, claimed.State)
	none, err := s.ClaimNext(ctx)
	require.NoError(t, err)
	assert.Nil(t, none, "a region being fetched is not claimed twice")
	require.ErrorIs(t, s.Requeue(ctx, "sf", 14), ErrRegionBusy)

	require.NoError(t, s.Progress(ctx, "sf", 5, 20))
	a := Archive{Bucket: "bucket", Key: "maps/regions/sf/a.pmtiles", Size: 20, Build: "2026-10-08", MaxZoom: 13, Bounds: sf}
	prev, ok, err := s.Finish(ctx, "sf", a)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Nil(t, prev)

	r, err := s.Get(ctx, "sf")
	require.NoError(t, err)
	assert.Equal(t, StateReady, r.State)
	require.NotNil(t, r.Archive)
	assert.Equal(t, a.Key, r.Archive.Key)
	assert.InDelta(t, -122.52, r.Archive.Bounds.MinLon, 1e-9)

	// A refresh that fails keeps the archive.
	require.NoError(t, s.Requeue(ctx, "sf", 14))
	_, err = s.ClaimNext(ctx)
	require.NoError(t, err)
	require.NoError(t, s.Fail(ctx, "sf", "source unreachable"))
	r, err = s.Get(ctx, "sf")
	require.NoError(t, err)
	assert.Equal(t, StateFailed, r.State)
	assert.Equal(t, "source unreachable", r.Error)
	require.NotNil(t, r.Archive)
	assert.Equal(t, a.Key, r.Archive.Key)

	// A fetch left behind by a dead worker is queued again.
	require.NoError(t, s.Requeue(ctx, "sf", 14))
	_, err = s.ClaimNext(ctx)
	require.NoError(t, err)
	require.NoError(t, s.ResetStale(ctx))
	r, _ = s.Get(ctx, "sf")
	assert.Equal(t, StateQueued, r.State)

	list, err := s.List(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 1)
	gone, err := s.Delete(ctx, "sf")
	require.NoError(t, err)
	assert.Equal(t, a.Key, gone.Archive.Key)
	_, err = s.Get(ctx, "sf")
	require.ErrorIs(t, err, ErrRegionNotFound)
}

func TestUploadStoreRealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := NewPostgresRegions(db)
	key := UploadPrefix + "metro.pmtiles"

	require.NoError(t, s.PutUpload(ctx, Region{
		ID: "metro", Name: "metro", State: StateReady, Bounds: sf, MaxZoom: 12,
		Archive: &Archive{Bucket: "b", Key: key, Size: 99, Build: "2026-10-08", MaxZoom: 12, Bounds: sf},
	}))
	r, err := s.Get(ctx, "metro")
	require.NoError(t, err)
	assert.Equal(t, OriginUpload, r.Origin)
	require.NotNil(t, r.Archive)
	assert.Equal(t, int64(99), r.Archive.Size)

	require.NoError(t, s.PutUpload(ctx, Region{
		ID: "metro", Name: "metro", State: StateFailed, Error: "not an archive",
		TotalBytes: 3, Archive: &Archive{Bucket: "b", Key: key, Size: 3},
	}))
	r, _ = s.Get(ctx, "metro")
	assert.Equal(t, StateFailed, r.State)
	assert.Nil(t, r.Archive, "a failed upload serves nothing")

	require.NoError(t, s.Create(ctx, Region{ID: "sf", Name: "SF", Bounds: sf, MaxZoom: 13}))
	require.NoError(t, s.PutUpload(ctx, Region{ID: "sf", Name: "sf", State: StateReady, Archive: &Archive{Key: UploadPrefix + "sf.pmtiles"}}))
	r, _ = s.Get(ctx, "sf")
	assert.Equal(t, OriginFetch, r.Origin, "an upload never replaces a fetched region")

	none, err := s.DeleteUploadsExcept(ctx, []string{key})
	require.NoError(t, err)
	assert.Empty(t, none)
	gone, err := s.DeleteUploadsExcept(ctx, nil)
	require.NoError(t, err)
	require.Len(t, gone, 1)
	assert.Equal(t, "metro", gone[0].ID)
}

func TestSettingsStoreRealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := NewPostgresSettings(db)
	got, err := s.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, DefaultSettings(), got)
	require.NoError(t, s.Set(ctx, Settings{Enabled: true, MaxZoom: 12, Bucket: "maps"}, "a@example.com"))
	got, err = s.Get(ctx)
	require.NoError(t, err)
	assert.True(t, got.Enabled)
	assert.Equal(t, "maps", got.Bucket)
	assert.Equal(t, "a@example.com", got.UpdatedBy)
}

// TestFetchLockRealDB: while one replica's pass holds the fetch lock, a
// second replica's pass is skipped and fetches nothing.
func TestFetchLockRealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	reader := NewReader(db)
	require.NoError(t, reader.Settings.Set(ctx, Settings{Enabled: true, MaxZoom: 3}, "a"))
	require.NoError(t, reader.Regions.Create(ctx, Region{ID: "sf", Name: "SF", Bounds: sf, MaxZoom: 3}))

	first := New(Deps{DB: db, Settings: reader.Settings, Regions: reader.Regions})
	unlock, ok, err := first.advisoryLock(ctx)
	require.NoError(t, err)
	require.True(t, ok)

	second := New(Deps{
		DB: db, Settings: reader.Settings, Regions: reader.Regions,
		Open: func(context.Context, Settings) (Bucket, error) {
			t.Fatal("the second replica opened the bucket")
			return Bucket{}, nil
		},
	})
	require.ErrorIs(t, second.Pass(ctx), bgloop.ErrSkipped)
	r, err := reader.Regions.Get(ctx, "sf")
	require.NoError(t, err)
	assert.Equal(t, StateQueued, r.State)

	unlock()
	_, ok, err = second.advisoryLock(ctx)
	require.NoError(t, err)
	assert.True(t, ok, "released, the lock is the next replica's")
}
