package observability

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

func newDepsMetrics(t *testing.T) *Metrics {
	t.Helper()
	m, err := New(Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	return m
}

func TestRecordStorageOperation(t *testing.T) {
	m := newDepsMetrics(t)
	ctx := context.Background()
	m.RecordStorageOperation(ctx, StorageOperation{Purpose: StoragePurposePortalAssets, Operation: "put", Duration: time.Millisecond, Written: 10})
	m.RecordStorageOperation(ctx, StorageOperation{Purpose: StoragePurposeWebhooks, Operation: "get", Reason: StorageReasonAccessDenied, Duration: time.Millisecond})
	(*Metrics)(nil).RecordStorageOperation(ctx, StorageOperation{Purpose: StoragePurposeWebhooks, Operation: "get", Written: 1})

	body := scrapeMetrics(t, m.Handler())
	for _, want := range []string{
		`storage_operations_total{operation="put",purpose="portal_assets",result="ok"} 1`,
		`storage_bytes_written_total{purpose="portal_assets"} 10`,
		`storage_operations_total{operation="get",purpose="webhooks",reason="access_denied",result="error"} 1`,
		`storage_operation_duration_seconds_count{operation="get",purpose="webhooks"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %s", want)
		}
	}
	if strings.Contains(body, `storage_bytes_written_total{purpose="webhooks"}`) {
		t.Error("a read counted bytes written")
	}
}

// The statement histogram recorded through DBMeterProvider keeps only its
// operation label, and a measurement at the threshold is counted as slow.
func TestDBMeterProvider(t *testing.T) {
	m := newDepsMetrics(t)
	h, err := m.DBMeterProvider(time.Second).Meter("driver").Float64Histogram(instDBClientOperationDuration, metric.WithUnit(unitSeconds))
	if err != nil {
		t.Fatal(err)
	}
	attrs := metric.WithAttributes(append(DBOperationAttributes("select"),
		attribute.String("db.operation.name", "sql.conn.query"), attribute.String("error.type", "x"))...)
	h.Record(context.Background(), 0.01, attrs)
	h.Record(context.Background(), 2, attrs)

	// Another histogram from the same meter is not the statement histogram.
	other, err := m.DBMeterProvider(time.Second).Meter("driver").Float64Histogram("db.other")
	if err != nil {
		t.Fatal(err)
	}
	other.Record(context.Background(), 5)

	body := scrapeMetrics(t, m.Handler())
	if !strings.Contains(body, `db_client_operation_duration_seconds_count{operation="select"} 2`) {
		t.Errorf("statement histogram missing or carrying more than its operation\n%s", body)
	}
	if strings.Contains(body, "db_operation_name") || strings.Contains(body, "error_type") {
		t.Error("the statement histogram kept the driver's own attributes")
	}
	if !strings.Contains(body, `db_client_slow_statements_total{operation="select"} 1`) {
		t.Errorf("slow statement not counted\n%s", body)
	}
}

// With no threshold the statement histogram is the meter's own; a disabled
// recorder gives a provider that records nothing.
func TestDBMeterProvider_Off(t *testing.T) {
	m := newDepsMetrics(t)
	h, err := m.DBMeterProvider(0).Meter("driver").Float64Histogram(instDBClientOperationDuration)
	if err != nil {
		t.Fatal(err)
	}
	if _, wrapped := h.(slowStatementHistogram); wrapped {
		t.Error("a zero threshold wrapped the histogram")
	}
	h.Record(context.Background(), 100, metric.WithAttributes(DBOperationAttributes("select")...))
	if strings.Contains(scrapeMetrics(t, m.Handler()), "db_client_slow_statements_total{") {
		t.Error("a zero threshold counted a slow statement")
	}

	nh, err := (*Metrics)(nil).DBMeterProvider(time.Second).Meter("driver").Float64Histogram(instDBClientOperationDuration)
	if err != nil {
		t.Fatal(err)
	}
	nh.Record(context.Background(), 5)
}

func TestDBConfigFromEnv(t *testing.T) {
	t.Setenv(envDBSlowStatement, "")
	t.Setenv(envTracesIncludeDBStatement, "")
	if got := DBConfigFromEnv(); got.SlowThreshold != DefaultDBSlowStatement || got.IncludeStatement {
		t.Errorf("defaults = %+v", got)
	}
	t.Setenv(envDBSlowStatement, "250ms")
	t.Setenv(envTracesIncludeDBStatement, "true")
	if got := DBConfigFromEnv(); got.SlowThreshold != 250*time.Millisecond || !got.IncludeStatement {
		t.Errorf("set = %+v", got)
	}
	t.Setenv(envDBSlowStatement, "0")
	if got := DBConfigFromEnv(); got.SlowThreshold != 0 {
		t.Errorf("zero = %+v", got)
	}
	for _, bad := range []string{"soon", "-1s"} {
		t.Setenv(envDBSlowStatement, bad)
		if got := DBConfigFromEnv(); got.SlowThreshold != DefaultDBSlowStatement {
			t.Errorf("%q = %+v", bad, got)
		}
	}
}

func TestHistogramAttributeFilter(t *testing.T) {
	if histogramAttributeFilter(instToolCallDuration) != nil {
		t.Error("an ordinary histogram was filtered")
	}
	f := histogramAttributeFilter(instDBClientOperationDuration)
	if !f(attribute.String(attrOperation, "select")) || f(attribute.String("error.type", "x")) {
		t.Error("the statement filter keeps the wrong attributes")
	}
}
