package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

func TestPromptUsage_EmptyIDsSkipsQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	out, err := store.PromptUsage(context.Background(), nil)
	assert.NoError(t, err)
	assert.Empty(t, out)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPromptUsage_AggregatesServeEvents(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	last := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT parameters->>'prompt_id', COUNT\\(\\*\\), MAX\\(timestamp\\)").
		WithArgs(pq.Array([]string{"p1", "p2"})).
		WillReturnRows(sqlmock.NewRows([]string{"prompt_id", "count", "max"}).
			AddRow("p1", int64(37), last))

	out, err := store.PromptUsage(context.Background(), []string{"p1", "p2"})
	require.NoError(t, err)
	require.Len(t, out, 1, "a prompt never served has no entry")
	assert.Equal(t, int64(37), out["p1"].RunCount)
	require.NotNil(t, out["p1"].LastRunAt)
	assert.True(t, out["p1"].LastRunAt.Equal(last))
	_, served := out["p2"]
	assert.False(t, served)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPromptUsage_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	mock.ExpectQuery("SELECT parameters->>'prompt_id'").
		WillReturnError(errors.New("db down"))

	_, err = store.PromptUsage(context.Background(), []string{"p1"})
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestResourceUsage_EmptyIDsSkipsQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	out, err := store.ResourceUsage(context.Background(), nil)
	assert.NoError(t, err)
	assert.Empty(t, out)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestResourceUsage_SumsSurfacesPerResource(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	older := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

	// The query groups by (resource, surface), so one resource read through two
	// doors arrives as two rows that must sum rather than overwrite.
	mock.ExpectQuery("SELECT parameters->>'resource_id'").
		WithArgs(pq.Array([]string{"r1", "r2"}), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"resource_id", "surface", "reads_short", "reads_long", "max"}).
			AddRow("r1", resource.SurfaceMCPRead, int64(5), int64(9), older).
			AddRow("r1", resource.SurfaceDownload, int64(2), int64(2), newer))

	out, err := store.ResourceUsage(context.Background(), []string{"r1", "r2"})
	require.NoError(t, err)
	require.Len(t, out, 1, "a resource never read has no entry")

	got := out["r1"]
	assert.Equal(t, int64(7), got.Reads30d, "30-day reads must sum across surfaces")
	assert.Equal(t, int64(11), got.Reads90d, "90-day reads must sum across surfaces")
	assert.Equal(t, int64(5), got.BySurface30d[resource.SurfaceMCPRead])
	assert.Equal(t, int64(2), got.BySurface30d[resource.SurfaceDownload])
	require.NotNil(t, got.LastReadAt)
	assert.True(t, got.LastReadAt.Equal(newer), "last read must be the most recent across surfaces")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestResourceUsage_SurfacelessRowsStillCount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	mock.ExpectQuery("SELECT parameters->>'resource_id'").
		WillReturnRows(sqlmock.NewRows([]string{"resource_id", "surface", "reads_short", "reads_long", "max"}).
			AddRow("r1", "", int64(3), int64(3), time.Now()))

	out, err := store.ResourceUsage(context.Background(), []string{"r1"})
	require.NoError(t, err)
	assert.Equal(t, int64(3), out["r1"].Reads30d)
	assert.Empty(t, out["r1"].BySurface30d, "an unlabeled row must not create an empty-named surface bucket")
}

func TestResourceUsage_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := New(db, Config{})
	mock.ExpectQuery("SELECT parameters->>'resource_id'").
		WillReturnError(errors.New("db down"))

	_, err = store.ResourceUsage(context.Background(), []string{"r1"})
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
