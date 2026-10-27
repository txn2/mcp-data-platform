// Package epformpage is the page the platform renders a form on (#2177,
// #2331): GET /forms/{endpoint}, a document of its own that fetches the spec
// the form's endpoint answers, renders its fields, answers the endpoint's bot
// check and posts the submission back, showing the field errors, the
// confirmation or following the redirect. An HTML or JSX asset embeds a form
// by declaring mcp:form:<endpoint>, which the asset is served with rewritten
// to this page's address, in a frame of its own inside the asset's; a site
// the platform does not host frames the same address, with the snippet the
// form's page in the portal offers.
//
// A site may frame the page only when the binding's allowed_origins list it
// (frame-ancestors). An asset frames it from inside a sandbox whose opaque
// origin no source expression names, so the asset's rewrite hands the page an
// embed marker (epform.EmbedParam), and a page opened with a valid one sends
// no frame-ancestors.
//
// The form's endpoint is called with no cookie. Who a signed-in person is
// reaches an authenticated form as a viewer token (epform.ViewerParam): the
// one an asset's rewrite wrote into the address, or, for a page opened
// directly, one the page signs from the browser's session. With neither, an
// authenticated form's page sends a browser opening it to sign in, and asks
// one that frames it to.
package epformpage

import (
	"context"
	_ "embed"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
)

// The routes.
const (
	// PathPrefix is the form pages' prefix; a form's page is
	// PathPrefix + its endpoint's name. It is epform.PagePrefix written out,
	// because the route gates read a pattern from this package's constants.
	PathPrefix = "/forms/"
	// ScriptPath serves the page's script. An endpoint name never starts
	// with "_", so it is never a form's page.
	ScriptPath = PathPrefix + "_/form.js"
	// solverPath is the bot check's browser script (internal/endpoint/ephttp).
	solverPath = "/botcheck/solver.js"
	// endpointsPrefix is where the form's endpoint answers.
	endpointsPrefix = "/endpoints/"
	// loginPath is the browser sign-in (pkg/browsersession's flow).
	loginPath = "/portal/auth/login"
	// ThemeParam picks the page's theme ("light" or "dark") over the
	// viewer's preference; the page does not pass it to the form.
	ThemeParam = "form_theme"
	// HeightMessage is the type of the message the page posts its parent
	// with its height, which the embed snippet sizes its frame by.
	HeightMessage = "mcp-form:height"
	// fetchDest is the header a browser names what a request is for in.
	fetchDest = "Sec-Fetch-Dest"
)

// contentPolicy is what the page is held to: its own script and the bot
// check's, requests to the deployment's origin, and the two bot check
// providers' widgets. The origin is named as well as 'self' because a page
// framed inside an asset has the asset's opaque origin. ancestors are the
// origins that may frame the page; nil sends no frame-ancestors, which is
// what an asset's frame needs.
func contentPolicy(origin string, ancestors []string) string {
	policy := "default-src 'none'; script-src 'self' " + origin + " https://challenges.cloudflare.com https://js.hcaptcha.com https://*.hcaptcha.com; " +
		"style-src 'self' 'unsafe-inline' https://*.hcaptcha.com; connect-src 'self' " + origin + " https://*.hcaptcha.com; " +
		"frame-src https://challenges.cloudflare.com https://*.hcaptcha.com; img-src 'self' data:; base-uri 'none'; form-action 'none'"
	if ancestors == nil {
		return policy
	}
	return policy + "; frame-ancestors " + strings.Join(append([]string{"'self'"}, ancestors...), " ")
}

//go:embed page.html
var pageHTML string

//go:embed form.js
var formJS []byte

var page = template.Must(template.New("form").Parse(pageHTML))

// Resolver is the endpoint runner, which says what a name serves.
type Resolver interface {
	Resolve(ctx context.Context, name string) (*eprun.Target, error)
}

// Handler serves the form pages.
type Handler struct {
	Runner Resolver
	// BaseURL is the deployment's public address; empty takes the request's
	// origin (assetrefs.BaseURL).
	BaseURL string
	// ViewerKey signs and opens viewer tokens and, derived from it, embed
	// markers (epform.ViewerKey); nil honors no marker and signs no viewer.
	ViewerKey []byte
	// Session is the signed-in person the request's session or key names,
	// nil for none (endpointwire.Authenticator).
	Session func(r *http.Request) (*eprun.Caller, error)
	// SignIn is where a browser signs in and comes back to returnTo; nil
	// when the deployment has no browser sign-in.
	SignIn func(returnTo string) string
}

// Mount registers the routes on mux.
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET "+ScriptPath, script)
	mux.HandleFunc("GET "+PathPrefix+"{name}", h.page)
}

// config is what the page is handed: the endpoint, where it answers, and
// what each request to it has to carry.
type config struct {
	Endpoint       string   `json:"endpoint"`
	URL            string   `json:"url"`
	BotCheck       bool     `json:"bot_check"`
	CheckedMethods []string `json:"checked_methods"`
	ViewerParam    string   `json:"viewer_param"`
	ViewerHeader   string   `json:"viewer_header"`
	ThemeParam     string   `json:"theme_param"`
	EmbedParam     string   `json:"embed_param"`
	HeightMessage  string   `json:"height_message"`
	// Viewer is the viewer token the page signed from the browser's
	// session, sent in place of one in the address.
	Viewer string `json:"viewer,omitempty"`
	// SignIn is where the page asks the person to sign in, set when an
	// authenticated form has no one to submit as.
	SignIn string `json:"sign_in,omitempty"`
	// Refused says why the signed-in person cannot fill in the form.
	Refused string `json:"refused,omitempty"`
}

// page answers a form's page, or 404 for a name that serves no enabled form.
func (h *Handler) page(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	t, err := h.Runner.Resolve(r.Context(), name)
	switch {
	case errors.Is(err, epbinding.ErrNotFound) || (err == nil && (!t.Binding.Enabled || !t.Form)):
		http.Error(w, "No form answers at this address.", http.StatusNotFound)
		return
	case err != nil:
		slog.ErrorContext(r.Context(), "form page: resolving the endpoint failed", "error", logsan.SanitizeForLog(err.Error()))
		http.Error(w, "The form could not be read.", http.StatusInternalServerError)
		return
	}
	b := t.Binding
	methods := b.CheckedMethods
	if methods == nil {
		methods = []string{}
	}
	cfg := config{
		Endpoint: b.Name, URL: endpointsPrefix + b.Name, BotCheck: b.AuthMode == epbinding.AuthBotCheck,
		CheckedMethods: methods, ViewerParam: epform.ViewerParam, ViewerHeader: epform.ViewerHeader, ThemeParam: ThemeParam,
		EmbedParam: epform.EmbedParam, HeightMessage: HeightMessage,
	}
	if b.AuthMode == epbinding.AuthAuthenticated && !h.identify(w, r, &cfg) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", contentPolicy(assetrefs.BaseURL(r, h.BaseURL), h.ancestors(r, b)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	if err := page.Execute(w, map[string]any{"Config": cfg, "Solver": solverPath, "Script": ScriptPath}); err != nil {
		slog.WarnContext(r.Context(), "form page: writing the page failed", "error", logsan.SanitizeForLog(err.Error()))
	}
}

// ancestors are the origins that may frame the page for r: none for a page
// opened with a valid embed marker (an asset's frame, from an opaque origin),
// otherwise the binding's allowed_origins.
func (h *Handler) ancestors(r *http.Request, b *epbinding.Binding) []string {
	if epform.OpenEmbed(epform.EmbedKey(h.ViewerKey), b.Name, r.URL.Query().Get(epform.EmbedParam), time.Now()) {
		return nil
	}
	if b.AllowedOrigins == nil {
		return []string{}
	}
	return b.AllowedOrigins
}

// identify finds who an authenticated form's page submits as: the viewer
// token in its address, or one signed from the request's session. With
// neither it sends a browser opening the page to sign in, and reports false,
// or has the page ask a frame's reader to. A session the platform refuses is
// never sent to sign in again, which would sign it in at once and refuse it
// again: the page says it cannot be filled in.
func (h *Handler) identify(w http.ResponseWriter, r *http.Request, cfg *config) bool {
	if r.URL.Query().Get(epform.ViewerParam) != "" {
		return true
	}
	tok, refused := h.sessionViewer(r, cfg.Endpoint)
	switch {
	case refused:
		cfg.Refused = "Your account cannot fill in this form."
		return true
	case tok != "":
		cfg.Viewer = tok
		return true
	case h.SignIn == nil:
		return true
	}
	cfg.SignIn = h.SignIn(r.URL.RequestURI())
	if r.Header.Get(fetchDest) == "iframe" {
		return true
	}
	// The target is always the platform's own sign-in path; only the escaped
	// return_to comes from the request, and the sign-in keeps a site-relative
	// path of it and nothing else (browsersession.sanitizeReturnTo).
	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect
	http.Redirect(w, r, cfg.SignIn, http.StatusSeeOther)
	return false
}

// sessionViewer is the viewer token for the person the request's session
// names, "" for none or when none can be signed, and whether the platform
// refused the session (an external persona reaches no endpoint).
func (h *Handler) sessionViewer(r *http.Request, endpoint string) (string, bool) {
	if h.Session == nil || len(h.ViewerKey) == 0 {
		return "", false
	}
	c, err := h.Session(r)
	if err != nil {
		return "", true
	}
	if c == nil {
		return "", false
	}
	v := epform.Viewer{UserID: c.UserID, Email: c.Email, AuthType: c.AuthType, Persona: c.Persona, Admin: c.Admin}
	tok, err := epform.SignViewer(h.ViewerKey, endpoint, v, time.Now())
	if err != nil {
		return "", false
	}
	return tok, false
}

// LoginURL is the deployment's browser sign-in returning to returnTo.
func LoginURL(returnTo string) string {
	return loginPath + "?" + url.Values{"return_to": {returnTo}}.Encode()
}

// script serves the page's script.
func script(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(formJS)
}
