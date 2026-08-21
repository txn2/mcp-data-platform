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
// It returns the Caller interface rather than the concrete SessionCaller
// because that is the whole of what a run does with it: both call sites hand
// the result straight to Options.Caller. Naming the interface here is what
// makes the relationship between the session plumbing and the engine legible —
// implementing an interface is invisible in Go's reference graph, and a
// SessionCaller nothing visibly connects to the engine reads as a second
// package sharing an import path.
//
// The identity the session authenticates as comes from ctx, which the caller
// has already decorated: a draft run carries its author's own identity, a
// platform run carries the script principal and the version author's captured roles.
// This function deliberately establishes no identity of its own — there is one
// place a script's authority is decided, and it is not here. label names the
// client in the handshake so the two run kinds are distinguishable in logs.
func Connect(ctx context.Context, server *mcp.Server, label string) (Caller, func(), error) {
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
	// textOf, not firstText: firstText substitutes an error PLACEHOLDER for a
	// result with no text, which is right for a failure and wrong here. A
	// successful call whose result carries no text block — an upstream that
	// answered with image content, or an empty string — must not hand the
	// script "the tool returned an error with no details" as data it will then
	// log or act on.
	text := textOf(res)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err == nil {
		return parsed, nil
	}
	// Anything else — plain prose, a JSON array, a gateway-proxied upstream
	// result that carries no structured content of its own — is handed over as
	// TextResultKey rather than failing the run. Since #1419 a script calls any
	// tool its author can call, and a tool whose answer is text is a tool an
	// author would reach for; refusing it here would make "any tool" false for
	// a class of them, and would report a call that SUCCEEDED as a failure.
	return map[string]any{TextResultKey: text}, nil
}

// firstText returns the first text block of a FAILED tool result, or a
// placeholder, so a refusal always reaches the author as a sentence.
func firstText(res *mcp.CallToolResult) string {
	if text := textOf(res); text != "" {
		return text
	}
	return "the tool returned an error with no details"
}

// textOf returns the first non-empty text block of a tool result, or "". It is
// the success path's reading: a result that carried no text carried no text,
// and saying so with an empty string is the only honest answer.
func textOf(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && tc.Text != "" {
			return tc.Text
		}
	}
	return ""
}
