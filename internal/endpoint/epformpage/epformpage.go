// Package epformpage is the page the platform renders a form on (#2177):
// GET /portal/forms/{endpoint}, a document of its own that fetches the spec
// the form's endpoint answers, renders its fields, answers the endpoint's bot
// check and posts the submission back, showing the field errors, the
// confirmation or following the redirect. An HTML or JSX asset embeds a form
// by declaring mcp:form:<endpoint>, which the asset is served with rewritten
// to this page's address, in a frame of its own inside the asset's.
//
// The page takes no session. A form embedded in an asset is framed with the
// asset's sandbox, whose opaque origin carries none, so who a signed-in viewer
// is reaches an authenticated form as the viewer token the asset's rewrite
// wrote into this page's address (epform.ViewerParam).
package epformpage

import (
	"context"
	_ "embed"
	"errors"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
)

// The routes.
const (
	// PathPrefix is the form pages' prefix; a form's page is
	// PathPrefix + its endpoint's name.
	PathPrefix = "/portal/forms/"
	// ScriptPath serves the page's script. An endpoint name never starts
	// with "_", so it is never a form's page.
	ScriptPath = PathPrefix + "_/form.js"
	// solverPath is the bot check's browser script (internal/endpoint/ephttp).
	solverPath = "/botcheck/solver.js"
	// endpointsPrefix is where the form's endpoint answers.
	endpointsPrefix = "/endpoints/"
	// ThemeParam picks the page's theme ("light" or "dark") over the
	// viewer's preference; the page does not pass it to the form.
	ThemeParam = "form_theme"
)

// contentPolicy is what the page is held to: its own script and the bot
// check's, requests to the deployment's origin, and the two bot check
// providers' widgets. The origin is named as well as 'self' because a page
// framed inside an asset has the asset's opaque origin. It sets no
// frame-ancestors, since an asset frames it from an opaque origin no source
// expression names.
func contentPolicy(origin string) string {
	return "default-src 'none'; script-src 'self' " + origin + " https://challenges.cloudflare.com https://js.hcaptcha.com https://*.hcaptcha.com; " +
		"style-src 'self' 'unsafe-inline' https://*.hcaptcha.com; connect-src 'self' " + origin + " https://*.hcaptcha.com; " +
		"frame-src https://challenges.cloudflare.com https://*.hcaptcha.com; img-src 'self' data:; base-uri 'none'; form-action 'none'"
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
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", contentPolicy(assetrefs.BaseURL(r, h.BaseURL)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	if err := page.Execute(w, map[string]any{"Config": cfg, "Solver": solverPath, "Script": ScriptPath}); err != nil {
		slog.WarnContext(r.Context(), "form page: writing the page failed", "error", logsan.SanitizeForLog(err.Error()))
	}
}

// script serves the page's script.
func script(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(formJS)
}
