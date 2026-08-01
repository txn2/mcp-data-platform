// Package accessgate refuses portal requests from callers whose roles map to no
// persona.
//
// Authenticating proves who a caller is; it does not decide what they may
// reach. The portal's authenticator admits any identity the configured
// providers accept, so on its own it would hand a session — and with it every
// org-shared knowledge page and the whole federated search surface — to any
// account an identity provider will issue a token for. This package is the
// authorization half: it resolves the caller's persona and refuses the request
// when there is none.
//
// It lives outside pkg/portal so the refusal, its branded page, and its tests
// stay one cohesive unit that the portal handler composes rather than embeds.
package accessgate

import (
	"embed"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

//go:embed templates/access_denied.html
var templateFS embed.FS

var deniedTemplate = template.Must(template.ParseFS(templateFS, "templates/access_denied.html"))

// PersonaResolver reports the persona a set of roles maps to, or nil when the
// roles map to none. It is satisfied by portal.PersonaResolver.
type PersonaResolver func(roles []string) *portal.PersonaInfo

// Brand is the chrome the denial page renders with, mirroring the share-guest
// landing page so a refusal reads as the same product as an admission.
type Brand struct {
	Name               string
	LogoSVG            string
	URL                string
	ImplementorName    string
	ImplementorLogoSVG string
	ImplementorURL     string
}

// Gate authorizes authenticated portal callers by persona.
type Gate struct {
	resolve PersonaResolver
	brand   Brand
}

// New creates a Gate. A nil resolver denies every caller: the gate is the
// portal's authorization boundary, and a boundary that cannot evaluate its
// input refuses rather than admits.
func New(resolve PersonaResolver, brand Brand) *Gate {
	return &Gate{resolve: resolve, brand: brand}
}

// deniedCSP locks the denial page to its own inline style. The page has no
// script, no form, and no remote assets; the inline SVG logos are markup, not
// image requests, so img-src covers only the data: favicon case.
const deniedCSP = "default-src 'none'; style-src 'unsafe-inline'; img-src data:"

// deniedMessage is the plain-text refusal, used verbatim for non-navigation
// callers (the SPA's fetches, API clients) and as the page's body copy.
const deniedMessage = "Your account is not assigned to a persona. " +
	"Ask an administrator to grant your account access."

// Allows reports whether roles map to a persona. Exported so a caller that has
// already authenticated — the SPA shell gate — can ask the same question the
// middleware asks without producing a response.
func (g *Gate) Allows(roles []string) bool {
	if g == nil || g.resolve == nil {
		return false
	}
	return g.resolve(roles) != nil
}

// Require returns middleware that refuses callers whose roles map to no
// persona. It must be composed INSIDE the portal's authentication middleware:
// it reads the authenticated user that middleware puts on the context, and
// treats a missing user as unauthenticated (401) rather than silently allowing
// the request through.
func (g *Gate) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := portal.GetUser(r.Context())
		if user == nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		if !g.Allows(user.Roles) {
			// Logged at warn like the MCP authorizer's denial: a refusal here is
			// either an account that needs granting or someone probing, and an
			// operator asked "why can't they get in" needs the roles they
			// actually presented.
			slog.Warn("portal access denied: no persona for roles",
				"user_id", logsan.SanitizeForLog(user.UserID),
				"email", logsan.SanitizeForLog(user.Email),
				"roles", logsan.SanitizeForLog(strings.Join(user.Roles, ",")),
				"path", logsan.SanitizeForLog(r.URL.Path),
			)
			g.Deny(w, r, user.Email)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// problemDetail is the RFC 9457 body the portal's own handlers return, extended
// with the refused account so a client that never got as far as /me can still
// name who it is signed in as. The status is fixed at 403 by the one caller.
type problemDetail struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	Email  string `json:"email,omitempty"`
}

// Deny writes the 403. A top-level browser navigation gets the branded page so
// the person reading it learns which account was refused and what to do about
// it; everything else gets the Problem Details body the portal's other errors
// use, so the SPA can tell "no access" apart from "signed out" and render the
// refusal in place instead of bouncing back through the identity provider.
func (g *Gate) Deny(w http.ResponseWriter, r *http.Request, email string) {
	if !isHTMLNavigation(r) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(problemDetail{
			Type:   "about:blank",
			Title:  http.StatusText(http.StatusForbidden),
			Status: http.StatusForbidden,
			Detail: deniedMessage,
			Email:  email,
		})
		return
	}
	w.Header().Set("Content-Security-Policy", deniedCSP)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_ = deniedTemplate.Execute(w, g.pageData(email))
}

// pageData assembles the template context for one refusal.
func (g *Gate) pageData(email string) map[string]any {
	return map[string]any{
		"BrandName": g.brandName(),
		// #nosec G203 -- operator-provided SVG from config, not user input
		"BrandLogoSVG": template.HTML(g.brand.LogoSVG),
		"BrandURL":     g.brand.URL,
		// #nosec G203 -- operator-provided SVG from config, not user input
		"ImplementorLogoSVG": template.HTML(g.brand.ImplementorLogoSVG),
		"ImplementorName":    g.brand.ImplementorName,
		"ImplementorURL":     g.brand.ImplementorURL,
		"Title":              "You do not have access yet",
		"Message":            deniedMessage,
		"Email":              email,
		"SignOutURL":         "/portal/auth/logout",
	}
}

// brandName returns the configured brand, defaulting to the platform name the
// public viewer and share-guest pages use.
func (g *Gate) brandName() string {
	if g.brand.Name == "" {
		return "MCP Data Platform"
	}
	return g.brand.Name
}

// isHTMLNavigation reports whether the request is a top-level browser
// navigation that should receive a page rather than a bare status.
func isHTMLNavigation(r *http.Request) bool {
	return r.Method == http.MethodGet &&
		strings.Contains(r.Header.Get("Accept"), "text/html")
}
