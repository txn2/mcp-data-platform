package datahub

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-datahub/pkg/types"

	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

func scrapeForTest(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// TestSetMetrics_RecordsDataHubRequest exercises the real instrumented client:
// GetTableContext -> client.GetEntity -> RecordDataHubRequest, then scrapes the
// recorder and asserts the datahub_requests series increments.
func TestSetMetrics_RecordsDataHubRequest(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	adapter, err := NewWithClient(Config{Platform: "trino"}, &mockDataHubClient{})
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	adapter.SetMetrics(m)

	if _, err := adapter.GetTableContext(context.Background(),
		semantic.TableIdentifier{Schema: "schema", Table: "table"}); err != nil {
		t.Fatalf("GetTableContext: %v", err)
	}

	body := scrapeForTest(t, m.Handler())
	for _, want := range []string{"datahub_requests_total", `operation="get_entity"`, `status="ok"`, "datahub_request_duration_seconds"} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q\n%s", want, body)
		}
	}
}

// TestInstrumentedClient_AllOps drives every decorated client method so each
// records under its operation label.
func TestInstrumentedClient_AllOps(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	ic := &instrumentedClient{Client: &mockDataHubClient{}, metrics: m}
	ctx := context.Background()
	_, _ = ic.GetEntity(ctx, "urn")
	_, _ = ic.GetSchema(ctx, "urn")
	_, _ = ic.GetSchemas(ctx, []string{"urn"})
	_, _ = ic.GetLineage(ctx, "urn")
	_, _ = ic.GetColumnLineage(ctx, "urn")
	_, _ = ic.GetGlossaryTerm(ctx, "urn")
	_, _ = ic.GetQueries(ctx, "urn")

	body := scrapeForTest(t, m.Handler())
	for _, want := range []string{
		`operation="get_entity"`, `operation="get_schema"`, `operation="get_schemas"`,
		`operation="get_lineage"`, `operation="get_column_lineage"`,
		`operation="get_glossary_term"`, `operation="get_queries"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q\n%s", want, body)
		}
	}
}

// TestInstrumentedClient_RecordsErrorStatus exercises the failure path: the
// underlying call errors, so the observation records status=upstream_err and
// the returned error is wrapped.
func TestInstrumentedClient_RecordsErrorStatus(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	mock := &mockDataHubClient{
		getEntityFunc: func(_ context.Context, _ string) (*types.Entity, error) {
			return nil, errors.New("datahub unavailable")
		},
	}
	ic := &instrumentedClient{Client: mock, metrics: m}
	if _, err := ic.GetEntity(context.Background(), "urn"); err == nil {
		t.Fatal("expected error from GetEntity")
	}

	body := scrapeForTest(t, m.Handler())
	if !strings.Contains(body, `status="upstream_err"`) {
		t.Errorf("scrape missing upstream_err status\n%s", body)
	}
}

// TestSetMetrics_Disabled is a no-op: the client is not wrapped, so behavior is
// unchanged and no panic occurs.
func TestSetMetrics_Disabled(t *testing.T) {
	adapter, err := NewWithClient(Config{Platform: "trino"}, &mockDataHubClient{})
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	before := adapter.client
	adapter.SetMetrics(nil) // disabled recorder
	if adapter.client != before {
		t.Error("SetMetrics(nil) must not wrap the client")
	}
}
