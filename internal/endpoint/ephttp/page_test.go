package ephttp

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

const (
	appShop   = "app_shop"
	appOther  = "app_other"
	testNonce = "dGVzdC1ub25jZQ"
	origin    = "https://www.client.example"
	cspHeader = "Content-Security-Policy"
)

// stampedBindings gives each binding the application its stamp names, the one
// thing only a real Runner.Resolve can put on a Target.
type stampedBindings struct {
	epbinding.Store
	byName map[string]*epbinding.Binding
	apps   map[string]string
}

func (s *stampedBindings) GetByName(_ context.Context, name string) (*epbinding.Binding, error) {
	if b, ok := s.byName[name]; ok {
		return b, nil
	}
	return nil, epbinding.ErrNotFound
}

func (s *stampedBindings) Stamp(_ context.Context, name string) (epbinding.Stamp, error) {
	if _, ok := s.byName[name]; !ok {
		return epbinding.Stamp{}, epbinding.ErrNotFound
	}
	return epbinding.Stamp{Binding: time.Unix(1, 0), Application: s.apps[name]}, nil
}

type oneScript struct{}

func (oneScript) GetByID(_ context.Context, id string) (*script.Script, error) {
	return &script.Script{ID: id, Version: 1}, nil
}

func (oneScript) GetVersion(_ context.Context, id string, version int) (*script.Version, error) {
	return &script.Version{ID: "v1", ScriptID: id, Version: version, Source: "def handle(req):\n    return {}\n"}, nil
}

// pageRunner resolves through a real eprun.Runner and answers through the
// fake, which records what the surface passed on.
type pageRunner struct {
	real *eprun.Runner
	fake *runner
}

func (p *pageRunner) Resolve(ctx context.Context, name string) (*eprun.Target, error) {
	return p.real.Resolve(ctx, name) //nolint:wrapcheck // the test's own pass-through
}

func (p *pageRunner) Serve(ctx context.Context, t *eprun.Target, req eprun.Request) eprun.Response {
	return p.fake.Serve(ctx, t, req)
}

type pageFixture struct {
	handler  *Handler
	fake     *runner
	bindings *stampedBindings
	portal   int
}

func newPageFixture() *pageFixture {
	f := &pageFixture{
		fake:     &runner{},
		bindings: &stampedBindings{byName: map[string]*epbinding.Binding{}, apps: map[string]string{}},
	}
	f.handler = &Handler{
		Runner: &pageRunner{real: &eprun.Runner{Bindings: f.bindings, Scripts: oneScript{}}, fake: f.fake},
		// A portal session that would succeed: a page origin must not read it.
		Authenticate: func(*http.Request) (*eprun.Caller, error) {
			f.portal++
			return &eprun.Caller{UserID: "portal", Email: "portal@example.com", Admin: true}, nil
		},
	}
	return f
}

// add registers an endpoint named name in application app.
func (f *pageFixture) add(name, mode, responseMode, app string) *epbinding.Binding {
	b := binding(name, mode)
	b.Path, b.ScriptID, b.ResponseMode = "", "script_"+name, responseMode
	b.CallerRateLimit = 100
	f.bindings.byName[name] = b
	f.bindings.apps[name] = app
	return b
}

func viewerRoute(name string, caller *eprun.Caller) PageRoute {
	return PageRoute{
		Application: appShop, Name: name, Nonce: testNonce,
		Caller: func(*http.Request) (*eprun.Caller, error) { return caller, nil },
	}
}

func (f *pageFixture) serve(method string, route PageRoute, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, "http://shop.pages.example.com/", strings.NewReader(""))
	maps.Copy(req.Header, header)
	rec := httptest.NewRecorder()
	f.handler.ServePage(rec, req, route)
	return rec
}

func TestServePage_AnswersAnEndpointOfTheApplication(t *testing.T) {
	f := newPageFixture()
	f.add("shop", epbinding.AuthPublic, epbinding.ResponsePage, appShop)
	rec := f.serve(http.MethodGet, viewerRoute("shop", nil), nil)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Len(t, f.fake.served, 1)
}

func TestServePage_AnEndpointOfAnotherApplicationAnswersAsIfAbsent(t *testing.T) {
	tests := []struct {
		name string
		app  string
	}{
		{name: "another application", app: appOther},
		{name: "no application", app: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPageFixture()
			f.add("orders", epbinding.AuthPublic, epbinding.ResponsePage, tt.app)
			rec := f.serve(http.MethodGet, viewerRoute("orders", nil), nil)
			assert.Equal(t, http.StatusNotFound, rec.Code)
			assert.Empty(t, f.fake.served, "the handler never ran")
		})
	}
}

func TestServePage_AnUnknownEndpointIs404(t *testing.T) {
	f := newPageFixture()
	assert.Equal(t, http.StatusNotFound, f.serve(http.MethodGet, viewerRoute("nope", nil), nil).Code)
}

func TestServePage_APageGetsThePagePolicyAndItsNonce(t *testing.T) {
	f := newPageFixture()
	b := f.add("shop", epbinding.AuthPublic, epbinding.ResponsePage, appShop)
	b.PageCDNs = []string{"https://cdn.example.com"}

	rec := f.serve(http.MethodGet, viewerRoute("shop", nil), nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, b.PagePolicy(testNonce), rec.Header().Get(cspHeader))
	assert.Contains(t, rec.Header().Get(cspHeader), "'nonce-"+testNonce+"'")
	require.Len(t, f.fake.served, 1)
	assert.Equal(t, testNonce, f.fake.served[0].CSPNonce)
}

func TestServePage_AnAPIBindingGetsTheSandboxPolicyAndNoNonce(t *testing.T) {
	f := newPageFixture()
	f.add("api", epbinding.AuthPublic, epbinding.ResponseAPI, appShop)

	rec := f.serve(http.MethodGet, viewerRoute("api", nil), nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, contentPolicy, rec.Header().Get(cspHeader))
	require.Len(t, f.fake.served, 1)
	assert.Empty(t, f.fake.served[0].CSPNonce)
}

func TestServeHTTP_APageBindingOffItsOriginIsSandboxed(t *testing.T) {
	f := newPageFixture()
	f.add("shop", epbinding.AuthPublic, epbinding.ResponsePage, appShop)
	mux := http.NewServeMux()
	f.handler.Mount(mux)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/endpoints/shop", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, contentPolicy, rec.Header().Get(cspHeader))
	assert.Empty(t, f.fake.served[0].CSPNonce)
}

func TestServePage_IdentityComesOnlyFromTheRoutesCaller(t *testing.T) {
	t.Run("the viewer the origin names is the caller", func(t *testing.T) {
		f := newPageFixture()
		f.add("orders", epbinding.AuthAuthenticated, epbinding.ResponseAPI, appShop)
		viewer := &eprun.Caller{UserID: "ann", Email: "ann@example.com", Persona: "analyst"}
		rec := f.serve(http.MethodGet, viewerRoute("orders", viewer), nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var out map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		assert.Equal(t, "ann@example.com", out["caller"])
		assert.Zero(t, f.portal, "a portal session is not read on a page origin")
	})

	t.Run("a portal session that would succeed does not sign in nobody", func(t *testing.T) {
		f := newPageFixture()
		f.add("orders", epbinding.AuthAuthenticated, epbinding.ResponseAPI, appShop)
		rec := f.serve(http.MethodGet, viewerRoute("orders", nil), http.Header{"Authorization": {"Bearer anything"}})
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Empty(t, f.fake.served)
		assert.Zero(t, f.portal)
	})

	t.Run("a refused viewer is 403", func(t *testing.T) {
		f := newPageFixture()
		f.add("orders", epbinding.AuthAuthenticated, epbinding.ResponseAPI, appShop)
		route := viewerRoute("orders", nil)
		route.Caller = func(*http.Request) (*eprun.Caller, error) { return nil, ErrForbidden }
		assert.Equal(t, http.StatusForbidden, f.serve(http.MethodPost, route, nil).Code)
		assert.Empty(t, f.fake.served)
	})

	t.Run("a viewer read that fails is nobody", func(t *testing.T) {
		f := newPageFixture()
		f.add("orders", epbinding.AuthAuthenticated, epbinding.ResponseAPI, appShop)
		route := viewerRoute("orders", nil)
		route.Caller = func(*http.Request) (*eprun.Caller, error) { return nil, errors.New("bad token") }
		assert.Equal(t, http.StatusUnauthorized, f.serve(http.MethodGet, route, nil).Code)
	})
}

func TestServePage_SignIn(t *testing.T) {
	tests := []struct {
		name         string
		method       string
		responseMode string
		wantSignIn   bool
		wantStatus   int
	}{
		{name: "a GET of an authenticated page with nobody", method: http.MethodGet, responseMode: epbinding.ResponsePage, wantSignIn: true, wantStatus: http.StatusSeeOther},
		{name: "a POST to an authenticated page with nobody", method: http.MethodPost, responseMode: epbinding.ResponsePage, wantStatus: http.StatusUnauthorized},
		{name: "a GET of an authenticated api endpoint with nobody", method: http.MethodGet, responseMode: epbinding.ResponseAPI, wantStatus: http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPageFixture()
			f.add("orders", epbinding.AuthAuthenticated, tt.responseMode, appShop)
			signIns := 0
			route := viewerRoute("orders", nil)
			route.SignIn = func(w http.ResponseWriter, _ *http.Request) {
				signIns++
				w.WriteHeader(http.StatusSeeOther)
			}

			rec := f.serve(tt.method, route, nil)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantSignIn, signIns == 1)
			assert.Empty(t, f.fake.served)
			if tt.wantStatus == http.StatusUnauthorized {
				assert.NotEmpty(t, rec.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

func TestServePage_APageWithNoSignInAnswers401(t *testing.T) {
	f := newPageFixture()
	f.add("orders", epbinding.AuthAuthenticated, epbinding.ResponsePage, appShop)
	assert.Equal(t, http.StatusUnauthorized, f.serve(http.MethodGet, viewerRoute("orders", nil), nil).Code)
}

func TestSiteOrigin_ThroughTheCORSAnswer(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		origin   string
		wantACAO string
	}{
		{name: "a public endpoint answers a listed site origin", mode: epbinding.AuthPublic, origin: origin, wantACAO: origin},
		{name: "a public endpoint does not answer another origin", mode: epbinding.AuthPublic, origin: "https://evil.example"},
		{name: "an authenticated endpoint never answers a site origin", mode: epbinding.AuthAuthenticated, origin: origin},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPageFixture()
			f.add("orders", tt.mode, epbinding.ResponseAPI, appShop)
			var asked []string
			f.handler.SiteOrigins = func(_ context.Context, applicationID string) []string {
				asked = append(asked, applicationID)
				return []string{origin}
			}
			viewer := &eprun.Caller{UserID: "ann", Email: "ann@example.com"}
			rec := f.serve(http.MethodGet, viewerRoute("orders", viewer), http.Header{"Origin": {tt.origin}})
			assert.Equal(t, tt.wantACAO, rec.Header().Get("Access-Control-Allow-Origin"))
			assert.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"), "credentials are never allowed across origins")
			if tt.mode == epbinding.AuthAuthenticated {
				assert.Empty(t, asked, "an authenticated endpoint never asks")
			} else {
				assert.Equal(t, []string{appShop}, asked)
			}
		})
	}
}

func TestSiteOrigin_Direct(t *testing.T) {
	f := newPageFixture()
	f.add("open", epbinding.AuthPublic, epbinding.ResponseAPI, appShop)
	f.add("bot", epbinding.AuthBotCheck, epbinding.ResponseAPI, appShop)
	f.add("private", epbinding.AuthAuthenticated, epbinding.ResponseAPI, appShop)
	sites := func(context.Context, string) []string { return []string{origin} }

	tests := []struct {
		name   string
		target string
		sites  func(context.Context, string) []string
		origin string
		want   bool
	}{
		{name: "a public endpoint and a listed origin", target: "open", sites: sites, origin: origin, want: true},
		{name: "a bot_check endpoint and a listed origin", target: "bot", sites: sites, origin: origin, want: true},
		{name: "an authenticated endpoint", target: "private", sites: sites, origin: origin},
		{name: "no Origin header", target: "open", sites: sites},
		{name: "nothing to ask", target: "open", origin: origin},
		{name: "an unlisted origin", target: "open", sites: sites, origin: "https://evil.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f.handler.SiteOrigins = tt.sites
			target, err := f.handler.Runner.Resolve(context.Background(), tt.target)
			require.NoError(t, err)
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://x.example/", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			assert.Equal(t, tt.want, f.handler.siteOrigin(req, target))
		})
	}
}
