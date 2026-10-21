//go:build integration

package storagecap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// TestPassRealDB runs a storage pass against a migrated database: the
// settings round-trip through platform_settings, the database and a bucket
// are read, a sample of each is kept, the history reads back, and a store
// over its threshold is recorded as alerting.
func TestPassRealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	settings := NewSettingsStore(db)
	limit, budgets := int64(1), map[string]int64{"portal": 1000}
	next, err := Input{DatabaseLimitBytes: &limit, Budgets: &budgets}.Apply(Defaults())
	require.NoError(t, err)
	require.NoError(t, settings.Set(ctx, next, "admin"))
	_, err = db.ExecContext(ctx,
		`INSERT INTO storage_usage (bucket, purpose, bytes, objects, updated_at) VALUES ('portal', 'portal_assets', 950, 3, NOW())`)
	require.NoError(t, err)

	alerts := levelalert.NewPostgresStore(db)
	svc := New(Deps{
		DB: db, Settings: settings, Spools: whstore.New(db).SpoolStates,
		Engine: levelalert.New(alerts, nil), Interval: time.Hour,
	})
	require.NoError(t, svc.Pass(ctx))

	r, err := svc.Report(ctx)
	require.NoError(t, err)
	stores := map[string]StoreView{}
	for _, s := range r.Stores {
		stores[s.Store] = s
	}
	require.Contains(t, stores, "database")
	assert.Positive(t, stores["database"].UsedBytes)
	assert.Equal(t, "critical", stores["database"].Level, "a database over a one-byte limit is critical")
	assert.Equal(t, int64(950), stores["bucket:portal"].UsedBytes)
	assert.Equal(t, "critical", stores["bucket:portal"].Level)
	assert.NotEmpty(t, r.Database.Tables)

	points, err := svc.History(ctx, "bucket:portal", 1)
	require.NoError(t, err)
	require.Len(t, points, 1)
	assert.Equal(t, int64(1000), *points[0].Total)

	recorded, err := alerts.List(ctx, AlertFamily)
	require.NoError(t, err)
	assert.Len(t, recorded, 2)

	budgetsRead, err := BudgetsFrom(settings)(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"portal": 1000}, budgetsRead)
}

// TestPassOncePerTickRealDB is #2203's fourth item: two replicas whose
// timers fire in the same tick take one sample between them, the second
// finding the first's; a tick an interval later samples again. The store's
// first sample is its birth, and its first day is left out of the fit.
func TestPassOncePerTickRealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	settings := NewSettingsStore(db)
	at := time.Date(2026, 10, 20, 3, 24, 7, 0, time.UTC)
	replica := func() *Service {
		return New(Deps{
			DB: db, Settings: settings, Engine: levelalert.New(levelalert.NewPostgresStore(db), nil),
			Interval: 15 * time.Minute, Now: func() time.Time { return at },
		})
	}
	a, b := replica(), replica()
	samples := func() int {
		var n int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM storage_samples WHERE store = 'database'`).Scan(&n))
		return n
	}

	require.NoError(t, a.Pass(ctx))
	at = at.Add(400 * time.Millisecond)
	require.ErrorIs(t, b.Pass(ctx), bgloop.ErrSkipped, "the second replica in one tick")
	assert.Equal(t, 1, samples())

	at = at.Add(15 * time.Minute)
	require.NoError(t, b.Pass(ctx))
	assert.Equal(t, 2, samples(), "the next tick samples again")

	r, err := b.Report(ctx)
	require.NoError(t, err)
	for _, s := range r.Stores {
		if s.Store == "database" {
			assert.Nil(t, s.Forecast.Rate7d)
			assert.Equal(t, 48.0, s.Forecast.Rate7dMinHistoryHours, "a store born a quarter hour ago waits out its first day")
		}
	}
}
