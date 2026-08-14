package middleware

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// This file pins the three middleware behaviors the managed-script source
// carries (#1283). All three are structural consequences of a script run being
// a per-run in-memory session with no model in it; none of them grants a script
// anything, which is the property that makes "no new authority" true rather
// than merely claimed.

// TestScriptSource_ExemptFromTheStatelessGates covers the first behavior: a
// script cannot perform the platform_info handshake and cannot perform the
// search-first discovery step, because there is no model in a script run to do
// either. It is exempted on exactly the same grounds as the portal and gateway
// shims, and the function still fails closed for anything unrecognized.
func TestScriptSource_ExemptFromTheStatelessGates(t *testing.T) {
	exempt := map[string]bool{
		SourceScript: true,
		SourceAdmin:  true,
		SourceREST:   true,
		SourceMCP:    false,
		"":           false,
		"inspector":  false,
	}
	for source, want := range exempt {
		if got := isStatelessShimSource(source); got != want {
			t.Errorf("isStatelessShimSource(%q) = %v, want %v", source, got, want)
		}
	}
}

// TestScriptSource_MintsAnIsolatedSessionID covers the second behavior: a script
// run keys its gate, provenance, and dedup state on an id of its own, so it can
// never advance or read the state of the person the run belongs to. The prefix
// is distinct from the portal one so an operator can separate the populations
// in audit rows without joining anything.
func TestScriptSource_MintsAnIsolatedSessionID(t *testing.T) {
	if !isIsolatedRunSource(SourceScript) || !isIsolatedRunSource(SourceAdmin) {
		t.Fatal("script and admin runs both need a minted per-run session id")
	}
	if isIsolatedRunSource(SourceMCP) || isIsolatedRunSource(SourceREST) || isIsolatedRunSource("") {
		t.Fatal("only the per-run in-memory sources mint an id")
	}

	scriptID, err := mintIsolatedRunSessionID(SourceScript)
	if err != nil {
		t.Fatalf("minting a script session id: %v", err)
	}
	if !strings.HasPrefix(scriptID, pkgsession.ScriptSessionPrefix) {
		t.Errorf("script session id %q lacks the script prefix", scriptID)
	}

	portalID, err := mintIsolatedRunSessionID(SourceAdmin)
	if err != nil {
		t.Fatalf("minting a portal session id: %v", err)
	}
	if !strings.HasPrefix(portalID, pkgsession.PortalSessionPrefix) {
		t.Errorf("portal session id %q lacks the portal prefix", portalID)
	}
	if pkgsession.IsHandle(scriptID) || pkgsession.IsHandle(portalID) {
		t.Error("a per-run id must never be mistaken for a platform_info handle")
	}

	second, err := mintIsolatedRunSessionID(SourceScript)
	if err != nil {
		t.Fatalf("minting a second script session id: %v", err)
	}
	if second == scriptID {
		t.Error("each run must get its own id")
	}
}

// TestScriptSource_DiscoveryScopeNeverKeysOnTheRunnersUser is the isolation
// property stated where it matters: the scope key of a script run is its own
// session, and on the degenerate no-id path it is empty rather than the user's.
func TestScriptSource_DiscoveryScopeNeverKeysOnTheRunnersUser(t *testing.T) {
	run := &PlatformContext{
		UserID: "jane", AuthType: AuthTypeOIDC, SessionID: "dpx_abc", Source: SourceScript,
	}
	if got, want := run.DiscoveryScopeKey(), "session:dpx_abc"; got != want {
		t.Errorf("script run DiscoveryScopeKey() = %q, want %q", got, want)
	}

	author := &PlatformContext{
		UserID: "jane", AuthType: AuthTypeOIDC, SessionID: "sess-1", Source: SourceMCP,
	}
	if run.DiscoveryScopeKey() == author.DiscoveryScopeKey() {
		t.Errorf("a script run collapsed onto its author's scope: %q", run.DiscoveryScopeKey())
	}

	degenerate := &PlatformContext{
		UserID: "jane", AuthType: AuthTypeOIDC, SessionID: "", Source: SourceScript,
	}
	if got := degenerate.DiscoveryScopeKey(); got != "" {
		t.Errorf("script run with no minted id = %q, want \"\" (ungateable, never user:jane)", got)
	}
}

// TestScriptSource_EnrichmentIsSkipped covers the third behavior. Enrichment
// appends context that varies with catalog state, which is precisely the
// variation the determinism contract promises a script will not see: the same
// script version on the same data must produce the same output, and a glossary
// edit is not a data change.
func TestScriptSource_EnrichmentIsSkipped(t *testing.T) {
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "rows"}}}
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "trino_query"}}

	scriptCtx := WithPlatformContext(context.Background(), &PlatformContext{Source: SourceScript})
	got, err := enrichToolResult(scriptCtx, &semanticEnricher{}, req, result)
	if err != nil {
		t.Fatalf("enrichToolResult: %v", err)
	}
	callResult, ok := got.(*mcp.CallToolResult)
	if !ok {
		t.Fatalf("expected a CallToolResult, got %T", got)
	}
	if len(callResult.Content) != 1 {
		t.Errorf("a script's result was modified: %d content blocks, want 1", len(callResult.Content))
	}
	if callResult.StructuredContent != nil {
		t.Error("a script's result must not gain synthesized structured content")
	}
}
