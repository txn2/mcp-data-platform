package mapwire

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	svc := assemble(db, nil, "", "managed-resources", nil, nil)
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
	assert.Nil(t, m.SiteCheck(), "no surface, no site check")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/portal/maps/regions", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// A save through the assembled service drops the allowed sites the gate read,
// so the replica that saved them uses them on its next request (#2393).
func TestSiteCheck_SaveIsSeenAtOnce(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	gate := maps.NewSiteGate(maps.NewReader(db).Settings, "")
	m := &Maps{gate: gate, svc: assemble(db, nil, "", "managed-resources", nil, gate.Forget)}
	check := m.SiteCheck()
	req := func() *http.Request {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/portal/maps/us.pmtiles", http.NoBody)
		r.Header.Set("Origin", "https://stores.example.com")
		return r
	}

	mock.ExpectQuery("SELECT value, updated_by, updated_at FROM platform_settings").WillReturnError(sql.ErrNoRows)
	assert.False(t, check(req()), "an unlisted site is refused")

	mock.ExpectExec("INSERT INTO platform_settings").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, m.svc.SaveSettings(context.Background(),
		maps.SettingsInput{MaxZoom: 3, AllowedOrigins: []string{"https://stores.example.com"}}, "a"))
	mock.ExpectQuery("SELECT value, updated_by, updated_at FROM platform_settings").WillReturnRows(
		sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).
			AddRow([]byte(`{"max_zoom":3,"allowed_origins":["https://stores.example.com"]}`), "a", time.Now()))
	assert.True(t, check(req()), "the saved site is served on the next request")
	require.NoError(t, mock.ExpectationsWereMet())
}
