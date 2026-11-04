package observability

import (
	"context"
	"strings"
	"testing"
)

func TestAppDBHealthGauges(t *testing.T) {
	m := newDepsMetrics(t)
	var nilMetrics *Metrics
	nilMetrics.RegisterAppDBHealth(nil)
	if body := scrapeMetrics(t, m.Handler()); strings.Contains(body, "appdb_health_") {
		t.Error("health series without a sampler")
	}
	m.RegisterAppDBHealth(func(context.Context) []AppDBHealthSample {
		return []AppDBHealthSample{{
			Connection: "orders", SizeBytes: 4096, TransactionIDAge: 1800, Connections: 7, MaxConnections: 100,
			IdleInTransaction: 1, LongRunning: 2, Blocked: 3, InvalidIndexes: 1, UnusedIndexes: 4, DriftObjects: -1,
			DeadTupleRatio: 0.25, CacheHitRatio: 0.99,
		}}
	})
	body := scrapeMetrics(t, m.Handler())
	for _, want := range []string{
		`appdb_health_size_bytes{connection="orders"} 4096`,
		`appdb_health_transaction_id_age{connection="orders"} 1800`,
		`appdb_health_connections{connection="orders"} 7`,
		`appdb_health_max_connections{connection="orders"} 100`,
		`appdb_health_idle_in_transaction_sessions{connection="orders"} 1`,
		`appdb_health_long_running_sessions{connection="orders"} 2`,
		`appdb_health_blocked_sessions{connection="orders"} 3`,
		`appdb_health_invalid_indexes{connection="orders"} 1`,
		`appdb_health_unused_indexes{connection="orders"} 4`,
		`appdb_health_schema_drift_objects{connection="orders"} -1`,
		`appdb_health_dead_tuple_ratio{connection="orders"} 0.25`,
		`appdb_health_cache_hit_ratio{connection="orders"} 0.99`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %s", want)
		}
	}
}
