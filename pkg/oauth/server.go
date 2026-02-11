package oauth //nolint:revive // max-public-structs: OAuth server requires multiple public types for its API surface

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// Token and crypto size constants.
const (
	// tokenByteLength is the byte length for secure tokens (auth codes, access tokens, refresh tokens).
	tokenByteLength = 32

	// stateByteLength is the byte length for upstream IdP state parameters.
	stateByteLength = 16

	// jwtPartCount is the expected number of parts in a JWT (header.payload.signature).
	jwtPartCount = 3

	// defaultRefreshTokenDays is the default refresh token lifetime in days.
	defaultRefreshTokenDays = 30

	// defaultRefreshTokenHours is the hours-per-day factor for computing refresh token TTL.
	defaultRefreshTokenHours = 24

	// defaultAuthCodeMinutes is the default authorization code lifetime in minutes.
	defaultAuthCodeMinutes = 10

	// defaultHTTPTimeoutSeconds is the default HTTP client timeout in seconds.
	defaultHTTPTimeoutSeconds = 30
)

// OAuth error code constants.
const (
	errInvalidRequest   = "invalid_request"
	errMethodNotAllowed = "method_not_allowed"
	errServerError      = "server_error"
)

// OAuth parameter name constants.
const (
	paramCode        = "code"
	paramState       = "state"
	paramScope       = "scope"
	paramRedirectURI = "redirect_uri"
	paramClientID    = "client_id"
)

// Errors returned by the OAuth server.
var (
	ErrStateNotFound = errors.New("authorization state not found")
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

	// SigningKey is the HMAC key used to sign JWT access tokens.
	// If not provided, tokens will be opaque (not recommended for production).
	// Generate with: openssl rand -base64 32
	SigningKey []byte

	// DCR configures Dynamic Client Registration.
	DCR DCRConfig

	// Upstream configures the upstream identity provider (e.g., Keycloak).
	Upstream *UpstreamConfig
}

// UpstreamConfig configures the upstream identity provider.
type UpstreamConfig struct {
	// Issuer is the upstream IdP issuer URL (e.g., Keycloak realm URL).
	Issuer string

	// ClientID is the MCP server's client ID in the upstream IdP.
	ClientID string

	// ClientSecret is the MCP server's client secret.
	ClientSecret string

	// RedirectURI is the callback URL for the upstream IdP.
	RedirectURI string
}

// Server is an OAuth 2.1 authorization server.
type Server struct {
	config     ServerConfig
	storage    Storage
	dcr        *DCRService
	stateStore StateStore
	httpClient *http.Client
}

// NewServer creates a new OAuth server.
func NewServer(config ServerConfig, storage Storage) (*Server, error) {
	if config.AccessTokenTTL == 0 {
		config.AccessTokenTTL = 1 * time.Hour
	}
	if config.RefreshTokenTTL == 0 {
		config.RefreshTokenTTL = defaultRefreshTokenHours * time.Hour * defaultRefreshTokenDays
	}
	if config.AuthCodeTTL == 0 {
		config.AuthCodeTTL = defaultAuthCodeMinutes * time.Minute
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
		config:     config,
		storage:    storage,
		dcr:        dcr,
		stateStore: NewMemoryStateStore(),
		httpClient: &http.Client{Timeout: defaultHTTPTimeoutSeconds * time.Second},
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
	if req.ResponseType != paramCode {
		return nil, fmt.Errorf("unsupported response_type")
	}
	return client, nil
}

// validatePKCE validates PKCE parameters if required.
func (*Server) validatePKCE(client *Client, req AuthorizationRequest) error {
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

	codeValue, err := generateSecureToken(tokenByteLength)
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
func (*Server) validateAuthorizationCode(code *AuthorizationCode, req TokenRequest) error {
	if code.Used {
		return fmt.Errorf("authorization code already used")
	}
	if code.IsExpired() {
		return fmt.Errorf("authorization code expired")
	}
	if code.ClientID != req.ClientID {
		return fmt.Errorf("client_id mismatch")
	}
	if !matchesRedirectURI(code.RedirectURI, req.RedirectURI) {
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
func (*Server) verifyCodeChallenge(code *AuthorizationCode, req TokenRequest) error {
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
	// Generate access token
	accessToken, err := s.generateAccessToken(client.ClientID, userID, userClaims, scope)
	if err != nil {
		return nil, fmt.Errorf("generating access token: %w", err)
	}

	// Generate refresh token
	refreshTokenValue, err := generateSecureToken(tokenByteLength)
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

// generateAccessToken creates a JWT access token with user claims.
// If no signing key is configured, falls back to an opaque token (not recommended).
func (s *Server) generateAccessToken(clientID, userID string, userClaims map[string]any, scope string) (string, error) {
	// If no signing key configured, fall back to opaque token
	if len(s.config.SigningKey) == 0 {
		return generateSecureToken(tokenByteLength)
	}

	now := time.Now()
	exp := now.Add(s.config.AccessTokenTTL)

	// Build JWT claims
	claims := jwt.MapClaims{
		"iss":   s.config.Issuer,
		"sub":   userID,
		"aud":   clientID,
		"exp":   exp.Unix(),
		"iat":   now.Unix(),
		"nbf":   now.Unix(),
		"scope": scope,
	}

	// Include upstream IdP claims (roles, email, etc.) under a nested key
	// to preserve the full user context for authorization
	if len(userClaims) > 0 {
		claims["claims"] = userClaims
	}

	// Sign the token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString(s.config.SigningKey)
	if err != nil {
		return "", fmt.Errorf("signing token: %w", err)
	}

	return signedToken, nil
}

// SigningKey returns the OAuth server's signing key.
// This is needed by the OAuth JWT authenticator to validate tokens.
func (s *Server) SigningKey() []byte {
	return s.config.SigningKey
}

// Issuer returns the OAuth server's issuer URL.
func (s *Server) Issuer() string {
	return s.config.Issuer
}

// RegisterClient handles Dynamic Client Registration.
func (s *Server) RegisterClient(ctx context.Context, req DCRRequest) (*DCRResponse, error) {
	if s.dcr == nil {
		return nil, fmt.Errorf("dynamic client registration is disabled")
	}
	return s.dcr.Register(ctx, req)
}

// ServeHTTP implements http.Handler for the OAuth server.
// Handles both standard paths (with /oauth prefix) and Claude Desktop compatible paths (without prefix).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/oauth/authorize", "/authorize":
		s.handleAuthorizeEndpoint(w, r)
	case "/oauth/callback", "/callback":
		s.handleCallbackEndpoint(w, r)
	case "/oauth/token", "/token":
		s.handleTokenEndpoint(w, r)
	case "/oauth/register", "/register":
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
		s.writeError(w, http.StatusMethodNotAllowed, errMethodNotAllowed, "POST required")
		return
	}

	if err := r.ParseForm(); err != nil {
		s.writeError(w, http.StatusBadRequest, errInvalidRequest, "could not parse form")
		return
	}

	req := TokenRequest{
		GrantType:    r.FormValue("grant_type"),
		Code:         r.FormValue(paramCode),
		RedirectURI:  r.FormValue(paramRedirectURI),
		ClientID:     r.FormValue(paramClientID),
		ClientSecret: r.FormValue("client_secret"),
		CodeVerifier: r.FormValue("code_verifier"),
		RefreshToken: r.FormValue("refresh_token"),
		Scope:        r.FormValue(paramScope),
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
		s.writeError(w, http.StatusBadRequest, errInvalidRequest, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, resp)
}

// handleRegisterEndpoint handles POST /oauth/register.
func (s *Server) handleRegisterEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, errMethodNotAllowed, "POST required")
		return
	}

	var req DCRRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, errInvalidRequest, "could not parse JSON")
		return
	}

	resp, err := s.RegisterClient(r.Context(), req)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, errInvalidRequest, err.Error())
		return
	}

	s.writeJSON(w, http.StatusCreated, resp)
}

// handleAuthorizeEndpoint handles GET /oauth/authorize.
// It validates the client request and redirects to the upstream IdP for authentication.
func (s *Server) handleAuthorizeEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, errMethodNotAllowed, "GET required")
		return
	}

	req := AuthorizationRequest{
		ResponseType:        r.URL.Query().Get("response_type"),
		ClientID:            r.URL.Query().Get(paramClientID),
		RedirectURI:         r.URL.Query().Get(paramRedirectURI),
		Scope:               r.URL.Query().Get(paramScope),
		State:               r.URL.Query().Get(paramState),
		CodeChallenge:       r.URL.Query().Get("code_challenge"),
		CodeChallengeMethod: r.URL.Query().Get("code_challenge_method"),
	}

	// Validate client request
	client, err := s.validateAuthorizationRequest(r.Context(), req)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, errInvalidRequest, err.Error())
		return
	}

	// Validate PKCE if required
	if err := s.validatePKCE(client, req); err != nil {
		s.writeError(w, http.StatusBadRequest, errInvalidRequest, err.Error())
		return
	}

	// Check if upstream IdP is configured
	if s.config.Upstream == nil {
		s.writeError(w, http.StatusInternalServerError, errServerError, "upstream IdP not configured")
		return
	}

	// Generate state for upstream IdP
	upstreamState, err := generateSecureToken(stateByteLength)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, errServerError, "failed to generate state")
		return
	}

	// Save authorization state to link callback to original request
	authState := &AuthorizationState{
		ClientID:            req.ClientID,
		RedirectURI:         req.RedirectURI,
		State:               req.State,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: req.CodeChallengeMethod,
		Scope:               req.Scope,
		UpstreamState:       upstreamState,
		CreatedAt:           time.Now(),
	}
	if err := s.stateStore.Save(upstreamState, authState); err != nil {
		s.writeError(w, http.StatusInternalServerError, errServerError, "failed to save state")
		return
	}

	// Build upstream IdP authorization URL
	upstreamURL := s.buildUpstreamAuthURL(upstreamState)
	http.Redirect(w, r, upstreamURL, http.StatusFound)
}

// handleLoginRequiredError handles the OIDC login_required error from prompt=none.
// Per OpenID Connect Core Section 3.1.2.6, when prompt=none is sent and the user
// has no active session, the IdP returns error=login_required. This method retries
// the authorization without prompt=none so the IdP shows its login form.
// Returns true if the error was handled (retry redirect issued), false otherwise.
func (s *Server) handleLoginRequiredError(w http.ResponseWriter, r *http.Request) bool {
	errParam := r.URL.Query().Get("error")
	if errParam != "login_required" {
		return false
	}

	stateParam := r.URL.Query().Get(paramState)
	if stateParam == "" {
		return false
	}

	authState, err := s.stateStore.Get(stateParam)
	if err != nil {
		return false
	}

	// Prevent infinite loop: only retry once
	if authState.PromptNoneAttempted {
		return false
	}

	// Mark that we tried prompt=none and re-save the state
	authState.PromptNoneAttempted = true
	_ = s.stateStore.Save(stateParam, authState)

	// Redirect to upstream IdP without prompt=none
	upstreamURL := s.buildUpstreamAuthURLWithPrompt(stateParam, false)
	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect -- URL built from server OIDC config, not user input
	http.Redirect(w, r, upstreamURL, http.StatusFound)
	return true
}

// handleCallbackEndpoint handles GET /oauth/callback.
// It receives the callback from the upstream IdP and exchanges the code for tokens.
func (s *Server) handleCallbackEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, errMethodNotAllowed, "GET required")
		return
	}

	// Check for login_required error from prompt=none (silent SSO fallback)
	if s.handleLoginRequiredError(w, r) {
		return
	}

	// Check for error from upstream IdP
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		errDesc := r.URL.Query().Get("error_description")
		s.writeError(w, http.StatusBadRequest, errParam, errDesc)
		return
	}

	upstreamCode := r.URL.Query().Get(paramCode)
	upstreamState := r.URL.Query().Get(paramState)

	if upstreamCode == "" || upstreamState == "" {
		s.writeError(w, http.StatusBadRequest, errInvalidRequest, "missing code or state")
		return
	}

	// Retrieve original authorization state
	authState, err := s.stateStore.Get(upstreamState)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_state", "authorization state not found")
		return
	}

	// Delete state to prevent replay
	_ = s.stateStore.Delete(upstreamState)

	// Exchange code with upstream IdP
	upstreamToken, err := s.exchangeUpstreamCode(r.Context(), upstreamCode)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "token_exchange_failed", err.Error())
		return
	}

	// Extract user info from upstream token
	userID, userClaims := s.extractUserFromUpstreamToken(upstreamToken)

	// Generate MCP authorization code for the original client
	mcpCode, err := s.Authorize(r.Context(), AuthorizationRequest{
		ResponseType:        paramCode,
		ClientID:            authState.ClientID,
		RedirectURI:         authState.RedirectURI,
		Scope:               authState.Scope,
		CodeChallenge:       authState.CodeChallenge,
		CodeChallengeMethod: authState.CodeChallengeMethod,
	}, userID, userClaims)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, errServerError, err.Error())
		return
	}

	// Redirect back to the original client with the MCP authorization code
	redirectURL := s.buildClientRedirectURL(authState.RedirectURI, mcpCode, authState.State)
	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect -- redirect URI validated during client registration
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// buildUpstreamAuthURL builds the authorization URL for the upstream IdP.
// Uses prompt=none by default to attempt silent SSO per OpenID Connect Core
// Section 3.1.2.1. If the user has an active IdP session, the IdP silently
// issues an authorization code. If not, the IdP returns error=login_required,
// which handleLoginRequiredError catches and retries without prompt=none.
func (s *Server) buildUpstreamAuthURL(state string) string {
	return s.buildUpstreamAuthURLWithPrompt(state, true)
}

// buildUpstreamAuthURLWithPrompt builds the upstream IdP authorization URL.
// When usePromptNone is true, adds prompt=none for silent SSO.
// When false, omits prompt so the IdP shows its login form.
func (s *Server) buildUpstreamAuthURLWithPrompt(state string, usePromptNone bool) string {
	params := url.Values{}
	params.Set("response_type", paramCode)
	params.Set(paramClientID, s.config.Upstream.ClientID)
	params.Set(paramRedirectURI, s.config.Upstream.RedirectURI)
	params.Set(paramState, state)
	params.Set(paramScope, "openid email profile")
	if usePromptNone {
		params.Set("prompt", "none")
	}

	// Construct the authorization URL
	authURL := strings.TrimSuffix(s.config.Upstream.Issuer, "/") + "/protocol/openid-connect/auth"
	return authURL + "?" + params.Encode()
}

// upstreamTokenResponse represents the token response from the upstream IdP.
type upstreamTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
}

// exchangeUpstreamCode exchanges an authorization code with the upstream IdP.
func (s *Server) exchangeUpstreamCode(ctx context.Context, code string) (*upstreamTokenResponse, error) {
	tokenURL := strings.TrimSuffix(s.config.Upstream.Issuer, "/") + "/protocol/openid-connect/token"

	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set(paramCode, code)
	data.Set(paramRedirectURI, s.config.Upstream.RedirectURI)
	data.Set(paramClientID, s.config.Upstream.ClientID)
	data.Set("client_secret", s.config.Upstream.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token exchange failed: %s", string(body))
	}

	var tokenResp upstreamTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}

	return &tokenResp, nil
}

// extractUserFromUpstreamToken extracts user information from the upstream token.
func (*Server) extractUserFromUpstreamToken(token *upstreamTokenResponse) (string, map[string]any) { //nolint:gocritic // unnamedResult: names would shadow local variables
	claims := extractTokenClaims(token)

	userID := "unknown"
	if sub, ok := claims["sub"].(string); ok {
		userID = sub
	}

	return userID, claims
}

// extractTokenClaims merges claims from ID and access tokens.
func extractTokenClaims(token *upstreamTokenResponse) map[string]any {
	claims := make(map[string]any)

	if token.IDToken != "" {
		if idClaims := decodeJWTClaims(token.IDToken); idClaims != nil {
			claims = idClaims
		}
	}

	if token.AccessToken == "" {
		return claims
	}

	accessClaims := decodeJWTClaims(token.AccessToken)
	if accessClaims == nil {
		return claims
	}

	mergeAccessClaims(claims, accessClaims)
	return claims
}

// mergeAccessClaims merges access token claims into existing claims.
// Role-related claims (realm_access, resource_access) always overwrite;
// other claims only fill gaps.
func mergeAccessClaims(claims, accessClaims map[string]any) {
	for key, value := range accessClaims {
		if key == "realm_access" || key == "resource_access" {
			claims[key] = value
		} else if _, exists := claims[key]; !exists {
			claims[key] = value
		}
	}
}

// decodeJWTClaims decodes the claims from a JWT without verification.
// This is safe because we received the token directly from the trusted upstream IdP.
func decodeJWTClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != jwtPartCount {
		return nil
	}

	// Decode the payload (second part)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}

	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}

	return claims
}

// buildClientRedirectURL builds the redirect URL back to the client.
func (*Server) buildClientRedirectURL(redirectURI, code, state string) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}

	q := u.Query()
	q.Set(paramCode, code)
	if state != "" {
		q.Set(paramState, state)
	}
	u.RawQuery = q.Encode()

	return u.String()
}

// handleMetadata handles GET /.well-known/oauth-authorization-server.
// Returns metadata with paths without /oauth prefix for Claude Desktop compatibility.
func (s *Server) handleMetadata(w http.ResponseWriter, _ *http.Request) {
	metadata := map[string]any{
		"issuer":                                s.config.Issuer,
		"authorization_endpoint":                s.config.Issuer + "/authorize",
		"token_endpoint":                        s.config.Issuer + "/token",
		"registration_endpoint":                 s.config.Issuer + "/register",
		"response_types_supported":              []string{paramCode},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256", "plain"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
	}

	s.writeJSON(w, http.StatusOK, metadata)
}

// writeJSON writes a JSON response.
func (*Server) writeJSON(w http.ResponseWriter, status int, data any) {
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
// Uses paths without /oauth prefix for Claude Desktop compatibility.
func BuildAuthorizationURL(baseURL, clientID, redirectURI, scope, state string) string {
	// Generate PKCE
	verifier := make([]byte, tokenByteLength)
	_, _ = rand.Read(verifier)
	codeVerifier := base64.RawURLEncoding.EncodeToString(verifier)
	challenge, _ := GenerateCodeChallenge(codeVerifier, PKCEMethodS256)

	return fmt.Sprintf(
		"%s/authorize?response_type=code&client_id=%s&redirect_uri=%s&scope=%s&state=%s&code_challenge=%s&code_challenge_method=S256",
		strings.TrimSuffix(baseURL, "/"),
		clientID,
		redirectURI,
		scope,
		state,
		challenge,
	)
}
