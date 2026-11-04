//go:build integration

package appdbwatch

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
	"github.com/txn2/mcp-data-platform/internal/platform/migrationstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// sent records every alert the engine sends.
type sent struct {
	mu   sync.Mutex
	keys []string
	docs []string
}

func (s *sent) Send(_ context.Context, _ []string, key string, p notification.Payload) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := json.Marshal(p)
	s.keys, s.docs = append(s.keys, key), append(s.docs, string(raw))
	return 1
}

func (s *sent) all() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.docs, "\n")
}

// canary is a literal a session's statement carries, which must reach no
// alert and no stored sample.
const canary = "canary-2409-3f9c1e"

// TestWatch_RealDB is the loop end to end against PostgreSQL (#2406, #2409):
// a pass samples the database and keeps the sample; a session idle in a
// transaction raises its alert at its level; a column added by hand raises
// the drift alert naming it; both resolve when the session ends and the
// column is dropped. The idle session's statement text reaches neither an
// alert nor the history.
func TestWatch_RealDB(t *testing.T) {
	testdb.OnAppDBTargets(t, func(t *testing.T) {
		db := testdb.AppDBTarget(t)
		multi, err := postgres.ParseMultiConfig("shop", map[string]map[string]any{"shop": db.Target})
		require.NoError(t, err)
		tk := postgres.NewMulti(multi)
		tk.SetConnectGuard(postgres.OwnDatabaseGuard(db.Platform))
		store := migrationstore.New(db.Platform)
		tk.SetMigrationStore(store)
		t.Cleanup(func() { _ = tk.Close() })
		ctx := context.Background()

		up := "CREATE TABLE orders (id bigint PRIMARY KEY, total numeric(10,2) NOT NULL)"
		m := postgres.Migration{
			Connection: "shop", Schema: "public", Version: 1, Name: "orders", Up: up,
			Transactional: true, Checksum: postgres.ChecksumOf(up, "", true),
		}
		_, err = store.Put(ctx, m)
		require.NoError(t, err)
		_, err = tk.Apply(ctx, postgres.ApplyOptions{Connection: "shop", Schema: "public", Actor: "t"}, []postgres.Migration{m})
		require.NoError(t, err)

		settingsStore := NewSettingsStore(db.Platform)
		s := Defaults()
		s.IdleInTransactionSeconds = levelalert.Thresholds{Warning: 0.001}
		s.Channels = []string{"ops"}
		require.NoError(t, settingsStore.Set(ctx, s, "t"))

		clock := time.Now().UTC()
		alerts := &sent{}
		svc := New(Deps{
			DB: db.Platform, Settings: settingsStore, Migrations: store,
			Engine: levelalert.New(levelalert.NewPostgresStore(db.Platform), alerts),
			Kit:    func() Kit { return tk },
			Now:    func() time.Time { return clock },
		})
		pass := func() {
			t.Helper()
			clock = clock.Add(time.Duration(s.IntervalSeconds) * time.Second)
			require.NoError(t, svc.Pass(ctx))
		}
		conditions := func() map[string]string {
			recs, err := levelalert.NewPostgresStore(db.Platform).List(ctx, AlertFamily)
			require.NoError(t, err)
			out := map[string]string{}
			for _, r := range recs {
				out[r.Key] = string(r.Level)
			}
			return out
		}

		target := db.TargetDB(t)
		idle, err := target.Begin()
		require.NoError(t, err)
		_, err = idle.Exec(`SELECT '` + canary + `'`)
		require.NoError(t, err)
		_, err = target.Exec(`ALTER TABLE orders ADD COLUMN coupon text`)
		require.NoError(t, err)

		pass()
		got := conditions()
		assert.Equal(t, "warning", got["appdb:shop:idle_in_transaction"])
		assert.Equal(t, "warning", got["appdb:shop:drift:public"])
		assert.Contains(t, alerts.all(), "column public.orders.coupon added")

		points, err := svc.History(ctx, "shop", 1)
		require.NoError(t, err)
		require.Len(t, points, 1)
		assert.True(t, points[0].Sample.Reachable)
		assert.Equal(t, 1, points[0].Sample.DriftObjects)
		assert.GreaterOrEqual(t, points[0].Sample.IdleInTransaction, 1)
		require.NotEmpty(t, svc.Gauges(ctx))

		require.ErrorIs(t, svc.Pass(ctx), bgloop.ErrSkipped, "a pass within the interval is skipped")
		points, err = svc.History(ctx, "shop", 1)
		require.NoError(t, err)
		assert.Len(t, points, 1)

		require.NoError(t, idle.Rollback())
		_, err = target.Exec(`ALTER TABLE orders DROP COLUMN coupon`)
		require.NoError(t, err)
		pass()
		assert.Empty(t, conditions(), "both conditions resolve")
		assert.Contains(t, alerts.all(), "Resolved: Application database shop")

		var history string
		require.NoError(t, db.Platform.QueryRow(`SELECT string_agg(summary::text, ' ') FROM appdb_health_samples`).Scan(&history))
		for _, sink := range map[string]string{"alerts": alerts.all(), "history": history} {
			assert.NotContains(t, sink, canary)
		}
	})
}
