package scriptrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SessionCaller issues a script's platform calls over one in-memory MCP session
// against the fully assembled server. It is the production Caller: every host
// binding a script invokes becomes an ordinary tool call, crossing the same
// authentication, authorization, gate, rate-limit, and audit middleware an
// agent's call crosses, with no second implementation to keep in step.
type SessionCaller struct {
	session *mcp.ClientSession
}

// Connect opens an in-memory MCP session against server and returns the Caller
// that drives it, plus the teardown for both ends.
//
// The identity the session authenticates as comes from ctx, which the caller
// has already decorated: a draft run carries its author's own identity, a
// platform run carries the script principal and the version author's captured roles.
// This function deliberately establishes no identity of its own — there is one
// place a script's authority is decided, and it is not here. label names the
// client in the handshake so the two run kinds are distinguishable in logs.
func Connect(ctx context.Context, server *mcp.Server, label string) (*SessionCaller, func(), error) {
	if server == nil {
		return nil, nil, errors.New("script execution is unavailable on this deployment")
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("opening a script session: %w", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: label, Version: "v1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		return nil, nil, fmt.Errorf("opening a script session: %w", err)
	}
	return &SessionCaller{session: session}, func() {
		_ = session.Close()
		_ = serverSession.Close()
	}, nil
}

// CallTool invokes one tool and returns its structured content.
func (c *SessionCaller) CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	res, err := c.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, fmt.Errorf("calling %s: %w", name, err)
	}
	if res.IsError {
		return nil, errors.New(firstText(res))
	}
	if structured, ok := res.StructuredContent.(map[string]any); ok {
		return structured, nil
	}
	// A tool that returns only a text block is still usable when that text is
	// the JSON envelope; parsing it here keeps a script working against a tool
	// that has not adopted structured output rather than failing on a shape
	// difference the author cannot do anything about.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(firstText(res)), &parsed); err != nil {
		return nil, fmt.Errorf("tool %s returned no structured result", name)
	}
	return parsed, nil
}

// firstText returns the first text block of a tool result, or a placeholder.
func firstText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && tc.Text != "" {
			return tc.Text
		}
	}
	return "the tool returned an error with no details"
}
