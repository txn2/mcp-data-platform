package shareguest

import (
	"context"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

//go:embed templates/landing.html
var templateFS embed.FS

var landingTemplate = template.Must(template.ParseFS(templateFS, "templates/landing.html"))

// Brand is the chrome the landing pages render with, mirroring the public
// viewer's header so a denial reads as the same product as an admission.
type Brand struct {
	Name               string
	LogoSVG            string
	URL                string
	ImplementorName    string
	ImplementorLogoSVG string
	ImplementorURL     string
}

// Denial describes one refused share request for rendering. The portal's
// share gate builds it from the share and verdict it already holds.
type Denial struct {
	// Status is the HTTP status of the refusal: 403 or 410.
	Status int
	// Message is the plain-text denial, used verbatim for non-HTML requests
	// and as page copy.
	Message string
	// Token is the share's viewer token, used to build the sign-in return
	// path and the one-time-link request path.
	Token string
	// RecipientEmail is the share's stored recipient address. It is only ever
	// used as a boolean (does this share name someone a link could go to);
	// the rendered page never displays it.
	RecipientEmail string
	// SignedInEmail, when non-empty, marks the wrong-account state: the
	// caller is signed in as this address but the share names someone else.
	// Naming it on the page makes the wrong-account case self-diagnosing.
	SignedInEmail string
}

// landingCSP locks the landing page down to its own inline style and script;
// the request-link button needs connect-src for its same-origin POST.
const landingCSP = "default-src 'none'; style-src 'unsafe-inline'; " +
	"script-src 'unsafe-inline'; img-src data:; connect-src 'self'"

// linkStatusParam is the query parameter a failed one-time-link claim
// redirects back with, so the landing page explains what happened and offers
// a fresh request.
const linkStatusParam = "link"

// linkStatusInvalid is the linkStatusParam value for a used or expired link.
const linkStatusInvalid = "invalid"

// Deny writes the refusal for a gated share request. Browser navigations get
// a branded page; everything else (the content and thumbnail subresources the
// pages fetch, API-style callers) keeps the plain-text status the gate always
// sent. Nil-safe: without a service the caller falls back to plain text.
func (s *Service) Deny(w http.ResponseWriter, r *http.Request, d Denial) {
	if !isHTMLNavigation(r) {
		http.Error(w, d.Message, d.Status)
		return
	}
	data := s.landingData(r, d)
	w.Header().Set("Content-Security-Policy", landingCSP)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(d.Status)
	_ = landingTemplate.Execute(w, data)
}

// isHTMLNavigation reports whether the request is a top-level browser
// navigation that should receive a page rather than a bare status.
func isHTMLNavigation(r *http.Request) bool {
	return r.Method == http.MethodGet &&
		strings.Contains(r.Header.Get("Accept"), "text/html")
}

// landingData assembles the template context for one denial.
func (s *Service) landingData(r *http.Request, d Denial) map[string]any {
	data := map[string]any{
		"BrandName":          s.brandName(),
		"BrandLogoSVG":       template.HTML(s.brand.LogoSVG), // #nosec G203 -- operator-provided SVG from config, not user input
		"BrandURL":           s.brand.URL,
		"ImplementorName":    s.brand.ImplementorName,
		"ImplementorLogoSVG": template.HTML(s.brand.ImplementorLogoSVG), // #nosec G203 -- operator-provided SVG from config
		"ImplementorURL":     s.brand.ImplementorURL,
		"Title":              "This item was shared privately",
		"Message":            d.Message,
	}
	viewPath := "/portal/view/" + url.PathEscape(d.Token)
	switch {
	case d.Status == http.StatusGone:
		data["Title"] = "This link is no longer available"
	case d.SignedInEmail != "":
		data["Title"] = "This link was shared with someone else"
		data["Message"] = "It is restricted to the person it was shared with, and you are signed in as " +
			d.SignedInEmail + ". Ask the sender to share it with you, or switch accounts."
		data["SignOutURL"] = "/portal/auth/logout"
	default:
		data["Message"] = "Sign in to view it with the access you were given."
		data["SignInURL"] = "/portal/auth/login?return_to=" + url.QueryEscape(viewPath)
		if d.RecipientEmail != "" && s.linksEnabled() {
			data["RequestLinkPath"] = viewPath + "/request-link"
		}
		if r.URL.Query().Get(linkStatusParam) == linkStatusInvalid {
			data["LinkNotice"] = "That one-time link was already used or has expired."
		}
		s.applyOptOutState(r.Context(), d, viewPath, data)
	}
	return data
}

// applyOptOutState adds the opt-out notice and opt-back-in action for an
// email share whose recipient has unsubscribed from notification emails
// (#1022). The landing page is the recipient's natural re-engagement point:
// it already knows the share's address, and an opted-out guest otherwise has
// no path back in short of asking the sharer. The copy stays third-person
// (like the one-time-link hint) because the page cannot know the viewer is
// the recipient, and it never displays the address itself.
func (s *Service) applyOptOutState(ctx context.Context, d Denial, viewPath string, data map[string]any) {
	if d.RecipientEmail == "" || !s.optedOut(ctx, d.RecipientEmail) {
		return
	}
	data["OptOutNotice"] = "The address this item was shared with has opted out of notification emails from this portal."
	if s.resubscribe != nil {
		data["ResubscribePath"] = viewPath + "/resubscribe"
	}
}

// optedOut reports whether email has notification delivery turned off. A
// missing callback or a lookup failure reads as not opted out: the notice is
// informational and must never break the landing page.
func (s *Service) optedOut(ctx context.Context, email string) bool {
	if s.optOutStatus == nil {
		return false
	}
	out, err := s.optOutStatus(ctx, email)
	if err != nil {
		slog.Warn("share guest landing: opt-out lookup failed", logKeyError, err)
		return false
	}
	return out
}

// brandName returns the configured brand, defaulting to the platform name the
// public viewer uses.
func (s *Service) brandName() string {
	if s.brand.Name == "" {
		return "MCP Data Platform"
	}
	return s.brand.Name
}
