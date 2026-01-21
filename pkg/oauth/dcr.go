package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"regexp"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// DCRConfig configures Dynamic Client Registration.
type DCRConfig struct {
	// Enabled enables DCR.
	Enabled bool

	// AllowedRedirectPatterns are regex patterns for allowed redirect URIs.
	AllowedRedirectPatterns []string

	// DefaultGrantTypes are the default grant types for new clients.
	DefaultGrantTypes []string

	// RequirePKCE requires PKCE for all clients.
	RequirePKCE bool
}

// DCRService handles Dynamic Client Registration.
type DCRService struct {
	storage  Storage
	config   DCRConfig
	patterns []*regexp.Regexp
}

// NewDCRService creates a new DCR service.
func NewDCRService(storage Storage, config DCRConfig) (*DCRService, error) {
	patterns := make([]*regexp.Regexp, 0, len(config.AllowedRedirectPatterns))
	for _, p := range config.AllowedRedirectPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid redirect pattern %q: %w", p, err)
		}
		patterns = append(patterns, re)
	}

	if len(config.DefaultGrantTypes) == 0 {
		config.DefaultGrantTypes = []string{"authorization_code", "refresh_token"}
	}

	return &DCRService{
		storage:  storage,
		config:   config,
		patterns: patterns,
	}, nil
}

// DCRRequest is a Dynamic Client Registration request.
type DCRRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
}

// DCRResponse is a Dynamic Client Registration response.
type DCRResponse struct {
	ClientID              string   `json:"client_id"`
	ClientSecret          string   `json:"client_secret,omitempty"`
	ClientName            string   `json:"client_name"`
	RedirectURIs          []string `json:"redirect_uris"`
	GrantTypes            []string `json:"grant_types"`
	ClientSecretExpiresAt int      `json:"client_secret_expires_at"` // 0 means never
}

// Register registers a new OAuth client.
func (s *DCRService) Register(ctx context.Context, req DCRRequest) (*DCRResponse, error) {
	if !s.config.Enabled {
		return nil, fmt.Errorf("dynamic client registration is disabled")
	}

	// Validate redirect URIs
	for _, uri := range req.RedirectURIs {
		if !s.isAllowedRedirectURI(uri) {
			return nil, fmt.Errorf("redirect URI not allowed: %s", uri)
		}
	}

	// Generate client credentials
	clientID, err := generateSecureToken(32)
	if err != nil {
		return nil, fmt.Errorf("generating client ID: %w", err)
	}

	clientSecret, err := generateSecureToken(48)
	if err != nil {
		return nil, fmt.Errorf("generating client secret: %w", err)
	}

	// Hash the client secret
	hashedSecret, err := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hashing client secret: %w", err)
	}

	// Determine grant types
	grantTypes := req.GrantTypes
	if len(grantTypes) == 0 {
		grantTypes = s.config.DefaultGrantTypes
	}

	// Create client
	client := &Client{
		ID:           generateID(),
		ClientID:     clientID,
		ClientSecret: string(hashedSecret),
		Name:         req.ClientName,
		RedirectURIs: req.RedirectURIs,
		GrantTypes:   grantTypes,
		RequirePKCE:  s.config.RequirePKCE,
		CreatedAt:    time.Now(),
		Active:       true,
	}

	if err := s.storage.CreateClient(ctx, client); err != nil {
		return nil, fmt.Errorf("creating client: %w", err)
	}

	return &DCRResponse{
		ClientID:              clientID,
		ClientSecret:          clientSecret, // Return unhashed secret to client
		ClientName:            req.ClientName,
		RedirectURIs:          req.RedirectURIs,
		GrantTypes:            grantTypes,
		ClientSecretExpiresAt: 0,
	}, nil
}

// isAllowedRedirectURI checks if a redirect URI is allowed.
func (s *DCRService) isAllowedRedirectURI(uri string) bool {
	if len(s.patterns) == 0 {
		return true // Allow all if no patterns configured
	}

	for _, pattern := range s.patterns {
		if pattern.MatchString(uri) {
			return true
		}
	}
	return false
}

// generateSecureToken generates a cryptographically secure token.
func generateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// generateID generates a unique ID.
func generateID() string {
	bytes := make([]byte, 16)
	_, _ = rand.Read(bytes)
	return base64.RawURLEncoding.EncodeToString(bytes)
}
