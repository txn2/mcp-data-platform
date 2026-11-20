package sqltables

import "testing"

// Every Trino call reads its statement's tables, and every Trino metric and
// span is labeled by its kind and summary (#2716). The statements are the
// shapes the acceptance criteria send: a join with a CTE, an aggregate, a
// CTAS and an INSERT ... SELECT.
var benchStatements = []string{
	`WITH recent AS (SELECT customer_id, max(order_date) AS last_order FROM memory.sales.orders WHERE order_date > DATE '2026-01-01' GROUP BY customer_id)
	 SELECT c.name, c.region, r.last_order FROM memory.sales.customers c JOIN recent r ON r.customer_id = c.id WHERE c.region IN ('west', 'east') ORDER BY r.last_order DESC LIMIT 100`,
	`SELECT region, count(*) AS n, sum(amount) AS total FROM scratch.default.payments GROUP BY region HAVING sum(amount) > 1000`,
	`CREATE TABLE scratch.default.summary AS SELECT region, sum(amount) AS total FROM memory.sales.orders GROUP BY region`,
	`INSERT INTO scratch.default.events (id, kind, at) SELECT id, kind, at FROM memory.webhooks.raw_events WHERE kind = 'order.created'`,
}

func BenchmarkExtract(b *testing.B) {
	b.ReportAllocs()
	for i := range b.N {
		_ = Extract(benchStatements[i%len(benchStatements)])
	}
}

func BenchmarkStatementKindAndSummary(b *testing.B) {
	b.ReportAllocs()
	for i := range b.N {
		s := benchStatements[i%len(benchStatements)]
		_, _ = StatementKind(s), Summary(s)
	}
}
