//go:build integration

package acceptance

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Issue #1615: the API gateway's outbound metric carried no principal, so a
// deployment could chart how much traffic a connection carried but not who
// caused it. #1614 took the automated principal's calls out of the catalog,
// which leaves the audit log and these metrics as where its volume is still
// visible -- and the metrics one could not answer the operator's question.
//
// The dev stack carries the exact pair the label exists to separate: the
// `ingest-service` persona (acme-ingest-key), an automated principal, and the
// `admin` persona (the default key), an interactive one. Both drive the same
// api-test-fixture connection.
const (
	ingestPersona1615 = "ingest-service"
	adminPersona1615  = "admin"
	outboundMetric    = "apigateway_outbound_total"
)

// metricsURL is the platform's own Prometheus endpoint. dev/start.sh pins
// OTEL_METRICS_ADDR to :9464; a deployment the suite is pointed at elsewhere
// supplies its own.
func metricsURL() string {
	if v := os.Getenv("ACCEPTANCE_METRICS_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:9464/metrics"
}

// scrapeOutbound reads /metrics and returns the value of every
// apigateway_outbound_total series, keyed by its full label set.
func scrapeOutbound(t *testing.T) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for line := range strings.SplitSeq(scrapeRaw(t), "\n") {
		if !strings.HasPrefix(line, outboundMetric+"{") {
			continue
		}
		lb, rb := strings.Index(line, "{"), strings.LastIndex(line, "}")
		if lb < 0 || rb < lb {
			continue
		}
		value, convErr := strconv.ParseFloat(strings.TrimSpace(line[rb+1:]), 64)
		if convErr != nil {
			continue
		}
		out[line[lb+1:rb]] = value
	}
	return out
}

// personaTotal sums every outbound series carrying the given persona.
func personaTotal(series map[string]float64, persona string) float64 {
	var total float64
	for labels, v := range series {
		if strings.Contains(labels, `persona="`+persona+`"`) {
			total += v
		}
	}
	return total
}

// TestIssue1615_AnOutboundCallRecordsTheCallingPersona is criteria 1 and 6: an
// outbound gateway call records the principal dimension, readable from the
// metrics endpoint, and it names the principal the way the tool-call metric
// does -- the persona, not a second naming of the same caller.
func TestIssue1615_AnOutboundCallRecordsTheCallingPersona(t *testing.T) {
	before := scrapeOutbound(t)

	c := connect(t)
	c.call("api_invoke_endpoint", map[string]any{
		"connection": apiTestConnection,
		"method":     "GET",
		"path":       "/v1/pagination/link",
		"purpose":    "Acceptance: one outbound gateway call as an interactive persona.",
	})

	after := scrapeOutbound(t)
	if got := personaTotal(after, adminPersona1615) - personaTotal(before, adminPersona1615); got < 1 {
		t.Fatalf("outbound calls recorded for persona %q moved by %v; want at least 1.\nseries now: %v",
			adminPersona1615, got, after)
	}

	// The persona is on the call counter, and the same name the tool-call
	// metric uses. mcp_tool_calls_total{persona="admin"} is the agreement
	// criterion 6 asks for: two metrics naming one caller identically.
	if !strings.Contains(scrapeRaw(t), `mcp_tool_calls_total{persona="`+adminPersona1615+`"`) {
		t.Fatalf("mcp_tool_calls_total does not carry persona=%q, so the two metrics do not agree on how a principal is named", adminPersona1615)
	}
}

// scrapeRaw returns the whole scrape body, for assertions that span metrics.
func scrapeRaw(t *testing.T) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, metricsURL(), http.NoBody)
	if err != nil {
		t.Fatalf("building the scrape request: %v", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("no metrics endpoint answers at %s (%v). `make dev` starts one; otherwise set ACCEPTANCE_METRICS_URL", metricsURL(), err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading the scrape body: %v", err)
	}
	return string(body)
}

// TestIssue1615_TwoPrincipalsOnOneConnectionAreSeparate is criterion 4: a
// deployment with one automated principal and one interactive persona can be
// told apart on the connection they share. Both drive api-test-fixture; the
// counter must move for each under its own name and not collapse into one
// unattributed series.
func TestIssue1615_TwoPrincipalsOnOneConnectionAreSeparate(t *testing.T) {
	before := scrapeOutbound(t)

	person := connect(t)
	machine := connectAs(t, devIngestAPIKey)
	for _, caller := range []struct {
		c       *client
		purpose string
	}{
		{person, "Acceptance: an analyst's call on the shared connection."},
		{machine, "Acceptance: an ingestion job's call on the shared connection."},
		{machine, "Acceptance: a second ingestion job call on the shared connection."},
	} {
		caller.c.call("api_invoke_endpoint", map[string]any{
			"connection": apiTestConnection,
			"method":     "GET",
			"path":       "/v1/pagination/link",
			"purpose":    caller.purpose,
		})
	}

	after := scrapeOutbound(t)
	machineDelta := personaTotal(after, ingestPersona1615) - personaTotal(before, ingestPersona1615)
	personDelta := personaTotal(after, adminPersona1615) - personaTotal(before, adminPersona1615)
	if machineDelta < 2 {
		t.Errorf("outbound calls for the automated persona %q moved by %v; want at least 2", ingestPersona1615, machineDelta)
	}
	if personDelta < 1 {
		t.Errorf("outbound calls for the interactive persona %q moved by %v; want at least 1", adminPersona1615, personDelta)
	}

	// Both principals used one connection, so the separation has to hold
	// within that connection's series rather than only across the total.
	scoped := map[string]float64{}
	for labels, v := range after {
		if strings.Contains(labels, `connection="`+apiTestConnection+`"`) {
			scoped[labels] = v
		}
	}
	if personaTotal(scoped, ingestPersona1615) == 0 || personaTotal(scoped, adminPersona1615) == 0 {
		t.Fatalf("connection %q does not carry both principals as separate series: %v", apiTestConnection, scoped)
	}
}

// TestIssue1615_TheLabelIsBounded is criterion 2: a caller the platform could
// not name records one fixed value rather than a new series. Every outbound
// series must carry a persona label, and the unattributed ones must all carry
// the same value.
func TestIssue1615_TheLabelIsBounded(t *testing.T) {
	c := connect(t)
	c.call("api_invoke_endpoint", map[string]any{
		"connection": apiTestConnection,
		"method":     "GET",
		"path":       "/v1/pagination/link",
		"purpose":    "Acceptance: populate the outbound series before reading the label set.",
	})

	personas := map[string]bool{}
	for labels := range scrapeOutbound(t) {
		if !strings.Contains(labels, `persona="`) {
			t.Fatalf("an outbound series carries no persona label: %s", labels)
		}
		start := strings.Index(labels, `persona="`) + len(`persona="`)
		end := strings.Index(labels[start:], `"`)
		personas[labels[start:start+end]] = true
		if labels[start:start+end] == "" {
			t.Errorf("an outbound series records an empty persona rather than one fixed value: %s", labels)
		}
	}
	if len(personas) == 0 {
		t.Fatal("no outbound series were recorded at all")
	}
	// The dev stack has a handful of personas; a label that grew per caller
	// would already be far past this by the time the suite has run.
	if len(personas) > 20 {
		t.Errorf("the persona label took %d distinct values, which is not a bounded dimension: %v", len(personas), personas)
	}
}

// TestIssue1615_BothBodyFormsRecordTheSamePrincipal sends the two forms
// api_invoke_endpoint's untyped `body` admits -- a JSON object and a string of
// JSON -- and asserts both record the caller under the same persona. The label
// is read off the call context rather than off the request, so neither form
// may change it.
func TestIssue1615_BothBodyFormsRecordTheSamePrincipal(t *testing.T) {
	before := scrapeOutbound(t)

	c := connectAs(t, devIngestAPIKey)
	c.call("api_invoke_endpoint", map[string]any{
		"connection": "util",
		"method":     "POST",
		"path":       "/util/fetch",
		"body":       map[string]any{"url": apiTestFixtureURL() + "/v1/pagination/link"},
		"purpose":    "Acceptance: an outbound call whose body was sent as a JSON object.",
	})
	objectForm := scrapeOutbound(t)

	c.call("api_invoke_endpoint", map[string]any{
		"connection": "util",
		"method":     "POST",
		"path":       "/util/fetch",
		"body":       `{"url": "` + apiTestFixtureURL() + `/v1/pagination/link"}`,
		"purpose":    "Acceptance: an outbound call whose body was sent as a string of JSON.",
	})
	stringForm := scrapeOutbound(t)

	objectDelta := personaTotal(objectForm, ingestPersona1615) - personaTotal(before, ingestPersona1615)
	stringDelta := personaTotal(stringForm, ingestPersona1615) - personaTotal(objectForm, ingestPersona1615)
	if objectDelta < 1 {
		t.Errorf("the object body form recorded %v calls for persona %q; want at least 1", objectDelta, ingestPersona1615)
	}
	if stringDelta < 1 {
		t.Errorf("the JSON-string body form recorded %v calls for persona %q; want at least 1", stringDelta, ingestPersona1615)
	}
}

// TestIssue1615_TheGatewayViewReadsTheSplit is criterion 3, executed through
// the surface the panel uses: the authenticated PromQL proxy the admin
// Dashboard's API Gateway tab queries. Both the root query and the
// per-connection one must come back with a persona dimension.
func TestIssue1615_TheGatewayViewReadsTheSplit(t *testing.T) {
	person := connect(t)
	machine := connectAs(t, devIngestAPIKey)
	for _, caller := range []*client{person, machine} {
		caller.call("api_invoke_endpoint", map[string]any{
			"connection": apiTestConnection,
			"method":     "GET",
			"path":       "/v1/pagination/link",
			"purpose":    "Acceptance: populate the gateway view's principal split.",
		})
	}

	// The two queries ui/src/pages/audit/promql.ts builds for the panels.
	root := `topk(10, sum by (persona) (increase(apigateway_outbound_total[1h])))`
	scoped := `topk(10, sum by (persona) (increase(apigateway_outbound_total{connection="` + apiTestConnection + `"}[1h])))`

	rootPersonas := awaitProxyPersonas(t, person, root, 1)
	if len(rootPersonas) == 0 {
		t.Fatal("the root outbound-by-principal query returned no principals")
	}
	// The panel's whole point: the connection's own traffic separates into
	// the automated principal and the interactive one.
	scopedPersonas := awaitProxyPersonas(t, person, scoped, 2)
	if !scopedPersonas[ingestPersona1615] || !scopedPersonas[adminPersona1615] {
		t.Fatalf("the per-connection query did not separate the two principals; got %v", scopedPersonas)
	}
}

// awaitProxyPersonas runs a PromQL query through the platform's authenticated
// proxy until it reports at least want distinct personas, or the wait runs
// out. Prometheus scrapes the platform on an interval, so a query issued the
// moment a call returns can outrun the scrape.
func awaitProxyPersonas(t *testing.T, c *client, query string, want int) map[string]bool {
	t.Helper()
	const (
		attempts = 40
		pause    = 500 * time.Millisecond
	)
	seen := map[string]bool{}
	for range attempts {
		status, body := c.rest("GET", "/api/v1/observability/query?query="+url.QueryEscape(query), nil)
		if status != http.StatusOK {
			t.Fatalf("the observability proxy answered %d for %q: %v. It needs the dev Prometheus (`make dev` starts it)", status, query, body)
		}
		seen = map[string]bool{}
		data, _ := body["data"].(map[string]any)
		results, _ := data["result"].([]any)
		for _, r := range results {
			row, _ := r.(map[string]any)
			metric, _ := row["metric"].(map[string]any)
			if persona, ok := metric["persona"].(string); ok && persona != "" {
				seen[persona] = true
			}
		}
		if len(seen) >= want {
			return seen
		}
		time.Sleep(pause)
	}
	return seen
}
