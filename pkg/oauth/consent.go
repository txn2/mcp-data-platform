package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// A client that registered itself through /oauth/register is not one an
// operator vouched for: the person signing in is asked, on a page the
// platform serves, whether that client may act as them, before a code is sent
// to its redirect URI (the MCP authorization spec's consent requirement for a
// proxy that brokers a third-party identity provider). A client configured
// under oauth.clients is not asked.
const (
	// ConsentPath is where the consent page posts its answer.
	ConsentPath = "/oauth/consent"

	// consentMaxAge is how long a consent page can be answered.
	consentMaxAge = 10 * time.Minute

	// consentKeyPrefix keeps a pending consent apart from the IdP states
	// sharing the state store.
	consentKeyPrefix = "consent:"

	// consentCookie binds a consent page to the browser it was served to;
	// the secure name carries the __Host- prefix (no Domain, Path /, Secure).
	consentCookie       = "mcp_oauth_consent"
	consentCookieSecure = "__Host-mcp_oauth_consent"

	// maxConsentFormBytes bounds the consent form, which carries two fields.
	maxConsentFormBytes = 4096

	consentAllow = "allow"
	consentDeny  = "deny"

	defaultConsentTitle = "MCP Data Platform"
)

// ConsentBranding names the deployment on the consent page: the portal's
// title and logos, so the page reads as the deployment's own.
type ConsentBranding struct {
	// Title is the deployment's name; empty is "MCP Data Platform".
	Title string
	// LogoLight and LogoDark are logo URLs for each theme; either may be
	// empty, in which case the other (or the built-in mark) is shown.
	LogoLight string
	LogoDark  string
}

// needsConsent reports whether a code for this client waits on the person's
// answer: a dynamically registered client's does. A client the store cannot
// read is asked about too, so a failed read never skips the page; askConsent
// then refuses it.
func (s *Server) needsConsent(ctx context.Context, clientID string) bool {
	client, err := s.storage.GetClient(ctx, clientID)
	return err != nil || client.DynamicallyRegistered
}

// askConsent holds the signed-in request under a fresh key and serves the
// consent page. The pending state carries who signed in and what the client
// asked for, never an upstream token; it is answered once, by the browser that
// holds the cookie set here, within consentMaxAge.
func (s *Server) askConsent(w http.ResponseWriter, r *http.Request, authState *AuthorizationState, userID string, claims map[string]any) {
	client, err := s.storage.GetClient(r.Context(), authState.ClientID)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, errInvalidRequest, "invalid client_id")
		return
	}
	id, err := generateSecureToken(tokenByteLength)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, errServerError, "failed to generate consent request")
		return
	}
	binding, err := generateSecureToken(tokenByteLength)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, errServerError, "failed to generate consent request")
		return
	}
	pending := *authState
	pending.UserID, pending.UserClaims = userID, claims
	pending.ConsentBinding = bindingDigest(binding)
	pending.CreatedAt = time.Now()
	if err := s.stateStore.SaveState(r.Context(), consentKeyPrefix+id, &pending); err != nil {
		s.writeError(w, http.StatusInternalServerError, errServerError, "failed to save state")
		return
	}
	secure := s.secureCookies(r)
	// HttpOnly and SameSite=Strict always; Secure whenever the issuer is
	// https or the request came over TLS, which an http issuer (a loopback
	// development server) cannot have.
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure follows the issuer's scheme (see above)
		Name: consentCookieName(secure), Value: binding, Path: "/",
		MaxAge: int(consentMaxAge / time.Second), HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
	})
	page := consentPage{
		Brand:      s.brand(),
		Email:      signedInAs(userID, claims),
		ClientName: strings.TrimSpace(client.Name),
		Action:     ConsentPath,
		ConsentID:  id,
	}
	page.Destination, page.RedirectURI = describeRedirect(authState.RedirectURI)
	s.renderConsent(w, http.StatusOK, authState.RedirectURI, page)
}

// handleConsentEndpoint answers POST /oauth/consent: the person allowed or
// denied the client on the consent page. The pending request is deleted before
// anything else, so each page is answered at most once, and it is honored only
// from the browser it was served to and only within consentMaxAge. Allow sends
// the code to the redirect URI; deny sends error=access_denied there (RFC 6749
// section 4.1.2.1).
func (s *Server) handleConsentEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, errMethodNotAllowed, "POST required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxConsentFormBytes)
	if err := r.ParseForm(); err != nil {
		s.renderEnded(w)
		return
	}
	secure := s.secureCookies(r)
	// Clears the binding cookie, with the attributes it was set with (see
	// askConsent).
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure follows the issuer's scheme (see askConsent)
		Name: consentCookieName(secure), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
	})
	pending, ok := s.takeConsent(r)
	if !ok {
		s.renderEnded(w)
		return
	}
	switch r.PostFormValue("decision") {
	case consentAllow:
		s.issueCode(w, r, pending, pending.UserID, pending.UserClaims)
	case consentDeny:
		s.redirectDenied(w, r, pending)
	default:
		s.renderEnded(w)
	}
}

// takeConsent removes and returns the pending request the form names, when the
// browser holds its binding and it is still answerable.
func (s *Server) takeConsent(r *http.Request) (*AuthorizationState, bool) {
	id := r.PostFormValue("consent_id")
	if id == "" {
		return nil, false
	}
	key := consentKeyPrefix + id
	pending, err := s.stateStore.GetState(r.Context(), key)
	if err != nil {
		return nil, false
	}
	if err := s.stateStore.DeleteState(r.Context(), key); err != nil {
		// Not answering a request that might be answered again.
		slog.Warn("oauth: consent request delete failed", logKeyError, logsan.SanitizeForLog(err.Error()))
		return nil, false
	}
	cookie, err := r.Cookie(consentCookieName(s.secureCookies(r)))
	if err != nil || pending.ConsentBinding == "" ||
		subtle.ConstantTimeCompare([]byte(bindingDigest(cookie.Value)), []byte(pending.ConsentBinding)) != 1 {
		return nil, false
	}
	if time.Since(pending.CreatedAt) > consentMaxAge {
		return nil, false
	}
	return pending, true
}

// redirectDenied sends the client its access_denied answer.
func (*Server) redirectDenied(w http.ResponseWriter, r *http.Request, pending *AuthorizationState) {
	u, err := url.Parse(pending.RedirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	q := u.Query()
	q.Set("error", "access_denied")
	q.Set("error_description", "the user denied the request")
	if pending.State != "" {
		q.Set(paramState, pending.State)
	}
	u.RawQuery = q.Encode()
	// #nosec G710 -- pending.RedirectURI was validated against the client's
	// registered redirect URIs and the DCR policy at /authorize.
	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect -- redirect URI validated at /authorize
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// secureCookies reports whether the consent cookie is sent over TLS only: when
// the issuer is https or the request arrived over TLS.
func (s *Server) secureCookies(r *http.Request) bool {
	return strings.HasPrefix(s.config.Issuer, "https://") || r.TLS != nil
}

// consentCookieName is the binding cookie's name for the transport.
func consentCookieName(secure bool) string {
	if secure {
		return consentCookieSecure
	}
	return consentCookie
}

// bindingDigest is what the state store keeps of a browser binding: its
// SHA-256, so a reader of the store cannot answer a consent page.
func bindingDigest(binding string) string {
	sum := sha256.Sum256([]byte(binding))
	return hex.EncodeToString(sum[:])
}

// brand is the configured branding with its defaults applied.
func (s *Server) brand() ConsentBranding {
	b := s.config.Branding
	if strings.TrimSpace(b.Title) == "" {
		b.Title = defaultConsentTitle
	}
	if b.LogoLight == "" {
		b.LogoLight = b.LogoDark
	}
	if b.LogoDark == "" {
		b.LogoDark = b.LogoLight
	}
	return b
}

// signedInAs is how the page names the person: their email, else their name,
// else their subject.
func signedInAs(userID string, claims map[string]any) string {
	for _, key := range []string{"email", "preferred_username", "name"} {
		if v, ok := claims[key].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return userID
}

// describeRedirect is what the page says about where the person is sent: the
// host for a web address, "this computer" for a loopback one, "this device"
// for a private-use scheme, and the whole URI beneath it.
func describeRedirect(redirectURI string) (destination, full string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI, redirectURI
	}
	switch {
	case isLoopbackURI(redirectURI):
		return "An application on this computer", redirectURI
	case u.Scheme == schemeHTTPS || u.Scheme == schemeHTTP:
		return u.Host, redirectURI
	default:
		return "An application on this device (" + u.Scheme + ")", redirectURI
	}
}

// consentPage is the data the consent template renders.
type consentPage struct {
	Brand       ConsentBranding
	Email       string
	ClientName  string
	Destination string
	RedirectURI string
	Action      string
	ConsentID   string
	Ended       bool
}

// renderConsent writes the consent page with headers that keep it out of a
// frame and away from any other origin: it loads nothing but its own inline
// style and the deployment's logos, and its form posts only here and follows
// the redirect only to the client's own origin.
func (*Server) renderConsent(w http.ResponseWriter, status int, redirectURI string, page consentPage) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", consentCSP(redirectURI, page.Brand))
	w.WriteHeader(status)
	if err := consentTemplate.Execute(w, page); err != nil {
		slog.Warn("oauth: rendering the consent page failed", logKeyError, logsan.SanitizeForLog(err.Error()))
	}
}

// renderEnded answers a consent form that cannot be honored: already
// answered, expired, from another browser, or naming nothing.
func (s *Server) renderEnded(w http.ResponseWriter) {
	s.renderConsent(w, http.StatusBadRequest, "", consentPage{Brand: s.brand(), Ended: true})
}

// consentCSP is the page's Content-Security-Policy. form-action names the
// client's origin as well as this one, because a browser holds the redirect
// that follows a form post to it.
func consentCSP(redirectURI string, brand ConsentBranding) string {
	formAction := "'self'"
	if o := redirectOrigin(redirectURI); o != "" {
		formAction += " " + o
	}
	img := "'self'"
	for _, logo := range []string{brand.LogoLight, brand.LogoDark} {
		if o := redirectOrigin(logo); o != "" && !strings.Contains(img, o) {
			img += " " + o
		}
	}
	return "default-src 'none'; style-src 'unsafe-inline'; img-src " + img +
		"; form-action " + formAction + "; frame-ancestors 'none'; base-uri 'none'"
}

// redirectOrigin is the CSP source for a URI's origin ("https://host:port"),
// or its scheme ("com.example.app:") when it has no host, or empty when it
// cannot be read or is relative.
func redirectOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || strings.ContainsAny(raw, " ;,'\"") {
		return ""
	}
	if u.Host == "" {
		return u.Scheme + ":"
	}
	return u.Scheme + "://" + u.Host
}

//nolint:gochecknoglobals // parsed once; the template is constant.
var consentTemplate = template.Must(template.New("consent").Parse(consentHTML))

// consentHTML is the consent page and the ended page, per the approved
// mockups: the deployment's mark and name, who is signed in, where the person
// is sent and what the client calls itself, then Deny and Allow.
const consentHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>{{if .Ended}}This request has ended{{else}}Connect an application{{end}} · {{.Brand.Title}}</title>
<style>
:root{--bg:#f1f5f9;--card:#fff;--fg:#020817;--muted:#5f6b7d;--border:#d9e1ec;--accent:#2563eb;--on-accent:#fff}
@media (prefers-color-scheme:dark){:root{--bg:#020817;--card:#131a26;--fg:#f8fafc;--muted:#a3b0c2;--border:#1e293b;--accent:#60a5fa;--on-accent:#0f172a}}
body{margin:0;background:var(--bg);color:var(--fg);font-family:system-ui,-apple-system,"Segoe UI",sans-serif}
.wrap{min-height:100vh;box-sizing:border-box;display:flex;align-items:center;justify-content:center;padding:32px 16px}
main{width:100%;max-width:480px;box-sizing:border-box;background:var(--card);border:1px solid var(--border);border-radius:12px;padding:32px;display:flex;flex-direction:column;gap:24px}
.brand{display:flex;align-items:center;gap:10px;font-size:15px;font-weight:600}
.brand img{height:28px;width:auto}
.brand svg{stroke:var(--accent)}
.logo-dark{display:none}
@media (prefers-color-scheme:dark){.logo-light{display:none}.logo-dark{display:inline}}
h1{margin:0;font-size:22px;line-height:1.3;font-weight:650}
p{margin:0;font-size:14px;line-height:1.5}
.muted{color:var(--muted)}
dl{margin:0;display:flex;flex-direction:column;gap:16px;border:1px solid var(--border);border-radius:8px;padding:16px}
dl div{display:flex;flex-direction:column;gap:4px}
dt{font-size:12px;font-weight:600;color:var(--muted);text-transform:uppercase;letter-spacing:.04em}
dd{margin:0}
.dest{font-size:18px;font-weight:600;word-break:break-word}
.uri{font-size:12px;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;color:var(--muted);word-break:break-all}
.name{font-size:15px}
.note{font-size:12px;color:var(--muted)}
.none{font-style:italic;color:var(--muted)}
form{margin:0;display:flex;gap:12px;justify-content:flex-end}
button{min-height:44px;padding:0 20px;border-radius:8px;font-size:15px;font-weight:600;font-family:inherit;cursor:pointer}
.deny{border:1px solid var(--border);background:var(--card);color:var(--fg)}
.allow{border:1px solid var(--accent);background:var(--accent);color:var(--on-accent)}
@media (max-width:480px){.wrap{padding:0;align-items:stretch}main{border:0;border-radius:0;padding:32px 16px}form{flex-direction:column-reverse}button{min-height:48px;font-size:16px}}
</style>
</head>
<body>
<div class="wrap">
<main>
<div class="brand">{{if .Brand.LogoLight}}<img class="logo-light" src="{{.Brand.LogoLight}}" alt=""><img class="logo-dark" src="{{.Brand.LogoDark}}" alt="">{{else}}<svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><ellipse cx="12" cy="5" rx="8" ry="3"></ellipse><path d="M4 5v6c0 1.7 3.6 3 8 3s8-1.3 8-3V5"></path><path d="M4 11v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6"></path></svg>{{end}}<span>{{.Brand.Title}}</span></div>
{{if .Ended}}
<div style="display:flex;flex-direction:column;gap:8px">
<h1>This request has ended</h1>
<p class="muted">It was already answered, it is more than 10 minutes old, or it was opened in another browser. Nothing was connected.</p>
</div>
<p>Go back to the application and start connecting again.</p>
{{else}}
<div style="display:flex;flex-direction:column;gap:8px">
<h1>Connect an application to your account?</h1>
<p class="muted">You are signed in as <strong style="color:var(--fg)">{{.Email}}</strong>.</p>
</div>
<dl>
<div><dt>Sends you back to</dt><dd class="dest">{{.Destination}}</dd><dd class="uri">{{.RedirectURI}}</dd></div>
<div><dt>Calls itself</dt>{{if .ClientName}}<dd class="name">{{.ClientName}}</dd><dd class="note">The application chose this name. The platform has not checked it.</dd>{{else}}<dd class="name none">No name given</dd>{{end}}</div>
</dl>
<p>If you allow it, this application can use the tools your role allows, as you, until its access ends or an administrator removes it.</p>
<p class="muted">Allow only an application you just asked to connect. If you did not, choose Deny.</p>
<form method="post" action="{{.Action}}">
<input type="hidden" name="consent_id" value="{{.ConsentID}}">
<button class="deny" type="submit" name="decision" value="deny">Deny</button>
<button class="allow" type="submit" name="decision" value="allow">Allow</button>
</form>
{{end}}
</main>
</div>
</body>
</html>
`
