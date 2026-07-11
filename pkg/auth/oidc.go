package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/sync/singleflight"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/oidcdiscovery"
)

const (
	// jwtPartCount is the number of parts in a JWT token (header.payload.signature).
	jwtPartCount = 3

	// defaultClockSkewSeconds is the default allowed clock skew for time-based claims.
	defaultClockSkewSeconds = 30

	// bitsPerByte is the number of bits in a byte, used for RSA key size calculation.
	bitsPerByte = 8

	// jwksCacheTTL is how long a fetched JWKS is considered fresh before an
	// on-demand refresh is triggered by the key-lookup path.
	jwksCacheTTL = 1 * time.Hour

	// jwksRefreshThrottle bounds how often a successful refresh is followed by
	// another on-demand refresh. After a success the cache holds fresh keys, so
	// the only reason to refetch soon is an unknown kid (rotation or a garbage-kid
	// flood); this window prevents such tokens from hammering the IdP.
	jwksRefreshThrottle = 1 * time.Minute

	// jwksFailedRefreshThrottle is the (much shorter) window applied after a
	// FAILED refresh. A failed refresh leaves the cache unable to serve the
	// requested key (a fail-closed state when the cache is also expired), so
	// recovery must not wait the full anti-hammer window: a transient IdP blip
	// should heal within seconds, while a hard outage is still bounded to roughly
	// one fetch per this interval per replica.
	jwksFailedRefreshThrottle = 5 * time.Second

	// jwksRefreshTimeout bounds a single on-demand JWKS refresh.
	jwksRefreshTimeout = 30 * time.Second

	// jwksRefreshKey is the single-flight key that collapses concurrent
	// on-demand refreshes into one fetch.
	jwksRefreshKey = "jwks"
)

// OIDCConfig configures OIDC authentication.
type OIDCConfig struct {
	// Issuer is the OIDC issuer URL.
	Issuer string

	// ClientID is the OAuth client ID.
	ClientID string

	// Audience is the expected audience claim.
	Audience string

	// RoleClaimPath is the path to roles in claims.
	RoleClaimPath string

	// RolePrefix filters roles to those with this prefix.
	RolePrefix string

	// ClockSkewSeconds is the allowed clock skew for time-based claims (default: 30).
	ClockSkewSeconds int

	// MaxTokenAge is the maximum allowed age of a token based on iat claim (0 = no limit).
	MaxTokenAge time.Duration

	// SkipIssuerVerification skips issuer verification (for testing).
	SkipIssuerVerification bool

	// SkipSignatureVerification skips JWT signature verification (for testing only).
	// WARNING: Never enable in production - allows forged tokens.
	SkipSignatureVerification bool
}

// OIDCAuthenticator authenticates using OIDC tokens.
type OIDCAuthenticator struct {
	cfg       OIDCConfig
	extractor *ClaimsExtractor

	// Cached JWKS
	mu   sync.RWMutex
	jwks *jwksCache

	// lastRefreshAttempt records when the most recent on-demand refresh was
	// attempted, and lastRefreshOK whether it succeeded (both guarded by mu). The
	// startup FetchJWKS does not set them, so the first cache-miss after boot is
	// never throttled. The outcome selects the throttle window: a short window
	// after a failure (fast recovery) and the full anti-hammer window after a
	// success.
	lastRefreshAttempt time.Time
	lastRefreshOK      bool

	// refreshGroup collapses concurrent on-demand refreshes into a single fetch.
	refreshGroup singleflight.Group

	// now is the clock used for cache expiry and refresh throttling. It defaults
	// to time.Now and is overridable in tests for deterministic timing.
	now func() time.Time
}

// clock returns the current time via the injectable clock, defaulting to
// time.Now when unset.
func (a *OIDCAuthenticator) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

type jwksCache struct {
	keys      map[string]*rsa.PublicKey // kid -> RSA public key
	rawKeys   map[string]any            // raw JWKS response for debugging
	expiresAt time.Time
}

// NewOIDCAuthenticator creates a new OIDC authenticator.
func NewOIDCAuthenticator(cfg OIDCConfig) (*OIDCAuthenticator, error) {
	if cfg.Issuer == "" {
		return nil, errors.New("oidc issuer is required")
	}

	extractor := &ClaimsExtractor{
		RoleClaimPath:    cfg.RoleClaimPath,
		RolePrefix:       cfg.RolePrefix,
		EmailClaimPath:   claimEmail,
		NameClaimPath:    claimName,
		SubjectClaimPath: claimSubject,
	}

	auth := &OIDCAuthenticator{
		cfg:       cfg,
		extractor: extractor,
	}

	// Fetch JWKS on startup unless signature verification is disabled
	if !cfg.SkipSignatureVerification {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := auth.FetchJWKS(ctx); err != nil {
			return nil, fmt.Errorf("fetching JWKS: %w", err)
		}
	}

	return auth, nil
}

// Authenticate validates the token and returns user info.
func (a *OIDCAuthenticator) Authenticate(ctx context.Context) (*middleware.UserInfo, error) {
	token := GetToken(ctx)
	if token == "" {
		return nil, errors.New("no token found in context")
	}

	// Parse and validate the JWT
	claims, err := a.parseAndValidateToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	// Extract user context
	uc, err := a.extractor.Extract(claims)
	if err != nil {
		return nil, fmt.Errorf("extracting claims: %w", err)
	}

	return &middleware.UserInfo{
		UserID:   uc.UserID,
		Email:    uc.Email,
		Name:     uc.Name,
		Claims:   uc.Claims,
		Roles:    uc.Roles,
		AuthType: middleware.AuthTypeOIDC,
	}, nil
}

// parseAndValidateToken parses and validates a JWT with signature verification.
//
// Returns ErrNotAJWT (unwrapped) when the credential clearly isn't a
// JWT, so ChainedAuthenticator can silently fall through to the next
// authenticator without logging a misleading "rejected" line for what
// is actually a normal API-key-handed-to-JWT-authenticator path.
func (a *OIDCAuthenticator) parseAndValidateToken(ctx context.Context, tokenString string) (map[string]any, error) {
	if !LooksLikeJWT(tokenString) {
		return nil, ErrNotAJWT
	}

	// If signature verification is disabled (testing only), use legacy parsing
	if a.cfg.SkipSignatureVerification {
		return a.parseTokenWithoutSignatureVerification(tokenString)
	}

	// Parse and verify the JWT signature
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		// Validate the algorithm is RSA-based
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		// Get the key ID from the header
		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, errors.New("token missing kid header")
		}

		// Get the public key from JWKS cache. The inbound request context is
		// threaded through so an on-demand refresh honors the caller's deadline.
		key, err := a.getPublicKey(ctx, kid)
		if err != nil {
			return nil, fmt.Errorf("getting public key: %w", err)
		}

		return key, nil
	})
	if err != nil {
		return nil, fmt.Errorf("verifying token: %w", err)
	}

	if !token.Valid {
		return nil, errors.New("invalid token")
	}

	// Extract claims as map
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid claims type")
	}

	// Convert to map[string]any for compatibility
	claimsMap := make(map[string]any)
	maps.Copy(claimsMap, claims)

	// Validate standard claims (issuer, audience)
	if err := a.validateClaims(claimsMap); err != nil {
		return nil, err
	}

	return claimsMap, nil
}

// parseTokenWithoutSignatureVerification parses JWT without verifying signature.
// WARNING: Only for testing - never use in production.
func (a *OIDCAuthenticator) parseTokenWithoutSignatureVerification(tokenString string) (map[string]any, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != jwtPartCount {
		return nil, errors.New("invalid JWT format")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decoding payload: %w", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("parsing claims: %w", err)
	}

	if err := a.validateClaims(claims); err != nil {
		return nil, err
	}

	return claims, nil
}

// getPublicKey retrieves an RSA public key by key ID from the JWKS cache,
// self-healing via an on-demand refresh when the cache is expired or the kid is
// unknown (e.g. after IdP key rotation). Concurrent refreshes are collapsed with
// single-flight and throttled to at most once per jwksRefreshThrottle.
//
// It fails closed: when the cache is expired and the refresh fails, it returns
// an error wrapping the fetch failure and never yields a key. A throttled or
// post-refresh miss returns the normal key-not-found error.
func (a *OIDCAuthenticator) getPublicKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	key, expired, found := a.lookupKey(kid)
	if found {
		return key, nil
	}

	if err := a.refreshForLookup(ctx, expired); err != nil {
		return nil, err
	}

	key, expired, found = a.lookupKey(kid)
	switch {
	case found:
		return key, nil
	case expired:
		return nil, errors.New("jwks cache expired")
	default:
		return nil, fmt.Errorf("key not found: %s", kid)
	}
}

// lookupKey returns the cached key for kid under a read lock. found is true only
// when the cache is loaded, unexpired, and contains kid. expired is true when the
// cache is absent or stale and therefore a refresh is warranted.
func (a *OIDCAuthenticator) lookupKey(kid string) (key *rsa.PublicKey, expired, found bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.jwks == nil || a.clock().After(a.jwks.expiresAt) {
		return nil, true, false
	}

	k, ok := a.jwks.keys[kid]
	return k, false, ok
}

// refreshForLookup performs an on-demand, single-flighted, throttled JWKS
// refresh triggered by a cache miss. cacheExpired reports whether the current
// cache is stale, which determines the fail-closed semantics.
//
// The fetch runs on a detached, bounded context in its own goroutine (via
// DoChan) so it always completes and populates the cache for future requests,
// while this caller waits only until its own ctx is done. That way a slow or
// hung IdP cannot pin a request goroutine past the caller's deadline.
//
// It returns an error only when an actual refresh was attempted, failed, and the
// cache was expired (fail closed), or when the caller's ctx is canceled. When
// the refresh is throttled, or fails while the cache is still valid (a mere
// unknown-kid miss), it returns nil and lets the caller re-inspect the cache and
// report the appropriate not-found error.
func (a *OIDCAuthenticator) refreshForLookup(ctx context.Context, cacheExpired bool) error {
	// The throttle is checked inside the single-flight function, not before
	// DoChan, on purpose: concurrent callers then attach to the one in-flight
	// fetch and share its result. A pre-DoChan throttle short-circuit would let a
	// caller that arrives while a refresh is in flight bail early and read the
	// still-stale (or expired) cache, returning a spurious miss for a token the
	// in-flight refresh is about to make verifiable. The saved goroutine per
	// throttled miss is not worth that correctness cost.
	ch := a.refreshGroup.DoChan(jwksRefreshKey, func() (any, error) {
		if a.refreshThrottled() {
			return struct{}{}, nil
		}

		a.mu.Lock()
		a.lastRefreshAttempt = a.clock()
		a.mu.Unlock()

		fctx, cancel := context.WithTimeout(context.Background(), jwksRefreshTimeout)
		defer cancel()
		err := a.RefreshJWKS(fctx)

		a.mu.Lock()
		a.lastRefreshOK = err == nil
		a.mu.Unlock()
		return struct{}{}, err
	})

	select {
	case <-ctx.Done():
		return fmt.Errorf("awaiting jwks refresh: %w", ctx.Err())
	case res := <-ch:
		if res.Err != nil && cacheExpired {
			return fmt.Errorf("refreshing jwks: %w", res.Err)
		}
		return nil
	}
}

// refreshThrottled reports whether an on-demand refresh was attempted within the
// applicable throttle window. The window is selected by the outcome of the last
// attempt, not by the current caller's cache state: a failed attempt uses the
// short recovery window so a transient IdP failure heals quickly, while a
// successful attempt uses the full anti-hammer window (a success means the cache
// holds fresh keys, so a soon-after refetch can only be an unknown-kid flood).
// Keying on the last outcome rather than per-caller state also keeps the window
// correct when single-flight collapses callers with differing cache states. The
// startup fetch leaves lastRefreshAttempt zero, so the first on-demand refresh
// after boot is never throttled.
func (a *OIDCAuthenticator) refreshThrottled() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.lastRefreshAttempt.IsZero() {
		return false
	}

	window := jwksFailedRefreshThrottle
	if a.lastRefreshOK {
		window = jwksRefreshThrottle
	}
	return a.clock().Sub(a.lastRefreshAttempt) < window
}

// validateClaims validates standard JWT claims.
func (a *OIDCAuthenticator) validateClaims(claims map[string]any) error {
	if err := a.validateRequiredClaims(claims); err != nil {
		return err
	}

	if err := a.validateTimeClaims(claims); err != nil {
		return err
	}

	return a.validateIdentityClaims(claims)
}

// validateRequiredClaims checks that required claims are present.
func (*OIDCAuthenticator) validateRequiredClaims(claims map[string]any) error {
	// REQUIRE sub claim - every token must have a subject
	sub, ok := claims["sub"].(string)
	if !ok || sub == "" {
		return errors.New("missing or invalid sub claim")
	}

	// REQUIRE exp claim - tokens must have an expiration
	if _, ok := claims["exp"].(float64); !ok {
		return errors.New("missing exp claim")
	}

	return nil
}

// validateTimeClaims validates time-based claims (exp, nbf, iat).
func (a *OIDCAuthenticator) validateTimeClaims(claims map[string]any) error {
	now := time.Now().Unix()
	skew := a.getClockSkew()

	// Check expiration with clock skew allowance
	exp, ok := claims["exp"].(float64)
	if !ok {
		return errors.New("missing exp claim")
	}
	if now > int64(exp)+skew {
		return errors.New("token expired")
	}

	// Check nbf (not before) if present
	if nbf, ok := claims["nbf"].(float64); ok {
		if now < int64(nbf)-skew {
			return errors.New("token not yet valid")
		}
	}

	// Check iat (issued at) for max token age
	if a.cfg.MaxTokenAge > 0 {
		if iat, ok := claims["iat"].(float64); ok {
			if now-int64(iat) > int64(a.cfg.MaxTokenAge.Seconds()) {
				return errors.New("token too old")
			}
		}
	}

	return nil
}

// validateIdentityClaims validates issuer and audience claims.
func (a *OIDCAuthenticator) validateIdentityClaims(claims map[string]any) error {
	// Check issuer
	if !a.cfg.SkipIssuerVerification {
		if iss, ok := claims["iss"].(string); !ok || iss != a.cfg.Issuer {
			return errors.New("invalid issuer")
		}
	}

	// REQUIRE audience when configured
	if a.cfg.Audience != "" && !a.checkAudience(claims) {
		return errors.New("invalid audience")
	}

	return nil
}

// getClockSkew returns the configured clock skew or default.
func (a *OIDCAuthenticator) getClockSkew() int64 {
	if a.cfg.ClockSkewSeconds > 0 {
		return int64(a.cfg.ClockSkewSeconds)
	}
	return defaultClockSkewSeconds
}

// checkAudience checks if the token audience matches.
func (a *OIDCAuthenticator) checkAudience(claims map[string]any) bool {
	switch aud := claims["aud"].(type) {
	case string:
		return aud == a.cfg.Audience
	case []any:
		for _, v := range aud {
			if s, ok := v.(string); ok && s == a.cfg.Audience {
				return true
			}
		}
	}
	return false
}

// FetchJWKS fetches the JWKS from the issuer and parses RSA public keys.
func (a *OIDCAuthenticator) FetchJWKS(ctx context.Context) error {
	jwksURI, err := a.discoverJWKSURI(ctx)
	if err != nil {
		return err
	}

	keys, rawKeys, err := a.fetchAndParseJWKS(ctx, jwksURI)
	if err != nil {
		return err
	}

	a.mu.Lock()
	a.jwks = &jwksCache{
		keys:      keys,
		rawKeys:   rawKeys,
		expiresAt: a.clock().Add(jwksCacheTTL),
	}
	a.mu.Unlock()

	return nil
}

// discoverJWKSURI fetches the OIDC discovery document to get the JWKS URI.
func (a *OIDCAuthenticator) discoverJWKSURI(ctx context.Context) (string, error) {
	doc, err := oidcdiscovery.Fetch(ctx, http.DefaultClient, a.cfg.Issuer)
	if err != nil {
		return "", fmt.Errorf("discovering jwks uri: %w", err)
	}
	if doc.JWKSURI == "" {
		return "", errors.New("jwks_uri not found in discovery document")
	}
	return doc.JWKSURI, nil
}

// fetchAndParseJWKS fetches the JWKS and parses RSA keys.
func (a *OIDCAuthenticator) fetchAndParseJWKS(ctx context.Context, jwksURI string) (keys map[string]*rsa.PublicKey, raw map[string]any, err error) {
	jwksReq, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, http.NoBody)
	if err != nil {
		return nil, nil, fmt.Errorf("creating JWKS request: %w", err)
	}

	jwksResp, err := http.DefaultClient.Do(jwksReq) // #nosec G704 -- URL from OIDC discovery document
	if err != nil {
		return nil, nil, fmt.Errorf("fetching JWKS: %w", err)
	}
	defer func() { _ = jwksResp.Body.Close() }()

	if jwksResp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("jwks request failed: %d", jwksResp.StatusCode)
	}

	var jwksResponse struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(jwksResp.Body).Decode(&jwksResponse); err != nil {
		return nil, nil, fmt.Errorf("parsing JWKS: %w", err)
	}

	keys, rawKeys := a.parseJWKSKeys(jwksResponse.Keys)
	if len(keys) == 0 {
		return nil, nil, errors.New("no valid RSA signing keys found in JWKS")
	}

	return keys, rawKeys, nil
}

// jwkKeyInfo holds parsed JWK key information.
type jwkKeyInfo struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// parseJWKSKeys parses raw JWKS keys into RSA public keys.
func (*OIDCAuthenticator) parseJWKSKeys(rawKeys []json.RawMessage) (rsaKeys map[string]*rsa.PublicKey, otherKeys map[string]any) {
	keys := make(map[string]*rsa.PublicKey)
	rawKeyMap := make(map[string]any)

	for _, keyData := range rawKeys {
		var keyInfo jwkKeyInfo
		if err := json.Unmarshal(keyData, &keyInfo); err != nil {
			continue
		}

		// Store raw key for debugging
		var raw any
		_ = json.Unmarshal(keyData, &raw)
		rawKeyMap[keyInfo.Kid] = raw

		// Only process RSA keys used for signing
		if !isSigningRSAKey(keyInfo) {
			continue
		}

		pubKey, err := parseRSAPublicKey(keyInfo.N, keyInfo.E)
		if err != nil {
			continue
		}

		keys[keyInfo.Kid] = pubKey
	}

	return keys, rawKeyMap
}

// isSigningRSAKey checks if a JWK key is an RSA signing key.
func isSigningRSAKey(keyInfo jwkKeyInfo) bool {
	if keyInfo.Kty != "RSA" {
		return false
	}
	return keyInfo.Use == "" || keyInfo.Use == "sig"
}

// parseRSAPublicKey parses RSA public key from JWK n and e values.
func parseRSAPublicKey(nStr, eStr string) (*rsa.PublicKey, error) {
	// Decode modulus (n)
	nBytes, err := base64.RawURLEncoding.DecodeString(nStr)
	if err != nil {
		return nil, fmt.Errorf("decoding modulus: %w", err)
	}

	// Decode exponent (e)
	eBytes, err := base64.RawURLEncoding.DecodeString(eStr)
	if err != nil {
		return nil, fmt.Errorf("decoding exponent: %w", err)
	}

	// Convert exponent bytes to int
	var e int
	for _, b := range eBytes {
		e = e<<bitsPerByte + int(b)
	}

	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: e,
	}, nil
}

// RefreshJWKS refreshes the JWKS cache. Call this periodically or when keys expire.
func (a *OIDCAuthenticator) RefreshJWKS(ctx context.Context) error {
	return a.FetchJWKS(ctx)
}

// Verify interface compliance.
var _ middleware.Authenticator = (*OIDCAuthenticator)(nil)
