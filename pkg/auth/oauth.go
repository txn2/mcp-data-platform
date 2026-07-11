package auth

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/golang-jwt/jwt/v5"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/oauth/signkey"
)

// errLegacyNoKID is a sentinel returned by the fast-path keyfunc when a token
// carries no kid header, signaling parseWithRing to fall back to trying every
// candidate key. It never escapes the package.
var errLegacyNoKID = errors.New("token has no kid header")

// OAuthJWTConfig configures the OAuth JWT authenticator.
type OAuthJWTConfig struct {
	// Issuer is the expected issuer claim in the JWT.
	Issuer string

	// SigningKey is the current HMAC key used to sign and verify JWT signatures.
	SigningKey []byte

	// PreviousSigningKeys are verify-only HMAC keys retained across a signing-key
	// rotation. Tokens minted with a prior key still verify while that key
	// remains here; drop keys after the access-token TTL has elapsed since the
	// rotation to complete it.
	PreviousSigningKeys [][]byte

	// Audience is the accepted aud claim value. Tokens minted for a
	// different audience are rejected. Defaults to Issuer, which is the
	// audience the platform's own OAuth server mints (the platform is
	// both the authorization server and the resource server).
	Audience string

	// RoleClaimPath is the path to roles within the nested "claims" object.
	// e.g., "realm_access.roles" extracts claims["claims"]["realm_access"]["roles"]
	RoleClaimPath string

	// RolePrefix filters roles to those with this prefix.
	RolePrefix string
}

// OAuthJWTAuthenticator validates JWT access tokens issued by our OAuth server.
type OAuthJWTAuthenticator struct {
	cfg       OAuthJWTConfig
	ring      *signkey.Ring
	extractor *ClaimsExtractor
}

// NewOAuthJWTAuthenticator creates a new OAuth JWT authenticator.
func NewOAuthJWTAuthenticator(cfg OAuthJWTConfig) (*OAuthJWTAuthenticator, error) {
	if cfg.Issuer == "" {
		return nil, fmt.Errorf("oauth issuer is required")
	}
	ring := signkey.NewRing(cfg.SigningKey, cfg.PreviousSigningKeys)
	if ring == nil {
		return nil, fmt.Errorf("oauth signing key is required")
	}
	if cfg.Audience == "" {
		cfg.Audience = cfg.Issuer
	}

	extractor := &ClaimsExtractor{
		RoleClaimPath:    cfg.RoleClaimPath,
		RolePrefix:       cfg.RolePrefix,
		EmailClaimPath:   claimEmail,
		NameClaimPath:    claimName,
		SubjectClaimPath: claimSubject,
	}

	return &OAuthJWTAuthenticator{
		cfg:       cfg,
		ring:      ring,
		extractor: extractor,
	}, nil
}

// Authenticate validates the JWT token and returns user info.
func (a *OAuthJWTAuthenticator) Authenticate(ctx context.Context) (*middleware.UserInfo, error) {
	token := GetToken(ctx)
	if token == "" {
		return nil, fmt.Errorf("no token found in context")
	}

	// Parse and validate the JWT
	claims, err := a.parseAndValidateToken(token)
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	// Extract user ID from sub claim
	userID, _ := claims["sub"].(string)
	if userID == "" {
		return nil, fmt.Errorf("missing sub claim")
	}

	// Extract nested user claims (from upstream IdP)
	var userClaims map[string]any
	if nested, ok := claims["claims"].(map[string]any); ok {
		userClaims = nested
	} else {
		userClaims = make(map[string]any)
	}

	// Extract roles from nested claims
	var roles []string
	if a.cfg.RoleClaimPath != "" && len(userClaims) > 0 {
		uc, err := a.extractor.Extract(userClaims)
		if err == nil {
			roles = uc.Roles
		}
	}

	// Also try to get email and display name from nested claims
	email, _ := userClaims["email"].(string)
	name, _ := userClaims["name"].(string)

	return &middleware.UserInfo{
		UserID:   userID,
		Email:    email,
		Name:     name,
		Claims:   userClaims,
		Roles:    roles,
		AuthType: middleware.AuthTypeOAuth,
	}, nil
}

// parseAndValidateToken parses and validates the JWT.
//
// Returns ErrNotAJWT (unwrapped) when the credential clearly isn't a
// JWT, so ChainedAuthenticator can silently fall through to the next
// authenticator without logging a misleading "rejected" line for what
// is actually a normal API-key-handed-to-JWT-authenticator path.
func (a *OAuthJWTAuthenticator) parseAndValidateToken(tokenString string) (map[string]any, error) {
	if !LooksLikeJWT(tokenString) {
		return nil, ErrNotAJWT
	}
	// Parse and verify the JWT signature and audience, selecting the
	// verification key from the ring by the token's kid header.
	token, err := a.parseWithRing(tokenString)
	if err != nil {
		return nil, err
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	// Extract claims as map
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims type")
	}

	// Verify issuer
	iss, ok := claims["iss"].(string)
	if !ok || iss != a.cfg.Issuer {
		return nil, fmt.Errorf("invalid issuer: got %q, want %q", iss, a.cfg.Issuer)
	}

	// Convert to map[string]any for compatibility
	claimsMap := make(map[string]any)
	maps.Copy(claimsMap, claims)

	return claimsMap, nil
}

// parseWithRing verifies the token signature against the key ring:
//
//   - kid present and known: verify with exactly that key.
//   - kid present but unknown: reject (the key was retired, or the token is
//     foreign).
//   - kid absent (legacy token minted before kid support): try the current key
//     then each previous key so live pre-upgrade sessions still verify.
//
// The common kid-bearing case verifies in a single parse: the keyfunc reads the
// already-decoded header to select the key. Only a legacy no-kid token pays a
// second parse via the candidate-key fallback.
func (a *OAuthJWTAuthenticator) parseWithRing(tokenString string) (*jwt.Token, error) {
	sawNoKID := false
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			sawNoKID = true
			return nil, errLegacyNoKID
		}
		key, ok := a.ring.VerificationKey(kid)
		if !ok {
			return nil, fmt.Errorf("unknown key id %q", kid)
		}
		return key, nil
	}, jwt.WithAudience(a.cfg.Audience))
	if err == nil {
		return token, nil
	}
	if !sawNoKID {
		return nil, fmt.Errorf("verifying token: %w", err)
	}
	return a.verifyLegacyNoKID(tokenString)
}

// verifyLegacyNoKID verifies a token that carries no kid header by trying the
// current key then each previous key. It returns as soon as a key's signature
// matches: a non-signature error (expiry, audience) means that key IS the signer
// and the token failed validation for another reason, so that error is surfaced
// verbatim rather than masked by a later key's signature mismatch. The ring
// always holds at least the current key, so the loop runs at least once.
//
// The algorithm is already pinned to HMAC by parseWithRing's fast-path keyfunc
// (it checks the method before the kid, and only routes here on a no-kid HMAC
// token), so the per-key keyfunc here simply returns the candidate key.
func (a *OAuthJWTAuthenticator) verifyLegacyNoKID(tokenString string) (*jwt.Token, error) {
	var lastErr error
	for _, key := range a.ring.CandidateKeys() {
		token, err := jwt.Parse(tokenString, func(*jwt.Token) (any, error) {
			return key, nil
		}, jwt.WithAudience(a.cfg.Audience))
		if err == nil {
			return token, nil
		}
		if !errors.Is(err, jwt.ErrTokenSignatureInvalid) {
			// The signature matched this key; the token failed for another
			// reason (expired, wrong audience). Report that, not a later key's
			// signature mismatch.
			return nil, fmt.Errorf("verifying token: %w", err)
		}
		lastErr = err
	}
	return nil, fmt.Errorf("verifying token: %w", lastErr)
}

// Verify interface compliance.
var _ middleware.Authenticator = (*OAuthJWTAuthenticator)(nil)
