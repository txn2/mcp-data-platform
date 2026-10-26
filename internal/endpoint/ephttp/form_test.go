package ephttp

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
)

var viewerKey = epform.ViewerKey([]byte("0123456789abcdef0123456789abcdef"))

// formFixture is the fixture with a signed-in form, a bot_check form and a
// signed-in endpoint that is not a form.
func formFixture() *fixture {
	f := &fixture{
		runner: &runner{
			bindings: map[string]*epbinding.Binding{
				"apply":   binding("apply", epbinding.AuthAuthenticated),
				"contact": binding("contact", epbinding.AuthBotCheck),
				"orders":  binding("orders", epbinding.AuthAuthenticated),
			},
			forms: map[string]bool{"apply": true, "contact": true},
		},
		checker: &checker{used: map[string]bool{}},
	}
	h := &Handler{
		Runner: f.runner, Checker: f.checker, ViewerKey: viewerKey,
		Authenticate: func(*http.Request) (*eprun.Caller, error) { return nil, nil }, //nolint:nilnil // nobody
	}
	f.mux = http.NewServeMux()
	h.Mount(f.mux)
	return f
}

func viewer(t *testing.T, endpoint string) string {
	t.Helper()
	tok, err := epform.SignViewer(viewerKey, endpoint, epform.Viewer{UserID: "u-ann", Email: "ann@example.com", Persona: "analyst"}, time.Now())
	require.NoError(t, err)
	return tok
}

// #2177: an authenticated form embedded in an asset takes the viewer token
// the asset was served with as its caller, since its frame sends no session.
func TestForm_AViewerTokenIsTheCallerOfAnAuthenticatedForm(t *testing.T) {
	f := formFixture()
	rec := f.do("POST", "/endpoints/apply/1", http.Header{epform.ViewerHeader: {viewer(t, "apply")}}, "x")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "ann@example.com", fields(t, rec)["caller"])

	rec = f.do("POST", "/endpoints/apply/1", http.Header{epform.ViewerHeader: {viewer(t, "other")}}, "x")
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "a token for another form is no caller")
	rec = f.do("POST", "/endpoints/apply/1", nil, "x")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	rec = f.do("POST", "/endpoints/orders/1", http.Header{epform.ViewerHeader: {viewer(t, "orders")}}, "x")
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "an endpoint that is not a form takes no viewer token")
}

// #2177: a form's page inside an asset runs at an opaque origin; a form
// answers its requests, never with credentials, and another endpoint does not.
func TestForm_AnswersTheOpaqueOriginOfAnEmbeddedPage(t *testing.T) {
	f := formFixture()
	pre := http.Header{"Origin": {"null"}, "Access-Control-Request-Method": {"POST"}}
	rec := f.do("OPTIONS", "/endpoints/contact/1", pre, "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "null", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"))
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), epform.ViewerHeader)

	rec = f.do("OPTIONS", "/endpoints/orders/1", pre, "")
	assert.Equal(t, http.StatusForbidden, rec.Code, "an endpoint that is not a form does not answer it")

	rec = f.do("GET", "/botcheck/challenge?endpoint=contact", http.Header{"Origin": {"null"}}, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "null", rec.Header().Get("Access-Control-Allow-Origin"))
}
