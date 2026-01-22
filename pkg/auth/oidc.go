package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
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
}

type jwksCache struct {
	keys      map[string]*rsa.PublicKey // kid -> RSA public key
	rawKeys   map[string]any            // raw JWKS response for debugging
	expiresAt time.Time
}

// NewOIDCAuthenticator creates a new OIDC authenticator.
func NewOIDCAuthenticator(cfg OIDCConfig) (*OIDCAuthenticator, error) {
	if cfg.Issuer == "" {
		return nil, fmt.Errorf("OIDC issuer is required")
	}

	extractor := &ClaimsExtractor{
		RoleClaimPath:    cfg.RoleClaimPath,
		RolePrefix:       cfg.RolePrefix,
		EmailClaimPath:   "email",
		NameClaimPath:    "name",
		SubjectClaimPath: "sub",
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
		return nil, fmt.Errorf("no token found in context")
	}

	// Parse and validate the JWT
	claims, err := a.parseAndValidateToken(token)
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
		Claims:   uc.Claims,
		Roles:    uc.Roles,
		AuthType: "oidc",
	}, nil
}

// parseAndValidateToken parses and validates a JWT with signature verification.
func (a *OIDCAuthenticator) parseAndValidateToken(tokenString string) (map[string]any, error) {
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
			return nil, fmt.Errorf("token missing kid header")
		}

		// Get the public key from JWKS cache
		key, err := a.getPublicKey(kid)
		if err != nil {
			return nil, fmt.Errorf("getting public key: %w", err)
		}

		return key, nil
	})
	if err != nil {
		return nil, fmt.Errorf("verifying token: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	// Extract claims as map
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims type")
	}

	// Convert to map[string]any for compatibility
	claimsMap := make(map[string]any)
	for k, v := range claims {
		claimsMap[k] = v
	}

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
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
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

// getPublicKey retrieves an RSA public key by key ID from the JWKS cache.
func (a *OIDCAuthenticator) getPublicKey(kid string) (*rsa.PublicKey, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.jwks == nil {
		return nil, fmt.Errorf("JWKS not loaded")
	}

	// Check if cache is expired
	if time.Now().After(a.jwks.expiresAt) {
		return nil, fmt.Errorf("JWKS cache expired")
	}

	key, ok := a.jwks.keys[kid]
	if !ok {
		return nil, fmt.Errorf("key not found: %s", kid)
	}

	return key, nil
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
func (a *OIDCAuthenticator) validateRequiredClaims(claims map[string]any) error {
	// REQUIRE sub claim - every token must have a subject
	sub, ok := claims["sub"].(string)
	if !ok || sub == "" {
		return fmt.Errorf("missing or invalid sub claim")
	}

	// REQUIRE exp claim - tokens must have an expiration
	if _, ok := claims["exp"].(float64); !ok {
		return fmt.Errorf("missing exp claim")
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
		return fmt.Errorf("missing exp claim")
	}
	if now > int64(exp)+skew {
		return fmt.Errorf("token expired")
	}

	// Check nbf (not before) if present
	if nbf, ok := claims["nbf"].(float64); ok {
		if now < int64(nbf)-skew {
			return fmt.Errorf("token not yet valid")
		}
	}

	// Check iat (issued at) for max token age
	if a.cfg.MaxTokenAge > 0 {
		if iat, ok := claims["iat"].(float64); ok {
			if now-int64(iat) > int64(a.cfg.MaxTokenAge.Seconds()) {
				return fmt.Errorf("token too old")
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
			return fmt.Errorf("invalid issuer")
		}
	}

	// REQUIRE audience when configured
	if a.cfg.Audience != "" && !a.checkAudience(claims) {
		return fmt.Errorf("invalid audience")
	}

	return nil
}

// getClockSkew returns the configured clock skew or default.
func (a *OIDCAuthenticator) getClockSkew() int64 {
	if a.cfg.ClockSkewSeconds > 0 {
		return int64(a.cfg.ClockSkewSeconds)
	}
	return 30 // default 30 second skew
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
		expiresAt: time.Now().Add(1 * time.Hour),
	}
	a.mu.Unlock()

	return nil
}

// discoverJWKSURI fetches the OIDC discovery document to get the JWKS URI.
func (a *OIDCAuthenticator) discoverJWKSURI(ctx context.Context) (string, error) {
	discoveryURL := strings.TrimSuffix(a.cfg.Issuer, "/") + "/.well-known/openid-configuration"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating discovery request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching discovery document: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discovery request failed: %d", resp.StatusCode)
	}

	var discovery struct {
		JwksURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&discovery); err != nil {
		return "", fmt.Errorf("parsing discovery document: %w", err)
	}

	if discovery.JwksURI == "" {
		return "", fmt.Errorf("jwks_uri not found in discovery document")
	}

	return discovery.JwksURI, nil
}

// fetchAndParseJWKS fetches the JWKS and parses RSA keys.
func (a *OIDCAuthenticator) fetchAndParseJWKS(ctx context.Context, jwksURI string) (map[string]*rsa.PublicKey, map[string]any, error) {
	jwksReq, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("creating JWKS request: %w", err)
	}

	jwksResp, err := http.DefaultClient.Do(jwksReq)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching JWKS: %w", err)
	}
	defer func() { _ = jwksResp.Body.Close() }()

	if jwksResp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("JWKS request failed: %d", jwksResp.StatusCode)
	}

	var jwksResponse struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(jwksResp.Body).Decode(&jwksResponse); err != nil {
		return nil, nil, fmt.Errorf("parsing JWKS: %w", err)
	}

	keys, rawKeys := a.parseJWKSKeys(jwksResponse.Keys)
	if len(keys) == 0 {
		return nil, nil, fmt.Errorf("no valid RSA signing keys found in JWKS")
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
func (a *OIDCAuthenticator) parseJWKSKeys(rawKeys []json.RawMessage) (map[string]*rsa.PublicKey, map[string]any) {
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
		e = e<<8 + int(b)
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
