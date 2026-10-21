package channelhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/toolratelimit"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// echoInput accepts whatever a route sends.
type echoInput map[string]any

// notifyServer registers a stand-in notify tool answering with what it was
// sent and who it was sent as, or the refusal answer gives it.
func notifyServer(t *testing.T, answer func(args echoInput) *mcp.CallToolResult) *mcp.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: toolNotify},
		func(ctx context.Context, _ *mcp.CallToolRequest, args echoInput) (*mcp.CallToolResult, any, error) {
			if answer != nil {
				if res := answer(args); res != nil {
					return res, nil, nil
				}
			}
			who := middleware.GetPreAuthenticatedUser(ctx)
			out := map[string]any{"args": map[string]any(args), "email": who.Email, "source": middleware.GetSource(ctx)}
			b, _ := json.Marshal(out)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
		})
	return server
}

// request is one call a test makes to the routes.
type request struct {
	method, path, body string
}

func serve(t *testing.T, api *API, rq request, user *access.User) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	api.Register(mux)
	req := httptest.NewRequestWithContext(context.Background(), rq.method, rq.path, bytes.NewBufferString(rq.body))
	if user != nil {
		req = req.WithContext(access.ContextWithUser(req.Context(), user))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

var analyst = &access.User{UserID: "u1", Email: "analyst@example.com", Roles: []string{"analyst"}}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q: %v", w.Body.String(), err)
	}
	return out
}

func TestRoutes_CallNotifyAsTheSignedInPerson(t *testing.T) {
	api := &API{Server: notifyServer(t, nil)}
	cases := []struct {
		method, path, body string
		want               map[string]any
	}{
		{http.MethodGet, "/api/v1/portal/channels", "", map[string]any{"action": "list"}},
		{
			http.MethodPost, "/api/v1/portal/assets/a1/channel-shares",
			`{"channel":"slack","target":"C1","access_mode":"authenticated","message":"Monday"}`,
			map[string]any{
				"action": "publish", "channel": "slack", "target": "C1", "asset": "a1", "message": "Monday",
				"access": "signed_in", "expires_in": "",
			},
		},
		{
			http.MethodPost, "/api/v1/portal/assets/a1/channel-shares",
			`{"channel":"list","access_mode":"public","expires_in":"168h"}`,
			map[string]any{"action": "publish", "channel": "list", "asset": "a1", "access": "public", "expires_in": "168h"},
		},
		{
			http.MethodPost, "/api/v1/portal/channels/ops/send", `{"title":"Late load","body":"40m","link":"https://p/r"}`,
			map[string]any{"action": "send", "channel": "ops", "title": "Late load", "body": "40m", "link": "https://p/r"},
		},
	}
	for _, tc := range cases {
		w := serve(t, api, request{tc.method, tc.path, tc.body}, analyst)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		got := decodeBody(t, w)
		args, _ := got["args"].(map[string]any)
		for k, v := range tc.want {
			if args[k] != v {
				t.Errorf("%s: arg %s = %v, want %v", tc.path, k, args[k], v)
			}
		}
		if got["email"] != "analyst@example.com" || got["source"] != middleware.SourceAdmin {
			t.Errorf("%s: called as %v from %v", tc.path, got["email"], got["source"])
		}
	}
}

func TestRoutes_TheToolsRefusalInItsOwnWords(t *testing.T) {
	refuse := func(echoInput) *mcp.CallToolResult {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: `notify: no channel named "x" is available to this session`}}}
	}
	w := serve(t, &API{Server: notifyServer(t, refuse)}, request{http.MethodPost, "/api/v1/portal/channels/x/send", `{"title":"t"}`}, analyst)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "is available to this session") {
		t.Errorf("refusal: %d %s", w.Code, w.Body.String())
	}
	limited := func(echoInput) *mcp.CallToolResult {
		return middleware.BuildErrorResult(middleware.NewToolError(toolratelimit.CodeRateLimited, "rate_limited", "too many calls", "pause"))
	}
	w = serve(t, &API{Server: notifyServer(t, limited)}, request{http.MethodPost, "/api/v1/portal/channels/x/send", `{"title":"t"}`}, analyst)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("rate limit: %d %s", w.Code, w.Body.String())
	}
	silent := func(echoInput) *mcp.CallToolResult { return &mcp.CallToolResult{IsError: true} }
	w = serve(t, &API{Server: notifyServer(t, silent)}, request{http.MethodPost, "/api/v1/portal/channels/x/send", `{"title":"t"}`}, analyst)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "refused") {
		t.Errorf("a refusal with no words: %d %s", w.Code, w.Body.String())
	}
	prose := func(echoInput) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "not json"}}}
	}
	w = serve(t, &API{Server: notifyServer(t, prose)}, request{http.MethodGet, "/api/v1/portal/channels", ""}, analyst)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("an unreadable answer: %d", w.Code)
	}
}

func TestRoutes_WhatIsRefusedBeforeTheTool(t *testing.T) {
	api := &API{Server: notifyServer(t, nil)}
	if w := serve(t, api, request{http.MethodGet, "/api/v1/portal/channels", ""}, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("signed out: %d", w.Code)
	}
	if w := serve(t, &API{}, request{http.MethodGet, "/api/v1/portal/channels", ""}, analyst); w.Code != http.StatusServiceUnavailable {
		t.Errorf("no server: %d", w.Code)
	}
	if w := serve(t, api, request{http.MethodPost, "/api/v1/portal/assets/a1/channel-shares", `{"access_mode":"public"}`}, analyst); w.Code != http.StatusBadRequest {
		t.Errorf("no channel: %d", w.Code)
	}
	if w := serve(t, api, request{http.MethodPost, "/api/v1/portal/assets/a1/channel-shares", `{"channel":"c","access_mode":"restricted"}`}, analyst); w.Code != http.StatusBadRequest {
		t.Errorf("a mode a channel share does not take: %d", w.Code)
	}
	if w := serve(t, api, request{http.MethodPost, "/api/v1/portal/channels/ops/publish", `{"asset_id":"a1"}`}, analyst); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Errorf("the removed publish route still answers: %d", w.Code)
	}
	if w := serve(t, api, request{http.MethodPost, "/api/v1/portal/channels/ops/send", `{"title":`}, analyst); w.Code != http.StatusBadRequest {
		t.Errorf("bad body: %d", w.Code)
	}
	if w := serve(t, api, request{http.MethodPost, "/api/v1/portal/channels/ops/send", `{"title":"t","extra":1}`}, analyst); w.Code != http.StatusBadRequest {
		t.Errorf("unknown field: %d", w.Code)
	}
	if w := serve(t, api, request{http.MethodPost, "/api/v1/portal/assets/a1/channel-shares", `{`}, analyst); w.Code != http.StatusBadRequest {
		t.Errorf("bad share body: %d", w.Code)
	}
	noTool := &API{Server: mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)}
	if w := serve(t, noTool, request{http.MethodGet, "/api/v1/portal/channels", ""}, analyst); w.Code != http.StatusServiceUnavailable {
		t.Errorf("no notify tool: %d %s", w.Code, w.Body.String())
	}
}

func TestRoutes_APersonaWithoutSharingGetsNoChannels(t *testing.T) {
	api := &API{Server: notifyServer(t, nil), SharingAllowed: func(u *access.User) bool { return u.UserID != "u1" }}
	for _, rq := range []request{
		{http.MethodGet, "/api/v1/portal/channels", ""},
		{http.MethodPost, "/api/v1/portal/assets/a1/channel-shares", `{"channel":"c","access_mode":"authenticated"}`},
	} {
		if w := serve(t, api, rq, analyst); w.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d, want 403", rq.method, rq.path, w.Code)
		}
	}
	other := &access.User{UserID: "u2", Email: "b@example.com"}
	if w := serve(t, api, request{http.MethodGet, "/api/v1/portal/channels", ""}, other); w.Code != http.StatusOK {
		t.Errorf("a persona with sharing: %d", w.Code)
	}
	if w := serve(t, api, request{http.MethodPost, "/api/v1/portal/assets/a1/channel-shares", `{}`}, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("signed out share: %d", w.Code)
	}
}

func TestSharingFor_ReadsTheResolvedPersona(t *testing.T) {
	if SharingFor(nil, "admin") != nil {
		t.Error("no registry built a check")
	}
	reg := persona.NewRegistry()
	for _, p := range []*persona.Persona{
		{Name: "admin", Roles: []string{"admin"}, Priority: 100, AllowSharing: new(false)},
		{Name: "viewer", Roles: []string{"viewer"}, AllowSharing: new(false)},
		{Name: "analyst", Roles: []string{"analyst"}},
	} {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	allowed := SharingFor(reg, "admin")
	for _, tc := range []struct {
		roles []string
		want  bool
	}{
		{[]string{"analyst"}, true},
		{[]string{"viewer"}, false},
		{[]string{"admin"}, true},
		{[]string{"nobody"}, false},
	} {
		if got := allowed(&access.User{Roles: tc.roles}); got != tc.want {
			t.Errorf("roles %v: %v, want %v", tc.roles, got, tc.want)
		}
	}
}
