// Package dbobs opens the platform's PostgreSQL pool through a driver wrapper
// that observes every statement (#1896): a client span per statement under the
// caller's span, a db_client_operation_duration_seconds{operation}
// observation, and a db_client_slow_statements_total count and WARN for a
// statement at or over the slow threshold.
//
// The operation is the statement's leading keyword (select, insert, update,
// ...), vector_search for a statement that ranks by a pgvector distance, and
// the transaction or connection step otherwise (begin, commit, rollback,
// prepare, connect, ping). A vector search is its own operation and its own
// span name, so what vector ranking costs is separable from ordinary queries
// without touching the call sites that rank.
//
// The SQL text is not on the span unless OTEL_TRACES_INCLUDE_DB_STATEMENT is
// set: a statement can quote the values it was built with, and a trace backend
// is outside the platform. A statement made outside any traced request (a
// background sweep) opens no span, so the trace backend does not fill with
// root spans nobody can place.
package dbobs

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"

	"github.com/XSAM/otelsql"
	// The PostgreSQL driver the pool is opened with, registered as "postgres".
	_ "github.com/lib/pq"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/internal/sqltables"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// driverName is the database/sql driver the pool is opened with (lib/pq).
const driverName = "postgres"

// OpVectorSearch is the operation of a statement that ranks by a pgvector
// distance operator.
const OpVectorSearch = "vector_search"

// spanPrefix names every statement span: postgres.select, postgres.vector_search.
const spanPrefix = "postgres."

// Span attribute keys from the OpenTelemetry database conventions.
const (
	attrDBSystem    = "db.system.name"
	attrDBOperation = "db.operation.name"
	attrDBSummary   = "db.query.summary"
	dbSystem        = "postgresql"
)

// vectorOperators are pgvector's distance operators: cosine, L2, inner
// product, L1. A statement holding one ranks by a vector.
//
//nolint:gochecknoglobals // a read-only lookup list.
var vectorOperators = []string{"<=>", "<->", "<#>", "<+>"}

// Open opens the pool on dsn with every statement observed, recording through
// m (nil records nothing) and reading the slow threshold and the statement
// opt-in from the environment (observability.DBConfigFromEnv).
func Open(dsn string, m *observability.Metrics) (*sql.DB, error) {
	db, err := otelsql.Open(driverName, dsn, Options(m, observability.DBConfigFromEnv())...)
	if err != nil {
		return nil, fmt.Errorf("postgres driver: %w", err)
	}
	return db, nil
}

// Options are the driver wrapper's options for m and cfg.
func Options(m *observability.Metrics, cfg observability.DBConfig) []otelsql.Option {
	return []otelsql.Option{
		otelsql.WithMeterProvider(m.DBMeterProvider(cfg.SlowThreshold)),
		otelsql.WithAttributes(attribute.String(attrDBSystem, dbSystem)),
		otelsql.WithSpanNameFormatter(func(_ context.Context, method otelsql.Method, query string) string {
			return spanPrefix + Operation(method, query)
		}),
		otelsql.WithAttributesGetter(spanAttributes),
		otelsql.WithInstrumentAttributesGetter(func(_ context.Context, method otelsql.Method, query string, _ []driver.NamedValue) []attribute.KeyValue {
			return observability.DBOperationAttributes(Operation(method, query))
		}),
		otelsql.WithSpanOptions(otelsql.SpanOptions{
			DisableQuery:         !cfg.IncludeStatement,
			DisableErrSkip:       true,
			OmitConnResetSession: true,
			OmitRows:             true,
			SpanFilter: func(ctx context.Context, _ otelsql.Method, _ string, _ []driver.NamedValue) bool {
				return trace.SpanContextFromContext(ctx).IsValid()
			},
		}),
	}
}

// spanAttributes are a statement span's attributes beyond the system: its
// operation and, for a statement, the summary of the tables it reads.
func spanAttributes(_ context.Context, method otelsql.Method, query string, _ []driver.NamedValue) []attribute.KeyValue {
	attrs := []attribute.KeyValue{attribute.String(attrDBOperation, strings.ToUpper(Operation(method, query)))}
	if query != "" {
		attrs = append(attrs, attribute.String(attrDBSummary, sqltables.Summary(query)))
	}
	return attrs
}

// Operation is the bounded operation a driver call is measured under.
func Operation(method otelsql.Method, query string) string {
	switch method {
	case otelsql.MethodConnQuery, otelsql.MethodConnExec, otelsql.MethodStmtQuery, otelsql.MethodStmtExec:
		if isVectorSearch(query) {
			return OpVectorSearch
		}
		return sqltables.StatementKind(query)
	case otelsql.MethodConnPrepare:
		return "prepare"
	case otelsql.MethodConnBeginTx:
		return "begin"
	case otelsql.MethodTxCommit:
		return "commit"
	case otelsql.MethodTxRollback:
		return "rollback"
	case otelsql.MethodConnPing:
		return "ping"
	case otelsql.MethodConnectorConnect:
		return "connect"
	default:
		return sqltables.KindOther
	}
}

// isVectorSearch reports whether a statement ranks by a pgvector distance.
func isVectorSearch(query string) bool {
	for _, op := range vectorOperators {
		if strings.Contains(query, op) {
			return true
		}
	}
	return false
}
