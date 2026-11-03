package observability

import (
	"context"
	"fmt"
	"sync"
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

// Application database instruments (#2402), beside the statement series:
//
//   - appdb_statement_cache_total{connection, result}  result: hit (the
//     statement was already prepared on the physical connection) or miss
//   - appdb_transactions_total{connection, outcome}    outcome: committed,
//     rolled_back, retried, idle_timeout
//   - appdb_pool_wait_timeouts_total{connection}       statements that found
//     no free pooled connection within pool_wait_timeout
//
// And, read from each pool on every scrape (AppDBPoolSampler), per replica:
//
//   - appdb_pool_connections{connection, state}        state: acquired, idle,
//     max
//   - appdb_pool_acquires_total{connection, wait}      wait: none (a free
//     connection was there) or waited
//   - appdb_pool_wait_seconds_total{connection}        time spent waiting
//
// Cardinality: connection is an operator-configured name; result, outcome,
// state and wait are closed sets.
const (
	instAppDBStmtCache    = "appdb_statement_cache"
	instAppDBTransactions = "appdb_transactions"
	instAppDBWaitTimeouts = "appdb_pool_wait_timeouts"
	instAppDBPoolConns    = "appdb_pool_connections"
	instAppDBPoolAcquires = "appdb_pool_acquires"
	instAppDBPoolWait     = "appdb_pool_wait_seconds"
)

// AppDBPoolSample is one pool's state as a scrape reads it.
type AppDBPoolSample struct {
	Connection         string
	Acquired, Idle     int64
	Max                int64
	Acquires, Waited   int64
	WaitSecondsInTotal float64
}

type appdbInstruments struct {
	cache        metric.Int64Counter
	transactions metric.Int64Counter
	waitTimeouts metric.Int64Counter
	conns        metric.Int64ObservableGauge
	acquires     metric.Int64ObservableCounter
	waitSeconds  metric.Float64ObservableCounter

	mu      sync.RWMutex
	sampler func() []AppDBPoolSample
}

func (m *Metrics) registerAppDBInstruments(meter metric.Meter) error {
	a := &m.appdb
	var err error
	if a.cache, err = meter.Int64Counter(instAppDBStmtCache,
		metric.WithDescription("Statements an application database ran, labeled by connection and result: hit (already prepared on the physical connection) or miss.")); err != nil {
		return wrapReg(instAppDBStmtCache, err)
	}
	if a.transactions, err = meter.Int64Counter(instAppDBTransactions,
		metric.WithDescription("Script transactions on an application database, labeled by connection and outcome (committed, rolled_back, retried, idle_timeout).")); err != nil {
		return wrapReg(instAppDBTransactions, err)
	}
	if a.waitTimeouts, err = meter.Int64Counter(instAppDBWaitTimeouts,
		metric.WithDescription("Statements that found no free pooled connection within the connection's pool_wait_timeout, labeled by connection.")); err != nil {
		return wrapReg(instAppDBWaitTimeouts, err)
	}
	if a.conns, err = meter.Int64ObservableGauge(instAppDBPoolConns,
		metric.WithDescription("Connections in an application database's pool on this replica, labeled by connection and state (acquired, idle, max).")); err != nil {
		return wrapReg(instAppDBPoolConns, err)
	}
	if a.acquires, err = meter.Int64ObservableCounter(instAppDBPoolAcquires,
		metric.WithDescription("Connections taken from an application database's pool on this replica, labeled by connection and wait: none (one was free) or waited.")); err != nil {
		return wrapReg(instAppDBPoolAcquires, err)
	}
	if a.waitSeconds, err = meter.Float64ObservableCounter(instAppDBPoolWait,
		metric.WithDescription("Seconds statements spent waiting for a pooled connection on this replica, labeled by connection."),
		metric.WithUnit(unitSeconds)); err != nil {
		return wrapReg(instAppDBPoolWait, err)
	}
	if _, err := meter.RegisterCallback(m.observeAppDBPools, a.conns, a.acquires, a.waitSeconds); err != nil {
		return fmt.Errorf(instErrFmt, "appdb pool callback", err)
	}
	return nil
}

// RegisterAppDBPools installs what the pool series are read from on each
// scrape: every application database pool's state. Nil-safe; a later call
// replaces the earlier one.
func (m *Metrics) RegisterAppDBPools(s func() []AppDBPoolSample) {
	if m == nil {
		return
	}
	m.appdb.mu.Lock()
	defer m.appdb.mu.Unlock()
	m.appdb.sampler = s
}

// observeAppDBPools is the scrape callback for the pool series.
func (m *Metrics) observeAppDBPools(_ context.Context, o metric.Observer) error {
	m.appdb.mu.RLock()
	sampler := m.appdb.sampler
	m.appdb.mu.RUnlock()
	if sampler == nil {
		return nil
	}
	for _, s := range sampler() {
		conn := attribute.String(attrConnection, s.Connection)
		o.ObserveInt64(m.appdb.conns, s.Acquired, metric.WithAttributes(conn, attribute.String(attrState, "acquired")))
		o.ObserveInt64(m.appdb.conns, s.Idle, metric.WithAttributes(conn, attribute.String(attrState, "idle")))
		o.ObserveInt64(m.appdb.conns, s.Max, metric.WithAttributes(conn, attribute.String(attrState, "max")))
		o.ObserveInt64(m.appdb.acquires, s.Acquires-s.Waited, metric.WithAttributes(conn, attribute.String(attrWait, "none")))
		o.ObserveInt64(m.appdb.acquires, s.Waited, metric.WithAttributes(conn, attribute.String(attrWait, "waited")))
		o.ObserveFloat64(m.appdb.waitSeconds, s.WaitSecondsInTotal, metric.WithAttributes(conn))
	}
	return nil
}

// attrWait labels whether an acquire waited.
const attrWait = "wait"

// RecordAppDBStatementCache records one statement's cache lookup. Nil-safe.
func (m *Metrics) RecordAppDBStatementCache(ctx context.Context, connection string, hit bool) {
	if m == nil || m.appdb.cache == nil {
		return
	}
	result := "miss"
	if hit {
		result = "hit"
	}
	m.appdb.cache.Add(ctx, 1, metric.WithAttributes(attribute.String(attrConnection, connection), attribute.String(attrResult, result)))
}

// RecordAppDBTransaction records how one script transaction ended, or one
// retry of it: outcome is committed, rolled_back, retried or idle_timeout
// (the postgres toolkit's TxOutcome values). Nil-safe.
func (m *Metrics) RecordAppDBTransaction(ctx context.Context, connection, outcome string) {
	if m == nil || m.appdb.transactions == nil {
		return
	}
	m.appdb.transactions.Add(ctx, 1, metric.WithAttributes(attribute.String(attrConnection, connection), attribute.String(attrOutcome, outcome)))
}

// RecordAppDBPoolWaitTimeout records a statement that found no free pooled
// connection in time. Nil-safe.
func (m *Metrics) RecordAppDBPoolWaitTimeout(ctx context.Context, connection string) {
	if m == nil || m.appdb.waitTimeouts == nil {
		return
	}
	m.appdb.waitTimeouts.Add(ctx, 1, metric.WithAttributes(attribute.String(attrConnection, connection)))
}
