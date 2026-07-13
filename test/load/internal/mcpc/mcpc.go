// Package mcpc wraps the official MCP SDK client for the load harness: it
// connects an authenticated session over streamable HTTP and exposes a
// tool-call helper that classifies transport errors and tool-level errors
// distinctly. Generic HTTP load tools cannot exercise this path (the MCP
// initialize handshake, session, and tools/call framing), which is why the
// harness is written in Go against the same SDK the platform ships.
package mcpc

import (
	"context"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Client connects MCP sessions to a target endpoint using a shared,
// authenticated HTTP client.
type Client struct {
	endpoint string
	http     *http.Client
	impl     *mcp.Implementation
}

// New returns a Client that dials endpoint (the platform's MCP base URL) with
// the supplied authenticated HTTP client.
func New(endpoint string, httpClient *http.Client) *Client {
	return &Client{
		endpoint: endpoint,
		http:     httpClient,
		impl:     &mcp.Implementation{Name: "mcp-data-platform-loadgen", Version: "1.0.0"},
	}
}

// Connect establishes one MCP session (performing the initialize handshake).
// The caller must Close the returned session. DisableStandaloneSSE is set so
// each session is a single request/response channel — the harness never waits
// on server-initiated notifications, and the standalone GET stream would
// otherwise hold an extra connection per session.
func (c *Client) Connect(ctx context.Context) (*mcp.ClientSession, error) {
	client := mcp.NewClient(c.impl, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             c.endpoint,
		HTTPClient:           c.http,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp connect: %w", err)
	}
	return session, nil
}

// CallResult reports the outcome of one tool call.
type CallResult struct {
	// TransportErr is a protocol/transport failure (connection, framing). Non-nil
	// means the call did not complete.
	TransportErr error
	// ToolErr is true when the call completed but the tool returned isError.
	ToolErr bool
	// Text is the first text content block, if any (for debugging / seeding).
	Text string
}

// Err returns a non-nil error when the call failed for any reason (transport or
// tool-level), so a recorder can classify the sample as an error.
func (r CallResult) Err() error {
	if r.TransportErr != nil {
		return r.TransportErr
	}
	if r.ToolErr {
		return fmt.Errorf("tool returned error: %s", r.Text)
	}
	return nil
}

// Call invokes a tool and returns a classified result. It never returns a Go
// error directly; inspect CallResult.Err.
func Call(ctx context.Context, s *mcp.ClientSession, name string, args map[string]any) CallResult {
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return CallResult{TransportErr: err}
	}
	return CallResult{ToolErr: res.IsError, Text: firstText(res)}
}

// firstText returns the first text content block of a tool result, or "".
func firstText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
