package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Postgres connection-kind instruments (#2400): every statement the postgres
// toolkit sends to a database an operator connected, which is not the
// platform's own (that is db_client_operation_duration_seconds). Exposed names:
//
//   - postgres_statements_total{connection, operation, status}  status is ok,
//     canceled, error, or the failure's SQLSTATE class (class_23 integrity,
//     class_42 syntax or access), never the error text
//   - postgres_statement_duration_seconds{connection, operation}
//   - postgres_slow_statements_total{connection, operation}  those that took
//     MCP_PLATFORM_DB_SLOW_STATEMENT_THRESHOLD (default 1s) or longer
//
// Cardinality: connection is an operator-configured name, operation is
// internal/sqltables' closed statement-kind set, status a closed set of
// SQLSTATE classes.
const (
	instPostgresStatements     = "postgres_statements"
	instPostgresStatementDur   = "postgres_statement_duration"
	instPostgresSlowStatements = "postgres_slow_statements"
)

type postgresInstruments struct {
	statements metric.Int64Counter
	duration   metric.Float64Histogram
	slow       metric.Int64Counter
}

func (m *Metrics) registerPostgresInstruments(meter metric.Meter) error {
	p := &m.pg
	var err error
	p.statements, err = meter.Int64Counter(instPostgresStatements,
		metric.WithDescription("Total statements the postgres connection kind sent, labeled by connection, operation (the statement's leading keyword or the transaction step) and status (ok, canceled, error, or the failure's SQLSTATE class as class_23)."))
	if err != nil {
		return wrapReg(instPostgresStatements, err)
	}
	p.slow, err = meter.Int64Counter(instPostgresSlowStatements,
		metric.WithDescription("Total postgres connection-kind statements that took the slow threshold (MCP_PLATFORM_DB_SLOW_STATEMENT_THRESHOLD, default 1s) or longer, labeled by connection and operation."))
	if err != nil {
		return wrapReg(instPostgresSlowStatements, err)
	}
	p.duration, err = meter.Float64Histogram(instPostgresStatementDur,
		metric.WithDescription("Seconds a postgres connection-kind statement took, labeled by connection and operation."),
		metric.WithUnit(unitSeconds), metric.WithExplicitBucketBoundaries(dbStatementDurationBuckets...))
	return wrapReg(instPostgresStatementDur, err)
}

// PostgresStatement is one statement a postgres connection ran.
type PostgresStatement struct {
	Connection, Operation string
	// Status is StatusOK, canceled, error, or class_ and the failure's
	// two-character SQLSTATE class.
	Status   string
	Duration time.Duration
	// Slow is whether Duration reached the slow threshold.
	Slow bool
}

// RecordPostgresStatement records one statement. Nil-safe.
func (m *Metrics) RecordPostgresStatement(ctx context.Context, s PostgresStatement) {
	if m == nil || m.pg.statements == nil {
		return
	}
	conn := attribute.String(attrConnection, s.Connection)
	op := attribute.String(attrOperation, s.Operation)
	m.pg.statements.Add(ctx, 1, metric.WithAttributes(conn, op, attribute.String(attrStatus, s.Status)))
	m.pg.duration.Record(ctx, s.Duration.Seconds(), metric.WithAttributes(conn, op))
	if s.Slow {
		m.pg.slow.Add(ctx, 1, metric.WithAttributes(conn, op))
	}
}
