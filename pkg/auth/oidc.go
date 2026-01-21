package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

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

	// SkipIssuerVerification skips issuer verification (for testing).
	SkipIssuerVerification bool
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
	keys      map[string]any
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

	return &OIDCAuthenticator{
		cfg:       cfg,
		extractor: extractor,
	}, nil
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

// parseAndValidateToken parses and validates a JWT.
func (a *OIDCAuthenticator) parseAndValidateToken(token string) (map[string]any, error) {
	// Split the JWT
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
	}

	// Decode the payload (middle part)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decoding payload: %w", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("parsing claims: %w", err)
	}

	// Validate standard claims
	if err := a.validateClaims(claims); err != nil {
		return nil, err
	}

	return claims, nil
}

// validateClaims validates standard JWT claims.
func (a *OIDCAuthenticator) validateClaims(claims map[string]any) error {
	// Check issuer
	if !a.cfg.SkipIssuerVerification {
		if iss, ok := claims["iss"].(string); !ok || iss != a.cfg.Issuer {
			return fmt.Errorf("invalid issuer")
		}
	}

	// Check audience if configured
	if a.cfg.Audience != "" {
		if !a.checkAudience(claims) {
			return fmt.Errorf("invalid audience")
		}
	}

	// Check expiration
	if exp, ok := claims["exp"].(float64); ok {
		if time.Now().Unix() > int64(exp) {
			return fmt.Errorf("token expired")
		}
	}

	return nil
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

// FetchJWKS fetches the JWKS from the issuer.
func (a *OIDCAuthenticator) FetchJWKS(ctx context.Context) error {
	// Discover the JWKS URI
	discoveryURL := strings.TrimSuffix(a.cfg.Issuer, "/") + "/.well-known/openid-configuration"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return fmt.Errorf("creating discovery request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching discovery document: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("discovery request failed: %d", resp.StatusCode)
	}

	var discovery struct {
		JwksURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&discovery); err != nil {
		return fmt.Errorf("parsing discovery document: %w", err)
	}

	// Fetch JWKS
	jwksReq, err := http.NewRequestWithContext(ctx, http.MethodGet, discovery.JwksURI, nil)
	if err != nil {
		return fmt.Errorf("creating JWKS request: %w", err)
	}

	jwksResp, err := http.DefaultClient.Do(jwksReq)
	if err != nil {
		return fmt.Errorf("fetching JWKS: %w", err)
	}
	defer func() { _ = jwksResp.Body.Close() }()

	if jwksResp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS request failed: %d", jwksResp.StatusCode)
	}

	var jwks map[string]any
	if err := json.NewDecoder(jwksResp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("parsing JWKS: %w", err)
	}

	a.mu.Lock()
	a.jwks = &jwksCache{
		keys:      jwks,
		expiresAt: time.Now().Add(1 * time.Hour),
	}
	a.mu.Unlock()

	return nil
}

// Verify interface compliance.
var _ middleware.Authenticator = (*OIDCAuthenticator)(nil)
