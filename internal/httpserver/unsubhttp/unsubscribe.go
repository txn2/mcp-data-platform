// Package unsubhttp serves the no-login unsubscribe endpoint linked from every
// notification email footer and named by the RFC 8058 List-Unsubscribe header.
//
// It also mints and verifies the HMAC token that link carries, so the one place
// that decides a token is valid is the one place that acts on it. The opt-out
// it records is an ordinary preference write: delivery mode "off" for the
// address the token names.
package unsubhttp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"html/template"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// unsubMACLabel domain-separates the unsubscribe MAC from any other use of
// the key material.
const unsubMACLabel = "notification-unsubscribe:"

// UnsubToken returns the opaque token an email footer's unsubscribe link
// carries for recipient. The token binds the address with an HMAC so only a
// holder of the emailed link can opt that address out; it deliberately never
// expires, since a stale footer link that stops working strands exactly the
// recipient the link exists for. It grants nothing but the opt-out: prefs are
// keyed by bare email, so it works identically for account holders and
// recipients with no account (#1001).
func UnsubToken(key []byte, email string) string {
	email = canonicalEmail(email)
	return base64.RawURLEncoding.EncodeToString([]byte(email)) + "." +
		base64.RawURLEncoding.EncodeToString(unsubMAC(key, email))
}

// VerifyUnsubToken validates tok and returns the recipient address it was
// minted for. ok is false for a malformed token or a MAC mismatch.
func VerifyUnsubToken(key []byte, tok string) (email string, ok bool) {
	emailPart, macPart, found := strings.Cut(tok, ".")
	if !found {
		return "", false
	}
	emailBytes, err := base64.RawURLEncoding.DecodeString(emailPart)
	if err != nil {
		return "", false
	}
	mac, err := base64.RawURLEncoding.DecodeString(macPart)
	if err != nil {
		return "", false
	}
	email = canonicalEmail(string(emailBytes))
	if !hmac.Equal(mac, unsubMAC(key, email)) {
		return "", false
	}
	return email, true
}

// unsubMAC computes the HMAC binding an address to the unsubscribe action.
func unsubMAC(key []byte, email string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(unsubMACLabel + email))
	return mac.Sum(nil)
}

// canonicalEmail lowercases and trims an address so minting and verification
// agree with the preference store's keying.
func canonicalEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// unsubPage is the minimal branded page the unsubscribe endpoint confirms
// (or refuses) on. It is server-rendered with no scripts: the viewer may
// have no account and no session. When Confirm is set it renders a form
// whose single button POSTs back to the same URL (the token rides the query
// string), turning the opt-out into a deliberate click.
var unsubPage = template.Must(template.New("unsub").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>{{.Title}} - {{.Brand}}</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
       background: #f4f5f7; color: #1a1d21; margin: 0; display: flex; min-height: 100vh;
       align-items: center; justify-content: center; }
.card { background: #fff; border: 1px solid #e2e4e8; border-radius: 8px; padding: 32px;
        max-width: 420px; margin: 16px; text-align: center; }
h1 { font-size: 17px; margin: 0 0 10px; }
p { font-size: 14px; color: #4a4f57; line-height: 1.5; margin: 0; }
.brand { font-size: 13px; font-weight: 700; margin-bottom: 18px; }
form { margin: 18px 0 0; }
button { padding: 10px 18px; border: 0; border-radius: 6px; background: #2563eb; color: #fff;
         font-size: 14px; font-weight: 600; font-family: inherit; cursor: pointer; }
</style>
</head>
<body>
<div class="card">
<div class="brand">{{.Brand}}</div>
<h1>{{.Title}}</h1>
<p>{{.Message}}</p>
{{if .Confirm}}<form method="post"><button type="submit">Unsubscribe</button></form>{{end}}
</div>
</body>
</html>
`))

// unsubPageData is the template context for unsubPage.
type unsubPageData struct {
	Brand, Title, Message string
	// Confirm renders the unsubscribe form on the GET confirmation page.
	Confirm bool
}

// UnsubscribeHandler serves GET and POST on
// /portal/notifications/unsubscribe?tok=..., the no-login opt-out linked from
// every notification email footer and named by the List-Unsubscribe header. A
// valid token writes delivery mode "off" for the address it names; the share and
// comment enqueue paths already honor that mode, so the recipient receives
// no further notification emails. One-time view links are unaffected: those
// are transactional sends the recipient asks for, not notifications.
type UnsubscribeHandler struct {
	Prefs notification.PrefsStore
	// Key signs and verifies the footer tokens; see UnsubToken.
	Key []byte
	// BrandName heads the confirmation page.
	BrandName string
}

// ServeHTTP routes the endpoint's three cases. GET renders a confirmation
// page and performs no mutation: corporate mail security layers (Safe Links,
// Proofpoint, and similar) prefetch URLs in message bodies, and the token is
// a bearer credential, so a mutating GET would let a recipient's own mail
// infrastructure silently opt them out (#1022). The opt-out records only on
// POST: either the RFC 8058 one-click body a mail provider sends on a real
// user action in its own UI, or the confirmation page's form submit.
func (h *UnsubscribeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if isOneClick(r) {
			h.serveOneClick(w, r)
			return
		}
		h.serveConfirm(w, r)
		return
	}
	email, ok := VerifyUnsubToken(h.Key, r.URL.Query().Get("tok"))
	if !ok {
		h.renderInvalid(w)
		return
	}
	h.renderConfirmPrompt(w, email)
}

// isOneClick reports whether a POST is the RFC 8058 one-click call: mail
// providers send body "List-Unsubscribe=One-Click" (RFC 8058 section 3.2),
// which the confirmation form never does.
func isOneClick(r *http.Request) bool {
	return r.PostFormValue("List-Unsubscribe") == "One-Click"
}

// serveConfirm handles the confirmation page's form POST: it records the
// opt-out and renders the confirmation page. The caller is a browser, so
// every outcome is a page.
func (h *UnsubscribeHandler) serveConfirm(w http.ResponseWriter, r *http.Request) {
	email, ok := VerifyUnsubToken(h.Key, r.URL.Query().Get("tok"))
	if !ok {
		h.renderInvalid(w)
		return
	}
	if !h.optOut(r, email) {
		h.renderPage(w, http.StatusInternalServerError, "Something went wrong",
			"The opt-out could not be recorded. Try the link again in a moment.")
		return
	}
	h.renderPage(w, http.StatusOK, "You are unsubscribed",
		"This address will no longer receive notification emails. One-time view links you request from a share page still work.")
}

// serveOneClick handles the RFC 8058 POST. The caller is a mail provider,
// not a browser, so responses are bare status codes: the token in the posted
// URL is the sole credential.
func (h *UnsubscribeHandler) serveOneClick(w http.ResponseWriter, r *http.Request) {
	email, ok := VerifyUnsubToken(h.Key, r.URL.Query().Get("tok"))
	if !ok {
		http.Error(w, "invalid unsubscribe token", http.StatusBadRequest)
		return
	}
	if !h.optOut(r, email) {
		http.Error(w, "opt-out could not be recorded", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// renderConfirmPrompt renders the no-mutation GET page asking for the
// deliberate click. It names the address so the holder of a forwarded link
// sees whose delivery they are about to stop; the token already grants the
// opt-out, so the page reveals nothing its holder could not do.
func (h *UnsubscribeHandler) renderConfirmPrompt(w http.ResponseWriter, email string) {
	h.render(w, http.StatusOK, unsubPageData{
		Title: "Unsubscribe from notification emails",
		Message: "Confirm to stop notification emails to " + email +
			". One-time view links requested from a share page will still work.",
		Confirm: true,
	})
}

// renderInvalid renders the bad-token page.
func (h *UnsubscribeHandler) renderInvalid(w http.ResponseWriter) {
	h.renderPage(w, http.StatusBadRequest, "This unsubscribe link is not valid",
		"The link may be incomplete. Use the unsubscribe link from a notification email, or ask the sender to stop sharing with this address.")
}

// optOut writes delivery mode "off" for email, reporting success.
func (h *UnsubscribeHandler) optOut(r *http.Request, email string) bool {
	mode := notification.ModeOff
	_, err := h.Prefs.Set(r.Context(), email, notification.PrefsUpdate{Mode: &mode})
	return err == nil
}

// renderPage writes one formless confirmation/refusal page.
func (h *UnsubscribeHandler) renderPage(w http.ResponseWriter, status int, title, message string) {
	h.render(w, status, unsubPageData{Title: title, Message: message})
}

// render writes one page. form-action 'self' admits exactly the confirmation
// form's same-URL POST under the otherwise deny-all policy.
func (h *UnsubscribeHandler) render(w http.ResponseWriter, status int, data unsubPageData) {
	data.Brand = h.BrandName
	if data.Brand == "" {
		data.Brand = "Data Platform"
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = unsubPage.Execute(w, data)
}
