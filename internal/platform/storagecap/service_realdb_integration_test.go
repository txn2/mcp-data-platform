//go:build integration

package storagecap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
