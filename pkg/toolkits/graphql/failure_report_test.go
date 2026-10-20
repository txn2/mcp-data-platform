package graphql

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/secretref"
	"github.com/txn2/mcp-data-platform/internal/timeoutcap"
	"github.com/txn2/mcp-data-platform/internal/upstreamretry"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

const updateDocument = `mutation Update($urn: String!) { updateDataset(urn: $urn, description: "d") { urn } }`

// resultMap reads a result's text as the JSON object a caller is handed.
func resultMap(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	content, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("result carries no text: %+v", res.Content)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(content.Text), &out); err != nil {
		t.Fatalf("result is not JSON: %v: %s", err, content.Text)
	}
	return out
}

// TestAFailureWithNoErrorsArrayCarriesStatusBodyAndRetryAdvice is #2211:
// an endpoint failing without a GraphQL errors array used to come back as
// upstream_error with errors absent and nothing else, so every such
// failure read the same. Each now carries the start of its body, and a
// failure the same document is expected to get past later says so, with
// the endpoint's Retry-After, by the rules the api gateway reports with,
// a query counting as a read.
func TestAFailureWithNoErrorsArrayCarriesStatusBodyAndRetryAdvice(t *testing.T) {
	cases := []struct {
		name          string
		document      string
		status        int
		body          string
		retryAfter    string
		wantRetryable bool
		wantAfter     float64
		wantBody      string
	}{
		{"503 to a query is retryable", datasetDocument, http.StatusServiceUnavailable, "<html>busy</html>", "7", true, 7, "<html>busy</html>"},
		{"503 to a mutation is not", updateDocument, http.StatusServiceUnavailable, "<html>busy</html>", "", false, 0, "<html>busy</html>"},
		{"429 to a mutation is", updateDocument, http.StatusTooManyRequests, `{"message":"slow down"}`, "", true, 0, `{"message":"slow down"}`},
		{"500 is not", datasetDocument, http.StatusInternalServerError, "system-error", "", false, 0, "system-error"},
		{"a 200 that is not GraphQL quotes its body", datasetDocument, http.StatusOK, "proxy says no", "", false, 0, "proxy says no"},
		{"a 200 GraphQL answer quotes nothing", datasetDocument, http.StatusOK, `{"data":{"dataset":null}}`, "", false, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := newUpstream(t)
			u.retryAfter = tc.retryAfter
			u.respond = func(graphQLRequest, int) (int, string) { return tc.status, tc.body }
			tk := newToolkit(t, u, "flat", nil)
			res, _, err := tk.handleQuery(context.Background(), nil, QueryInput{Connection: "gql", Query: tc.document, Variables: json.RawMessage(`{"urn":"x"}`)})
			if err != nil {
				t.Fatalf("handleQuery: %v", err)
			}
			if res.IsError {
				t.Fatalf("an answer the endpoint gave is not a tool error: %+v", res.Content)
			}
			out := resultMap(t, res)
			if got, _ := out["status"].(float64); int(got) != tc.status {
				t.Errorf("status = %v; want %d", out["status"], tc.status)
			}
			if got, _ := out["upstream_body"].(string); got != tc.wantBody {
				t.Errorf("upstream_body = %q; want %q", got, tc.wantBody)
			}
			// The keys the script host reads are the ones it reads off
			// api_invoke_endpoint.
			seen := upstreamretry.FromResult(out)
			if seen.Retryable != tc.wantRetryable {
				t.Errorf("upstream_retryable = %v; want %v", seen.Retryable, tc.wantRetryable)
			}
			if got, _ := out[upstreamretry.KeyRetryAfter].(float64); got != tc.wantAfter {
				t.Errorf("retry_after_seconds = %v; want %v", got, tc.wantAfter)
			}
			if tc.wantRetryable && seen.Status != tc.status {
				t.Errorf("the host reads status %d; want %d", seen.Status, tc.status)
			}
		})
	}
}

// TestUpstreamBodyIsBoundedAtARuneBoundary keeps a large error page from
// becoming the result, and never cuts a character in half.
func TestUpstreamBodyIsBoundedAtARuneBoundary(t *testing.T) {
	long := strings.Repeat("a", maxUpstreamBody-1) + "é" + strings.Repeat("b", 100)
	got := upstreamBody([]byte(long))
	if !strings.HasSuffix(got, "...") || len(got) > maxUpstreamBody+3 {
		t.Fatalf("not bounded: %d bytes", len(got))
	}
	if strings.ContainsRune(got, '�') || !json.Valid([]byte(`"`+strings.TrimSuffix(got, "...")+`"`)) {
		t.Error("the cut split a character")
	}
	if got := upstreamBody([]byte("  short \n")); got != "short" {
		t.Errorf("short body = %q", got)
	}
}

// TestASecretTheConnectionFilledIsRedactedFromTheBody holds the body the
// result quotes to the redaction every answer already passes through: an
// endpoint that echoes the stored secret in its error page does not hand it
// to the caller.
func TestASecretTheConnectionFilledIsRedactedFromTheBody(t *testing.T) {
	u := newUpstream(t)
	u.respond = func(graphQLRequest, int) (int, string) {
		return http.StatusBadGateway, "bad gateway for token s3cr3t-value"
	}
	tk := newToolkit(t, u, "flat", nil)
	ctx, redactor := secretref.WithRedactor(context.Background())
	redactor.Add("token", "s3cr3t-value")
	res, _, err := tk.handleQuery(ctx, nil, QueryInput{Connection: "gql", Query: datasetDocument, Variables: json.RawMessage(`{"urn":"x"}`)})
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	body, _ := resultMap(t, res)["upstream_body"].(string)
	if strings.Contains(body, "s3cr3t-value") || body == "" {
		t.Errorf("upstream_body = %q; the secret must be redacted and the rest kept", body)
	}
}

// TestATransportFailureIsAStructuredResult is the transport half of #2211:
// a send the endpoint never answered comes back as a result naming the
// failure under transport_error, failed, with the error code a caller and a
// script's host read as the upstream being unavailable.
func TestATransportFailureIsAStructuredResult(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	u.server.Close() // the endpoint stops answering: connection refused

	res, _, err := tk.handleQuery(context.Background(), nil, QueryInput{Connection: "gql", Query: datasetDocument, Variables: json.RawMessage(`{"urn":"x"}`)})
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	if !res.IsError {
		t.Fatal("a send nobody answered is a failed call")
	}
	out := resultMap(t, res)
	msg, _ := out["transport_error"].(string)
	if !strings.Contains(msg, "graphql: calling http://127.0.0.1") {
		t.Errorf("transport_error = %q", msg)
	}
	if failed, _ := out["upstream_error"].(bool); !failed || out["connection"] != "gql" || out["kind"] != "QUERY" {
		t.Errorf("result = %v", out)
	}
	env, _ := out["error"].(map[string]any)
	if env["code"] != upstreamretry.CodeUnavailable || env["category"] != upstreamretry.CategoryUnavailable {
		t.Errorf("error envelope = %v", env)
	}
	if outcome, _ := auditMeta(t, res); outcome != observability.OutcomeTransportErr {
		t.Errorf("audit outcome = %q", outcome)
	}
}

// TestACappedTimeoutIsReportedAndNamedWhenItFires is #2210: a caller's
// timeout_seconds above the connection's call_timeout is replaced by it,
// and both the result and the timeout failure now say so.
func TestACappedTimeoutIsReportedAndNamedWhenItFires(t *testing.T) {
	u := newUpstream(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	u.respond = func(_ graphQLRequest, callNo int) (int, string) {
		if callNo > 1 {
			<-release // the second document never gets an answer in time
		}
		return http.StatusOK, `{"data":{"dataset":null}}`
	}
	tk := newToolkit(t, u, "flat", map[string]any{"call_timeout": "1s"})
	in := QueryInput{Connection: "gql", Query: datasetDocument, Variables: json.RawMessage(`{"urn":"x"}`), TimeoutSeconds: 240}

	res, _, err := tk.handleQuery(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	out := resultMap(t, res)
	if out["timeout_applied_seconds"] != float64(1) || out["timeout_capped_by"] != timeoutcap.CappedByCallTimeout || out["timeout_requested_seconds"] != float64(240) {
		t.Errorf("a successful call reports the cap; got %v", out)
	}

	res, _, err = tk.handleQuery(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	out = resultMap(t, res)
	msg, _ := out["transport_error"].(string)
	if !strings.Contains(msg, "timed out after 1s (the connection's call_timeout; timeout_seconds 240 was capped)") {
		t.Errorf("transport_error = %q", msg)
	}
	if out["timeout_capped_by"] != timeoutcap.CappedByCallTimeout {
		t.Errorf("a failed call reports the cap too; got %v", out)
	}
	if outcome, _ := auditMeta(t, res); outcome != observability.OutcomeUpstreamTimeout {
		t.Errorf("audit outcome = %q", outcome)
	}
}

// TestALowerTimeoutIsNotReportedAsCapped keeps the report to the case it
// is for.
func TestALowerTimeoutIsNotReportedAsCapped(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":null}}`)
	tk := newToolkit(t, u, "flat", map[string]any{"call_timeout": "30s"})
	res, _, err := tk.handleQuery(context.Background(), nil, QueryInput{Connection: "gql", Query: datasetDocument, Variables: json.RawMessage(`{"urn":"x"}`), TimeoutSeconds: 5})
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	out := resultMap(t, res)
	for _, key := range []string{"timeout_applied_seconds", "timeout_capped_by", "timeout_requested_seconds"} {
		if _, ok := out[key]; ok {
			t.Errorf("%s reported for a timeout that was not capped", key)
		}
	}
}

func TestCallTimeoutOwner(t *testing.T) {
	if got := callTimeoutOwner(5, timeoutcap.Cap{}); got != "the call's timeout_seconds" {
		t.Errorf("a lowered timeout = %q", got)
	}
	if got := callTimeoutOwner(0, timeoutcap.Cap{}); got != timeoutcap.CallTimeoutOwner {
		t.Errorf("no timeout = %q", got)
	}
	_, capped := timeoutcap.Apply(time.Second, 9, timeoutcap.CappedByCallTimeout)
	if got := callTimeoutOwner(9, capped); got != timeoutcap.CallTimeoutOwner {
		t.Errorf("a capped timeout = %q", got)
	}
}
