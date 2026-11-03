package observability

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRecordPostgresStatement(t *testing.T) {
	m := newDepsMetrics(t)
	ctx := context.Background()
	m.RecordPostgresStatement(ctx, PostgresStatement{Connection: "orders", Operation: "select", Status: StatusOK, Duration: time.Millisecond})
	m.RecordPostgresStatement(ctx, PostgresStatement{Connection: "orders", Operation: "insert", Status: "class_23", Duration: 2 * time.Second, Slow: true})
	(*Metrics)(nil).RecordPostgresStatement(ctx, PostgresStatement{Connection: "orders"})

	body := scrapeMetrics(t, m.Handler())
	for _, want := range []string{
		`postgres_statements_total{connection="orders",operation="select",status="ok"} 1`,
		`postgres_statements_total{connection="orders",operation="insert",status="class_23"} 1`,
		`postgres_statement_duration_seconds_count{connection="orders",operation="insert"} 1`,
		`postgres_slow_statements_total{connection="orders",operation="insert"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %s", want)
		}
	}
	if strings.Contains(body, `postgres_slow_statements_total{connection="orders",operation="select"}`) {
		t.Error("a fast statement was counted as slow")
	}
}

func TestAppDBInstruments(t *testing.T) {
	m := newDepsMetrics(t)
	ctx := context.Background()
	m.RecordAppDBStatementCache(ctx, "orders", true)
	m.RecordAppDBStatementCache(ctx, "orders", true)
	m.RecordAppDBStatementCache(ctx, "orders", false)
	m.RecordAppDBTransaction(ctx, "orders", "committed")
	m.RecordAppDBTransaction(ctx, "orders", "retried")
	m.RecordAppDBPoolWaitTimeout(ctx, "orders")
	var nilMetrics *Metrics
	nilMetrics.RecordAppDBStatementCache(ctx, "orders", true)
	nilMetrics.RecordAppDBTransaction(ctx, "orders", "committed")
	nilMetrics.RecordAppDBPoolWaitTimeout(ctx, "orders")
	nilMetrics.RegisterAppDBPools(nil)

	body := scrapeMetrics(t, m.Handler())
	if strings.Contains(body, "appdb_pool_connections") {
		t.Error("pool series without a sampler")
	}
	m.RegisterAppDBPools(func() []AppDBPoolSample {
		return []AppDBPoolSample{{Connection: "orders", Acquired: 2, Idle: 3, Max: 5, Acquires: 10, Waited: 4, WaitSecondsInTotal: 0.5}}
	})
	body = scrapeMetrics(t, m.Handler())
	for _, want := range []string{
		`appdb_statement_cache_total{connection="orders",result="hit"} 2`,
		`appdb_statement_cache_total{connection="orders",result="miss"} 1`,
		`appdb_transactions_total{connection="orders",outcome="committed"} 1`,
		`appdb_transactions_total{connection="orders",outcome="retried"} 1`,
		`appdb_pool_wait_timeouts_total{connection="orders"} 1`,
		`appdb_pool_connections{connection="orders",state="acquired"} 2`,
		`appdb_pool_connections{connection="orders",state="max"} 5`,
		`appdb_pool_acquires_total{connection="orders",wait="none"} 6`,
		`appdb_pool_acquires_total{connection="orders",wait="waited"} 4`,
		`appdb_pool_wait_seconds_total{connection="orders"} 0.5`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %s", want)
		}
	}
}
