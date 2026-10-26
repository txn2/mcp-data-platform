package epformpage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
)

type runner map[string]*eprun.Target

func (r runner) Resolve(_ context.Context, name string) (*eprun.Target, error) {
	if name == "broken" {
		return nil, errors.New("db down")
	}
	t, ok := r[name]
	if !ok {
		return nil, epbinding.ErrNotFound
	}
	return t, nil
}

func fixture() *http.ServeMux {
	r := runner{
		"contact": {Form: true, Binding: &epbinding.Binding{Name: "contact", Enabled: true, AuthMode: epbinding.AuthBotCheck, CheckedMethods: []string{"POST"}}},
		"open":    {Form: true, Binding: &epbinding.Binding{Name: "open", Enabled: true, AuthMode: epbinding.AuthPublic}},
		"off":     {Form: true, Binding: &epbinding.Binding{Name: "off", AuthMode: epbinding.AuthPublic}},
		"lookup":  {Binding: &epbinding.Binding{Name: "lookup", Enabled: true, AuthMode: epbinding.AuthPublic}},
	}
	mux := http.NewServeMux()
	(&Handler{Runner: r, BaseURL: "https://data.example.com"}).Mount(mux)
	return mux
}

func get(mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody))
	return rec
}

// #2177: a form's page carries what its script needs to fetch and post the
// form, under a policy that admits the deployment's origin and nothing more.
func TestPage_ServesAFormsPage(t *testing.T) {
	mux := fixture()
	rec := get(mux, "/portal/forms/contact")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `"endpoint":"contact"`)
	assert.Contains(t, body, `"url":"/endpoints/contact"`)
	assert.Contains(t, body, `"bot_check":true`)
	assert.Contains(t, body, `"checked_methods":["POST"]`)
	assert.Contains(t, body, `"viewer_param":"`+epform.ViewerParam+`"`)
	assert.Contains(t, body, `src="/botcheck/solver.js"`)
	assert.Contains(t, body, `src="`+ScriptPath+`"`)
	csp := rec.Header().Get("Content-Security-Policy")
	assert.Contains(t, csp, "script-src 'self' https://data.example.com ")
	assert.Contains(t, csp, "connect-src 'self' https://data.example.com ")
	assert.Contains(t, csp, "form-action 'none'")
	assert.NotContains(t, csp, "frame-ancestors", "an asset frames it from an opaque origin")
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

	open := get(mux, "/portal/forms/open").Body.String()
	assert.Contains(t, open, `"bot_check":false`)
	assert.Contains(t, open, `"checked_methods":[]`)
}

func TestPage_NoForm(t *testing.T) {
	mux := fixture()
	for _, name := range []string{"missing", "off", "lookup"} {
		assert.Equal(t, http.StatusNotFound, get(mux, "/portal/forms/"+name).Code, name)
	}
	assert.Equal(t, http.StatusInternalServerError, get(mux, "/portal/forms/broken").Code)
}

func TestScript(t *testing.T) {
	rec := get(fixture(), ScriptPath)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript"))
	assert.Contains(t, rec.Body.String(), "PlatformBotCheck")
}

// The paths and parameter assetrefs restates to stay free of this layer are
// the ones served here.
func TestReferenceRewriteNamesThisPage(t *testing.T) {
	assert.Equal(t, PathPrefix, assetrefs.FormPagePrefix)
	assert.Equal(t, epform.ViewerParam, assetrefs.FormViewerParam)
}
