package observability

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeConnector yields a *sql.DB whose Stats() works without a real database.
// We never execute queries in these tests, only read pool stats, so Connect
// returning an error is fine.
type fakeConnector struct{}

func (fakeConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("fake connector: no real connection")
}
func (fakeConnector) Driver() driver.Driver { return fakeDriver{} }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("fake driver: no open")
}

func newEnabledMetrics(t *testing.T) *Metrics {
	t.Helper()
	m, err := New(Config{Enabled: true, ListenAddr: ":0"})
	if err != nil {
		t.Fatalf("New(enabled) err = %v", err)
	}
	if m == nil {
		t.Fatal("New(enabled) returned nil recorder")
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	return m
}

func TestRecordToolkitInstruments(t *testing.T) {
	m := newEnabledMetrics(t)
	ctx := context.Background()

	m.RecordTrinoQuery(ctx, StatusOK, "select", 120*time.Millisecond)
	m.RecordTrinoQuery(ctx, StatusUpstreamErr, "select", 30*time.Millisecond)
	m.RecordDataHubRequest(ctx, "get_entity", StatusOK, 40*time.Millisecond)
	m.RecordS3Operation(ctx, "get_object", StatusOK, 15*time.Millisecond)
	m.RecordOAuthIssuance(ctx, "authorization_code", StatusOK)
	m.RecordOAuthRefresh(ctx, StatusOK, 60*time.Millisecond)

	body := scrapeMetrics(t, m.Handler())

	mustContain := []string{
		"trino_queries_total",
		`query_kind="select"`,
		`status="ok"`,
		`status="upstream_err"`,
		"trino_query_duration_seconds",
		"datahub_requests_total",
		`operation="get_entity"`,
		"datahub_request_duration_seconds",
		"s3_operations_total",
		`operation="get_object"`,
		"s3_operation_duration_seconds",
		"oauth_token_issuance_total",
		`grant_type="authorization_code"`,
		"oauth_token_refresh_total",
		"oauth_token_refresh_duration_seconds",
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("scrape body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

func TestRegisterDBPool(t *testing.T) {
	m := newEnabledMetrics(t)
	db := sql.OpenDB(fakeConnector{})
	t.Cleanup(func() { _ = db.Close() })

	m.RegisterDBPool(db, "platform")

	body := scrapeMetrics(t, m.Handler())
	mustContain := []string{
		`db_pool_open_connections{pool="platform"}`,
		`db_pool_in_use{pool="platform"}`,
		`db_pool_idle{pool="platform"}`,
		`db_pool_wait_count_total{pool="platform"}`,
		`db_pool_wait_duration_seconds_total{pool="platform"}`,
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("scrape body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

func TestRegisterDBPoolDuplicateNameIgnored(t *testing.T) {
	m := newEnabledMetrics(t)
	db := sql.OpenDB(fakeConnector{})
	t.Cleanup(func() { _ = db.Close() })

	m.RegisterDBPool(db, "platform")
	m.RegisterDBPool(db, "platform") // duplicate name must be ignored

	m.dbMu.RLock()
	n := len(m.dbPools)
	m.dbMu.RUnlock()
	if n != 1 {
		t.Fatalf("dbPools len = %d, want 1 (duplicate name should be ignored)", n)
	}
}

func TestRegisterDBPoolNilSafe(t *testing.T) {
	var m *Metrics // disabled recorder
	db := sql.OpenDB(fakeConnector{})
	t.Cleanup(func() { _ = db.Close() })
	m.RegisterDBPool(db, "platform") // must not panic

	enabled := newEnabledMetrics(t)
	enabled.RegisterDBPool(nil, "platform") // nil db must not panic or register
	enabled.dbMu.RLock()
	n := len(enabled.dbPools)
	enabled.dbMu.RUnlock()
	if n != 0 {
		t.Fatalf("dbPools len = %d, want 0 (nil db must not register)", n)
	}
}

func TestToolkitRecordMethodsNilSafe(_ *testing.T) {
	var m *Metrics
	ctx := context.Background()
	// None of these may panic on a nil recorder.
	m.RecordTrinoQuery(ctx, StatusOK, "select", time.Millisecond)
	m.RecordDataHubRequest(ctx, "get_entity", StatusOK, time.Millisecond)
	m.RecordS3Operation(ctx, "get_object", StatusOK, time.Millisecond)
	m.RecordOAuthIssuance(ctx, "authorization_code", StatusOK)
	m.RecordOAuthRefresh(ctx, StatusOK, time.Millisecond)
}

func TestUpstreamStatus(t *testing.T) {
	if got := UpstreamStatus(nil); got != StatusOK {
		t.Errorf("UpstreamStatus(nil) = %q, want %q", got, StatusOK)
	}
	if got := UpstreamStatus(errors.New("boom")); got != StatusUpstreamErr {
		t.Errorf("UpstreamStatus(err) = %q, want %q", got, StatusUpstreamErr)
	}
}
