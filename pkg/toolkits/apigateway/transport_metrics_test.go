package apigateway

import (
	"context"
	"net/http"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/apigwmetrics"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// TestToolkit_SetMetrics_WrapsExistingConnections covers the toolkit's half of
// the outbound-metrics wiring: what a connection's client is wrapped with is
// internal/apigwmetrics' business and tested there, but WHEN the toolkit wraps
// is this package's. A connection added before metrics arrived must be
// instrumented retroactively, SetMetrics(nil) must not unwrap, and a repeated
// SetMetrics must not nest a second recorder.
func TestToolkit_SetMetrics_WrapsExistingConnections(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()

	tk := New("primary")
	cfg := Config{
		BaseURL:        "http://127.0.0.1:1", // unused; we never call out
		CallTimeout:    1,
		ConnectTimeout: 1,
		AuthMode:       AuthModeNone,
	}
	if addErr := tk.addParsedConnection("primary", cfg); addErr != nil {
		t.Fatalf("addParsedConnection: %v", addErr)
	}

	// Before SetMetrics the transport is the bare http.Transport.
	bareTransport := tk.connections["primary"].client.Transport
	if _, wrapped := apigwmetrics.Wraps(tk.connections["primary"].client, "primary"); wrapped {
		t.Fatal("connection transport pre-SetMetrics should not be wrapped")
	}

	tk.SetMetrics(m)

	// After SetMetrics the transport must be wrapped, and what it wrapped must
	// be the original bare transport so the SSRF / redirect handling built into
	// it continues to apply.
	base, wrapped := apigwmetrics.Wraps(tk.connections["primary"].client, "primary")
	if !wrapped {
		t.Fatalf("connection transport post-SetMetrics is %T, want the outbound recorder",
			tk.connections["primary"].client.Transport)
	}
	if base != bareTransport {
		t.Error("the recorder wrapped something other than the pre-wrap transport; ssrf/redirect guards depend on the original")
	}

	// SetMetrics(nil) is a no-op that does not unwrap (documented).
	tk.SetMetrics(nil)
	if _, stillWrapped := apigwmetrics.Wraps(tk.connections["primary"].client, "primary"); !stillWrapped {
		t.Error("SetMetrics(nil) unwrapped the transport; the contract is no-op")
	}

	// Repeated calls with the same recorder must not double-wrap, which would
	// double-record every outbound call.
	tk.SetMetrics(m)
	tk.SetMetrics(m)
	base, wrapped = apigwmetrics.Wraps(tk.connections["primary"].client, "primary")
	if !wrapped {
		t.Fatalf("expected a wrapped transport after repeated SetMetrics, got %T",
			tk.connections["primary"].client.Transport)
	}
	if _, nested := apigwmetrics.Wraps(&http.Client{Transport: base}, "primary"); nested {
		t.Error("repeated SetMetrics(m) double-wrapped the transport")
	}
}
