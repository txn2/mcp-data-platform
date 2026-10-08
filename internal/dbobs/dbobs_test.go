package dbobs

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/XSAM/otelsql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// secret is a literal a statement is built with; it must reach no span or
// series unless the statement opt-in is on.
const secret = "tok-8f2a"

// rigSeq numbers the rigs a process opens.
var rigSeq atomic.Int64

type rig struct {
	db      *sql.DB
	mock    sqlmock.Sqlmock
	metrics *observability.Metrics
	spans   *tracetest.SpanRecorder
	tracer  *observability.Tracer
}

func newRig(t *testing.T, cfg observability.DBConfig) *rig {
	t.Helper()
	// sqlmock refuses a DSN it has already registered in this process, so a
	// repeated run (-count, the schedule lane) needs one of its own.
	dsn := fmt.Sprintf("dbobs_%s_%d", strings.ReplaceAll(t.Name(), "/", "_"), rigSeq.Add(1))
	_, mock, err := sqlmock.NewWithDSN(dsn, sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	m, err := observability.New(observability.Config{Enabled: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	sr := tracetest.NewSpanRecorder()
	tr := observability.NewTracerFromProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)), observability.TracingConfig{Enabled: true})
	db, err := otelsql.Open("sqlmock", dsn, Options(m, cfg)...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &rig{db: db, mock: mock, metrics: m, spans: sr, tracer: tr}
}

func (r *rig) scrape(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(r.metrics.Handler())
	defer srv.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

// query runs one statement and drains its rows.
func query(ctx context.Context, t *testing.T, db *sql.DB, stmt string, args ...any) {
	t.Helper()
	rows, err := db.QueryContext(ctx, stmt, args...)
	require.NoError(t, err)
	defer rows.Close() //nolint:errcheck // drained below
	for rows.Next() {
	}
	require.NoError(t, rows.Err())
}

func attrs(s sdktrace.ReadOnlySpan) map[string]string {
	out := map[string]string{}
	for _, kv := range s.Attributes() {
		out[string(kv.Key)] = kv.Value.String()
	}
	return out
}

func (r *rig) span(t *testing.T, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, s := range r.spans.Ended() {
		if s.Name() == name {
			return s
		}
	}
	t.Fatalf("no span %q among %d", name, len(r.spans.Ended()))
	return nil
}

// A statement inside a traced request is a postgres.<operation> span under
// the caller's span, named by its leading keyword, with the tables it reads
// and without its text; a pgvector ranking is its own vector_search span; the
// statement histogram carries the operation and nothing else.
func TestStatementsAreObserved(t *testing.T) {
	r := newRig(t, observability.DBConfig{SlowThreshold: time.Hour})
	r.mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM memory_records WHERE token = $1")).
		WithArgs(secret).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("1"))
	r.mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM assets ORDER BY embedding <=> $1 LIMIT 5")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	r.mock.ExpectBegin()
	r.mock.ExpectExec("UPDATE assets").WillReturnResult(sqlmock.NewResult(0, 1))
	r.mock.ExpectCommit()

	ctx, root := r.tracer.Start(context.Background(), "tool_call")
	query(ctx, t, r.db, "SELECT id FROM memory_records WHERE token = $1", secret)
	query(ctx, t, r.db, "SELECT id FROM assets ORDER BY embedding <=> $1 LIMIT 5", "[0.1]")
	tx, err := r.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "UPDATE assets SET name = 'x'")
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	root.End()
	require.NoError(t, r.mock.ExpectationsWereMet())

	sel := r.span(t, "postgres.select")
	assert.Equal(t, root.SpanContext().SpanID(), sel.Parent().SpanID())
	a := attrs(sel)
	assert.Equal(t, "postgresql", a[attrDBSystem])
	assert.Equal(t, "SELECT", a[attrDBOperation])
	assert.Equal(t, "SELECT memory_records", a[attrDBSummary])
	assert.NotContains(t, a, "db.query.text")
	for _, s := range r.spans.Ended() {
		for k, v := range attrs(s) {
			assert.NotContains(t, v, secret, "span %s attribute %s", s.Name(), k)
		}
	}
	assert.Equal(t, "VECTOR_SEARCH", attrs(r.span(t, "postgres.vector_search"))[attrDBOperation])
	r.span(t, "postgres.update")
	r.span(t, "postgres.begin")
	r.span(t, "postgres.commit")

	body := r.scrape(t)
	for _, op := range []string{"select", OpVectorSearch, "update", "begin", "commit"} {
		assert.Contains(t, body, `db_client_operation_duration_seconds_count{operation="`+op+`"}`)
	}
	assert.NotContains(t, body, "db_operation_name")
	assert.NotContains(t, body, "db_client_slow_statements_total{")
}

// A statement made outside any traced request opens no span: a background
// sweep does not mint root spans. It is still measured.
func TestUntracedStatementOpensNoSpan(t *testing.T) {
	r := newRig(t, observability.DBConfig{})
	r.mock.ExpectExec("DELETE FROM sessions").WillReturnResult(sqlmock.NewResult(0, 3))
	_, err := r.db.ExecContext(context.Background(), "DELETE FROM sessions WHERE expires_at < now()")
	require.NoError(t, err)
	assert.Empty(t, r.spans.Ended())
	assert.Contains(t, r.scrape(t), `db_client_operation_duration_seconds_count{operation="delete"} 1`)
}

// With the opt-in on, the span carries the statement's text.
func TestStatementTextIsOptIn(t *testing.T) {
	r := newRig(t, observability.DBConfig{IncludeStatement: true})
	r.mock.ExpectExec("UPDATE t").WillReturnResult(sqlmock.NewResult(0, 1))
	ctx, root := r.tracer.Start(context.Background(), "tool_call")
	_, err := r.db.ExecContext(ctx, "UPDATE t SET a = 1")
	require.NoError(t, err)
	root.End()
	assert.Equal(t, "UPDATE t SET a = 1", attrs(r.span(t, "postgres.update"))["db.query.text"])
}

// A statement at or over the slow threshold is counted by its operation.
func TestSlowStatementIsCounted(t *testing.T) {
	r := newRig(t, observability.DBConfig{SlowThreshold: time.Nanosecond})
	r.mock.ExpectExec("INSERT INTO t").WillDelayFor(time.Millisecond).WillReturnResult(sqlmock.NewResult(1, 1))
	_, err := r.db.ExecContext(context.Background(), "INSERT INTO t VALUES (1)")
	require.NoError(t, err)
	assert.Contains(t, r.scrape(t), `db_client_slow_statements_total{operation="insert"} 1`)
}

func TestOperation(t *testing.T) {
	cases := []struct {
		method otelsql.Method
		query  string
		want   string
	}{
		{otelsql.MethodConnQuery, "select 1", "select"},
		{otelsql.MethodStmtExec, "INSERT INTO t VALUES ($1)", "insert"},
		{otelsql.MethodConnExec, "UPDATE t SET v = v <-> $1", OpVectorSearch},
		{otelsql.MethodStmtQuery, "SELECT 1 ORDER BY e <#> $1", OpVectorSearch},
		{otelsql.MethodConnPrepare, "SELECT 1", "prepare"},
		{otelsql.MethodConnBeginTx, "", "begin"},
		{otelsql.MethodTxCommit, "", "commit"},
		{otelsql.MethodTxRollback, "", "rollback"},
		{otelsql.MethodConnPing, "", "ping"},
		{otelsql.MethodConnectorConnect, "", "connect"},
		{otelsql.MethodRows, "", "other"},
		{otelsql.MethodConnQuery, "FROBNICATE", "other"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, Operation(c.method, c.query), "%s %q", c.method, c.query)
	}
}

// Open builds a pool on the PostgreSQL driver without connecting.
func TestOpen(t *testing.T) {
	db, err := Open("postgres://u:p@127.0.0.1:1/x?sslmode=disable", nil)
	require.NoError(t, err)
	require.NoError(t, db.Close())
}
