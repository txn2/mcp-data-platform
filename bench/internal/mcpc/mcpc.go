// Package mcpc wraps the official MCP SDK client for the benchmark harness: it
// connects an authenticated session over streamable HTTP, mints the platform's
// dps_ session handle via platform_info, lists the tools the arm's persona
// exposes (with measurement plumbing stripped), and threads the handle into
// every tool call so audit rows correlate to the run. Adapted from
// test/load/internal/mcpc with the session-handle layer added.
package mcpc

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/bench/internal/llm"
)

// sessionHandleArg is the tool-call argument the platform's session-handle
// middleware injects into every tool schema and expects threaded back. The
// harness owns this plumbing so it is invisible to the model and uniform
// across arms.
const sessionHandleArg = "session_id"

// infoToolName mints the handle and reports the platform version. It is
// excluded from the tool set shown to the model: it is platform
// infrastructure the harness calls, not a data tool under test.
const infoToolName = "platform_info"

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
		impl:     &mcp.Implementation{Name: "mcp-data-platform-bench", Version: "1.0.0"},
	}
}

// Connect establishes one MCP session (performing the initialize handshake).
// The caller must Close the returned session.
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

// SessionInfo is what the harness needs from platform_info.
type SessionInfo struct {
	// Handle is the dps_ session handle used to correlate audit rows.
	Handle string
	// PlatformVersion is the deployment's version string, for the manifest.
	PlatformVersion string
}

// infoPayload is the subset of the platform_info result the harness reads.
type infoPayload struct {
	SessionID string `json:"session_id"`
	Version   string `json:"version"`
}

// Mint calls platform_info and extracts the dps_ session handle and platform
// version. The handle is a tool-result value by design (the MCP spec removed
// the session header), read from structured content with a text-JSON fallback.
func Mint(ctx context.Context, s *mcp.ClientSession) (SessionInfo, error) {
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: infoToolName, Arguments: map[string]any{}})
	if err != nil {
		return SessionInfo{}, fmt.Errorf("platform_info: %w", err)
	}
	if res.IsError {
		return SessionInfo{}, fmt.Errorf("platform_info returned error: %s", FirstText(res))
	}
	var payload infoPayload
	if res.StructuredContent != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err == nil {
			_ = json.Unmarshal(raw, &payload)
		}
	}
	if payload.SessionID == "" {
		_ = json.Unmarshal([]byte(FirstText(res)), &payload)
	}
	if payload.SessionID == "" {
		return SessionInfo{}, fmt.Errorf("platform_info result carries no session_id (text: %.200s)", FirstText(res))
	}
	return SessionInfo{Handle: payload.SessionID, PlatformVersion: payload.Version}, nil
}

// ListTools returns the session's tools as adapter ToolDefs, dropping
// platform_info and stripping the injected session_id property from each input
// schema so the model never sees the measurement plumbing.
func ListTools(ctx context.Context, s *mcp.ClientSession) ([]llm.ToolDef, error) {
	res, err := s.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	out := make([]llm.ToolDef, 0, len(res.Tools))
	for _, t := range res.Tools {
		if t.Name == infoToolName {
			continue
		}
		schema, err := stripSessionArg(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("tool %s: %w", t.Name, err)
		}
		out = append(out, llm.ToolDef{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	return out, nil
}

// stripSessionArg removes the session_id property (and its required-list
// entry) from a tool input schema.
func stripSessionArg(schema any) (json.RawMessage, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal input schema: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse input schema: %w", err)
	}
	if props, ok := m["properties"].(map[string]any); ok {
		delete(props, sessionHandleArg)
	}
	if req, ok := m["required"].([]any); ok {
		kept := make([]any, 0, len(req))
		for _, r := range req {
			if s, ok := r.(string); ok && s == sessionHandleArg {
				continue
			}
			kept = append(kept, r)
		}
		m["required"] = kept
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("re-marshal input schema: %w", err)
	}
	return out, nil
}

// CallResult reports the outcome of one tool call.
type CallResult struct {
	// TransportErr is a protocol/transport failure; the call did not complete
	// and produces no audit row.
	TransportErr error
	// ToolErr is true when the call completed but the tool returned isError.
	ToolErr bool
	// ErrorCode is the platform error contract's stable code from the result's
	// structured error envelope ("unauthorized", "search_required", ...), or
	// "" when the result carries none. The pipeline uses it to tell platform
	// refusals (short-circuited outer to the audit middleware, so no audit
	// row) from handler-level tool errors (audited).
	ErrorCode string
	// Text is the concatenation of all text content blocks.
	Text string
}

// errorEnvelope mirrors pkg/middleware's structured error payload.
type errorEnvelope struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

// Call invokes a tool, injecting the session handle. It never returns a Go
// error directly; inspect the CallResult.
func Call(ctx context.Context, s *mcp.ClientSession, name string, args map[string]any, handle string) CallResult {
	sent := make(map[string]any, len(args)+1)
	maps.Copy(sent, args)
	if handle != "" {
		sent[sessionHandleArg] = handle
	}
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: sent})
	if err != nil {
		return CallResult{TransportErr: err}
	}
	return CallResult{ToolErr: res.IsError, ErrorCode: errorCode(res), Text: allText(res)}
}

// PreAuditRefusal reports whether a structured error code marks a platform
// refusal issued outer to the audit middleware (so it leaves no audit row):
// authentication, session, workflow-gate, setup, and rate-limit refusals. The
// episode loops use it to bound the audit read-back (a refused call cannot have
// a row), and the reviewer promote path uses it to classify a refused
// apply_knowledge as a harness failure rather than a measured miss. One shared
// copy keeps every path's classification identical.
func PreAuditRefusal(code string) bool {
	switch code {
	case "unauthenticated", "unauthorized", "session_required", "session_expired",
		"search_required", "setup_required", "rate_limited":
		return true
	}
	return false
}

// errorCode extracts the structured error code from a tool result, if any.
func errorCode(res *mcp.CallToolResult) string {
	if res == nil || !res.IsError || res.StructuredContent == nil {
		return ""
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return ""
	}
	var env errorEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return ""
	}
	return env.Error.Code
}

// FirstText returns the first text content block of a tool result, or "".
func FirstText(res *mcp.CallToolResult) string {
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

// allText joins every text content block, preserving order.
func allText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}
