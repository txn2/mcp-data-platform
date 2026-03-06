package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// provenanceContextKey is the context key for provenance tool calls.
type provenanceContextKey struct{}

// ProvenanceToolCall records a single tool invocation for provenance tracking.
type ProvenanceToolCall struct {
	ToolName  string `json:"tool_name"`
	Timestamp string `json:"timestamp"`
	Summary   string `json:"summary,omitempty"`
}

// WithProvenanceToolCalls adds provenance tool calls to the context.
func WithProvenanceToolCalls(ctx context.Context, calls []ProvenanceToolCall) context.Context {
	return context.WithValue(ctx, provenanceContextKey{}, calls)
}

// GetProvenanceToolCalls retrieves provenance tool calls from the context.
func GetProvenanceToolCalls(ctx context.Context) []ProvenanceToolCall {
	if calls, ok := ctx.Value(provenanceContextKey{}).([]ProvenanceToolCall); ok {
		return calls
	}
	return nil
}

// maxCallsPerSession caps provenance buffer per session to prevent unbounded growth.
const maxCallsPerSession = 100

// ProvenanceTracker accumulates tool call records per session.
type ProvenanceTracker struct {
	mu       sync.Mutex
	sessions map[string][]ProvenanceToolCall
}

// NewProvenanceTracker creates a new provenance tracker.
func NewProvenanceTracker() *ProvenanceTracker {
	return &ProvenanceTracker{
		sessions: make(map[string][]ProvenanceToolCall),
	}
}

// Record adds a tool call to a session's provenance buffer.
// Empty session IDs are ignored to prevent cross-user provenance mixing.
// Each session is capped at maxCallsPerSession entries (oldest are evicted).
func (pt *ProvenanceTracker) Record(sessionID, toolName string, params map[string]any) {
	if sessionID == "" {
		slog.Debug("provenance: skipping record for empty session ID", "tool", toolName)
		return
	}
	slog.Debug("provenance.record", "session_id", sessionID, "tool", toolName)

	pt.mu.Lock()
	defer pt.mu.Unlock()

	call := ProvenanceToolCall{
		ToolName:  toolName,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Summary:   summarizeParams(params),
	}

	buf := pt.sessions[sessionID]
	buf = append(buf, call)
	if len(buf) > maxCallsPerSession {
		buf = buf[len(buf)-maxCallsPerSession:]
	}
	pt.sessions[sessionID] = buf
}

// Harvest returns and clears the accumulated provenance for a session.
func (pt *ProvenanceTracker) Harvest(sessionID string) []ProvenanceToolCall {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	calls := pt.sessions[sessionID]
	delete(pt.sessions, sessionID)
	slog.Info("provenance.harvest", "session_id", sessionID, "count", len(calls))
	return calls
}

// CleanupBefore removes all sessions with no activity since the given cutoff time.
func (pt *ProvenanceTracker) CleanupBefore(cutoff time.Time) int {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	cutoffStr := cutoff.UTC().Format(time.RFC3339)
	removed := 0
	for sid, calls := range pt.sessions {
		if len(calls) == 0 || calls[len(calls)-1].Timestamp < cutoffStr {
			delete(pt.sessions, sid)
			removed++
		}
	}
	return removed
}

// maxSummaryLength caps the parameter summary string length.
const maxSummaryLength = 200

// summarizeParams creates a brief summary of tool call parameters.
func summarizeParams(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}
	data, err := json.Marshal(params)
	if err != nil {
		return ""
	}
	s := string(data)
	if len(s) > maxSummaryLength {
		s = s[:maxSummaryLength] + "..."
	}
	return s
}

// MCPProvenanceMiddleware tracks tool calls per session and injects
// accumulated provenance into the context when save_artifact is called.
func MCPProvenanceMiddleware(tracker *ProvenanceTracker, saveToolName string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodToolsCall {
				return next(ctx, method, req)
			}

			toolName, err := extractToolName(req)
			if err != nil {
				return next(ctx, method, req)
			}

			pc := GetPlatformContext(ctx)
			sessionID := ""
			if pc != nil {
				sessionID = pc.SessionID
			} else {
				slog.Warn("provenance: PlatformContext missing, cannot track tool call", "tool", toolName)
			}

			if toolName == saveToolName {
				calls := tracker.Harvest(sessionID)
				ctx = WithProvenanceToolCalls(ctx, calls)
				return next(ctx, method, req)
			}

			params := extractToolParams(req)
			tracker.Record(sessionID, toolName, params)

			return next(ctx, method, req)
		}
	}
}

// extractToolParams extracts tool parameters from a tools/call request.
func extractToolParams(req mcp.Request) map[string]any {
	params := req.GetParams()
	if params == nil {
		return nil
	}
	callParams, ok := params.(*mcp.CallToolParamsRaw)
	if !ok || callParams == nil {
		return nil
	}
	if callParams.Arguments == nil {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal(callParams.Arguments, &result); err != nil {
		return nil
	}
	return result
}
