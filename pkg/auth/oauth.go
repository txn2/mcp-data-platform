package auth

import (
	"context"
	"fmt"
	"maps"

	"github.com/golang-jwt/jwt/v5"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// OAuthJWTConfig configures the OAuth JWT authenticator.
type OAuthJWTConfig struct {
	// Issuer is the expected issuer claim in the JWT.
	Issuer string

	// SigningKey is the HMAC key used to verify JWT signatures.
	SigningKey []byte

	// RoleClaimPath is the path to roles within the nested "claims" object.
	// e.g., "realm_access.roles" extracts claims["claims"]["realm_access"]["roles"]
	RoleClaimPath string

	// RolePrefix filters roles to those with this prefix.
	RolePrefix string
}

// OAuthJWTAuthenticator validates JWT access tokens issued by our OAuth server.
type OAuthJWTAuthenticator struct {
	cfg       OAuthJWTConfig
	extractor *ClaimsExtractor
}

// NewOAuthJWTAuthenticator creates a new OAuth JWT authenticator.
func NewOAuthJWTAuthenticator(cfg OAuthJWTConfig) (*OAuthJWTAuthenticator, error) {
	if cfg.Issuer == "" {
		return nil, fmt.Errorf("oauth issuer is required")
	}
	if len(cfg.SigningKey) == 0 {
		return nil, fmt.Errorf("oauth signing key is required")
	}

	extractor := &ClaimsExtractor{
		RoleClaimPath:    cfg.RoleClaimPath,
		RolePrefix:       cfg.RolePrefix,
		EmailClaimPath:   "email",
		NameClaimPath:    "name",
		SubjectClaimPath: "sub",
	}

	return &OAuthJWTAuthenticator{
		cfg:       cfg,
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

	// Also try to get email from nested claims
	email, _ := userClaims["email"].(string)

	return &middleware.UserInfo{
		UserID:   userID,
		Email:    email,
		Claims:   userClaims,
		Roles:    roles,
		AuthType: "oauth",
	}, nil
}

// parseAndValidateToken parses and validates the JWT.
func (a *OAuthJWTAuthenticator) parseAndValidateToken(tokenString string) (map[string]any, error) {
	// Parse and verify the JWT signature
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (any, error) {
		// Validate the algorithm is HMAC
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return a.cfg.SigningKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parsing token: %w", err)
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

// Verify interface compliance.
var _ middleware.Authenticator = (*OAuthJWTAuthenticator)(nil)
