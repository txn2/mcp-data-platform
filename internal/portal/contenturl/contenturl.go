// Package contenturl mints and checks the expiring, signed URL that reads one
// version of a portal asset without a portal session (#1848).
//
// An application that embeds the platform hands a browser a link to a report a
// script wrote. A share link is the wrong tool for that: it outlives the need,
// and it names the asset rather than the version the application showed. A
// signed URL names one version, carries its own expiry, and is checked with an
// HMAC under a key derived from the browser-session signing key, so every
// replica verifies what any replica minted and nothing is stored.
package contenturl

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Path is where a signed URL is served; the token follows it.
const Path = "/api/v1/portal/content/"

// KeyLabel derives this package's key from the browser-session signing key,
// so a token minted here is never valid as any other signed value.
const KeyLabel = "asset-content-url"

// TTL bounds.
const (
	DefaultTTL = 5 * time.Minute
	MaxTTL     = 24 * time.Hour
)

// ErrExpired refuses a token past its expiry; ErrInvalid one that is malformed
// or was not signed with this key.
var (
	ErrExpired = errors.New("this link has expired")
	ErrInvalid = errors.New("this link is not valid")
)

// A token's payload is asset|version|expiry, the numbers in decimal.
const (
	tokenFields = 3
	decimal     = 10
	int64Bits   = 64
)

// Target is the version a token reads.
type Target struct {
	AssetID string
	Version int
	Expires time.Time
}

// Sign returns the token for one version, valid until expires.
func Sign(key []byte, t Target) string {
	payload := t.AssetID + "|" + strconv.Itoa(t.Version) + "|" + strconv.FormatInt(t.Expires.Unix(), decimal)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(mac(key, payload))
}

// Verify checks a token and returns the version it names. A token signed with
// another key, or altered, is ErrInvalid; a genuine one past its expiry is
// ErrExpired.
func Verify(key []byte, token string, now time.Time) (Target, error) {
	payloadPart, macPart, ok := strings.Cut(token, ".")
	if !ok {
		return Target{}, ErrInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return Target{}, ErrInvalid
	}
	sum, err := base64.RawURLEncoding.DecodeString(macPart)
	if err != nil || !hmac.Equal(sum, mac(key, string(payload))) {
		return Target{}, ErrInvalid
	}
	parts := strings.Split(string(payload), "|")
	if len(parts) != tokenFields {
		return Target{}, ErrInvalid
	}
	version, vErr := strconv.Atoi(parts[1])
	unix, uErr := strconv.ParseInt(parts[2], decimal, int64Bits)
	if vErr != nil || uErr != nil || parts[0] == "" {
		return Target{}, ErrInvalid
	}
	t := Target{AssetID: parts[0], Version: version, Expires: time.Unix(unix, 0)}
	if !now.Before(t.Expires) {
		return t, ErrExpired
	}
	return t, nil
}

// ClampTTL holds a requested lifetime to (0, MaxTTL], taking the default for
// none.
func ClampTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return DefaultTTL
	}
	return min(ttl, MaxTTL)
}

func mac(key []byte, payload string) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(payload)) // a hash.Hash never returns an error
	return m.Sum(nil)
}
