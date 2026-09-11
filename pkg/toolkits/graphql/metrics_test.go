package graphql

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/membudget"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// scrapeMetrics reads the recorder's exposition text.
func scrapeMetrics(t *testing.T, m *observability.Metrics) string {
	t.Helper()
	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatalf("scrape req: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scrape: %v", err)
	}
	return string(body)
}

// TestOutboundMetricsClassifyOnTheBody is the metrics half of #1678: a
// 200 carrying an errors array is counted as upstream_err under the 2xx
// class, a clean 200 as ok, and a transport failure as before. The
// recorder is wired after the connection was registered, which is the
// order the platform wires it in.
func TestOutboundMetricsClassifyOnTheBody(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()

	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	tk.SetMetrics(m)

	u.respond = answer(`{"data":{"dataset":{"urn":"u1","name":"orders"}}}`)
	callQuery(t, tk, QueryInput{Connection: "gql", Query: datasetDocument})
	u.respond = answer(`{"data":null,"errors":[{"message":"denied"}]}`)
	callQuery(t, tk, QueryInput{Connection: "gql", Query: datasetDocument})
	callQuery(t, tk, QueryInput{Connection: "gql", Query: datasetDocument})
	u.respond = func(graphQLRequest, int) (int, string) { return http.StatusBadGateway, "<html/>" }
	callQuery(t, tk, QueryInput{Connection: "gql", Query: datasetDocument})
	u.server.Close()
	refuseQuery(t, tk, QueryInput{Connection: "gql", Query: datasetDocument})

	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`apigateway_outbound_total{connection="gql",http_status_class="2xx",persona="unknown",status_category="ok"} 1`,
		`apigateway_outbound_total{connection="gql",http_status_class="2xx",persona="unknown",status_category="upstream_err"} 2`,
		`apigateway_outbound_total{connection="gql",http_status_class="5xx",persona="unknown",status_category="upstream_err"} 1`,
		`apigateway_outbound_total{connection="gql",http_status_class="other",persona="unknown",status_category="upstream_err"} 1`,
		`apigateway_outbound_duration_seconds_count{connection="gql",http_status_class="2xx",status_category="upstream_err"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q\n--- body ---\n%s", want, body)
		}
	}
}

// TestAnAnswerThePlatformCouldNotReadIsCountedAsAFailure: a send whose
// body the platform refused to buffer, here because the in-flight
// budget is exhausted, is still one outbound call, and not a successful
// one.
func TestAnAnswerThePlatformCouldNotReadIsCountedAsAFailure(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()

	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":{"urn":"u1","name":"orders"}}}`)
	tk := newToolkit(t, u, "flat", nil)
	tk.SetMetrics(m)
	budget := membudget.New(1)
	if !budget.Acquire(1) {
		t.Fatal("the budget refused its own first byte")
	}
	tk.SetMemBudget(budget)

	if msg := refuseQuery(t, tk, QueryInput{Connection: "gql", Query: datasetDocument}); !strings.Contains(msg, "budget") {
		t.Fatalf("refusal = %q; want the budget named", msg)
	}
	body := scrapeMetrics(t, m)
	want := `apigateway_outbound_total{connection="gql",http_status_class="2xx",persona="unknown",status_category="upstream_err"} 1`
	if !strings.Contains(body, want) {
		t.Errorf("scrape missing %q\n--- body ---\n%s", want, body)
	}
}
