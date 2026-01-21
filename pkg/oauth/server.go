package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ServerConfig configures the OAuth server.
type ServerConfig struct {
	// Issuer is the OAuth issuer URL.
	Issuer string

	// AccessTokenTTL is the access token lifetime.
	AccessTokenTTL time.Duration

	// RefreshTokenTTL is the refresh token lifetime.
	RefreshTokenTTL time.Duration

	// AuthCodeTTL is the authorization code lifetime.
	AuthCodeTTL time.Duration

	// DCR configures Dynamic Client Registration.
	DCR DCRConfig
}

// Server is an OAuth 2.1 authorization server.
type Server struct {
	config  ServerConfig
	storage Storage
	dcr     *DCRService
}

// NewServer creates a new OAuth server.
func NewServer(config ServerConfig, storage Storage) (*Server, error) {
	if config.AccessTokenTTL == 0 {
		config.AccessTokenTTL = 1 * time.Hour
	}
	if config.RefreshTokenTTL == 0 {
		config.RefreshTokenTTL = 24 * time.Hour * 30 // 30 days
	}
	if config.AuthCodeTTL == 0 {
		config.AuthCodeTTL = 10 * time.Minute
	}

	var dcr *DCRService
	if config.DCR.Enabled {
		var err error
		dcr, err = NewDCRService(storage, config.DCR)
		if err != nil {
			return nil, fmt.Errorf("creating DCR service: %w", err)
		}
	}

	return &Server{
		config:  config,
		storage: storage,
		dcr:     dcr,
	}, nil
}

// AuthorizationRequest represents an authorization request.
type AuthorizationRequest struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	Scope               string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
}

// TokenRequest represents a token request.
type TokenRequest struct {
	GrantType    string
	Code         string
	RedirectURI  string
	ClientID     string
	ClientSecret string
	CodeVerifier string
	RefreshToken string
	Scope        string
}

// TokenResponse represents a token response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// ErrorResponse represents an OAuth error response.
type ErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// validateAuthorizationRequest validates the authorization request and returns the client.
func (s *Server) validateAuthorizationRequest(ctx context.Context, req AuthorizationRequest) (*Client, error) {
	client, err := s.storage.GetClient(ctx, req.ClientID)
	if err != nil {
		return nil, fmt.Errorf("invalid client_id")
	}
	if !client.Active {
		return nil, fmt.Errorf("client is not active")
	}
	if !client.ValidRedirectURI(req.RedirectURI) {
		return nil, fmt.Errorf("invalid redirect_uri")
	}
	if req.ResponseType != "code" {
		return nil, fmt.Errorf("unsupported response_type")
	}
	return client, nil
}

// validatePKCE validates PKCE parameters if required.
func (s *Server) validatePKCE(client *Client, req AuthorizationRequest) error {
	if !client.RequirePKCE {
		return nil
	}
	if req.CodeChallenge == "" {
		return fmt.Errorf("code_challenge required")
	}
	if req.CodeChallengeMethod != "S256" && req.CodeChallengeMethod != "plain" {
		return fmt.Errorf("invalid code_challenge_method")
	}
	return nil
}

// Authorize handles the authorization endpoint.
func (s *Server) Authorize(ctx context.Context, req AuthorizationRequest, userID string, userClaims map[string]any) (string, error) {
	client, err := s.validateAuthorizationRequest(ctx, req)
	if err != nil {
		return "", err
	}

	if err := s.validatePKCE(client, req); err != nil {
		return "", err
	}

	codeValue, err := generateSecureToken(32)
	if err != nil {
		return "", fmt.Errorf("generating authorization code: %w", err)
	}

	code := &AuthorizationCode{
		ID:            generateID(),
		Code:          codeValue,
		ClientID:      req.ClientID,
		UserID:        userID,
		UserClaims:    userClaims,
		CodeChallenge: req.CodeChallenge,
		RedirectURI:   req.RedirectURI,
		Scope:         req.Scope,
		ExpiresAt:     time.Now().Add(s.config.AuthCodeTTL),
		Used:          false,
		CreatedAt:     time.Now(),
	}

	if err := s.storage.SaveAuthorizationCode(ctx, code); err != nil {
		return "", fmt.Errorf("saving authorization code: %w", err)
	}

	return codeValue, nil
}

// Token handles the token endpoint.
func (s *Server) Token(ctx context.Context, req TokenRequest) (*TokenResponse, error) {
	switch req.GrantType {
	case "authorization_code":
		return s.handleAuthorizationCodeGrant(ctx, req)
	case "refresh_token":
		return s.handleRefreshTokenGrant(ctx, req)
	default:
		return nil, fmt.Errorf("unsupported grant_type")
	}
}

// validateAuthorizationCode validates the authorization code state.
func (s *Server) validateAuthorizationCode(code *AuthorizationCode, req TokenRequest) error {
	if code.Used {
		return fmt.Errorf("authorization code already used")
	}
	if code.IsExpired() {
		return fmt.Errorf("authorization code expired")
	}
	if code.ClientID != req.ClientID {
		return fmt.Errorf("client_id mismatch")
	}
	if code.RedirectURI != req.RedirectURI {
		return fmt.Errorf("redirect_uri mismatch")
	}
	return nil
}

// validateClientCredentials validates client credentials.
func (s *Server) validateClientCredentials(ctx context.Context, req TokenRequest) (*Client, error) {
	client, err := s.storage.GetClient(ctx, req.ClientID)
	if err != nil {
		return nil, fmt.Errorf("invalid client_id")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(client.ClientSecret), []byte(req.ClientSecret)); err != nil {
		return nil, fmt.Errorf("invalid client credentials")
	}
	return client, nil
}

// verifyCodeChallenge verifies PKCE code challenge if used.
func (s *Server) verifyCodeChallenge(code *AuthorizationCode, req TokenRequest) error {
	if code.CodeChallenge == "" {
		return nil
	}
	if req.CodeVerifier == "" {
		return fmt.Errorf("code_verifier required")
	}
	valid, err := VerifyCodeChallenge(req.CodeVerifier, code.CodeChallenge, PKCEMethodS256)
	if err != nil || !valid {
		return fmt.Errorf("invalid code_verifier")
	}
	return nil
}

// handleAuthorizationCodeGrant handles the authorization code grant.
func (s *Server) handleAuthorizationCodeGrant(ctx context.Context, req TokenRequest) (*TokenResponse, error) {
	code, err := s.storage.GetAuthorizationCode(ctx, req.Code)
	if err != nil {
		return nil, fmt.Errorf("invalid authorization code")
	}

	if err := s.validateAuthorizationCode(code, req); err != nil {
		return nil, err
	}

	client, err := s.validateClientCredentials(ctx, req)
	if err != nil {
		return nil, err
	}

	if err := s.verifyCodeChallenge(code, req); err != nil {
		return nil, err
	}

	code.Used = true
	_ = s.storage.DeleteAuthorizationCode(ctx, code.Code)

	return s.generateTokens(ctx, client, code.UserID, code.UserClaims, code.Scope)
}

// handleRefreshTokenGrant handles the refresh token grant.
func (s *Server) handleRefreshTokenGrant(ctx context.Context, req TokenRequest) (*TokenResponse, error) {
	// Retrieve refresh token
	token, err := s.storage.GetRefreshToken(ctx, req.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("invalid refresh token")
	}

	if token.IsExpired() {
		_ = s.storage.DeleteRefreshToken(ctx, token.Token)
		return nil, fmt.Errorf("refresh token expired")
	}

	if token.ClientID != req.ClientID {
		return nil, fmt.Errorf("client_id mismatch")
	}

	// Validate client credentials
	client, err := s.storage.GetClient(ctx, req.ClientID)
	if err != nil {
		return nil, fmt.Errorf("invalid client_id")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(client.ClientSecret), []byte(req.ClientSecret)); err != nil {
		return nil, fmt.Errorf("invalid client credentials")
	}

	// Delete old refresh token (rotation)
	_ = s.storage.DeleteRefreshToken(ctx, token.Token)

	// Generate new tokens
	scope := req.Scope
	if scope == "" {
		scope = token.Scope
	}

	return s.generateTokens(ctx, client, token.UserID, token.UserClaims, scope)
}

// generateTokens generates access and refresh tokens.
func (s *Server) generateTokens(ctx context.Context, client *Client, userID string, userClaims map[string]any, scope string) (*TokenResponse, error) {
	// Generate access token (in production, use JWT)
	accessToken, err := generateSecureToken(32)
	if err != nil {
		return nil, fmt.Errorf("generating access token: %w", err)
	}

	// Generate refresh token
	refreshTokenValue, err := generateSecureToken(32)
	if err != nil {
		return nil, fmt.Errorf("generating refresh token: %w", err)
	}

	// Save refresh token
	refreshToken := &RefreshToken{
		ID:         generateID(),
		Token:      refreshTokenValue,
		ClientID:   client.ClientID,
		UserID:     userID,
		UserClaims: userClaims,
		Scope:      scope,
		ExpiresAt:  time.Now().Add(s.config.RefreshTokenTTL),
		CreatedAt:  time.Now(),
	}

	if err := s.storage.SaveRefreshToken(ctx, refreshToken); err != nil {
		return nil, fmt.Errorf("saving refresh token: %w", err)
	}

	return &TokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.config.AccessTokenTTL.Seconds()),
		RefreshToken: refreshTokenValue,
		Scope:        scope,
	}, nil
}

// RegisterClient handles Dynamic Client Registration.
func (s *Server) RegisterClient(ctx context.Context, req DCRRequest) (*DCRResponse, error) {
	if s.dcr == nil {
		return nil, fmt.Errorf("dynamic client registration is disabled")
	}
	return s.dcr.Register(ctx, req)
}

// ServeHTTP implements http.Handler for the OAuth server.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/oauth/token":
		s.handleTokenEndpoint(w, r)
	case "/oauth/register":
		s.handleRegisterEndpoint(w, r)
	case "/.well-known/oauth-authorization-server":
		s.handleMetadata(w, r)
	default:
		http.NotFound(w, r)
	}
}

// handleTokenEndpoint handles POST /oauth/token.
func (s *Server) handleTokenEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}

	if err := r.ParseForm(); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", "could not parse form")
		return
	}

	req := TokenRequest{
		GrantType:    r.FormValue("grant_type"),
		Code:         r.FormValue("code"),
		RedirectURI:  r.FormValue("redirect_uri"),
		ClientID:     r.FormValue("client_id"),
		ClientSecret: r.FormValue("client_secret"),
		CodeVerifier: r.FormValue("code_verifier"),
		RefreshToken: r.FormValue("refresh_token"),
		Scope:        r.FormValue("scope"),
	}

	// Support Basic auth for client credentials
	if req.ClientID == "" || req.ClientSecret == "" {
		if username, password, ok := r.BasicAuth(); ok {
			req.ClientID = username
			req.ClientSecret = password
		}
	}

	resp, err := s.Token(r.Context(), req)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, resp)
}

// handleRegisterEndpoint handles POST /oauth/register.
func (s *Server) handleRegisterEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}

	var req DCRRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", "could not parse JSON")
		return
	}

	resp, err := s.RegisterClient(r.Context(), req)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	s.writeJSON(w, http.StatusCreated, resp)
}

// handleMetadata handles GET /.well-known/oauth-authorization-server.
func (s *Server) handleMetadata(w http.ResponseWriter, _ *http.Request) {
	metadata := map[string]any{
		"issuer":                                s.config.Issuer,
		"authorization_endpoint":                s.config.Issuer + "/oauth/authorize",
		"token_endpoint":                        s.config.Issuer + "/oauth/token",
		"registration_endpoint":                 s.config.Issuer + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256", "plain"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
	}

	s.writeJSON(w, http.StatusOK, metadata)
}

// writeJSON writes a JSON response.
func (s *Server) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// writeError writes an OAuth error response.
func (s *Server) writeError(w http.ResponseWriter, status int, err, desc string) {
	s.writeJSON(w, status, ErrorResponse{Error: err, ErrorDescription: desc})
}

// StartCleanupRoutine starts a background routine to clean up expired codes and tokens.
func (s *Server) StartCleanupRoutine(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.storage.CleanupExpiredCodes(ctx)
				_ = s.storage.CleanupExpiredTokens(ctx)
			}
		}
	}()
}

// BuildAuthorizationURL builds an authorization URL.
func BuildAuthorizationURL(baseURL, clientID, redirectURI, scope, state string) string {
	// Generate PKCE
	verifier := make([]byte, 32)
	_, _ = rand.Read(verifier)
	codeVerifier := base64.RawURLEncoding.EncodeToString(verifier)
	challenge, _ := GenerateCodeChallenge(codeVerifier, PKCEMethodS256)

	return fmt.Sprintf(
		"%s/oauth/authorize?response_type=code&client_id=%s&redirect_uri=%s&scope=%s&state=%s&code_challenge=%s&code_challenge_method=S256",
		strings.TrimSuffix(baseURL, "/"),
		clientID,
		redirectURI,
		scope,
		state,
		challenge,
	)
}
