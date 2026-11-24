// Package brandingapi is the unauthenticated branding route the portal reads
// before anyone signs in: the deployment's name, title, logos, sign-in options
// and the scheme its references are written under (#2776). Extracted from
// pkg/admin for its size budget.
package brandingapi

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/buildinfo"
	"github.com/txn2/mcp-data-platform/internal/platform/portalcfg"
	"github.com/txn2/mcp-data-platform/internal/refscheme"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// Response is returned by the unauthenticated branding endpoint.
type Response struct {
	Name            string `json:"name" example:"acme-data-platform"`
	Version         string `json:"version" example:"1.55.11"`
	PortalTitle     string `json:"portal_title" example:"ACME Data Platform"`
	BrandName       string `json:"brand_name" example:"ACME"`
	BrandURL        string `json:"brand_url" example:"https://acme.example.com"`
	VersionURL      string `json:"version_url" example:"https://acme.example.com/changelog"`
	PortalTagline   string `json:"portal_tagline" example:"Sign in to access your data."`
	OIDCButtonLabel string `json:"oidc_button_label" example:"Sign in with ACME Keycloak"`
	PortalLogo      string `json:"portal_logo" example:"https://example.com/logo.svg"`
	PortalLogoLight string `json:"portal_logo_light" example:"https://example.com/logo-light.svg"`
	PortalLogoDark  string `json:"portal_logo_dark" example:"https://example.com/logo-dark.svg"`
	OIDCEnabled     bool   `json:"oidc_enabled" example:"false"`
	// LoginRedirect: the login screen sends a signed-out visitor straight to
	// the identity provider; true only with portal.login_redirect and OIDC on.
	LoginRedirect bool `json:"login_redirect" example:"false"`
	// AllowAPIKeyLogin: /portal/?login=api_key offers the API key form.
	AllowAPIKeyLogin bool `json:"allow_api_key_login" example:"true"`
	// ReferenceScheme is the scheme references are written under (#2776):
	// the portal copies and shows references with it, and reads both it and
	// mcp:.
	ReferenceScheme string `json:"reference_scheme" example:"mcp"`
}

// Handler answers GET /api/v1/admin/public/branding.
// This endpoint is unauthenticated and returns only non-sensitive display info.
func Handler(cfg *platform.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { write(w, cfg) }
}

func write(w http.ResponseWriter, cfg *platform.Config) {
	resp := Response{
		Version:          buildinfo.Version,
		AllowAPIKeyLogin: true,
		ReferenceScheme:  refscheme.Scheme(),
	}
	if cfg != nil {
		resp.Name = cfg.Server.Name
		resp.PortalTitle = cfg.Portal.Title
		resp.BrandName = cfg.Portal.BrandName
		resp.BrandURL = cfg.Portal.BrandURL
		resp.VersionURL = cfg.Portal.VersionURL
		resp.PortalTagline = cfg.Portal.Tagline
		resp.OIDCButtonLabel = cfg.Portal.OIDCButtonLabel
		resp.PortalLogo = cfg.Portal.Logo
		resp.PortalLogoLight = cfg.Portal.LogoLight
		resp.PortalLogoDark = cfg.Portal.LogoDark
		resp.OIDCEnabled = cfg.Auth.BrowserSession.Enabled && cfg.Auth.OIDC.Enabled
		resp.LoginRedirect, _ = portalcfg.LoginRedirect(cfg.Portal.LoginRedirect, resp.OIDCEnabled)
		resp.AllowAPIKeyLogin = portalcfg.APIKeyLoginAllowed(cfg.Portal.AllowAPIKeyLogin)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = wirejson.Encode(w, resp)
}
