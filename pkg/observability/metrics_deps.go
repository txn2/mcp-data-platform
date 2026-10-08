package observability

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// Dependency instruments (#1896): what the platform asks of its own object
// storage and of PostgreSQL. Exposed names:
//
//   - storage_operations_total{purpose, operation, result, reason}  every
//     object the platform puts, gets, lists or deletes in a bucket it owns;
//     reason is set on a failure only
//   - storage_operation_duration_seconds{purpose, operation}
//   - storage_bytes_written_total{purpose}  bytes the platform stored
//   - db_client_operation_duration_seconds{operation}  every SQL statement
//     the platform's pool runs, timed by the driver wrapper (internal/dbobs)
//     through DBMeterProvider; operation is the statement's leading keyword,
//     vector_search for a pgvector ranking, or the transaction step
//   - db_client_slow_statements_total{operation}  the statements of those
//     that took the slow threshold or longer
//
// Cardinality: purpose, operation, result and reason are closed sets in the
// code (below and in internal/objectobs, internal/dbobs).
const (
	instStorageOperations   = "storage_operations"
	instStorageDuration     = "storage_operation_duration"
	instStorageBytesWritten = "storage_bytes_written"
	instDBSlowStatements    = "db_client_slow_statements"

	// instDBClientOperationDuration is the OpenTelemetry semantic-convention
	// histogram the SQL driver wrapper records. Its name is the convention's,
	// not ours, so it exports as db_client_operation_duration_seconds.
	instDBClientOperationDuration = "db.client.operation.duration"

	attrPurpose = "purpose"
)

// The purposes a platform-owned object operation is reported under: what the
// bucket is being used for, independent of which bucket a deployment named.
const (
	StoragePurposePortalAssets  = "portal_assets"
	StoragePurposeResources     = "resources"
	StoragePurposeThumbnails    = "thumbnails"
	StoragePurposeScriptOutputs = "script_outputs"
	StoragePurposeExports       = "exports"
	StoragePurposeWebhooks      = "webhooks"
)

// The reason classes a failed object operation carries, so a credential or
// bucket-policy mistake reads differently from an outage.
const (
	StorageReasonAccessDenied  = "access_denied"
	StorageReasonBucketMissing = "bucket_missing"
	StorageReasonQuotaExceeded = "quota_exceeded"
	StorageReasonNotFound      = "not_found"
	StorageReasonOther         = "other"
)

// The result values of an object operation.
const (
	resultOK    = "ok"
	resultError = "error"
)

// dbStatementDurationBuckets resolve a SQL statement, most of which finish in
// well under the tool-call histogram's first bucket.
var dbStatementDurationBuckets = []float64{
	0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30,
}

// envDBSlowStatement is the duration at or over which a statement is counted
// on db_client_slow_statements_total and logged at WARN (a Go duration, e.g.
// "500ms"; "0" turns the count and the log off).
const envDBSlowStatement = "MCP_PLATFORM_DB_SLOW_STATEMENT_THRESHOLD"

// envTracesIncludeDBStatement lets a statement span carry the SQL text
// (db.query.text). Off by default: the text can quote the values a statement
// was built with, and a trace backend is outside the platform, the same rule
// OTEL_TRACES_INCLUDE_USER_EMAIL follows for the caller's address.
const envTracesIncludeDBStatement = "OTEL_TRACES_INCLUDE_DB_STATEMENT"

// DefaultDBSlowStatement is the slow-statement threshold when
// MCP_PLATFORM_DB_SLOW_STATEMENT_THRESHOLD is unset.
const DefaultDBSlowStatement = time.Second

// DBConfig is how the platform's SQL driver wrapper observes statements.
type DBConfig struct {
	// SlowThreshold is the statement duration counted as slow; zero turns
	// the slow count and its log off.
	SlowThreshold time.Duration
	// IncludeStatement puts the SQL text on the statement span.
	IncludeStatement bool
}

// DBConfigFromEnv reads the statement observation settings. An unparseable or
// negative threshold is the default.
func DBConfigFromEnv() DBConfig {
	cfg := DBConfig{
		SlowThreshold:    DefaultDBSlowStatement,
		IncludeStatement: parseBoolEnv(envTracesIncludeDBStatement, false),
	}
	if raw := strings.TrimSpace(os.Getenv(envDBSlowStatement)); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d >= 0 {
			cfg.SlowThreshold = d
		}
	}
	return cfg
}

// depInstruments are the dependency series.
type depInstruments struct {
	storageOps      metric.Int64Counter
	storageDuration metric.Float64Histogram
	storageWritten  metric.Int64Counter
	slowStatements  metric.Int64Counter
}

// registerDepInstruments registers the dependency series.
func (m *Metrics) registerDepInstruments(meter metric.Meter) error {
	d := &m.deps
	var err error
	counter := func(name, desc string) metric.Int64Counter {
		if err != nil {
			return nil
		}
		var c metric.Int64Counter
		c, err = meter.Int64Counter(name, metric.WithDescription(desc))
		err = wrapReg(name, err)
		return c
	}
	d.storageOps = counter(instStorageOperations,
		"Total object operations the platform made on the buckets it owns, labeled by purpose (portal_assets, resources, thumbnails, script_outputs, exports, webhooks), operation (put, get, get_range, list, delete), result (ok, error) and, on a failure, reason (access_denied, bucket_missing, quota_exceeded, not_found, other).")
	d.storageWritten = counter(instStorageBytesWritten,
		"Total bytes the platform stored in the buckets it owns, labeled by purpose.")
	d.slowStatements = counter(instDBSlowStatements,
		"Total SQL statements that took the slow threshold (MCP_PLATFORM_DB_SLOW_STATEMENT_THRESHOLD, default 1s) or longer, labeled by operation.")
	if err != nil {
		return err
	}
	d.storageDuration, err = meter.Float64Histogram(instStorageDuration,
		metric.WithDescription("Seconds an object operation on a platform-owned bucket took, labeled by purpose and operation."),
		metric.WithUnit(unitSeconds))
	return wrapReg(instStorageDuration, err)
}

// StorageOperation is one object operation on a bucket the platform owns.
type StorageOperation struct {
	// Purpose is one of the StoragePurpose* values; Operation is put, get,
	// get_range, list or delete.
	Purpose, Operation string
	// Reason is empty for a success and one of the StorageReason* classes for
	// a failure.
	Reason   string
	Duration time.Duration
	// Written is the bytes a put stored.
	Written int64
}

// RecordStorageOperation records one object operation. Nil-safe.
func (m *Metrics) RecordStorageOperation(ctx context.Context, o StorageOperation) {
	if m == nil {
		return
	}
	p := attribute.String(attrPurpose, o.Purpose)
	op := attribute.String(attrOperation, o.Operation)
	attrs := []attribute.KeyValue{p, op, attribute.String(attrResult, resultOK)}
	if o.Reason != "" {
		attrs = []attribute.KeyValue{p, op, attribute.String(attrResult, resultError), attribute.String(attrReason, o.Reason)}
	}
	m.deps.storageOps.Add(ctx, 1, metric.WithAttributes(attrs...))
	m.deps.storageDuration.Record(ctx, o.Duration.Seconds(), metric.WithAttributes(p, op))
	if o.Written > 0 {
		m.deps.storageWritten.Add(ctx, o.Written, metric.WithAttributes(p))
	}
}

// DBOperationAttributes is the one label a statement is measured under: its
// operation. The driver wrapper hands these to the statement histogram, whose
// view keeps nothing else (histogramAttributeFilter).
func DBOperationAttributes(operation string) []attribute.KeyValue {
	return []attribute.KeyValue{attribute.String(attrOperation, operation)}
}

// histogramAttributeFilter is the attribute filter a histogram's view applies:
// nil (keep everything) except for the driver's statement histogram, whose
// wrapper also records the driver method and the error type. Those are
// dimensions nobody reads on a latency chart, and the method would split one
// statement's latency across the calls database/sql made to run it.
func histogramAttributeFilter(name string) attribute.Filter {
	if name != instDBClientOperationDuration {
		return nil
	}
	return func(kv attribute.KeyValue) bool { return kv.Key == attrOperation }
}

// DBMeterProvider is the meter provider the SQL driver wrapper records
// statement durations through, so they reach /metrics and the OTLP push beside
// the platform's own series, with the slow-statement count taken from the same
// measurement. Nil-safe: a disabled recorder gives a no-op provider.
func (m *Metrics) DBMeterProvider(slow time.Duration) metric.MeterProvider {
	if m == nil || m.provider == nil {
		return noop.NewMeterProvider()
	}
	return slowStatementProvider{MeterProvider: m.provider, m: m, slow: slow}
}

// slowStatementProvider hands out meters whose statement histogram also
// counts and logs a statement at or over the slow threshold.
type slowStatementProvider struct {
	metric.MeterProvider
	m    *Metrics
	slow time.Duration
}

// Meter wraps the provider's meter.
func (p slowStatementProvider) Meter(name string, opts ...metric.MeterOption) metric.Meter {
	return slowStatementMeter{Meter: p.MeterProvider.Meter(name, opts...), p: p}
}

// slowStatementMeter wraps the statement histogram and passes every other
// instrument through.
type slowStatementMeter struct {
	metric.Meter
	p slowStatementProvider
}

// Float64Histogram wraps the statement histogram.
func (s slowStatementMeter) Float64Histogram(name string, opts ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	h, err := s.Meter.Float64Histogram(name, opts...)
	if err != nil || name != instDBClientOperationDuration || s.p.slow <= 0 {
		return h, err //nolint:wrapcheck // the meter's own error, returned to the instrumentation that asked
	}
	return slowStatementHistogram{Float64Histogram: h, p: s.p}, nil
}

// slowStatementHistogram records the statement duration and, at or over the
// threshold, counts it and logs it on the statement's context.
type slowStatementHistogram struct {
	metric.Float64Histogram
	p slowStatementProvider
}

// Record observes one statement.
func (h slowStatementHistogram) Record(ctx context.Context, seconds float64, opts ...metric.RecordOption) {
	h.Float64Histogram.Record(ctx, seconds, opts...)
	if seconds < h.p.slow.Seconds() {
		return
	}
	attrs := metric.NewRecordConfig(opts).Attributes()
	op, _ := attrs.Value(attrOperation)
	operation := op.AsString()
	h.p.m.deps.slowStatements.Add(ctx, 1, metric.WithAttributes(attribute.String(attrOperation, operation)))
	slog.WarnContext(ctx, "slow database statement",
		"operation", operation,
		"duration_ms", time.Duration(seconds*float64(time.Second)).Milliseconds(),
		"threshold_ms", h.p.slow.Milliseconds())
}
