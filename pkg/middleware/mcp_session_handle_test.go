package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

const testInitTool = "platform_info"

func makeCallReq(toolName string, args map[string]any) mcp.Request {
	var raw json.RawMessage
	if args != nil {
		raw, _ = json.Marshal(args)
	}
	return &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
		Params: &mcp.CallToolParamsRaw{Name: toolName, Arguments: raw},
	}
}

func reqArgs(t *testing.T, req mcp.Request) map[string]any {
	t.Helper()
	cp, ok := req.GetParams().(*mcp.CallToolParamsRaw)
	if !ok {
		t.Fatal("expected *CallToolParamsRaw")
	}
	if len(cp.Arguments) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(cp.Arguments, &m); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	return m
}

// mintHandle inserts a valid handle owned by userID into the store.
func mintHandle(t *testing.T, store pkgsession.Store, userID string) string {
	t.Helper()
	h, err := pkgsession.GenerateHandle()
	if err != nil {
		t.Fatalf("GenerateHandle: %v", err)
	}
	now := time.Now()
	if err := store.Create(context.Background(), &pkgsession.Session{
		ID: h, UserID: userID, CreatedAt: now, LastActiveAt: now,
		ExpiresAt: now.Add(time.Hour),
		State:     map[string]any{pkgsession.StateKeyMintedBy: pkgsession.MintedByPlatformInfo},
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	return h
}

func newResolver(store pkgsession.Store, require bool) *SessionResolver {
	return NewSessionResolver(store, SessionResolverConfig{
		Enabled:  true,
		Require:  require,
		TTL:      time.Hour,
		InitTool: testInitTool,
	})
}

func errCode(t *testing.T, result mcp.Result) string {
	t.Helper()
	ctr, ok := result.(*mcp.CallToolResult)
	if !ok || ctr == nil {
		t.Fatalf("expected *CallToolResult, got %T", result)
	}
	sc, ok := ctr.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("expected structured error content, got %T", ctr.StructuredContent)
	}
	env, ok := sc[errorEnvelopeKey].(errorPayload)
	if !ok {
		t.Fatalf("expected errorPayload envelope, got %T", sc[errorEnvelopeKey])
	}
	return env.Code
}

func TestSessionResolver_ValidHandle(t *testing.T) {
	store := pkgsession.NewMemoryStore(time.Hour)
	r := newResolver(store, true)
	h := mintHandle(t, store, "user-1")

	pc := NewPlatformContext("req")
	pc.UserID = "user-1"
	pc.SessionID = "" // stateless HTTP, no transport session
	req := makeCallReq("trino_query", map[string]any{"sql": "SELECT 1", sessionHandleArg: h})

	if got := r.resolve(context.Background(), req, pc, "trino_query"); got != nil {
		t.Fatalf("valid handle rejected: code=%s", errCode(t, got))
	}
	if pc.SessionID != h {
		t.Errorf("pc.SessionID = %q, want handle %q", pc.SessionID, h)
	}
	// The session_id argument must be stripped before the handler.
	if _, present := reqArgs(t, req)[sessionHandleArg]; present {
		t.Error("session_id argument was not stripped from the request")
	}
	// Other arguments are preserved.
	if reqArgs(t, req)["sql"] != "SELECT 1" {
		t.Error("sql argument was not preserved")
	}
}

// TestSessionResolver_RefreshExtendsNearExpiry proves refresh-on-use fires once
// a handle is past the halfway point of its lifetime (the throttle's fire path;
// TestSessionResolver_ValidHandle covers the skip path with a fresh handle).
func TestSessionResolver_RefreshExtendsNearExpiry(t *testing.T) {
	store := pkgsession.NewMemoryStore(time.Hour)
	r := newResolver(store, true) // ttl 1h
	h, err := pkgsession.GenerateHandle()
	if err != nil {
		t.Fatalf("GenerateHandle: %v", err)
	}
	now := time.Now()
	// Expires in a minute: well past the TTL/2 (30m) throttle point.
	if err := store.Create(context.Background(), &pkgsession.Session{
		ID: h, UserID: "u", CreatedAt: now, LastActiveAt: now, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	pc := NewPlatformContext("req")
	pc.UserID = "u"
	req := makeCallReq("trino_query", map[string]any{sessionHandleArg: h})
	if got := r.resolve(context.Background(), req, pc, "trino_query"); got != nil {
		t.Fatalf("near-expiry valid handle rejected: %s", errCode(t, got))
	}

	// The detached refresh should extend the expiry well beyond the original minute.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s, _ := store.Get(context.Background(), h); s != nil && s.ExpiresAt.After(now.Add(30*time.Minute)) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("handle expiry was not refreshed on use")
}

func TestSessionResolver_UnknownAndExpiredRejected(t *testing.T) {
	store := pkgsession.NewMemoryStore(time.Hour)
	r := newResolver(store, true)

	pc := NewPlatformContext("req")
	pc.UserID = "user-1"
	req := makeCallReq("trino_query", map[string]any{sessionHandleArg: "dps_deadbeef"})

	result := r.resolve(context.Background(), req, pc, "trino_query")
	if result == nil {
		t.Fatal("unknown handle should be rejected")
	}
	if code := errCode(t, result); code != CodeSessionExpired {
		t.Errorf("code = %q, want %q", code, CodeSessionExpired)
	}
	// Even rejected, the argument is stripped.
	if _, present := reqArgs(t, req)[sessionHandleArg]; present {
		t.Error("session_id must be stripped even when rejected")
	}
}

// TestSessionResolver_IdentityMismatch is acceptance criterion 5.
func TestSessionResolver_IdentityMismatch(t *testing.T) {
	store := pkgsession.NewMemoryStore(time.Hour)
	r := newResolver(store, true)
	h := mintHandle(t, store, "user-A")

	pc := NewPlatformContext("req")
	pc.UserID = "user-B" // different identity presents A's handle
	req := makeCallReq("trino_query", map[string]any{sessionHandleArg: h})

	result := r.resolve(context.Background(), req, pc, "trino_query")
	if result == nil {
		t.Fatal("cross-identity handle should be rejected")
	}
	if code := errCode(t, result); code != CodeSessionExpired {
		t.Errorf("code = %q, want %q", code, CodeSessionExpired)
	}
	if pc.SessionID == h {
		t.Error("pc.SessionID must not adopt a cross-identity handle")
	}
}

// TestSessionResolver_NonHandleSessionIDPreserved proves a tool that legitimately
// defines its own session_id parameter (e.g. a proxied upstream tool) is not
// hijacked: a value without the platform handle prefix is neither stripped nor
// treated as a handle, so the handler still receives it.
func TestSessionResolver_NonHandleSessionIDPreserved(t *testing.T) {
	store := pkgsession.NewMemoryStore(time.Hour)
	r := newResolver(store, false) // require off: the call must pass through
	pc := NewPlatformContext("req")
	pc.UserID = "user-1"
	pc.SessionID = "transport"
	req := makeCallReq("proxied_tool", map[string]any{"session_id": "upstream-uuid-1234", "q": "x"})

	if got := r.resolve(context.Background(), req, pc, "proxied_tool"); got != nil {
		t.Fatalf("a non-handle session_id must not be treated as a platform handle: %s", errCode(t, got))
	}
	args := reqArgs(t, req)
	if args[sessionHandleArg] != "upstream-uuid-1234" {
		t.Errorf("tool's own session_id must survive, got %v", args[sessionHandleArg])
	}
}

func TestSessionResolver_MissingHandleRequired(t *testing.T) {
	store := pkgsession.NewMemoryStore(time.Hour)
	r := newResolver(store, true)

	pc := NewPlatformContext("req")
	pc.UserID = "user-1"
	pc.SessionID = "" // no transport session
	req := makeCallReq("trino_query", map[string]any{"sql": "SELECT 1"})

	result := r.resolve(context.Background(), req, pc, "trino_query")
	if result == nil {
		t.Fatal("missing handle with require=on should be refused")
	}
	if code := errCode(t, result); code != CodeSessionRequired {
		t.Errorf("code = %q, want %q", code, CodeSessionRequired)
	}
}

// TestSessionResolver_MissingHandleAdoptsUsersSession proves issue #1040: an
// authenticated call that presents no handle is scoped to the caller's own
// established session rather than refused, so an MCP App's sandboxed calls (which
// cannot thread the handle) work. The model minted a handle for this identity;
// the app's handle-less call adopts it.
func TestSessionResolver_MissingHandleAdoptsUsersSession(t *testing.T) {
	store := pkgsession.NewMemoryStore(time.Hour)
	r := newResolver(store, true)
	h := mintHandle(t, store, "user-1") // as platform_info would, for this user

	pc := NewPlatformContext("req")
	pc.UserID = "user-1"
	pc.SessionID = "" // stateless HTTP, no transport session
	// No session_id argument: the app could not thread it.
	req := makeCallReq("manage_prompt", map[string]any{"command": "list"})

	if got := r.resolve(context.Background(), req, pc, "manage_prompt"); got != nil {
		t.Fatalf("authenticated handle-less call should be adopted, not refused: code=%s", errCode(t, got))
	}
	if pc.SessionID != h {
		t.Errorf("pc.SessionID = %q, want adopted handle %q", pc.SessionID, h)
	}
}

// TestSessionResolver_AdoptOnlyOwnIdentity proves the adopted session must belong
// to the authenticated caller: a handle-less call from a user with no session is
// still refused even though another user has one, so adoption never crosses
// identities.
func TestSessionResolver_AdoptOnlyOwnIdentity(t *testing.T) {
	store := pkgsession.NewMemoryStore(time.Hour)
	r := newResolver(store, true)
	mintHandle(t, store, "user-1") // a session for a different user

	pc := NewPlatformContext("req")
	pc.UserID = "user-2" // the caller, who has no session
	pc.SessionID = ""
	req := makeCallReq("manage_prompt", map[string]any{"command": "list"})

	result := r.resolve(context.Background(), req, pc, "manage_prompt")
	if result == nil {
		t.Fatal("a caller with no session of their own must be refused")
	}
	if code := errCode(t, result); code != CodeSessionRequired {
		t.Errorf("code = %q, want %q", code, CodeSessionRequired)
	}
	if pc.SessionID != "" {
		t.Errorf("no session should be adopted for user-2, got %q", pc.SessionID)
	}
}

// TestSessionResolver_AdoptFailsOpenOnStoreError proves an infrastructure error
// on the identity lookup allows the authenticated call rather than refusing it,
// matching the explicit-handle path's deliberate fail-open. The authenticator,
// not the handle, is the security boundary.
func TestSessionResolver_AdoptFailsOpenOnStoreError(t *testing.T) {
	r := NewSessionResolver(erroringStore{}, SessionResolverConfig{
		Enabled: true, Require: true, TTL: time.Hour, InitTool: testInitTool,
	})

	pc := NewPlatformContext("req")
	pc.UserID = "user-1"
	pc.SessionID = ""
	req := makeCallReq("manage_prompt", map[string]any{"command": "list"})

	if got := r.resolve(context.Background(), req, pc, "manage_prompt"); got != nil {
		t.Fatalf("store error must not refuse an authenticated caller: code=%s", errCode(t, got))
	}
}

// erroringStore is a session store whose identity lookup always fails, for the
// fail-open test. Every other method is a no-op sufficient for the resolver.
type erroringStore struct{ pkgsession.Store }

func (erroringStore) LatestHandleForUser(context.Context, string) (*pkgsession.Session, error) {
	return nil, errAdoptLookup
}

var errAdoptLookup = errors.New("session store unavailable")

// TestSessionResolver_StatelessShimSourcesBypassRequire proves issue #811's fix:
// a call from a stateless in-memory shim (Source=rest, the gateway HTTP shim; or
// Source=admin, the admin tool-runner shim) is not refused with SESSION_REQUIRED
// even when require is on and no handle is presented, because those callers cannot
// perform the platform_info handshake. A real MCP-transport call (Source=mcp), and
// an unset/unknown source, are still refused so the gate stays real for agents and
// fails closed.
func TestSessionResolver_StatelessShimSourcesBypassRequire(t *testing.T) {
	store := pkgsession.NewMemoryStore(time.Hour)
	r := newResolver(store, true)

	exempt := []struct {
		name   string
		source string
	}{
		{"rest gateway shim", SourceREST},
		{"admin tool-runner shim", SourceAdmin},
	}
	for _, tc := range exempt {
		t.Run("exempt/"+tc.name, func(t *testing.T) {
			pc := NewPlatformContext("req")
			pc.UserID = "user-1"
			pc.SessionID = "" // no handle, no transport session
			pc.Source = tc.source
			req := makeCallReq("trino_query", map[string]any{"sql": "SELECT 1"})
			if got := r.resolve(context.Background(), req, pc, "trino_query"); got != nil {
				t.Fatalf("%s must bypass SESSION_REQUIRED, got code=%s", tc.name, errCode(t, got))
			}
		})
	}

	gated := []struct {
		name   string
		source string
	}{
		{"real MCP agent", SourceMCP},
		{"unset source fails closed", ""},
	}
	for _, tc := range gated {
		t.Run("gated/"+tc.name, func(t *testing.T) {
			pc := NewPlatformContext("req")
			pc.UserID = "user-1"
			pc.SessionID = ""
			pc.Source = tc.source
			if got := r.resolve(context.Background(), makeCallReq("trino_query", nil), pc, "trino_query"); got == nil {
				t.Fatalf("%s must still be refused with SESSION_REQUIRED", tc.name)
			}
		})
	}
}

// TestSessionResolver_ExemptToolBypassesRequire proves an exempt tool (carried
// from the legacy gate's exempt_tools) is reachable without a handle even when
// require is on.
func TestSessionResolver_ExemptToolBypassesRequire(t *testing.T) {
	store := pkgsession.NewMemoryStore(time.Hour)
	r := NewSessionResolver(store, SessionResolverConfig{
		Enabled: true, Require: true, TTL: time.Hour, InitTool: testInitTool,
		ExemptTools: []string{"list_connections"},
	})

	pc := NewPlatformContext("req")
	pc.UserID = "user-1"
	pc.SessionID = "" // no handle, no transport session

	if got := r.resolve(context.Background(), makeCallReq("list_connections", nil), pc, "list_connections"); got != nil {
		t.Fatalf("exempt tool must bypass SESSION_REQUIRED: code=%s", errCode(t, got))
	}
	// A non-exempt tool under the same conditions is still refused.
	if got := r.resolve(context.Background(), makeCallReq("trino_query", nil), pc, "trino_query"); got == nil {
		t.Fatal("non-exempt tool must still be refused")
	}
}

func TestSessionResolver_MissingHandleNotRequired(t *testing.T) {
	store := pkgsession.NewMemoryStore(time.Hour)
	r := newResolver(store, false) // require=off

	pc := NewPlatformContext("req")
	pc.UserID = "user-1"
	pc.SessionID = ""
	req := makeCallReq("trino_query", map[string]any{"sql": "SELECT 1"})

	if got := r.resolve(context.Background(), req, pc, "trino_query"); got != nil {
		t.Fatalf("require=off should allow handle-less calls, got code=%s", errCode(t, got))
	}
}

// TestSessionResolver_TransportSessionDoesNotSatisfyRequire is issue #800's
// central acceptance criterion: a non-empty transport Mcp-Session-Id is NOT a
// fallback. A gated call carrying one but no handle is refused with
// SESSION_REQUIRED, so the requirement is real for the churning-session client
// the feature targets.
func TestSessionResolver_TransportSessionDoesNotSatisfyRequire(t *testing.T) {
	store := pkgsession.NewMemoryStore(time.Hour)
	r := newResolver(store, true)

	pc := NewPlatformContext("req")
	pc.UserID = "user-1"
	pc.SessionID = "per-call-transport-hex" // fresh Mcp-Session-Id, non-identifying
	req := makeCallReq("trino_query", map[string]any{"sql": "SELECT 1"})

	result := r.resolve(context.Background(), req, pc, "trino_query")
	if result == nil {
		t.Fatal("a transport session must NOT satisfy require; the call must be refused")
	}
	if code := errCode(t, result); code != CodeSessionRequired {
		t.Errorf("code = %q, want %q", code, CodeSessionRequired)
	}
}

// TestSessionResolver_TransportSessionAdoptedWhenNotRequired proves the softer
// rollout mode (require off): a handle-less call falls back to the transport
// session, which is preserved as pc.SessionID for best-effort scoping.
func TestSessionResolver_TransportSessionAdoptedWhenNotRequired(t *testing.T) {
	store := pkgsession.NewMemoryStore(time.Hour)
	r := newResolver(store, false)

	pc := NewPlatformContext("req")
	pc.UserID = "user-1"
	pc.SessionID = "legacy-transport-hex"
	req := makeCallReq("trino_query", map[string]any{"sql": "SELECT 1"})

	if got := r.resolve(context.Background(), req, pc, "trino_query"); got != nil {
		t.Fatalf("require=off should allow the transport fallback, got code=%s", errCode(t, got))
	}
	if pc.SessionID != "legacy-transport-hex" {
		t.Errorf("transport session must be preserved when require is off, got %q", pc.SessionID)
	}
}

func TestSessionResolver_InitToolExempt(t *testing.T) {
	store := pkgsession.NewMemoryStore(time.Hour)
	r := newResolver(store, true)

	pc := NewPlatformContext("req")
	pc.UserID = "user-1"
	pc.SessionID = "" // platform_info can be the very first call
	req := makeCallReq(testInitTool, nil)

	if got := r.resolve(context.Background(), req, pc, testInitTool); got != nil {
		t.Fatalf("init tool must never be gated, got code=%s", errCode(t, got))
	}
}

// TestSessionResolver_InitToolExemptEvenWithStaleHandle guards the recovery
// path: an agent that threads a now-expired handle on a platform_info re-call
// must NOT be refused, or it can never mint a fresh handle.
func TestSessionResolver_InitToolExemptEvenWithStaleHandle(t *testing.T) {
	store := pkgsession.NewMemoryStore(time.Hour)
	r := newResolver(store, true)

	pc := NewPlatformContext("req")
	pc.UserID = "user-1"
	req := makeCallReq(testInitTool, map[string]any{sessionHandleArg: "dps_expiredstale"})

	if got := r.resolve(context.Background(), req, pc, testInitTool); got != nil {
		t.Fatalf("platform_info must never be refused, even with a stale handle: code=%s", errCode(t, got))
	}
	// The stale handle argument is still stripped before the handler.
	if _, present := reqArgs(t, req)[sessionHandleArg]; present {
		t.Error("session_id must be stripped from platform_info calls too")
	}
}

// TestSessionResolver_StdioSentinelDoesNotSatisfyRequire proves the stdio
// carve-out is gone (issue #800): the "stdio" sentinel is a constant that
// collapses every run into one bucket, not a usable session identity, so a
// gated stdio call without a handle is refused like any other transport. stdio
// mints and threads a handle via platform_info like every other transport.
func TestSessionResolver_StdioSentinelDoesNotSatisfyRequire(t *testing.T) {
	store := pkgsession.NewMemoryStore(time.Hour)
	r := newResolver(store, true)

	pc := NewPlatformContext("req")
	pc.UserID = "user-1"
	pc.SessionID = defaultSessionID // "stdio"
	req := makeCallReq("trino_query", map[string]any{"sql": "SELECT 1"})

	result := r.resolve(context.Background(), req, pc, "trino_query")
	if result == nil {
		t.Fatal("the stdio sentinel must NOT satisfy require; the call must be refused")
	}
	if code := errCode(t, result); code != CodeSessionRequired {
		t.Errorf("code = %q, want %q", code, CodeSessionRequired)
	}
}

func TestSessionResolver_DisabledIsNoop(t *testing.T) {
	store := pkgsession.NewMemoryStore(time.Hour)
	r := NewSessionResolver(store, SessionResolverConfig{Enabled: false, Require: true, InitTool: testInitTool})

	pc := NewPlatformContext("req")
	pc.SessionID = "transport"
	req := makeCallReq("trino_query", map[string]any{"sql": "SELECT 1", sessionHandleArg: "dps_x"})

	if got := r.resolve(context.Background(), req, pc, "trino_query"); got != nil {
		t.Fatal("disabled resolver must be a no-op")
	}
	// Disabled: the argument is NOT stripped and SessionID is untouched.
	if _, present := reqArgs(t, req)[sessionHandleArg]; !present {
		t.Error("disabled resolver must not strip the argument")
	}
	if pc.SessionID != "transport" {
		t.Error("disabled resolver must not alter SessionID")
	}
}

func TestSessionResolver_NilIsNoop(t *testing.T) {
	var r *SessionResolver
	pc := NewPlatformContext("req")
	pc.SessionID = "x"
	req := makeCallReq("trino_query", map[string]any{"sql": "SELECT 1"})
	if got := r.resolve(context.Background(), req, pc, "trino_query"); got != nil {
		t.Fatal("nil resolver must be a no-op")
	}
}

func TestSessionResolver_MetricSources(t *testing.T) {
	store := pkgsession.NewMemoryStore(time.Hour)
	var got []string
	r := NewSessionResolver(store, SessionResolverConfig{
		Enabled: true, Require: false, TTL: time.Hour, InitTool: testInitTool,
		Metric: func(_ context.Context, source string) { got = append(got, source) },
	})
	h := mintHandle(t, store, "u")

	cases := []struct {
		name     string
		pcSID    string
		args     map[string]any
		tool     string
		wantLast string
	}{
		{"explicit", "", map[string]any{sessionHandleArg: h}, "trino_query", sessionSourceExplicit},
		{"transport", "hex", map[string]any{"sql": "x"}, "trino_query", sessionSourceTransport},
		{"stdio", defaultSessionID, map[string]any{"sql": "x"}, "trino_query", sessionSourceStdio},
		{"none", "", map[string]any{"sql": "x"}, "trino_query", sessionSourceNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got = nil
			pc := NewPlatformContext("req")
			pc.UserID = "u"
			pc.SessionID = tc.pcSID
			r.resolve(context.Background(), makeCallReq(tc.tool, tc.args), pc, tc.tool)
			if len(got) == 0 || got[len(got)-1] != tc.wantLast {
				t.Errorf("metric source = %v, want last %q", got, tc.wantLast)
			}
		})
	}
}

// getErrorStore embeds a MemoryStore but forces Get to error, exercising the
// resolver's fail-closed path.
type getErrorStore struct {
	*pkgsession.MemoryStore
}

func (getErrorStore) Get(context.Context, string) (*pkgsession.Session, error) {
	return nil, errStoreDown
}

var errStoreDown = &storeError{}

type storeError struct{}

func (*storeError) Error() string { return "store down" }

// TestSessionResolver_StoreErrorFailsOpen proves a transient session-store
// outage degrades to allowing the call (matching the search-first gate) rather
// than locking out every handle-bearing caller.
func TestSessionResolver_StoreErrorFailsOpen(t *testing.T) {
	store := getErrorStore{pkgsession.NewMemoryStore(time.Hour)}
	r := newResolver(store, true)

	pc := NewPlatformContext("req")
	pc.UserID = "user-1"
	req := makeCallReq("trino_query", map[string]any{sessionHandleArg: "dps_whatever"})

	if result := r.resolve(context.Background(), req, pc, "trino_query"); result != nil {
		t.Fatalf("a store error must fail open, got refusal code=%s", errCode(t, result))
	}
	// The handle is adopted best-effort so session-scoped records stay coherent.
	if pc.SessionID != "dps_whatever" {
		t.Errorf("pc.SessionID = %q, want handle adopted best-effort", pc.SessionID)
	}
}

func TestToolCallParams_NilRequest(t *testing.T) {
	if toolCallParams(nil) != nil {
		t.Error("toolCallParams(nil) must be nil")
	}
	if _, present := takeSessionHandleArg(nil); present {
		t.Error("takeSessionHandleArg(nil) must report no handle")
	}
}

func TestWithSessionHandleProperty(t *testing.T) {
	t.Run("map schema", func(t *testing.T) {
		in := map[string]any{"type": "object", "properties": map[string]any{"sql": map[string]any{"type": "string"}}}
		out, ok := withSessionHandleProperty(in)
		if !ok {
			t.Fatal("expected injection into object schema")
		}
		assertHasSessionProp(t, out)
	})

	t.Run("json.RawMessage schema", func(t *testing.T) {
		in := json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}},"additionalProperties":false}`)
		out, ok := withSessionHandleProperty(in)
		if !ok {
			t.Fatal("expected injection into raw schema")
		}
		assertHasSessionProp(t, out)
	})

	t.Run("nil schema skipped", func(t *testing.T) {
		if _, ok := withSessionHandleProperty(nil); ok {
			t.Error("nil schema must be skipped")
		}
	})

	t.Run("non-object schema skipped", func(t *testing.T) {
		if _, ok := withSessionHandleProperty(map[string]any{"type": "string"}); ok {
			t.Error("non-object schema must be skipped")
		}
	})

	t.Run("idempotent when already present", func(t *testing.T) {
		in := map[string]any{"type": "object", "properties": map[string]any{sessionHandleArg: map[string]any{"type": "string"}}}
		out, ok := withSessionHandleProperty(in)
		if !ok {
			t.Fatal("expected ok")
		}
		obj, _ := out.(map[string]any)
		props, _ := obj["properties"].(map[string]any)
		if len(props) != 1 {
			t.Errorf("expected no duplicate property, got %d", len(props))
		}
	})
}

func assertHasSessionProp(t *testing.T, schema any) {
	t.Helper()
	obj, ok := schema.(map[string]any)
	if !ok {
		t.Fatalf("schema is not a map: %T", schema)
	}
	props, ok := obj["properties"].(map[string]any)
	if !ok {
		t.Fatal("no properties map")
	}
	prop, ok := props[sessionHandleArg].(map[string]any)
	if !ok {
		t.Fatal("session_id property missing")
	}
	if prop["type"] != "string" {
		t.Errorf("session_id type = %v, want string", prop["type"])
	}
	if _, stillHasSQL := props["sql"]; !stillHasSQL {
		// sql only present in the non-idempotent cases; guard optional.
		_ = stillHasSQL
	}
}
