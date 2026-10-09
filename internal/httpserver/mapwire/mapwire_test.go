package mapwire

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/maps"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
)

func TestBuild_NeedsADatabase(t *testing.T) {
	assert.Nil(t, Build(nil))
}

func TestBucketCache(t *testing.T) {
	ctx := context.Background()
	b := &bucketCache{defaultBucket: "managed-resources", clients: map[string]*s3adapter.ClientAdapter{}}

	_, err := b.open(ctx, maps.Settings{})
	require.ErrorContains(t, err, "no S3 connection is configured", "no toolkit, no client")

	_, err = b.open(ctx, maps.Settings{S3Connection: "elsewhere"})
	require.ErrorContains(t, err, `opening S3 connection "elsewhere"`)

	_, err = (&bucketCache{}).open(ctx, maps.Settings{})
	require.ErrorContains(t, err, "no bucket is named")

	cached := s3adapter.NewFor(nil, "maps")
	b.clients["primary"] = cached
	got, err := b.open(ctx, maps.Settings{S3Connection: "primary", Bucket: "maps"})
	require.NoError(t, err)
	assert.Same(t, cached, got.Objects)
	assert.Equal(t, "maps", got.Name)
	got, err = b.open(ctx, maps.Settings{S3Connection: "primary"})
	require.NoError(t, err)
	assert.Equal(t, "managed-resources", got.Name, "an empty bucket is the managed-resources one")
}

// The assembled service reads the settings section and refreshes the maps
// page through the store it was given; a store without the reconcile
// capability is a clean no-op.
func TestAssemble(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	svc := assemble(db, nil, "", "managed-resources", nil)
	require.NotNil(t, svc)

	mock.ExpectQuery("SELECT value, updated_by, updated_at FROM platform_settings").WillReturnError(sql.ErrNoRows)
	st, err := svc.State(context.Background())
	require.NoError(t, err)
	assert.False(t, st.Enabled)

	mock.ExpectExec("INSERT INTO platform_settings").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, svc.SaveSettings(context.Background(), maps.SettingsInput{MaxZoom: 3}, "a"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBucketOf(t *testing.T) {
	f := bucketOrDefault("managed-resources")
	assert.Equal(t, "managed-resources", f(maps.Settings{}))
	assert.Equal(t, "maps", f(maps.Settings{Bucket: "maps"}))
}

// A deployment with no database has no maps surface, and its composition
// root calls the surface's methods all the same.
func TestNilMapsMountsAndRunsNothing(t *testing.T) {
	var m *Maps
	mux := http.NewServeMux()
	m.Mount(mux, func() func(http.Handler) http.Handler { t.Fatal("no admin routes without a surface"); return nil }, nil)
	m.Start(context.Background())
	m.Stop()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/portal/maps/regions", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
