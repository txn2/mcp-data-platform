package apigateway

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func TestClassifyInvokeOutcome(t *testing.T) {
	cases := []struct {
		name string
		in   InvokeOutput
		want string
	}{
		{"2xx → ok", InvokeOutput{Status: 200}, observability.OutcomeOK},
		{"3xx → ok", InvokeOutput{Status: 302}, observability.OutcomeOK},
		{"4xx → upstream_4xx", InvokeOutput{Status: 404}, observability.OutcomeUpstream4xx},
		{"4xx edge low", InvokeOutput{Status: 400}, observability.OutcomeUpstream4xx},
		{"4xx edge high", InvokeOutput{Status: 499}, observability.OutcomeUpstream4xx},
		{"5xx → upstream_5xx", InvokeOutput{Status: 503}, observability.OutcomeUpstream5xx},
		{"5xx edge low", InvokeOutput{Status: 500}, observability.OutcomeUpstream5xx},
		{"5xx edge high", InvokeOutput{Status: 599}, observability.OutcomeUpstream5xx},
		{"status 0 + ctx deadline → upstream_timeout", InvokeOutput{Status: 0, Error: `Get "https://api.example.com/x": context deadline exceeded`}, observability.OutcomeUpstreamTimeout},
		{"status 0 + Client.Timeout → upstream_timeout", InvokeOutput{Status: 0, Error: `Get "https://api.example.com/x": net/http: request canceled (Client.Timeout exceeded while awaiting headers)`}, observability.OutcomeUpstreamTimeout},
		{"status 0 + i/o timeout → upstream_timeout", InvokeOutput{Status: 0, Error: `read tcp 10.0.0.1:443: i/o timeout`}, observability.OutcomeUpstreamTimeout},
		{"status 0 + DNS → transport_err", InvokeOutput{Status: 0, Error: `Get "https://nope.invalid/x": dial tcp: lookup nope.invalid: no such host`}, observability.OutcomeTransportErr},
		{"status 0 + connection refused → transport_err", InvokeOutput{Status: 0, Error: `Get "https://api.example.com/x": dial tcp 10.0.0.1:443: connect: connection refused`}, observability.OutcomeTransportErr},
		{"status 0 + TLS → transport_err", InvokeOutput{Status: 0, Error: `Get "https://api.example.com/x": tls: handshake failure`}, observability.OutcomeTransportErr},
		{"status 0 + EOF → transport_err", InvokeOutput{Status: 0, Error: `Get "https://api.example.com/x": EOF`}, observability.OutcomeTransportErr},
		{"status 0 + connection reset → transport_err", InvokeOutput{Status: 0, Error: `read tcp 10.0.0.1:443: connection reset by peer`}, observability.OutcomeTransportErr},
		{"status 0 + empty error → transport_err", InvokeOutput{Status: 0}, observability.OutcomeTransportErr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyInvokeOutcome(tc.in); got != tc.want {
				t.Errorf("ClassifyInvokeOutcome = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildInvokeResult(t *testing.T) {
	cases := []struct {
		name        string
		in          InvokeOutput
		wantOutcome string
		wantIsError bool
		wantMsg     string
	}{
		{
			name:        "2xx success leaves IsError false, ok outcome, no message",
			in:          InvokeOutput{Status: 200, Body: map[string]any{"id": 1}},
			wantOutcome: observability.OutcomeOK,
			wantIsError: false,
			wantMsg:     "",
		},
		{
			name:        "upstream 404 leaves IsError false (gateway succeeded), upstream_4xx outcome, status text in meta",
			in:          InvokeOutput{Status: 404, Body: map[string]any{"error": "not found"}},
			wantOutcome: observability.OutcomeUpstream4xx,
			wantIsError: false,
			wantMsg:     "Not Found",
		},
		{
			name:        "upstream 503 leaves IsError false (gateway succeeded), upstream_5xx outcome, status text in meta",
			in:          InvokeOutput{Status: 503, Body: "Service Unavailable"},
			wantOutcome: observability.OutcomeUpstream5xx,
			wantIsError: false,
			wantMsg:     "Service Unavailable",
		},
		{
			name:        "upstream 500 with explicit Error prefers Error over status text",
			in:          InvokeOutput{Status: 500, Error: "read body capped at 10MiB"},
			wantOutcome: observability.OutcomeUpstream5xx,
			wantIsError: false,
			wantMsg:     "read body capped at 10MiB",
		},
		{
			name:        "transport error → IsError true, transport_err outcome, message populated",
			in:          InvokeOutput{Status: 0, Error: `Get "https://x.example.com/y": dial tcp: connection refused`},
			wantOutcome: observability.OutcomeTransportErr,
			wantIsError: true,
			wantMsg:     `Get "https://x.example.com/y": dial tcp: connection refused`,
		},
		{
			name:        "upstream timeout → IsError true, upstream_timeout outcome, message populated",
			in:          InvokeOutput{Status: 0, Error: `Get "https://x.example.com/y": context deadline exceeded`},
			wantOutcome: observability.OutcomeUpstreamTimeout,
			wantIsError: true,
			wantMsg:     `Get "https://x.example.com/y": context deadline exceeded`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := buildInvokeResult(tc.in)
			if r.IsError != tc.wantIsError {
				t.Errorf("IsError = %v, want %v", r.IsError, tc.wantIsError)
			}
			gotOutcome, _ := r.Meta[observability.MetaAuditOutcome].(string)
			if gotOutcome != tc.wantOutcome {
				t.Errorf("Meta[audit_outcome] = %q, want %q", gotOutcome, tc.wantOutcome)
			}
			gotMsg, _ := r.Meta[observability.MetaAuditOutcomeMessage].(string)
			if gotMsg != tc.wantMsg {
				t.Errorf("Meta[audit_outcome_message] = %q, want %q", gotMsg, tc.wantMsg)
			}
			// Every successful result still contains the JSON-encoded
			// InvokeOutput as text content so MCP clients reading the
			// body get the full upstream response shape regardless of
			// whether IsError is set.
			if len(r.Content) == 0 {
				t.Errorf("Content is empty; expected JSON-encoded InvokeOutput payload")
			}
		})
	}
}

// TestBuildInvokeResult_MetaPresentOnSuccess proves the audit_outcome
// stamp lands on EVERY result, not just the failure paths. The audit
// middleware uses this to populate audit_logs.error_category for
// success rows too (with value "ok"), which lets a dashboard query
// for "what fraction of calls have error_category != 'ok'" without
// needing to special-case NULLs.
func TestBuildInvokeResult_MetaPresentOnSuccess(t *testing.T) {
	r := buildInvokeResult(InvokeOutput{Status: 200, Body: map[string]any{"id": 1}})
	if r.Meta == nil {
		t.Fatal("Meta is nil on success path")
	}
	v, ok := r.Meta[observability.MetaAuditOutcome].(string)
	if !ok || v != observability.OutcomeOK {
		t.Errorf("Meta[audit_outcome] = %v, want %q", r.Meta[observability.MetaAuditOutcome], observability.OutcomeOK)
	}
	// No message expected on the success path because InvokeOutput.Error is empty.
	if _, present := r.Meta[observability.MetaAuditOutcomeMessage]; present {
		t.Errorf("Meta[audit_outcome_message] should be absent on success path, got %v", r.Meta[observability.MetaAuditOutcomeMessage])
	}
}

// TestBuildInvokeResult_PreservesJSONBody guards the structured-content
// contract: even when IsError = true (transport / timeout), the JSON
// body carrying the upstream status and scrubbed error MUST still be
// present so MCP clients (and the REST shim's JSON unmarshal path) can
// inspect the failure shape. Regression test for the temptation to
// replace the content with a plain error string.
func TestBuildInvokeResult_PreservesJSONBody(t *testing.T) {
	r := buildInvokeResult(InvokeOutput{Status: 0, Error: `dial tcp: connection refused`})
	if !r.IsError {
		t.Fatal("expected IsError = true for transport error")
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("first content block is %T, want *mcp.TextContent", r.Content[0])
	}
	if tc.Text == "" {
		t.Error("text content is empty; transport-error result must still carry the JSON body")
	}
}
