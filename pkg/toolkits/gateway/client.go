package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	clientName    = "mcp-data-platform-gateway"
	clientVersion = "0.1.0"
)

// upstreamClient wraps an outbound MCP client session against a single
// remote MCP server. It is safe for concurrent CallTool invocations because
// the SDK's ClientSession handles that internally.
type upstreamClient struct {
	session *mcp.ClientSession
	cfg     Config
	// oauth is non-nil when AuthMode == AuthModeOAuth. It backs the
	// authRoundTripper's bearer token and feeds the OAuth status endpoint.
	oauth *oauthTokenSource
}

// dial opens a client connection to the configured endpoint and returns a
// usable upstreamClient. The caller is responsible for calling Close.
// The optional store is used by authorization_code OAuth grants to load
// and persist refresh tokens; pass nil for client_credentials and for
// non-OAuth modes.
func dial(ctx context.Context, cfg Config, connection string, store TokenStore) (*upstreamClient, error) {
	var oauth *oauthTokenSource
	if cfg.AuthMode == AuthModeOAuth {
		oauth = newOAuthTokenSource(cfg.OAuth, connection, store)
	}
	httpClient := buildHTTPClient(cfg, oauth)

	client := mcp.NewClient(&mcp.Implementation{
		Name:    clientName,
		Version: clientVersion,
	}, nil)

	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   cfg.Endpoint,
		HTTPClient: httpClient,
		// DisableStandaloneSSE: do NOT open a long-poll GET against the
		// upstream's Streamable HTTP endpoint after initialize.
		//
		// Per MCP spec §2.2.3, after a successful initialize the client MAY
		// open a `GET /` with `Accept: text/event-stream` so the server can
		// push asynchronous notifications (tools/list_changed, progress,
		// etc.). The go-sdk does this SYNCHRONOUSLY inside Client.Connect's
		// sessionUpdated() callback BEFORE sending notifications/initialized.
		//
		// When the upstream is fronted by Cloudflare (or any proxy with
		// response buffering), the proxy buffers the SSE response waiting
		// for the upstream to produce data. If the upstream has nothing to
		// push (the steady state for most servers), the proxy holds the
		// connection open until ITS timeout — Cloudflare returns HTTP 524
		// after ~100s. The synchronous Connect() then takes the full 100s+
		// to return, and our 10s dial context has long since expired by
		// the time it gets to send notifications/initialized — which then
		// surfaces as the misleading "round trip: context deadline exceeded
		// on notifications/initialized" error.
		//
		// We don't currently consume server-pushed notifications in the
		// gateway forwarder — every tool call is a synchronous request /
		// response. Disabling the standalone SSE eliminates the proxy hang
		// without losing functionality. If we add streaming-tool support
		// later, revisit this and either negotiate with the operator's
		// proxy config (disable buffering for /api/mcp) or open the SSE
		// stream lazily on demand.
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", cfg.Endpoint, err)
	}

	return &upstreamClient{session: session, cfg: cfg, oauth: oauth}, nil
}

// listTools fetches the current tool catalog from the upstream.
func (u *upstreamClient) listTools(ctx context.Context) ([]*mcp.Tool, error) {
	res, err := u.session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	return res.Tools, nil
}

// callTool forwards a call to the upstream, returning the raw result.
func (u *upstreamClient) callTool(ctx context.Context, name string, args any) (*mcp.CallToolResult, error) {
	res, err := u.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("call tool %s: %w", name, err)
	}
	return res, nil
}

// close terminates the upstream session.
func (u *upstreamClient) close() error {
	if u == nil || u.session == nil {
		return nil
	}
	if err := u.session.Close(); err != nil {
		return fmt.Errorf("close upstream session: %w", err)
	}
	return nil
}

// buildHTTPClient constructs an HTTP client with the configured auth scheme.
// For AuthModeNone it returns nil, letting the SDK use its default client.
// The oauth source is required when AuthMode is "oauth" and ignored
// otherwise; pass nil for the non-OAuth modes.
func buildHTTPClient(cfg Config, oauth *oauthTokenSource) *http.Client {
	if cfg.AuthMode == AuthModeNone {
		return nil
	}
	return &http.Client{
		Transport: &authRoundTripper{
			mode:        cfg.AuthMode,
			credential:  cfg.Credential,
			tokenSource: oauth,
			base:        http.DefaultTransport,
		},
	}
}

// authRoundTripper injects an outbound auth header on every request.
type authRoundTripper struct {
	mode        string
	credential  string
	tokenSource *oauthTokenSource
	base        http.RoundTripper
}

// RoundTrip adds the configured auth header to req and delegates to the base.
func (a *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if err := a.applyAuth(req); err != nil {
		return nil, err
	}
	resp, err := a.base.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("round trip: %w", err)
	}
	return resp, nil
}

// applyAuth sets the appropriate header on req. For OAuth, this lazily
// fetches/refreshes the token; failures here surface as transport errors
// to the MCP client, which the gateway forwarder then attributes back as
// upstream:<connection>: errors in the audit log.
func (a *authRoundTripper) applyAuth(req *http.Request) error {
	switch a.mode {
	case AuthModeBearer:
		req.Header.Set("Authorization", "Bearer "+a.credential)
	case AuthModeAPIKey:
		req.Header.Set("X-API-Key", a.credential)
	case AuthModeOAuth:
		if a.tokenSource == nil {
			return errors.New("oauth: token source not configured")
		}
		token, err := a.tokenSource.Token(req.Context())
		if err != nil {
			return fmt.Errorf("oauth: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return nil
}
