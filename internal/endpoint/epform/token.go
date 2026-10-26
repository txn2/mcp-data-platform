package epform

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/portal/shareguest"
)

// KeyLabel domain-separates the form token key from the key it is derived
// from.
const KeyLabel = "endpoint-form-v1"

// TokenTTL is how long a form may be submitted after it was shown.
const TokenTTL = 24 * time.Hour

// maxTokenLen bounds a token before anything is decoded from it, and so the
// spec it can carry.
const maxTokenLen = 256 << 10

// Key is the form token key, derived from master (the bot check's challenge
// key, which every replica derives from the browser-session signing key), so
// a token one replica signs opens on another. Nil when master is nil: such a
// deployment answers no form.
func Key(master []byte) []byte {
	if len(master) == 0 {
		return nil
	}
	return shareguest.DeriveKey(master, KeyLabel)
}

// tokenBody is what a form token carries, signed.
type tokenBody struct {
	Endpoint string `json:"e"`
	Spec     Spec   `json:"s"`
	Issued   int64  `json:"i"`
}

// Sign is the form token for spec, shown on endpoint at now.
func Sign(key []byte, endpoint string, spec Spec, now time.Time) (string, error) {
	payload, err := json.Marshal(tokenBody{Endpoint: endpoint, Spec: spec, Issued: now.Unix()})
	if err != nil {
		return "", fmt.Errorf("encoding the form token: %w", err)
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	if len(body) > maxTokenLen {
		return "", fmt.Errorf("the form spec is too large to carry: %d bytes encoded, at most %d", len(body), maxTokenLen)
	}
	return body + "." + base64.RawURLEncoding.EncodeToString(mac(key, body)), nil
}

func mac(key []byte, body string) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(body)) // a hash.Hash never returns an error
	return m.Sum(nil)
}

// Open is the spec a token carries and when it was issued, or ErrRefused
// with why: no token, one signed with another key or altered, one issued for
// another endpoint, or one past TokenTTL.
func Open(key []byte, endpoint, token string, now time.Time) (Spec, time.Time, error) {
	if token == "" {
		return Spec{}, time.Time{}, refused("it carries no " + TokenField)
	}
	body, sig, ok := strings.Cut(token, ".")
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if !ok || len(token) > maxTokenLen || err != nil || !hmac.Equal(got, mac(key, body)) {
		return Spec{}, time.Time{}, refused("its " + TokenField + " is not one this form issued, or was changed")
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return Spec{}, time.Time{}, refused("its " + TokenField + " cannot be read")
	}
	var t tokenBody
	if err := json.Unmarshal(payload, &t); err != nil {
		return Spec{}, time.Time{}, refused("its " + TokenField + " cannot be read")
	}
	issued := time.Unix(t.Issued, 0)
	switch {
	case t.Endpoint != endpoint:
		return Spec{}, time.Time{}, refused("its " + TokenField + " was issued by another form")
	case now.Sub(issued) > TokenTTL:
		return Spec{}, time.Time{}, refused("the form was shown more than " + TokenTTL.String() + " ago; load it again")
	}
	return t.Spec, issued, nil
}
