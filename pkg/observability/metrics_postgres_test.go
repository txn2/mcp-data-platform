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
