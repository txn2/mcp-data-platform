package trino

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/query"
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

// TestSetMetrics_RecordsTrinoQuery exercises the real instrumented client:
// GetTableSchema -> client.DescribeTable -> RecordTrinoQuery, then asserts the
// trino_queries series increments with the describe_table query_kind.
func TestSetMetrics_RecordsTrinoQuery(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	adapter, err := NewWithClient(Config{Catalog: "hive", Schema: "sales", ConnectionName: "test"}, &mockTrinoClient{})
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	adapter.SetMetrics(m)

	if _, err := adapter.GetTableSchema(context.Background(), query.TableIdentifier{Table: "orders"}); err != nil {
		t.Fatalf("GetTableSchema: %v", err)
	}

	body := scrapeForTest(t, m.Handler())
	for _, want := range []string{"trino_queries_total", `query_kind="describe_table"`, `status="ok"`, "trino_query_duration_seconds"} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q\n%s", want, body)
		}
	}
}

// TestInstrumentedClient_AllOps drives every decorated client method so each
// records under its query_kind.
func TestInstrumentedClient_AllOps(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	ic := &instrumentedClient{Client: &mockTrinoClient{}, metrics: m}
	ctx := context.Background()
	_, _ = ic.Query(ctx, "SELECT 1", trinoclient.QueryOptions{})
	_, _ = ic.ListCatalogs(ctx)
	_, _ = ic.ListSchemas(ctx, "hive")
	_, _ = ic.ListTables(ctx, "hive", "sales")
	_, _ = ic.DescribeTable(ctx, "hive", "sales", "orders")

	body := scrapeForTest(t, m.Handler())
	for _, want := range []string{
		`query_kind="select"`, `query_kind="list_catalogs"`, `query_kind="list_schemas"`,
		`query_kind="list_tables"`, `query_kind="describe_table"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q\n%s", want, body)
		}
	}
}

// TestInstrumentedClient_RecordsErrorStatus exercises the failure path.
func TestInstrumentedClient_RecordsErrorStatus(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	mock := &mockTrinoClient{
		describeTableFunc: func(_ context.Context, _, _, _ string) (*trinoclient.TableInfo, error) {
			return nil, errors.New("trino unavailable")
		},
	}
	ic := &instrumentedClient{Client: mock, metrics: m}
	if _, err := ic.DescribeTable(context.Background(), "hive", "sales", "orders"); err == nil {
		t.Fatal("expected error from DescribeTable")
	}

	body := scrapeForTest(t, m.Handler())
	if !strings.Contains(body, `status="upstream_err"`) {
		t.Errorf("scrape missing upstream_err status\n%s", body)
	}
}

func TestQueryKind(t *testing.T) {
	cases := map[string]string{
		"SELECT * FROM t":      "select",
		"  show schemas":       "show",
		"INSERT INTO t VALUES": "insert",
		"WITH x AS (...)":      "with",
		"VACUUM t":             kindOther,
		"":                     kindOther,
	}
	for sql, want := range cases {
		if got := queryKind(sql); got != want {
			t.Errorf("queryKind(%q) = %q, want %q", sql, got, want)
		}
	}
}

// TestSetMetrics_DisabledTrino confirms SetMetrics(nil) leaves the client unwrapped.
func TestSetMetrics_DisabledTrino(t *testing.T) {
	adapter, _ := NewWithClient(Config{ConnectionName: "test"}, &mockTrinoClient{})
	before := adapter.client
	adapter.SetMetrics(nil)
	if adapter.client != before {
		t.Error("SetMetrics(nil) must not wrap the client")
	}
}

// Ensure the instrumented client still satisfies the Client interface.
var _ Client = (*instrumentedClient)(nil)
