package apigateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/selfcall"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

// selfConnection adds a passthrough connection whose network upstream must
// not be reached, served in process by self when selfDispatch is set.
func selfConnection(t *testing.T, self http.Handler, selfDispatch bool) *Toolkit {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("a self_dispatch call must not reach the network upstream")
	}))
	t.Cleanup(srv.Close)
	tk := New("test")
	tk.SetSelfHandler(self)
	if err := tk.AddConnection("pa", map[string]any{
		"base_url": srv.URL, "auth_mode": AuthModeNone, "identity_passthrough": true,
		"self_dispatch": selfDispatch,
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	return tk
}

// TestSelfDispatch_ServesATokenlessCallAsItsCaller: a call that carries a
// caller but no token is handed to the platform's own handler with that
// caller on the request, and nothing else is (#2086).
func TestSelfDispatch_ServesATokenlessCallAsItsCaller(t *testing.T) {
	var seen mcpcontext.Caller
	self := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = selfcall.From(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	tk := selfConnection(t, self, true)
	ctx := mcpcontext.WithCaller(context.Background(), mcpcontext.Caller{UserID: "script:s", Roles: []string{"admin"}})
	res, _, err := tk.handleInvoke(ctx, nil, InvokeInput{Connection: "pa", Method: "GET", Path: "/api/v1/admin/personas"})
	if err != nil || res.IsError {
		t.Fatalf("handleInvoke: err=%v result=%s", err, textContent(res))
	}
	if seen.UserID != "script:s" || len(seen.Roles) != 1 {
		t.Errorf("self handler saw caller %+v; want script:s with its roles", seen)
	}
}

// TestSelfDispatch_ATokenIsServedInProcessAsAToolCall: a call carrying a
// token is served by the platform's own handler with the token on it, marked
// as a tool call's and naming no caller, so the authenticators check the token
// and the agent gates see an agent (#2656).
func TestSelfDispatch_ATokenIsServedInProcessAsAToolCall(t *testing.T) {
	var auth string
	var byTool, named bool
	self := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, byTool = r.Header.Get("Authorization"), selfcall.ByTool(r.Context())
		_, named = selfcall.From(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	tk := selfConnection(t, self, true)
	ctx := mcpcontext.WithCaller(mcpcontext.WithAuthToken(context.Background(), "tok"), mcpcontext.Caller{UserID: "u"})
	if res, _, err := tk.handleInvoke(ctx, nil, InvokeInput{Connection: "pa", Method: "GET", Path: "/x"}); err != nil || res.IsError {
		t.Fatalf("handleInvoke: err=%v", err)
	}
	if auth != "Bearer tok" || !byTool || named {
		t.Errorf("self handler saw Authorization %q byTool=%v named=%v; want the caller's token, marked, naming no one", auth, byTool, named)
	}
}

// TestSelfDispatch_AScriptOnAnotherUpstreamIsToldWhy: a saved script's call
// on a passthrough connection to another upstream is refused naming the
// reason and the remedy, not as a missing sign-in.
func TestSelfDispatch_AScriptOnAnotherUpstreamIsToldWhy(t *testing.T) {
	tk := selfConnection(t, http.NotFoundHandler(), false)
	ctx := mcpcontext.WithSource(mcpcontext.WithCaller(context.Background(), mcpcontext.Caller{UserID: "script:s"}), mcpcontext.SourceScript)
	res, _, err := tk.handleInvoke(ctx, nil, InvokeInput{Connection: "pa", Method: "GET", Path: "/x"})
	if err != nil || !res.IsError {
		t.Fatalf("want an error result; err=%v", err)
	}
	for _, want := range []string{`connection \"pa\"`, "a saved script's run has none", "holds its own credentials"} {
		if !strings.Contains(textContent(res), want) {
			t.Errorf("refusal %q lacks %q", textContent(res), want)
		}
	}
	if !tk.ScriptRunCannotServe("pa") {
		t.Error("ScriptRunCannotServe(pa) = false; want true for a passthrough connection to another upstream")
	}
}

func TestScriptRunCannotServe(t *testing.T) {
	tk := selfConnection(t, http.NotFoundHandler(), true)
	if tk.ScriptRunCannotServe("pa") {
		t.Error("a self_dispatch connection serves a script's run")
	}
	if tk.ScriptRunCannotServe("missing") {
		t.Error("an unknown connection is not reported")
	}
}

func TestSelfDispatch_ConfigRules(t *testing.T) {
	tk := New("test")
	err := tk.AddConnection("pa", map[string]any{"base_url": "http://x", "auth_mode": AuthModeNone, "self_dispatch": true})
	if err == nil || !strings.Contains(err.Error(), "self_dispatch requires identity_passthrough") {
		t.Errorf("self_dispatch without passthrough: err = %v", err)
	}
}
