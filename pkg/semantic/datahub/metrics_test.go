package datahub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"
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
	_, _ = ic.SearchAcrossEntities(ctx, "q")
	_, _ = ic.SemanticSearch(ctx, "q")
	_, _ = ic.SearchDocuments(ctx, "q")
	_, _ = ic.GetRelatedDocuments(ctx, "urn")
	_, _ = ic.GetDocument(ctx, "urn")
	_, _ = ic.ListTags(ctx, "PII")
	_, _ = ic.ListDomains(ctx)
	_, _, _ = ic.GetRootGlossaryNodes(ctx, 0, 10)
	_, _, _ = ic.GetRootGlossaryTerms(ctx, 0, 10)
	_, _ = ic.GetGlossaryNodeChildren(ctx, "urn", 0, 10)
	_, _ = ic.GetGlossaryParentChain(ctx, "urn")

	body := scrapeForTest(t, m.Handler())
	for _, want := range []string{
		`operation="get_entity"`, `operation="get_schema"`, `operation="get_schemas"`,
		`operation="get_lineage"`, `operation="get_column_lineage"`,
		`operation="get_glossary_term"`, `operation="get_queries"`,
		`operation="search_across_entities"`, `operation="semantic_search"`,
		`operation="search_documents"`, `operation="get_related_documents"`,
		`operation="get_document"`, `operation="list_tags"`, `operation="list_domains"`,
		`operation="get_root_glossary_nodes"`, `operation="get_root_glossary_terms"`,
		`operation="get_glossary_node_children"`, `operation="get_glossary_parent_chain"`,
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

// TestSetMetrics_NilRecorderTransparent confirms the new contract:
// SetMetrics installs the instrumenting decorator unconditionally (the
// platform gates the CALL on metrics-or-tracing), and with a nil/disabled
// recorder and no active trace the decorator is behaviorally transparent —
// it delegates, records nothing (nil-safe), and emits no span.
func TestSetMetrics_NilRecorderTransparent(t *testing.T) {
	adapter, err := NewWithClient(Config{Platform: "trino"}, &mockDataHubClient{})
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	before := adapter.client
	adapter.SetMetrics(nil) // disabled recorder
	if adapter.client == before {
		t.Fatal("SetMetrics now wraps unconditionally; gating moved to the platform call site")
	}
	// The wrapped client must still delegate without panicking on a nil recorder.
	if _, err := adapter.client.GetEntity(context.Background(), "urn:li:dataset:(x)"); err != nil {
		t.Errorf("wrapped client delegate failed: %v", err)
	}
}

// TestUpstreamStatusOnACatalogMiss is #1610. A URN the catalog holds no entity
// for is an answer, so it records as a served request rather than as an
// upstream failure; anything else the client reports is still a failure.
func TestUpstreamStatusOnACatalogMiss(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"a served read", nil, observability.StatusOK},
		{"a URN the catalog holds nothing for", fmt.Errorf("get entity: %w", dhclient.ErrNotFound), observability.StatusOK},
		{"a catalog that could not be reached", errors.New("dial tcp: connection refused"), observability.StatusUpstreamErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := upstreamStatus(tt.err); got != tt.want {
				t.Errorf("upstreamStatus(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

// TestInstrumentedClient_RecordsACatalogMissAsServed is #1610 through the
// decorator rather than through upstreamStatus alone: a URN the catalog holds
// no entity for is counted as a served request, and the error still reaches the
// caller with its sentinel intact.
func TestInstrumentedClient_RecordsACatalogMissAsServed(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	mock := &mockDataHubClient{
		getEntityFunc: func(_ context.Context, _ string) (*types.Entity, error) {
			return nil, fmt.Errorf("get entity: %w", dhclient.ErrNotFound)
		},
	}
	ic := &instrumentedClient{Client: mock, metrics: m}
	_, err = ic.GetEntity(context.Background(), "urn")
	if !errors.Is(err, dhclient.ErrNotFound) {
		t.Fatalf("GetEntity error = %v, want the not-found sentinel to survive the decorator", err)
	}

	body := scrapeForTest(t, m.Handler())
	if !strings.Contains(body, `datahub_requests_total{operation="get_entity",status="ok"}`) {
		t.Errorf("a catalog miss was not recorded as a served request\n%s", body)
	}
	if strings.Contains(body, `status="upstream_err"`) {
		t.Errorf("a catalog miss was recorded as an upstream failure\n%s", body)
	}
}
