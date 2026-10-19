// Package scripttoken is the signed token a managed script mints and verifies
// (#2175): platform.token.mint and platform.token.verify. A token is an
// HMAC-SHA256 over a JSON body carrying the script's claims, the endpoints or
// the script it is bound to, its expiry, an id, and whether it may be used
// once. The key is derived from a stored secret the script names, and the
// script never sees the secret's value.
//
// It is the same form the bot check's proof-of-work challenge is signed in
// (internal/botcheck), kept in-house rather than a JWT: the platform both
// mints and verifies, so nothing outside it has to read one.
package scripttoken

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// keyLabel domain-separates a token key from the stored secret it is derived
// from, so a secret that also signs something else does not sign tokens with
// the same key.
const keyLabel = "script-token-v1"

// Binding prefixes: a token minted inside an endpoint request is bound to
// endpoints by name, one minted anywhere else to the script that minted it.
const (
	bindEndpoint = "endpoint:"
	bindScript   = "script:"
)

// Limits on what a token carries and how long it lives.
const (
	// MaxTokenLen bounds a token before anything is decoded from it.
	MaxTokenLen = 8192
	// MaxClaimsBytes bounds the claims a script puts in one token.
	MaxClaimsBytes = 4096
	// MaxTTL is the longest a token may live.
	MaxTTL = 30 * 24 * time.Hour
	// MaxEndpoints is the most endpoints one token may be bound to.
	MaxEndpoints = 16
	idBytes      = 16
)

// Reasons a token is refused, as verify returns them to the script.
const (
	ReasonMalformed     = "malformed"
	ReasonTampered      = "tampered"
	ReasonExpired       = "expired"
	ReasonOtherEndpoint = "other_endpoint"
	ReasonUsed          = "used"
)

// ErrRefused is the error every refusal wraps; Reason reads which it was.
var ErrRefused = errors.New("token refused")

// refusal is a verification failure with the reason a script reads.
type refusal struct{ reason, detail string }

func (r *refusal) Error() string { return "token refused: " + r.detail }
func (*refusal) Unwrap() error   { return ErrRefused }

func refuse(reason, detail string) error { return &refusal{reason: reason, detail: detail} }

// Reason is the reason a Verify error carries, or "" for any other error.
func Reason(err error) string {
	if r, ok := errors.AsType[*refusal](err); ok {
		return r.reason
	}
	return ""
}

// Key derives the signing key from a stored secret's value.
func Key(secret string) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write([]byte(keyLabel)) // a hash.Hash never returns an error
	return m.Sum(nil)
}

// Binding is where a token is minted or verified: inside an endpoint request,
// the endpoint's name; anywhere else, the script.
type Binding struct {
	Endpoint string
	ScriptID string
}

// own is the binding a token minted here carries when the script names no
// endpoints.
func (b Binding) own() string {
	if b.Endpoint != "" {
		return bindEndpoint + b.Endpoint
	}
	return bindScript + b.ScriptID
}

// body is what a token carries, signed.
type body struct {
	Claims    map[string]any `json:"c"`
	Bound     []string       `json:"b"`
	Expires   int64          `json:"x"`
	ID        string         `json:"i"`
	SingleUse bool           `json:"s,omitempty"`
}

// Mint is one token's request.
type Mint struct {
	Claims map[string]any
	TTL    time.Duration
	// Endpoints names the endpoints the token verifies at; empty binds it to
	// where it was minted.
	Endpoints []string
	SingleUse bool
}

// Token is a verified token.
type Token struct {
	Claims    map[string]any
	Expires   time.Time
	ID        string
	SingleUse bool
}

// Sign mints a token under key at binding, valid for m.TTL from now.
func Sign(key []byte, at Binding, m Mint, now time.Time) (string, error) {
	if m.TTL <= 0 || m.TTL > MaxTTL {
		return "", fmt.Errorf("ttl must be more than 0 and at most %s, got %s", MaxTTL, m.TTL)
	}
	bound, err := bindings(at, m.Endpoints)
	if err != nil {
		return "", err
	}
	claims, err := checkedClaims(m.Claims)
	if err != nil {
		return "", err
	}
	id := make([]byte, idBytes)
	if _, err := rand.Read(id); err != nil {
		return "", fmt.Errorf("reading a token id: %w", err)
	}
	payload, err := json.Marshal(body{
		Claims: claims, Bound: bound, Expires: now.Add(m.TTL).Unix(),
		ID: hex.EncodeToString(id), SingleUse: m.SingleUse,
	})
	if err != nil {
		return "", fmt.Errorf("encoding a token: %w", err)
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	return enc + "." + base64.RawURLEncoding.EncodeToString(mac(key, enc)), nil
}

// bindings is what a token minted at at is bound to: the endpoints named, or
// where it was minted.
func bindings(at Binding, endpoints []string) ([]string, error) {
	if len(endpoints) > MaxEndpoints {
		return nil, fmt.Errorf("a token is bound to at most %d endpoints, got %d", MaxEndpoints, len(endpoints))
	}
	if len(endpoints) == 0 {
		return []string{at.own()}, nil
	}
	bound := make([]string, 0, len(endpoints))
	for _, e := range endpoints {
		if strings.TrimSpace(e) == "" {
			return nil, errors.New("an endpoint name is empty")
		}
		bound = append(bound, bindEndpoint+e)
	}
	return bound, nil
}

// checkedClaims is the claims a token carries, an empty set for none, refused
// past MaxClaimsBytes encoded.
func checkedClaims(claims map[string]any) (map[string]any, error) {
	if claims == nil {
		claims = map[string]any{}
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return nil, fmt.Errorf("encoding the claims: %w", err)
	}
	if len(raw) > MaxClaimsBytes {
		return nil, fmt.Errorf("the claims are %d bytes encoded; a token carries at most %d", len(raw), MaxClaimsBytes)
	}
	return claims, nil
}

func mac(key []byte, enc string) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(enc)) // a hash.Hash never returns an error
	return m.Sum(nil)
}

// Verify reads a token key signed, refusing one that is malformed, whose
// signature is not key's, that has expired, or that is not bound to at. A
// single-use token's use is the caller's to record.
func Verify(key []byte, at Binding, token string, now time.Time) (Token, error) {
	b, err := signedBody(key, token)
	if err != nil {
		return Token{}, err
	}
	expires := time.Unix(b.Expires, 0).UTC()
	if !now.Before(expires) {
		return Token{}, refuse(ReasonExpired, "the token expired at "+expires.Format(time.RFC3339))
	}
	here := at.own()
	if !slices.Contains(b.Bound, here) {
		return Token{}, refuse(ReasonOtherEndpoint, "the token was minted for "+strings.Join(b.Bound, ", ")+", not "+here)
	}
	if b.Claims == nil {
		b.Claims = map[string]any{}
	}
	return Token{Claims: b.Claims, Expires: expires, ID: b.ID, SingleUse: b.SingleUse}, nil
}

// signedBody reads the body of a token key signed, refusing one that is not
// in the form a mint returns or whose signature is another key's.
func signedBody(key []byte, token string) (body, error) {
	if token == "" || len(token) > MaxTokenLen {
		return body{}, refuse(ReasonMalformed, "the token is empty or too long")
	}
	enc, sig, ok := strings.Cut(token, ".")
	if !ok {
		return body{}, refuse(ReasonMalformed, "the token is not in the form a mint returns")
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return body{}, refuse(ReasonMalformed, "the token's signature does not decode")
	}
	if !hmac.Equal(got, mac(key, enc)) {
		return body{}, refuse(ReasonTampered, "the token was not signed with this key, or was changed after it was")
	}
	payload, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return body{}, refuse(ReasonMalformed, "the token's body does not decode")
	}
	var b body
	if err := json.Unmarshal(payload, &b); err != nil {
		return body{}, refuse(ReasonMalformed, "the token's body is not a token")
	}
	return b, nil
}
