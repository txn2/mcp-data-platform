package gateway

import (
	"context"
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
}

// dial opens a client connection to the configured endpoint and returns a
// usable upstreamClient. The caller is responsible for calling Close.
func dial(ctx context.Context, cfg Config) (*upstreamClient, error) {
	httpClient := buildHTTPClient(cfg)

	client := mcp.NewClient(&mcp.Implementation{
		Name:    clientName,
		Version: clientVersion,
	}, nil)

	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   cfg.Endpoint,
		HTTPClient: httpClient,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", cfg.Endpoint, err)
	}

	return &upstreamClient{session: session, cfg: cfg}, nil
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
func buildHTTPClient(cfg Config) *http.Client {
	if cfg.AuthMode == AuthModeNone {
		return nil
	}
	return &http.Client{
		Transport: &authRoundTripper{
			mode:       cfg.AuthMode,
			credential: cfg.Credential,
			base:       http.DefaultTransport,
		},
	}
}

// authRoundTripper injects an outbound auth header on every request.
type authRoundTripper struct {
	mode       string
	credential string
	base       http.RoundTripper
}

// RoundTrip adds the configured auth header to req and delegates to the base.
func (a *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	switch a.mode {
	case AuthModeBearer:
		req.Header.Set("Authorization", "Bearer "+a.credential)
	case AuthModeAPIKey:
		req.Header.Set("X-API-Key", a.credential)
	}
	resp, err := a.base.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("round trip: %w", err)
	}
	return resp, nil
}
