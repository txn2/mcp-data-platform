package notification

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"html/template"
	"net/http"
	"strings"
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
// have no account and no session.
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
</style>
</head>
<body>
<div class="card">
<div class="brand">{{.Brand}}</div>
<h1>{{.Title}}</h1>
<p>{{.Message}}</p>
</div>
</body>
</html>
`))

// UnsubscribeHandler serves GET /portal/notifications/unsubscribe?tok=...,
// the no-login opt-out linked from every notification email footer. A valid
// token writes delivery mode "off" for the address it names; the share and
// comment enqueue paths already honor that mode, so the recipient receives
// no further notification emails. One-time view links are unaffected: those
// are transactional sends the recipient asks for, not notifications.
type UnsubscribeHandler struct {
	Prefs PrefsStore
	// Key signs and verifies the footer tokens; see UnsubToken.
	Key []byte
	// BrandName heads the confirmation page.
	BrandName string
}

// ServeHTTP verifies the token and records the opt-out.
func (h *UnsubscribeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	email, ok := VerifyUnsubToken(h.Key, r.URL.Query().Get("tok"))
	if !ok {
		h.renderPage(w, http.StatusBadRequest, "This unsubscribe link is not valid",
			"The link may be incomplete. Use the unsubscribe link from a notification email, or ask the sender to stop sharing with this address.")
		return
	}
	mode := ModeOff
	if _, err := h.Prefs.Set(r.Context(), email, PrefsUpdate{Mode: &mode}); err != nil {
		h.renderPage(w, http.StatusInternalServerError, "Something went wrong",
			"The opt-out could not be recorded. Try the link again in a moment.")
		return
	}
	h.renderPage(w, http.StatusOK, "You are unsubscribed",
		"This address will no longer receive notification emails. One-time view links you request from a share page still work.")
}

// renderPage writes one confirmation/refusal page.
func (h *UnsubscribeHandler) renderPage(w http.ResponseWriter, status int, title, message string) {
	brand := h.BrandName
	if brand == "" {
		brand = "Data Platform"
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = unsubPage.Execute(w, map[string]string{"Brand": brand, "Title": title, "Message": message})
}
