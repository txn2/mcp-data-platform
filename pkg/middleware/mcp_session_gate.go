package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrCategorySetupRequired is the error category for session gate violations.
const ErrCategorySetupRequired = "setup_required"

// defaultGateSessionTTL is the default TTL for session gate entries.
const defaultGateSessionTTL = 30

// SessionGateConfig configures the session initialization gate middleware.
type SessionGateConfig struct {
	// InitTool is the tool that initializes the session (e.g., "platform_info").
	InitTool string

	// ExemptTools lists tool names that bypass the gate.
	ExemptTools []string

	// SessionTTL is how long an initialized session is remembered.
	// Defaults to 30 minutes.
	SessionTTL time.Duration

	// CleanupInterval is how often the cleanup routine runs.
	// Defaults to 1 minute.
	CleanupInterval time.Duration
}

// SessionGate tracks which sessions have called the init tool.
// It is safe for concurrent use.
type SessionGate struct {
	mu         sync.RWMutex
	sessions   map[string]time.Time // session ID → initialization time
	initTool   string
	exemptSet  map[string]bool
	sessionTTL time.Duration
	done       chan struct{}
	gateCount  int64 // total gating violations
	retryCount int64 // total successful retries (init after gate)
}

// NewSessionGate creates a new session gate tracker.
func NewSessionGate(cfg SessionGateConfig) *SessionGate {
	ttl := cfg.SessionTTL
	if ttl == 0 {
		ttl = defaultGateSessionTTL * time.Minute
	}

	exemptSet := make(map[string]bool, len(cfg.ExemptTools)+1)
	// The init tool itself is always exempt.
	exemptSet[cfg.InitTool] = true
	for _, t := range cfg.ExemptTools {
		exemptSet[t] = true
	}

	return &SessionGate{
		sessions:   make(map[string]time.Time),
		initTool:   cfg.InitTool,
		exemptSet:  exemptSet,
		sessionTTL: ttl,
		done:       make(chan struct{}),
	}
}

// RecordInit marks a session as initialized.
func (g *SessionGate) RecordInit(sessionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	_, existed := g.sessions[sessionID]
	g.sessions[sessionID] = time.Now()

	if existed {
		g.retryCount++
	}
}

// IsInitialized returns true if the session has called the init tool.
func (g *SessionGate) IsInitialized(sessionID string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	initTime, ok := g.sessions[sessionID]
	if !ok {
		return false
	}
	// Check TTL expiration.
	return time.Since(initTime) < g.sessionTTL
}

// IsExempt returns true if the tool bypasses the gate.
func (g *SessionGate) IsExempt(toolName string) bool {
	return g.exemptSet[toolName]
}

// IncrementGateCount increments the gating violation counter and returns the new count.
func (g *SessionGate) IncrementGateCount() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gateCount++
	return g.gateCount
}

// Stats returns current gate statistics.
func (g *SessionGate) Stats() (gateViolations, retries, activeSessions int64) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.gateCount, g.retryCount, int64(len(g.sessions))
}

// StartCleanup starts a background goroutine that evicts expired sessions.
func (g *SessionGate) StartCleanup(interval time.Duration) {
	if interval == 0 {
		interval = 1 * time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-g.done:
				return
			case <-ticker.C:
				g.cleanup()
			}
		}
	}()
}

// Stop stops the background cleanup goroutine.
func (g *SessionGate) Stop() {
	close(g.done)
}

// cleanup evicts sessions that have expired.
func (g *SessionGate) cleanup() {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now()
	for id, initTime := range g.sessions {
		if now.Sub(initTime) > g.sessionTTL {
			delete(g.sessions, id)
		}
	}
}

// MCPSessionGateMiddleware creates MCP protocol-level middleware that gates
// all tool calls until the init tool (e.g., platform_info) has been called
// in the current session.
//
// This middleware must be positioned INNER to MCPToolCallMiddleware so that
// PlatformContext (with SessionID and ToolName) is available. It should be
// positioned OUTER to rule enforcement and enrichment so that gated calls
// never reach those layers.
func MCPSessionGateMiddleware(gate *SessionGate) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodToolsCall {
				return next(ctx, method, req)
			}
			pc := GetPlatformContext(ctx)
			if pc == nil {
				return next(ctx, method, req)
			}
			if errResult := gate.checkAccess(pc); errResult != nil {
				return errResult, nil
			}
			return next(ctx, method, req)
		}
	}
}

// checkAccess evaluates whether a tool call should proceed or be gated.
// Returns nil if the call is allowed; returns an error result if gated.
func (g *SessionGate) checkAccess(pc *PlatformContext) mcp.Result {
	// The init tool itself records initialization and is always allowed.
	if pc.ToolName == g.initTool {
		g.RecordInit(pc.SessionID)
		return nil
	}

	// Exempt tools bypass the gate.
	if g.IsExempt(pc.ToolName) {
		return nil
	}

	// Initialized sessions proceed normally.
	if g.IsInitialized(pc.SessionID) {
		return nil
	}

	// Gate violation: session not initialized.
	count := g.IncrementGateCount()
	slog.Warn("session gate: tool called before platform_info",
		"tool", pc.ToolName,
		"session_id", pc.SessionID,
		"user_id", pc.UserID,
		"total_violations", count,
	)
	return createSessionGateError(g.initTool, pc.ToolName)
}

// createSessionGateError builds a SETUP_REQUIRED error result.
func createSessionGateError(initTool, blockedTool string) mcp.Result {
	msg := fmt.Sprintf(
		"SETUP_REQUIRED: You must call %s before using %s (or any other tool). "+
			"%s contains critical agent instructions for query routing, operational rules, "+
			"and platform capabilities. Call %s first, then retry your request.",
		initTool, blockedTool, initTool, initTool,
	)

	result := &mcp.CallToolResult{}
	result.SetError(&PlatformError{
		Category: ErrCategorySetupRequired,
		Message:  msg,
	})
	return result
}
