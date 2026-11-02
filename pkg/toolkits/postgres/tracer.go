package postgres

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/internal/sqltables"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// Span attribute keys from the OpenTelemetry database conventions, the ones
// internal/dbobs puts on the platform's own statements.
const (
	attrDBSystem     = "db.system.name"
	attrDBOperation  = "db.operation.name"
	attrDBSummary    = "db.query.summary"
	attrDBNamespace  = "db.namespace"
	attrDBQueryText  = "db.query.text"
	attrDBStatusCode = "db.response.status_code"
	attrConnection   = "mcp.connection"
	dbSystem         = "postgresql"
	spanPrefix       = "postgres."
	tracerName       = "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// statementTracer reports every statement a connection's pool runs: a span
// named postgres.<operation> inside a traced call (none outside one), and one
// observation of the postgres_statement series.
//
// A span carries the SQL text only when OTEL_TRACES_INCLUDE_DB_STATEMENT is
// set, and never a bound parameter's value or a failure's message: a failed
// statement's span says its SQLSTATE and nothing a PostgreSQL error repeats
// from the data.
type statementTracer struct {
	connection string
	database   string
	metrics    *atomic.Pointer[observability.Metrics]
	cfg        observability.DBConfig
}

type traceKey struct{}

type traceState struct {
	start     time.Time
	operation string
	span      trace.Span
}

// TraceQueryStart implements pgx.QueryTracer.
func (t *statementTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	st := &traceState{start: time.Now(), operation: sqltables.StatementKind(data.SQL)}
	if trace.SpanContextFromContext(ctx).IsValid() {
		attrs := []attribute.KeyValue{
			attribute.String(attrDBSystem, dbSystem),
			attribute.String(attrDBOperation, strings.ToUpper(st.operation)),
			attribute.String(attrDBSummary, sqltables.Summary(data.SQL)),
			attribute.String(attrDBNamespace, t.database),
			attribute.String(attrConnection, t.connection),
		}
		if t.cfg.IncludeStatement {
			attrs = append(attrs, attribute.String(attrDBQueryText, data.SQL))
		}
		ctx, st.span = otel.Tracer(tracerName).Start(ctx, spanPrefix+st.operation,
			trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
	}
	return context.WithValue(ctx, traceKey{}, st)
}

// TraceQueryEnd implements pgx.QueryTracer.
func (t *statementTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	st, ok := ctx.Value(traceKey{}).(*traceState)
	if !ok {
		return
	}
	elapsed := time.Since(st.start)
	status := statusOf(data.Err)
	if st.span != nil {
		if data.Err != nil {
			st.span.SetAttributes(attribute.String(attrDBStatusCode, sqlState(data.Err)))
			st.span.SetStatus(codes.Error, "statement failed: "+status)
		}
		st.span.End()
	}
	t.metrics.Load().RecordPostgresStatement(ctx, observability.PostgresStatement{
		Connection: t.connection,
		Operation:  st.operation,
		Status:     status,
		Duration:   elapsed,
		Slow:       elapsed >= t.cfg.SlowThreshold,
	})
}

// statusOf is a statement's bounded status: ok, the SQLSTATE class of a
// PostgreSQL error ("class_23"), canceled for a context that ended, or error.
func statusOf(err error) string {
	switch {
	case err == nil:
		return observability.StatusOK
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	}
	if code := sqlState(err); len(code) == sqlStateLen {
		return "class_" + code[:sqlStateClassLen]
	}
	return "error"
}

// A SQLSTATE is five characters, the first two its class.
const (
	sqlStateLen      = 5
	sqlStateClassLen = 2
)

// sqlState is a PostgreSQL error's SQLSTATE, or "" for any other error.
func sqlState(err error) string {
	if pg, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pg.Code
	}
	return ""
}
