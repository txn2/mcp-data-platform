package apigateway_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigateway "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// This file is the external test package for the api-gateway toolkit: it
// imports pkg/middleware, which imports the toolkit, so it cannot live in
// package apigateway. That is also the reason the persona travels from the
// tool-call middleware to the outbound transport through pkg/mcpcontext
// rather than through the PlatformContext that already holds it.

// personaAuthn authenticates every call as one fixed principal.
type personaAuthn struct{ userID string }

func (a *personaAuthn) Authenticate(_ context.Context) (*middleware.UserInfo, error) {
	return &middleware.UserInfo{UserID: a.userID, Roles: []string{a.userID}}, nil
}

// personaAuthz resolves every call to one fixed persona, standing in for the
// real registry's role-to-persona mapping.
type personaAuthz struct{ persona string }

func (a *personaAuthz) IsAuthorized(_ context.Context, _ string, _ []string, _, _ string) (allowed bool, personaName, reason string) {
	return true, a.persona, ""
}

// gatewayLookup names the toolkit the tool belongs to, as the registry does.
type gatewayLookup struct{ conn string }

func (l *gatewayLookup) GetToolkitForTool(_ string) registry.ToolkitMatch {
	return registry.ToolkitMatch{
		Kind: apigateway.Kind, Name: "apigateway", Connection: l.conn, Found: true,
	}
}

// TestOutboundMetricLabelsCallingPersona_Integration is the end-to-end proof
// for #1615: a real api_invoke_endpoint call, through the real tool-call
// middleware, against a real upstream, records an outbound series labeled with
// the persona the authorizer resolved. Two personas driving the same connection
// must appear as two series so an operator can tell an automated principal's
// traffic from an analyst's.
//
// It is an integration test rather than two unit tests because the label's
// whole value rests on the persona surviving the trip from the authorizer,
// through the context the handler runs under, into the http.Client's
// RoundTripper. A test that hands the transport a persona directly proves
// nothing about that trip.
func TestOutboundMetricLabelsCallingPersona_Integration(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()

	// callAs drives one api_invoke_endpoint call through a server whose
	// tool-call middleware resolves the given persona, sharing the one
	// metrics recorder and the one upstream across both callers.
	callAs := func(persona string) {
		t.Helper()
		tk := apigateway.NewMulti(apigateway.MultiConfig{
			DefaultName: "shared",
			Instances: map[string]apigateway.Config{
				"shared": {
					BaseURL:          upstream.URL,
					AuthMode:         apigateway.AuthModeNone,
					ConnectTimeout:   2 * time.Second,
					CallTimeout:      5 * time.Second,
					MaxResponseBytes: 1 << 20,
					TrustLevel:       apigateway.TrustLevelTrusted,
				},
			},
		})
		tk.SetMetrics(m)
		defer func() { _ = tk.Close() }()

		server := mcp.NewServer(&mcp.Implementation{Name: "gw-persona", Version: "v0"}, nil)
		tk.RegisterTools(server)
		server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
			&personaAuthn{userID: persona},
			&personaAuthz{persona: persona},
			&gatewayLookup{conn: "shared"},
			middleware.ToolCallConfig{Transport: "stdio", AdminPersona: "admin"},
		))

		ctx := context.Background()
		st, ct := mcp.NewInMemoryTransports()
		if _, connErr := server.Connect(ctx, st, nil); connErr != nil {
			t.Fatalf("server connect: %v", connErr)
		}
		sess, connErr := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, ct, nil)
		if connErr != nil {
			t.Fatalf("client connect: %v", connErr)
		}
		defer func() { _ = sess.Close() }()

		res, callErr := sess.CallTool(ctx, &mcp.CallToolParams{
			Name: apigateway.ToolInvokeEndpoint,
			Arguments: map[string]any{
				"connection": "shared",
				"method":     http.MethodGet,
				"path":       "/things",
			},
		})
		if callErr != nil {
			t.Fatalf("CallTool as %s: %v", persona, callErr)
		}
		if res.IsError {
			t.Fatalf("CallTool as %s returned a tool error: %v", persona, res.Content)
		}
	}

	callAs("ingest-service")
	callAs("ingest-service")
	callAs("analyst")

	body := scrapePersonaMetrics(t, m.Handler())
	want := []string{
		`apigateway_outbound_total{connection="shared",http_status_class="2xx",persona="ingest-service",status_category="ok"} 2`,
		`apigateway_outbound_total{connection="shared",http_status_class="2xx",persona="analyst",status_category="ok"} 1`,
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("scrape missing %q\n--- body ---\n%s", w, body)
		}
	}
	// The two personas' calls must not have collapsed into one unattributed
	// series, which is the failure this ticket exists to prevent.
	if strings.Contains(body, `apigateway_outbound_total{connection="shared",http_status_class="2xx",persona="unknown"`) {
		t.Errorf("an authorized call recorded as an unresolved principal\n--- body ---\n%s", body)
	}
}

func scrapePersonaMetrics(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scrape body: %v", err)
	}
	return string(body)
}
