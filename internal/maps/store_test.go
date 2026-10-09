package maps

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	return db, mock
}

var regionCols = []string{
	"id", "name", "preset", "origin", "min_lon", "min_lat", "max_lon", "max_lat", "max_zoom",
	"state", "progress_bytes", "total_bytes", "error", "requested_at", "started_at", "finished_at",
	"archive_bucket", "archive_key", "archive_size", "archive_build", "archive_min_zoom", "archive_max_zoom",
	"archive_min_lon", "archive_min_lat", "archive_max_lon", "archive_max_lat", "archive_ready_at", "created_by",
}

func readyRow(rows *sqlmock.Rows) *sqlmock.Rows {
	now := time.Now()
	return rows.AddRow("sf", "San Francisco", "", "fetch", -122.52, 37.7, -122.35, 37.83, 13,
		"ready", 10, 10, "", now, now, now,
		"bucket", "maps/regions/sf/a.pmtiles", 10, "2026-10-08", 0, 13,
		-122.52, 37.7, -122.35, 37.83, now, "a")
}

func queuedRow(rows *sqlmock.Rows, id string) *sqlmock.Rows {
	return rows.AddRow(id, id, "", "fetch", -1.0, -1.0, 1.0, 1.0, 13,
		"queued", 0, 0, "", time.Now(), nil, nil,
		"", "", 0, "", 0, 0, 0.0, 0.0, 0.0, 0.0, nil, "a")
}

func TestPostgresSettings(t *testing.T) {
	db, mock := mockDB(t)
	s := NewPostgresSettings(db)
	ctx := context.Background()

	mock.ExpectQuery("SELECT value, updated_by, updated_at FROM platform_settings").WithArgs(SettingsSection).
		WillReturnError(sql.ErrNoRows)
	got, err := s.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, DefaultSettings(), got, "never saved is the defaults")

	at := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT value").WithArgs(SettingsSection).WillReturnRows(
		sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).
			AddRow([]byte(`{"enabled":true,"max_zoom":12}`), "a@example.com", at))
	got, err = s.Get(ctx)
	require.NoError(t, err)
	assert.True(t, got.Enabled)
	assert.Equal(t, 12, got.MaxZoom)
	assert.Equal(t, "a@example.com", got.UpdatedBy)

	mock.ExpectQuery("SELECT value").WillReturnRows(
		sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).AddRow([]byte(`{`), "", at))
	_, err = s.Get(ctx)
	require.ErrorContains(t, err, "decoding maps settings")

	mock.ExpectQuery("SELECT value").WillReturnError(errors.New("boom"))
	_, err = s.Get(ctx)
	require.ErrorContains(t, err, "reading maps settings")

	mock.ExpectExec("INSERT INTO platform_settings").
		WithArgs(SettingsSection, []byte(`{"enabled":true,"s3_connection":"","bucket":"","max_zoom":14,"source_url":""}`), "a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Set(ctx, Settings{Enabled: true, MaxZoom: 14, UpdatedBy: "ignored"}, "a"))

	mock.ExpectExec("INSERT INTO platform_settings").WillReturnError(errors.New("boom"))
	require.ErrorContains(t, s.Set(ctx, Settings{}, "a"), "storing maps settings")
}

func TestPostgresRegions_Reads(t *testing.T) {
	db, mock := mockDB(t)
	s := NewPostgresRegions(db)
	ctx := context.Background()

	mock.ExpectQuery("SELECT .* FROM map_regions ORDER BY name, id").
		WillReturnRows(queuedRow(readyRow(sqlmock.NewRows(regionCols)), "la"))
	list, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.NotNil(t, list[0].Archive)
	assert.Equal(t, "maps/regions/sf/a.pmtiles", list[0].Archive.Key)
	assert.NotNil(t, list[0].StartedAt)
	assert.Nil(t, list[1].Archive, "a region never ready has no archive")
	assert.Nil(t, list[1].StartedAt)

	mock.ExpectQuery("FROM map_regions ORDER BY").WillReturnRows(sqlmock.NewRows(regionCols))
	list, err = s.List(ctx)
	require.NoError(t, err)
	assert.NotNil(t, list)

	mock.ExpectQuery("FROM map_regions ORDER BY").WillReturnError(errors.New("boom"))
	_, err = s.List(ctx)
	require.Error(t, err)

	mock.ExpectQuery("FROM map_regions ORDER BY").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("short"))
	_, err = s.List(ctx)
	require.Error(t, err)

	mock.ExpectQuery("FROM map_regions WHERE id = ").WithArgs("sf").WillReturnRows(readyRow(sqlmock.NewRows(regionCols)))
	r, err := s.Get(ctx, "sf")
	require.NoError(t, err)
	assert.Equal(t, "sf", r.ID)

	mock.ExpectQuery("FROM map_regions WHERE id = ").WithArgs("x").WillReturnError(sql.ErrNoRows)
	_, err = s.Get(ctx, "x")
	require.ErrorIs(t, err, ErrRegionNotFound)

	mock.ExpectQuery("FROM map_regions WHERE id = ").WithArgs("x").WillReturnError(errors.New("boom"))
	_, err = s.Get(ctx, "x")
	require.ErrorContains(t, err, "reading map region")
}

func TestPostgresRegions_Writes(t *testing.T) {
	db, mock := mockDB(t)
	s := NewPostgresRegions(db)
	ctx := context.Background()

	mock.ExpectExec("INSERT INTO map_regions").WithArgs("sf", "SF", "", -122.52, 37.7, -122.35, 37.83, 13, "a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Create(ctx, Region{ID: "sf", Name: "SF", Bounds: sf, MaxZoom: 13, CreatedBy: "a"}))
	mock.ExpectExec("INSERT INTO map_regions").WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.Create(ctx, Region{ID: "sf"}), ErrRegionExists)
	mock.ExpectExec("INSERT INTO map_regions").WillReturnError(errors.New("boom"))
	require.ErrorContains(t, s.Create(ctx, Region{ID: "sf"}), "creating map region")

	mock.ExpectQuery("DELETE FROM map_regions WHERE id = ").WithArgs("sf").
		WillReturnRows(readyRow(sqlmock.NewRows(regionCols)))
	gone, err := s.Delete(ctx, "sf")
	require.NoError(t, err)
	assert.Equal(t, "sf", gone.ID)
	mock.ExpectQuery("DELETE FROM map_regions WHERE id = ").WillReturnError(sql.ErrNoRows)
	_, err = s.Delete(ctx, "sf")
	require.ErrorIs(t, err, ErrRegionNotFound)
	mock.ExpectQuery("DELETE FROM map_regions WHERE id = ").WillReturnError(errors.New("boom"))
	_, err = s.Delete(ctx, "sf")
	require.ErrorContains(t, err, "deleting map region")

	mock.ExpectExec("UPDATE map_regions SET state = 'queued', max_zoom").WithArgs("sf", 12).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Requeue(ctx, "sf", 12))
	mock.ExpectExec("UPDATE map_regions SET state = 'queued', max_zoom").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("FROM map_regions WHERE id = ").WillReturnRows(queuedRow(sqlmock.NewRows(regionCols), "sf"))
	require.ErrorIs(t, s.Requeue(ctx, "sf", 12), ErrRegionBusy)
	mock.ExpectExec("UPDATE map_regions SET state = 'queued', max_zoom").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("FROM map_regions WHERE id = ").WillReturnError(sql.ErrNoRows)
	require.ErrorIs(t, s.Requeue(ctx, "x", 12), ErrRegionNotFound)
	mock.ExpectExec("UPDATE map_regions SET state = 'queued', max_zoom").WillReturnError(errors.New("boom"))
	require.ErrorContains(t, s.Requeue(ctx, "x", 12), "queueing map region")

	mock.ExpectExec("UPDATE map_regions SET state = 'queued', progress_bytes = 0").WillReturnResult(sqlmock.NewResult(0, 2))
	require.NoError(t, s.ResetStale(ctx))
	mock.ExpectExec("UPDATE map_regions SET state = 'queued', progress_bytes = 0").WillReturnError(errors.New("boom"))
	require.Error(t, s.ResetStale(ctx))

	mock.ExpectQuery("UPDATE map_regions SET state = 'fetching'").WillReturnRows(queuedRow(sqlmock.NewRows(regionCols), "sf"))
	claimed, err := s.ClaimNext(ctx)
	require.NoError(t, err)
	assert.Equal(t, "sf", claimed.ID)
	mock.ExpectQuery("UPDATE map_regions SET state = 'fetching'").WillReturnError(sql.ErrNoRows)
	claimed, err = s.ClaimNext(ctx)
	require.NoError(t, err)
	assert.Nil(t, claimed)
	mock.ExpectQuery("UPDATE map_regions SET state = 'fetching'").WillReturnError(errors.New("boom"))
	_, err = s.ClaimNext(ctx)
	require.Error(t, err)

	mock.ExpectExec("UPDATE map_regions SET progress_bytes").WithArgs("sf", int64(5), int64(10)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Progress(ctx, "sf", 5, 10))
	mock.ExpectExec("UPDATE map_regions SET progress_bytes").WillReturnError(errors.New("boom"))
	require.Error(t, s.Progress(ctx, "sf", 5, 10))

	mock.ExpectExec("UPDATE map_regions SET state = 'failed'").WithArgs("sf", "why").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Fail(ctx, "sf", "why"))
	mock.ExpectExec("UPDATE map_regions SET state = 'failed'").WillReturnError(errors.New("boom"))
	require.Error(t, s.Fail(ctx, "sf", "why"))
}

func TestPostgresRegions_Finish(t *testing.T) {
	db, mock := mockDB(t)
	s := NewPostgresRegions(db)
	ctx := context.Background()
	a := Archive{Bucket: "bucket", Key: "maps/regions/sf/b.pmtiles", Size: 20, Build: "2026-10-09", MaxZoom: 13, Bounds: sf}

	mock.ExpectBegin()
	mock.ExpectQuery("FOR UPDATE").WithArgs("sf").WillReturnRows(readyRow(sqlmock.NewRows(regionCols)))
	mock.ExpectExec("UPDATE map_regions SET state = 'ready'").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	prev, ok, err := s.Finish(ctx, "sf", a)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "maps/regions/sf/a.pmtiles", prev.Key, "the archive it replaced is returned for removal")

	mock.ExpectBegin()
	mock.ExpectQuery("FOR UPDATE").WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()
	_, ok, err = s.Finish(ctx, "sf", a)
	require.NoError(t, err)
	assert.False(t, ok, "a region deleted or requeued mid-fetch is not finished")

	mock.ExpectBegin()
	mock.ExpectQuery("FOR UPDATE").WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	_, _, err = s.Finish(ctx, "sf", a)
	require.Error(t, err)

	mock.ExpectBegin()
	mock.ExpectQuery("FOR UPDATE").WillReturnRows(readyRow(sqlmock.NewRows(regionCols)))
	mock.ExpectExec("UPDATE map_regions SET state = 'ready'").WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	_, _, err = s.Finish(ctx, "sf", a)
	require.Error(t, err)

	mock.ExpectBegin()
	mock.ExpectQuery("FOR UPDATE").WillReturnRows(readyRow(sqlmock.NewRows(regionCols)))
	mock.ExpectExec("UPDATE map_regions SET state = 'ready'").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit().WillReturnError(errors.New("boom"))
	_, _, err = s.Finish(ctx, "sf", a)
	require.Error(t, err)

	mock.ExpectBegin().WillReturnError(errors.New("boom"))
	_, _, err = s.Finish(ctx, "sf", a)
	require.Error(t, err)
}

func TestPostgresRegions_Uploads(t *testing.T) {
	db, mock := mockDB(t)
	s := NewPostgresRegions(db)
	ctx := context.Background()

	mock.ExpectExec("INSERT INTO map_regions .*'upload'").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.PutUpload(ctx, Region{
		ID: "metro", Name: "metro", State: StateReady,
		Archive: &Archive{Bucket: "b", Key: UploadPrefix + "metro.pmtiles", Size: 9},
	}))
	mock.ExpectExec("INSERT INTO map_regions").WillReturnError(errors.New("boom"))
	require.Error(t, s.PutUpload(ctx, Region{ID: "metro"}))

	mock.ExpectQuery("DELETE FROM map_regions WHERE origin = 'upload'").
		WillReturnRows(queuedRow(sqlmock.NewRows(regionCols), "old"))
	gone, err := s.DeleteUploadsExcept(ctx, nil)
	require.NoError(t, err)
	require.Len(t, gone, 1)
	mock.ExpectQuery("DELETE FROM map_regions WHERE origin = 'upload'").WillReturnError(errors.New("boom"))
	_, err = s.DeleteUploadsExcept(ctx, []string{"k"})
	require.Error(t, err)
	mock.ExpectQuery("DELETE FROM map_regions WHERE origin = 'upload'").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("short"))
	_, err = s.DeleteUploadsExcept(ctx, []string{"k"})
	require.Error(t, err)
}

func TestAdvisoryLock(t *testing.T) {
	db, mock := mockDB(t)
	s := New(Deps{DB: db})
	ctx := context.Background()

	mock.ExpectQuery("SELECT pg_try_advisory_lock").WithArgs(FetchLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(true))
	mock.ExpectExec("SELECT pg_advisory_unlock").WithArgs(FetchLockKey).WillReturnResult(sqlmock.NewResult(0, 0))
	unlock, ok, err := s.advisoryLock(ctx)
	require.NoError(t, err)
	assert.True(t, ok)
	unlock()

	mock.ExpectQuery("SELECT pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(false))
	_, ok, err = s.advisoryLock(ctx)
	require.NoError(t, err)
	assert.False(t, ok, "another replica holds it")

	mock.ExpectQuery("SELECT pg_try_advisory_lock").WillReturnError(errors.New("boom"))
	_, _, err = s.advisoryLock(ctx)
	require.ErrorContains(t, err, "taking the fetch lock")
}

func TestNewReader(t *testing.T) {
	assert.Nil(t, NewReader(nil))
	db, _ := mockDB(t)
	assert.NotNil(t, NewReader(db))
}
