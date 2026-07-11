package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/txn2/mcp-data-platform/pkg/oauth/signkey"
)

const rotationIssuer = "https://oauth.example.com"

var (
	keyCurrent  = []byte("current-signing-key-at-least-32-bytes")
	keyPrevious = []byte("previous-signing-key-at-least-32-byte")
	keyRetired  = []byte("retired-signing-key-at-least-32-bytesX")
)

// signToken mints an HS256 token for the rotation issuer signed with key. When
// setKID is true the token carries the kid derived from key (the post-change
// behavior); when false it omits the header (a legacy pre-change token).
func signToken(t *testing.T, key []byte, setKID bool) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": rotationIssuer,
		"sub": "user-123",
		"aud": rotationIssuer,
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
		"nbf": now.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	if setKID {
		token.Header["kid"] = signkey.KeyID(key)
	}
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}
	return signed
}

func newRotationAuth(t *testing.T, current []byte, previous ...[]byte) *OAuthJWTAuthenticator {
	t.Helper()
	a, err := NewOAuthJWTAuthenticator(OAuthJWTConfig{
		Issuer:              rotationIssuer,
		SigningKey:          current,
		PreviousSigningKeys: previous,
	})
	if err != nil {
		t.Fatalf("creating authenticator: %v", err)
	}
	return a
}

func mustAuth(t *testing.T, a *OAuthJWTAuthenticator, token string) {
	t.Helper()
	if _, err := a.Authenticate(WithToken(context.Background(), token)); err != nil {
		t.Fatalf("expected token to verify, got error: %v", err)
	}
}

func mustReject(t *testing.T, a *OAuthJWTAuthenticator, token string) {
	t.Helper()
	if _, err := a.Authenticate(WithToken(context.Background(), token)); err == nil {
		t.Fatal("expected token to be rejected, but it verified")
	}
}

func TestOAuthJWT_KidSelection(t *testing.T) {
	a := newRotationAuth(t, keyCurrent, keyPrevious)

	t.Run("current key with its kid verifies", func(t *testing.T) {
		mustAuth(t, a, signToken(t, keyCurrent, true))
	})

	t.Run("previous key with its kid verifies", func(t *testing.T) {
		mustAuth(t, a, signToken(t, keyPrevious, true))
	})

	t.Run("unknown kid is rejected", func(t *testing.T) {
		// Signed with a key not in the ring; its kid is therefore unknown.
		mustReject(t, a, signToken(t, keyRetired, true))
	})

	t.Run("kid pointing at a key that did not sign it is rejected", func(t *testing.T) {
		// Sign with previous key but forge the current key's kid header: kid
		// drives selection, so verification uses the current key and the
		// signature fails.
		now := time.Now()
		claims := jwt.MapClaims{
			"iss": rotationIssuer, "sub": "u", "aud": rotationIssuer,
			"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "nbf": now.Unix(),
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		token.Header["kid"] = signkey.KeyID(keyCurrent)
		signed, err := token.SignedString(keyPrevious)
		if err != nil {
			t.Fatalf("signing: %v", err)
		}
		mustReject(t, a, signed)
	})
}

func TestOAuthJWT_LegacyNoKid(t *testing.T) {
	a := newRotationAuth(t, keyCurrent, keyPrevious)

	t.Run("no-kid token signed with current key verifies", func(t *testing.T) {
		mustAuth(t, a, signToken(t, keyCurrent, false))
	})

	t.Run("no-kid token signed with previous key verifies", func(t *testing.T) {
		mustAuth(t, a, signToken(t, keyPrevious, false))
	})

	t.Run("no-kid token signed with an unknown key is rejected", func(t *testing.T) {
		mustReject(t, a, signToken(t, keyRetired, false))
	})
}

func TestOAuthJWT_Rotation(t *testing.T) {
	// Before the old key is dropped: current=new, previous=[old].
	beforeDrop := newRotationAuth(t, keyCurrent, keyPrevious)
	oldToken := signToken(t, keyPrevious, true)
	mustAuth(t, beforeDrop, oldToken)

	// After the old key is dropped from previous_signing_keys.
	afterDrop := newRotationAuth(t, keyCurrent)
	mustReject(t, afterDrop, oldToken)

	// The same holds for a legacy (no-kid) token signed with the old key.
	legacyOld := signToken(t, keyPrevious, false)
	mustAuth(t, beforeDrop, legacyOld)
	mustReject(t, afterDrop, legacyOld)
}

func TestOAuthJWT_RejectsNonHMACAlg(t *testing.T) {
	a := newRotationAuth(t, keyCurrent, keyPrevious)

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating rsa key: %v", err)
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iss": rotationIssuer, "sub": "u", "aud": rotationIssuer,
		"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "nbf": now.Unix(),
	}
	// RS256 is the classic HS/RS confusion vector; the HMAC algorithm pin must
	// reject an asymmetric-signed token rather than treat the RSA public key as
	// an HMAC secret.
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(rsaKey)
	if err != nil {
		t.Fatalf("signing rs256 token: %v", err)
	}
	mustReject(t, a, signed)
}

func TestOAuthJWT_LegacyExpiredSurfacesExpiryError(t *testing.T) {
	// A legacy (no-kid) token signed with the current key but expired must report
	// expiry, not a spurious signature-invalid error from a later candidate key.
	a := newRotationAuth(t, keyCurrent, keyPrevious)

	now := time.Now()
	claims := jwt.MapClaims{
		"iss": rotationIssuer, "sub": "u", "aud": rotationIssuer,
		"exp": now.Add(-time.Hour).Unix(), // expired
		"iat": now.Add(-2 * time.Hour).Unix(),
		"nbf": now.Add(-2 * time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(keyCurrent)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}

	_, authErr := a.Authenticate(WithToken(context.Background(), signed))
	if authErr == nil {
		t.Fatal("expected expired token to be rejected")
	}
	if !errors.Is(authErr, jwt.ErrTokenExpired) {
		t.Errorf("error = %v, want it to wrap jwt.ErrTokenExpired (not a signature error)", authErr)
	}
}

func TestNewOAuthJWTAuthenticator_PreviousKeysOnly(t *testing.T) {
	// A ring still requires a current signing key even if previous keys exist.
	_, err := NewOAuthJWTAuthenticator(OAuthJWTConfig{
		Issuer:              rotationIssuer,
		PreviousSigningKeys: [][]byte{keyPrevious},
	})
	if err == nil {
		t.Error("expected error when only previous keys are set (no current signing key)")
	}
}
