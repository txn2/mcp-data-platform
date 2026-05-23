package connoauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// maxTokenResponseBytes caps the response body read from a token
// endpoint. Real responses are KB at most; a 1 MiB ceiling prevents
// an OOM if a misbehaving (or malicious) IdP streams indefinitely.
const maxTokenResponseBytes = 1 << 20

// logKeyTokenURLHost is the structured-log field name used for the
// IdP token endpoint host across exchange / refresh / status log
// lines. Consolidated as a constant so SIEM dashboards and grep
// queries align across the unified flow.
// #nosec G101 -- field NAME for structured logging, not a credential value
const logKeyTokenURLHost = "token_url_host"

// ExchangeInput collects the parameters of an authorization_code
// token exchange. The admin callback handler builds this from the
// PKCE state + code + per-kind connection config and hands it to
// Exchange().
type ExchangeInput struct {
	// Config is the per-connection IdP settings. Required.
	Config Config
	// Code is the IdP-issued authorization code from the callback's
	// `code` query parameter.
	Code string
	// CodeVerifier is the PKCE verifier the admin handler stored at
	// oauth-start time. Required by RFC 7636 §4.5.
	CodeVerifier string
	// RedirectURI must exactly match the redirect_uri used at
	// oauth-start AND registered with the IdP. Required by RFC 6749
	// §4.1.3.
	RedirectURI string
}

// ExchangeResult is the parsed response from an authorization_code
// exchange. Mirrors the token-endpoint response shape with the
// addition of RefreshExpiresAt (Keycloak's refresh_expires_in
// resolved to an absolute deadline).
type ExchangeResult struct {
	AccessToken      string
	RefreshToken     string
	ExpiresAt        time.Time
	RefreshExpiresAt time.Time
	Scope            string
	// IDToken is the OIDC ID token if the IdP issued one (Keycloak,
	// Okta, Auth0 typically do when `openid` is in scope). Reserved
	// for future use by the admin callback (e.g., extracting the
	// AuthenticatedBy claim); the current callers use the started_by
	// captured at oauth-start.
	IDToken string
}

// Exchange performs the authorization_code → tokens POST against the
// IdP's token endpoint. Replaces the two duplicate
// exchangeAuthorizationCode / exchangeAPIGatewayCode functions from
// the per-kind handlers — both old kinds funnel through this code
// now, so security guards (timeout, redirect-refusal, body cap)
// cannot drift between kinds.
func Exchange(ctx context.Context, in ExchangeInput) (*ExchangeResult, error) {
	if err := validateExchangeInput(in); err != nil {
		return nil, err
	}
	req, err := buildExchangeRequest(ctx, in)
	if err != nil {
		return nil, err
	}
	client, err := newTokenExchangeClient(in.Config)
	if err != nil {
		return nil, err
	}
	body, err := postExchange(client, req, in.Config.TokenURL)
	if err != nil {
		return nil, err
	}
	return decodeExchangeResponse(body)
}

// validateExchangeInput rejects malformed ExchangeInput before any
// network call. Keeps Exchange's hot path focused on the happy case
// and surfaces validation errors with a consistent prefix for
// callers to grep.
func validateExchangeInput(in ExchangeInput) error {
	if in.Config.TokenURL == "" {
		return errors.New("connoauth: exchange: token_url is required")
	}
	if in.Config.ClientID == "" {
		return errors.New("connoauth: exchange: client_id is required")
	}
	if in.Code == "" {
		return errors.New("connoauth: exchange: authorization code is required")
	}
	if in.CodeVerifier == "" {
		return errors.New("connoauth: exchange: PKCE code_verifier is required")
	}
	if in.RedirectURI == "" {
		return errors.New("connoauth: exchange: redirect_uri is required")
	}
	return nil
}

// buildExchangeRequest assembles the credential-bearing POST. When
// the configured auth style is AuthStyleInHeader (the OAuth 2.1
// default), client_id and client_secret travel via HTTP Basic; when
// AuthStyleInParams, they go in the form body. Some legacy IdPs
// require the params form; the per-connection config selects.
func buildExchangeRequest(ctx context.Context, in ExchangeInput) (*http.Request, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", in.Code)
	form.Set("redirect_uri", in.RedirectURI)
	form.Set("code_verifier", in.CodeVerifier)
	form.Set("client_id", in.Config.ClientID)
	if in.Config.EndpointAuthStyle == oauth2.AuthStyleInParams {
		form.Set("client_secret", in.Config.ClientSecret)
	}

	// #nosec G107 G704 -- TokenURL is operator-authored connection config
	// (admin endpoint, validated by per-kind ParseConfig before reaching
	// here). Same sink shape as the per-kind handlers this replaces.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, in.Config.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("connoauth: exchange: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if in.Config.EndpointAuthStyle != oauth2.AuthStyleInParams {
		req.SetBasicAuth(in.Config.ClientID, in.Config.ClientSecret)
	}
	return req, nil
}

// postExchange performs the POST, drains and caps the body, and
// translates non-2xx into a structured error. Returns the raw body
// (capped) on success so decodeExchangeResponse can JSON-decode it.
func postExchange(client *http.Client, req *http.Request, tokenURL string) ([]byte, error) {
	start := time.Now()
	tokenHost := urlHost(tokenURL)
	// #nosec G107 G704 -- request URL is the operator-authored OAuth token
	// endpoint from a validated connection config; client is the locally
	// constructed newTokenExchangeClient with CheckRedirect refusing 3xx
	// and a hard timeout.
	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("connoauth: exchange: transport error",
			logKeyTokenURLHost, tokenHost, "error", err)
		return nil, fmt.Errorf("connoauth: exchange: token request: %w", err)
	}
	// Drain remaining bytes before close so net/http can pool the
	// underlying TCP connection. With LimitReader capping the read,
	// an oversize body would otherwise leave bytes on the wire and
	// drop the connection (every exchange would then re-handshake).
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("connoauth: exchange: read body: %w", err)
	}
	if int64(len(body)) > maxTokenResponseBytes {
		slog.Warn("connoauth: exchange: response exceeds size cap",
			logKeyTokenURLHost, tokenHost, "limit_bytes", maxTokenResponseBytes)
		return nil, fmt.Errorf("connoauth: exchange: response exceeds %d-byte cap (likely misbehaving IdP)", maxTokenResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		slog.Warn("connoauth: exchange: non-200 from token endpoint",
			logKeyTokenURLHost, tokenHost,
			"status", resp.StatusCode,
			"duration", time.Since(start),
			"body_excerpt", trimBody(body))
		return nil, fmt.Errorf("connoauth: exchange: token endpoint returned %d: %s", resp.StatusCode, trimBody(body))
	}
	slog.Info("connoauth: exchange: success",
		logKeyTokenURLHost, tokenHost,
		"duration", time.Since(start),
		"body_len", len(body))
	return body, nil
}

// decodeExchangeResponse parses the IdP's token-endpoint JSON. The
// minimum required field is access_token; refresh_token,
// expires_in, refresh_expires_in, scope, and id_token are all
// optional.
func decodeExchangeResponse(body []byte) (*ExchangeResult, error) {
	var raw struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		TokenType        string `json:"token_type"`
		ExpiresIn        int64  `json:"expires_in"`
		RefreshExpiresIn int64  `json:"refresh_expires_in"`
		Scope            string `json:"scope"`
		IDToken          string `json:"id_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, errors.New("connoauth: exchange: malformed JSON response")
	}
	if raw.Error != "" {
		return nil, fmt.Errorf("connoauth: exchange: upstream error %s: %s", raw.Error, raw.ErrorDescription)
	}
	if raw.AccessToken == "" {
		return nil, errors.New("connoauth: exchange: token response missing access_token")
	}
	now := time.Now()
	result := &ExchangeResult{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		Scope:        raw.Scope,
		IDToken:      raw.IDToken,
	}
	if raw.ExpiresIn > 0 {
		result.ExpiresAt = now.Add(time.Duration(raw.ExpiresIn) * time.Second)
	}
	if raw.RefreshExpiresIn > 0 {
		result.RefreshExpiresAt = now.Add(time.Duration(raw.RefreshExpiresIn) * time.Second)
	}
	return result, nil
}

// urlHost returns the host portion of u for logs. Falls back to the
// raw value when parsing fails so logs are never empty. Internal —
// the admin handler uses its own URLHost helper for cross-package
// log-field consistency.
func urlHost(u string) string {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return u
	}
	return parsed.Host
}

// trimBody caps the size of upstream error bodies surfaced in error
// strings so a misbehaving upstream can't blow up an audit log.
func trimBody(body []byte) string {
	const limit = 256
	if len(body) <= limit {
		return string(body)
	}
	return string(body[:limit]) + "..."
}
