package epformpage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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

var viewerKey = epform.ViewerKey([]byte("0123456789abcdef0123456789abcdef"))

// sessionHeader names the test session: a user id, or "refused" for one the
// platform refuses.
const sessionHeader = "X-Test-Session"

func session(r *http.Request) (*eprun.Caller, error) {
	switch who := r.Header.Get(sessionHeader); who {
	case "":
		return nil, nil //nolint:nilnil // no session is no caller and no error, as the authenticator answers
	case "refused":
		return nil, errors.New("an external persona reaches no endpoint")
	default:
		return &eprun.Caller{UserID: who, Email: who + "@example.com", Persona: "analyst"}, nil
	}
}

func targets() runner {
	return runner{
		"contact": {Form: true, Binding: &epbinding.Binding{Name: "contact", Enabled: true, AuthMode: epbinding.AuthBotCheck, CheckedMethods: []string{"POST"}}},
		"open":    {Form: true, Binding: &epbinding.Binding{Name: "open", Enabled: true, AuthMode: epbinding.AuthPublic}},
		"listed":  {Form: true, Binding: &epbinding.Binding{Name: "listed", Enabled: true, AuthMode: epbinding.AuthPublic, AllowedOrigins: []string{"https://www.example.org"}}},
		"members": {Form: true, Binding: &epbinding.Binding{Name: "members", Enabled: true, AuthMode: epbinding.AuthAuthenticated, Callers: []string{"*"}}},
		"off":     {Form: true, Binding: &epbinding.Binding{Name: "off", AuthMode: epbinding.AuthPublic}},
		"lookup":  {Binding: &epbinding.Binding{Name: "lookup", Enabled: true, AuthMode: epbinding.AuthPublic}},
	}
}

func fixture() *http.ServeMux {
	mux := http.NewServeMux()
	(&Handler{Runner: targets(), BaseURL: "https://data.example.com", ViewerKey: viewerKey, Session: session, SignIn: LoginURL}).Mount(mux)
	return mux
}

func get(mux *http.ServeMux, path string, headers ...string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	mux.ServeHTTP(rec, req)
	return rec
}

// pageConfig is the configuration a served page hands its script.
func pageConfig(t *testing.T, body string) config {
	t.Helper()
	_, rest, ok := strings.Cut(body, `<script type="application/json" id="form-config">`)
	require.True(t, ok, body)
	raw, _, _ := strings.Cut(rest, "</script>")
	var cfg config
	require.NoError(t, json.Unmarshal([]byte(raw), &cfg))
	return cfg
}

// #2177: a form's page carries what its script needs to fetch and post the
// form, under a policy that admits the deployment's origin and nothing more.
func TestPage_ServesAFormsPage(t *testing.T) {
	mux := fixture()
	rec := get(mux, "/forms/contact")
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
	assert.Contains(t, csp, "frame-ancestors 'self'", "a binding that lists no origin is framed by none")
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	cfg := pageConfig(t, body)
	assert.Equal(t, epform.EmbedParam, cfg.EmbedParam)
	assert.Equal(t, HeightMessage, cfg.HeightMessage)
	assert.Empty(t, cfg.Viewer)
	assert.Empty(t, cfg.SignIn, "a bot_check form asks no one to sign in")

	open := get(mux, "/forms/open").Body.String()
	assert.Contains(t, open, `"bot_check":false`)
	assert.Contains(t, open, `"checked_methods":[]`)
}

func TestPage_NoForm(t *testing.T) {
	mux := fixture()
	for _, name := range []string{"missing", "off", "lookup"} {
		assert.Equal(t, http.StatusNotFound, get(mux, "/forms/"+name).Code, name)
	}
	assert.Equal(t, http.StatusInternalServerError, get(mux, "/forms/broken").Code)
}

// #2331: a site may frame a form's page only when its binding lists the
// site's origin; an asset's frame, from an opaque origin, carries the embed
// marker its rewrite signed and is held to no frame-ancestors.
func TestPage_FrameAncestors(t *testing.T) {
	mux := fixture()
	ancestors := func(path string) string {
		_, fa, _ := strings.Cut(get(mux, path).Header().Get("Content-Security-Policy"), "frame-ancestors")
		return fa
	}
	assert.Equal(t, " 'self' https://www.example.org", ancestors("/forms/listed"))
	assert.Equal(t, " 'self'", ancestors("/forms/open"))

	marker := epform.SignEmbed(epform.EmbedKey(viewerKey), "listed", time.Now())
	assert.Empty(t, ancestors("/forms/listed?"+url.Values{epform.EmbedParam: {marker}}.Encode()), "an asset's frame")
	other := epform.SignEmbed(epform.EmbedKey(viewerKey), "open", time.Now())
	assert.Equal(t, " 'self' https://www.example.org",
		ancestors("/forms/listed?"+url.Values{epform.EmbedParam: {other}}.Encode()), "a marker for another form")
	stale := epform.SignEmbed(epform.EmbedKey(viewerKey), "listed", time.Now().Add(-epform.EmbedTTL-time.Hour))
	assert.Equal(t, " 'self' https://www.example.org",
		ancestors("/forms/listed?"+url.Values{epform.EmbedParam: {stale}}.Encode()), "an expired marker")
	assert.Equal(t, " 'self' https://www.example.org", ancestors("/forms/listed?form_embed=forged"))
}

// #2331: an authenticated form opened directly submits as the browser's
// signed-in person; one with no session sends a browser to sign in, or asks a
// frame's reader to; a session the platform refuses is never sent back.
func TestPage_AuthenticatedFormSignsIn(t *testing.T) {
	mux := fixture()

	signedIn := get(mux, "/forms/members", sessionHeader, "ann")
	require.Equal(t, http.StatusOK, signedIn.Code)
	cfg := pageConfig(t, signedIn.Body.String())
	who, err := epform.OpenViewer(viewerKey, "members", cfg.Viewer, time.Now())
	require.NoError(t, err)
	assert.Equal(t, epform.Viewer{UserID: "ann", Email: "ann@example.com", Persona: "analyst"}, who)
	assert.Empty(t, cfg.SignIn)

	opened := get(mux, "/forms/members?ref=42", fetchDest, "document")
	require.Equal(t, http.StatusSeeOther, opened.Code)
	assert.Equal(t, "/portal/auth/login?return_to="+url.QueryEscape("/forms/members?ref=42"), opened.Header().Get("Location"))

	framed := get(mux, "/forms/members", fetchDest, "iframe")
	require.Equal(t, http.StatusOK, framed.Code)
	assert.Equal(t, LoginURL("/forms/members"), pageConfig(t, framed.Body.String()).SignIn)

	refused := get(mux, "/forms/members", sessionHeader, "refused", fetchDest, "document")
	require.Equal(t, http.StatusOK, refused.Code, "a refused session is not sent to sign in again")
	rc := pageConfig(t, refused.Body.String())
	assert.NotEmpty(t, rc.Refused)
	assert.Empty(t, rc.SignIn)

	carried := get(mux, "/forms/members?"+epform.ViewerParam+"=tok", fetchDest, "document")
	require.Equal(t, http.StatusOK, carried.Code, "an asset's viewer token is the person")
	assert.Empty(t, pageConfig(t, carried.Body.String()).Viewer)
}

// Without a browser sign-in or a key, an authenticated form's page is served
// as it is and the endpoint answers who may submit.
func TestPage_AuthenticatedFormWithoutSignIn(t *testing.T) {
	mux := http.NewServeMux()
	(&Handler{Runner: targets()}).Mount(mux)
	rec := get(mux, "/forms/members", fetchDest, "document", sessionHeader, "ann")
	require.Equal(t, http.StatusOK, rec.Code)
	cfg := pageConfig(t, rec.Body.String())
	assert.Empty(t, cfg.Viewer)
	assert.Empty(t, cfg.SignIn)
}

func TestScript(t *testing.T) {
	rec := get(fixture(), ScriptPath)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript"))
	assert.Contains(t, rec.Body.String(), "PlatformBotCheck")
}

// The path assetrefs restates to stay free of this layer is the one served
// here.
func TestReferenceRewriteNamesThisPage(t *testing.T) {
	assert.Equal(t, PathPrefix, assetrefs.FormPagePrefix)
	assert.Equal(t, PathPrefix, epform.PagePrefix)
	assert.Equal(t, epbinding.PathPrefix, assetrefs.EndpointPathPrefix, "an endpoint reference is rewritten to where endpoints answer (#2457)")
}

// previews owns one preview, fp1, opened by ann.
type previews struct{ err error }

func (p previews) Owner(_ context.Context, id string) (string, error) {
	if p.err != nil {
		return "", p.err
	}
	if id != "fp1" {
		return "", ErrNoPreview
	}
	return "ann@example.com", nil
}

// TestPreview is #2494's preview page: rendered for the person who opened
// it, signed for the preview's own name and answered at its own route;
// refused to anyone else; a browser with no session is sent to sign in.
func TestPreview(t *testing.T) {
	mux := http.NewServeMux()
	h := &Handler{Runner: targets(), BaseURL: "https://data.example.com", ViewerKey: viewerKey, Session: session, SignIn: LoginURL, Previews: previews{}}
	h.Mount(mux)

	rec := get(mux, "/forms/_preview/fp1", sessionHeader, "ann")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	cfg := pageConfig(t, rec.Body.String())
	assert.True(t, cfg.Preview)
	assert.Equal(t, "/endpoints/_preview/fp1", cfg.URL)
	assert.Equal(t, eprun.PreviewPrefix+"fp1", cfg.Endpoint)
	v, err := epform.OpenViewer(viewerKey, eprun.PreviewPrefix+"fp1", cfg.Viewer, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "ann@example.com", v.Email)
	assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'self'")

	rec = get(mux, "/forms/_preview/fp1", sessionHeader, "bob")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	cfg = pageConfig(t, rec.Body.String())
	assert.Contains(t, cfg.Refused, "belongs to the person who opened it")
	assert.Empty(t, cfg.Viewer)
	assert.Equal(t, http.StatusForbidden, get(mux, "/forms/_preview/fp1", sessionHeader, "refused").Code)

	rec = get(mux, "/forms/_preview/fp1")
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Contains(t, rec.Header().Get("Location"), "/portal/auth/login?return_to=%2Fforms%2F_preview%2Ffp1")

	assert.Equal(t, http.StatusNotFound, get(mux, "/forms/_preview/gone", sessionHeader, "ann").Code)
	broken := http.NewServeMux()
	(&Handler{Runner: targets(), Previews: previews{err: errors.New("down")}}).Mount(broken)
	assert.Equal(t, http.StatusInternalServerError, get(broken, "/forms/_preview/fp1").Code)
	nosession := http.NewServeMux()
	(&Handler{Runner: targets(), Previews: previews{}}).Mount(nosession)
	assert.Equal(t, http.StatusForbidden, get(nosession, "/forms/_preview/fp1").Code, "no session reader: nobody")
}

// #2494: a signed-in person opening a public form's page is signed a viewer,
// so they are served the version the script runs.
func TestPage_PublicFormSignsASignedInViewer(t *testing.T) {
	mux := fixture()
	cfg := pageConfig(t, get(mux, "/forms/open", sessionHeader, "ann").Body.String())
	v, err := epform.OpenViewer(viewerKey, "open", cfg.Viewer, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "ann@example.com", v.Email)
	assert.Empty(t, pageConfig(t, get(mux, "/forms/open").Body.String()).Viewer, "no session, no viewer")
}
