package epform

import (
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/portal/shareguest"
)

// PagePrefix is the hosted form pages' prefix (#2331): a form's page is
// PagePrefix + its endpoint's name.
const PagePrefix = "/forms/"

// EmbedLabel domain-separates the embed marker key from the viewer token key
// it is derived from.
const EmbedLabel = "endpoint-form-embed-v1"

// EmbedParam is the query parameter a form's page is handed its embed marker
// in; the page does not pass it to the form.
const EmbedParam = "form_embed"

// EmbedTTL is how long an embed marker is honored after the asset it was
// written into was served.
const EmbedTTL = 12 * time.Hour

// An embed marker says the platform wrote this form's address into an asset
// it served (#2331). A form's page refuses to be framed by a site its
// binding's allowed_origins do not list, but an asset frames it from inside
// a sandbox whose opaque origin no frame-ancestors source expression can
// name. The marker is how the page tells that frame from a site's: with a
// valid one it sends no frame-ancestors.

// embedKind marks an embed marker's body, so no other token signed with the
// same key reads as one.
const embedKind = "embed"

// embedBody is what an embed marker carries, signed.
type embedBody struct {
	Kind     string `json:"k"`
	Endpoint string `json:"e"`
	Expires  int64  `json:"x"`
}

// EmbedKey is the embed marker key, derived from the viewer token key; nil
// when that is nil.
func EmbedKey(viewerKey []byte) []byte {
	if len(viewerKey) == 0 {
		return nil
	}
	return shareguest.DeriveKey(viewerKey, EmbedLabel)
}

// SignEmbed is the embed marker for endpoint, honored until now plus
// EmbedTTL; "" without a key.
func SignEmbed(key []byte, endpoint string, now time.Time) string {
	if len(key) == 0 {
		return ""
	}
	payload, err := json.Marshal(embedBody{Kind: embedKind, Endpoint: endpoint, Expires: now.Add(EmbedTTL).Unix()})
	if err != nil {
		return ""
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(mac(key, body))
}

// OpenEmbed reports whether token is an embed marker signed with key for
// endpoint and not expired.
func OpenEmbed(key []byte, endpoint, token string, now time.Time) bool {
	if len(key) == 0 || token == "" || len(token) > maxTokenLen {
		return false
	}
	eb, ok := readEmbed(key, token)
	return ok && eb.Kind == embedKind && eb.Endpoint == endpoint && now.Unix() <= eb.Expires
}

// readEmbed is what a marker signed with key carries, and whether it was
// signed with key and reads.
func readEmbed(key []byte, token string) (embedBody, bool) {
	body, sig, ok := strings.Cut(token, ".")
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if !ok || err != nil || !hmac.Equal(got, mac(key, body)) {
		return embedBody{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return embedBody{}, false
	}
	var eb embedBody
	if json.Unmarshal(payload, &eb) != nil {
		return embedBody{}, false
	}
	return eb, true
}
