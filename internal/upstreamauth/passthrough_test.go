package upstreamauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/selfcall"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

func TestCallerToken(t *testing.T) {
	tok, err := CallerToken(mcpcontext.WithAuthToken(context.Background(), "t1"), "kind", "c")
	if err != nil || tok != "t1" {
		t.Fatalf("with a token: %q, %v", tok, err)
	}
	_, err = CallerToken(mcpcontext.WithSource(context.Background(), mcpcontext.SourceScript), "kind", "c")
	if err == nil || !strings.HasPrefix(err.Error(), `kind: connection "c" sends each call with the caller's own sign-in token`) ||
		!strings.Contains(err.Error(), "holds its own credentials") {
		t.Errorf("a script's run: %v", err)
	}
	_, err = CallerToken(context.Background(), "kind", "c")
	if err == nil || err.Error() != "kind: identity passthrough requires an authenticated caller token, but none was present on the request" {
		t.Errorf("no caller: %v", err)
	}
}

// TestPassthroughRequest: a token is forwarded; on a SelfDispatch connection a
// token-less call with a caller is marked to be served in process, and any
// other token-less call is refused (#2086).
func TestPassthroughRequest(t *testing.T) {
	fresh := func() *http.Request {
		return httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://u/x", http.NoBody)
	}
	caller := mcpcontext.WithCaller(context.Background(), mcpcontext.Caller{UserID: "script:s"})

	out, err := Config{ErrPrefix: "k"}.PassthroughRequest(mcpcontext.WithAuthToken(caller, "tok"), fresh(), "c")
	if err != nil || out.Header.Get(AuthorizationHeader) != "Bearer tok" {
		t.Fatalf("with a token: %v %v", out, err)
	}
	marked, err := Config{ErrPrefix: "k", SelfDispatch: true}.PassthroughRequest(caller, fresh(), "c")
	if _, ok := selfcall.From(marked.Context()); err != nil || !ok || marked.Header.Get(AuthorizationHeader) != "" {
		t.Fatalf("self dispatch: %v %v", marked, err)
	}
	if _, err := (Config{ErrPrefix: "k"}).PassthroughRequest(caller, fresh(), "c"); err == nil {
		t.Fatal("a token-less call on a connection to another upstream was not refused")
	}
}

func TestValidateIdentityPassthrough_SelfDispatch(t *testing.T) {
	if err := (Config{ErrPrefix: "k", AuthMode: AuthModeNone, SelfDispatch: true}).ValidateIdentityPassthrough(); err == nil ||
		err.Error() != "k: self_dispatch requires identity_passthrough" {
		t.Errorf("err = %v", err)
	}
	if err := (Config{ErrPrefix: "k", AuthMode: AuthModeNone, SelfDispatch: true, IdentityPassthrough: true}).ValidateIdentityPassthrough(); err != nil {
		t.Errorf("err = %v", err)
	}
}
