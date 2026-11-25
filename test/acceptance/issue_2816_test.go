//go:build integration

package acceptance

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #2816: a SESSION_EXPIRED refusal says why: the handle expired, is one
// this deployment never issued, or belongs to another identity. For an
// expired or unknown handle of an authenticated caller it carries a new
// session_id for that caller, so the refused write is resent in one step;
// another identity's handle gets none. Each refusal's audit row records its
// reason.
//
// Wire forms: memory_capture's session_id, content and type are strings, sent
// as strings; the new session_id is read from the refusal's structuredContent
// and its text. No parameter touched admits a second form.

// issue2816Capture calls memory_capture with exactly the handle given.
func issue2816Capture(t *testing.T, c *client, handle, content string) (*mcp.CallToolResult, string) {
	t.Helper()
	res, err := c.session.CallTool(c.ctx, &mcp.CallToolParams{Name: "memory_capture", Arguments: map[string]any{
		"session_id": handle, "content": content, "type": "personal_preference",
	}})
	if err != nil {
		t.Fatalf("memory_capture: transport error: %v", err)
	}
	var b strings.Builder
	for _, ct := range res.Content {
		if tc, ok := ct.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return res, b.String()
}

// issue2816Fresh is the session_id a refusal carries, "" for none.
func issue2816Fresh(res *mcp.CallToolResult) string {
	sc, _ := res.StructuredContent.(map[string]any)
	s, _ := sc["session_id"].(string)
	return s
}

// TestIssue2816_AnExpiredHandleIsAnsweredWithANewOne: a capture with a handle
// past its lifetime is refused with reason expired and a new session_id;
// resending with it succeeds.
func TestIssue2816_AnExpiredHandleIsAnsweredWithANewOne(t *testing.T) {
	c := connect(t)
	old := c.sessionID
	if _, err := devDB(t).ExecContext(context.Background(), `UPDATE sessions SET expires_at = NOW() - interval '1 minute' WHERE id = $1`, old); err != nil {
		t.Fatalf("expiring the handle: %v", err)
	}
	res, text := issue2816Capture(t, c, old, "Acceptance #2816: I prefer weekly summaries.")
	if !res.IsError || !strings.Contains(text, "SESSION_EXPIRED (reason: expired)") {
		t.Fatalf("the expired handle was not refused as expired: %s", text)
	}
	fresh := issue2816Fresh(res)
	if fresh == "" || fresh == old || !strings.Contains(text, fresh) {
		t.Fatalf("the refusal carries no new session_id: %q %s", fresh, text)
	}
	if res, text := issue2816Capture(t, c, fresh, "Acceptance #2816: I prefer weekly summaries."); res.IsError {
		t.Errorf("resending with the new handle was refused: %s", text)
	}
	issue2816AuditReason(t, "expired")
}

// TestIssue2816_AnUnknownHandleSaysSo: a handle never issued here is refused
// with reason unknown, saying a carried-over id is not valid, with a new one.
func TestIssue2816_AnUnknownHandleSaysSo(t *testing.T) {
	c := connect(t)
	res, text := issue2816Capture(t, c, "dps_neverissued2816000000000000", "Acceptance #2816.")
	if !res.IsError || !strings.Contains(text, "reason: unknown") || !strings.Contains(text, "earlier conversation") || issue2816Fresh(res) == "" {
		t.Fatalf("unknown handle: %s", text)
	}
	issue2816AuditReason(t, "unknown")
}

// TestIssue2816_AnotherIdentitysHandleGetsNone: a handle minted for the
// analyst, presented by the administrator, is refused with reason
// other_identity and no new handle.
func TestIssue2816_AnotherIdentitysHandleGetsNone(t *testing.T) {
	theirs := connectAs(t, "acme-analyst-key").sessionID
	res, text := issue2816Capture(t, connect(t), theirs, "Acceptance #2816.")
	if !res.IsError || !strings.Contains(text, "reason: other_identity") {
		t.Fatalf("another identity's handle: %s", text)
	}
	if fresh := issue2816Fresh(res); fresh != "" || strings.Contains(text, "new session_id") {
		t.Errorf("another identity's handle was answered with a new one: %q", fresh)
	}
	issue2816AuditReason(t, "other_identity")
}

// issue2816AuditReason waits for the newest SESSION_EXPIRED audit row to
// record reason.
func issue2816AuditReason(t *testing.T, reason string) {
	t.Helper()
	db := devDB(t)
	waitFor(t, "an audit row recording reason "+reason, func() (bool, string) {
		var msg string
		err := db.QueryRowContext(context.Background(), `SELECT error_message FROM audit_logs
			WHERE tool_name = 'memory_capture' AND error_message LIKE 'SESSION_EXPIRED%'
			ORDER BY timestamp DESC LIMIT 1`).Scan(&msg)
		if err != nil {
			return false, err.Error()
		}
		return strings.Contains(msg, "(reason: "+reason+")"), msg
	})
}
