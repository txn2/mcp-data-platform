package selfcall

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

func TestWithFrom(t *testing.T) {
	if _, ok := From(context.Background()); ok {
		t.Error("an unmarked request carries no caller")
	}
	if _, ok := From(With(context.Background(), mcpcontext.Caller{})); ok {
		t.Error("a caller naming no user is no caller")
	}
	c, ok := From(With(context.Background(), mcpcontext.Caller{UserID: "script:s", Roles: []string{"admin"}}))
	if !ok || c.UserID != "script:s" || c.Roles[0] != "admin" {
		t.Errorf("From = %+v, %v", c, ok)
	}
}

func TestHandler(t *testing.T) {
	var h Handler
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("before Set: %d; want 503", rec.Code)
	}
	h.Set(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody))
	if rec.Code != http.StatusTeapot {
		t.Errorf("after Set: %d; want the installed handler's answer", rec.Code)
	}
}

func TestForCall(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	caller := mcpcontext.WithCaller(context.Background(), mcpcontext.Caller{UserID: "script:s"})
	if _, ok := ForCall(context.Background(), req); ok {
		t.Error("a call with no caller is not served in process")
	}
	if _, ok := ForCall(mcpcontext.WithAuthToken(caller, "tok"), req); ok {
		t.Error("a call carrying a token goes over the wire with it")
	}
	marked, ok := ForCall(caller, req)
	if c, ok2 := From(marked.Context()); !ok || !ok2 || c.UserID != "script:s" {
		t.Errorf("a token-less call with a caller is marked for it: %v %v %+v", ok, ok2, c)
	}
}

type tripper string

func (t tripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Status: string(t), Body: http.NoBody}, nil
}

func TestTransport(t *testing.T) {
	rt := Transport(tripper("self"), tripper("network"))
	plain := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	marked := plain.WithContext(With(context.Background(), mcpcontext.Caller{UserID: "u"}))
	for req, want := range map[*http.Request]string{plain: "network", marked: "self"} {
		res, err := rt.RoundTrip(req)
		if err == nil {
			_ = res.Body.Close()
		}
		if err != nil || res.Status != want {
			t.Errorf("RoundTrip = %v, %v; want %s", res, err, want)
		}
	}
}

type installer struct{ h http.Handler }

func (i *installer) SetSelfHandler(h http.Handler) { i.h = h }

func TestInstall(t *testing.T) {
	takes, ignores := &installer{}, struct{}{}
	h := http.NotFoundHandler()
	Install([]any{ignores, takes}, h)
	if takes.h == nil {
		t.Error("the toolkit that serves a self connection was not given the handler")
	}
}

func TestPersonaError(t *testing.T) {
	err := &PersonaError{Surface: "admin API", Persona: "admin", Need: []string{"admin"}, Caller: "script:s", Has: []string{"analyst"}}
	want := `the admin API requires the persona "admin", which the roles [admin] reach; script:s presents [analyst]`
	if err.Error() != want {
		t.Errorf("Error() = %q", err.Error())
	}
}
