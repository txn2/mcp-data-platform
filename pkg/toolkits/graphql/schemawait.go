package graphql

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/upstreamretry"
)

// defaultSchemaWait bounds how long a call waits for a connection whose first
// schema read is still in flight (#2707). A replica that has just started
// serves before its connections' schemas are read, and a scheduled script
// firing in those seconds would otherwise fail on a condition that clears by
// itself. The call's own deadline bounds the wait as well.
const defaultSchemaWait = 30 * time.Second

// schemaPoll is how often a waiting call looks again. Each look also reads
// the store, so a schema another replica already stored is taken at once.
const schemaPoll = 250 * time.Millisecond

// noSchemaHint is what a caller is told to do about a schema still being read.
const noSchemaHint = "The schema read finishes on its own; the same call is expected to pass once it has. " +
	"This is the endpoint's schema not being read yet, not a fault in the call."

// SetSchemaWait sets how long a call waits for a first schema read; zero or
// less restores the default. Tests shorten it.
func (t *Toolkit) SetSchemaWait(d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.schemaWait = d
}

func (t *Toolkit) schemaWaitBound() time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.schemaWait > 0 {
		return t.schemaWait
	}
	return defaultSchemaWait
}

// schemaPending waits for the named connection's first schema read, and
// answers with the upstream-unavailable refusal when it is still in flight at
// the bound. It returns nil when the connection holds a schema, when its read
// failed (the call reports that cause itself), and when there is no such
// connection (the call reports that too).
func (t *Toolkit) schemaPending(ctx context.Context, name string) *mcp.CallToolResult {
	if name == "" {
		return nil
	}
	// The held state first, with no store read: a connection holding its
	// schema (every call but the first seconds of a replica) costs nothing.
	if c, _, ok := t.serving(ctx, name); !ok || !readPending(c) {
		return nil
	}
	deadline := time.Now().Add(t.schemaWaitBound())
	for {
		if !time.Now().Before(deadline) || !sleepCtx(ctx, schemaPoll) {
			return pendingResult(name)
		}
		c, _, ok := t.current(ctx, name)
		if !ok || !readPending(c) {
			return nil
		}
	}
}

// pendingResult is the refusal for a schema still being read: the error
// envelope every tool carries, under the code a caller and a script's host
// read as the upstream being unavailable, so a script run it ends is the
// upstream's and retryable rather than the script's.
func pendingResult(name string) *mcp.CallToolResult {
	msg := noSchemaMessage(name, "")
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{
			Text: msg + "\n\nCode: " + upstreamretry.CodeUnavailable + "\nHint: " + noSchemaHint,
		}},
		StructuredContent: map[string]any{"error": map[string]any{
			"code": upstreamretry.CodeUnavailable, "category": upstreamretry.CategoryUnavailable,
			"message": msg, "hint": noSchemaHint,
		}},
	}
}

// readPending reports whether a connection holds no schema and records no
// failed read: its first read has not finished.
func readPending(c *conn) bool {
	c.schemaMu.RLock()
	defer c.schemaMu.RUnlock()
	return c.schema == nil && c.schemaErr == ""
}

// sleepCtx waits d, reporting false when ctx ends first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
