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

// RefreshInput collects the parameters of a refresh_token grant.
// Mirrors ExchangeInput so the two paths look the same to callers
// and so security guards (timeout, redirect-refusal, body cap)
// cannot drift.
type RefreshInput struct {
	// Config is the per-connection IdP settings. Required.
	Config Config
	// RefreshToken is the long-lived refresh token issued by the
	// IdP at exchange or the most recent successful refresh.
	// Required (the refresh-token grant has no other auth path).
	RefreshToken string
}

// RefreshResult is the parsed response from a refresh_token grant.
// Shape matches ExchangeResult so persistRefreshed can write the
// rotated set back the same way it persists the initial exchange.
type RefreshResult struct {
	AccessToken      string
	RefreshToken     string
	ExpiresAt        time.Time
	RefreshExpiresAt time.Time
	Scope            string
}

// Refresh performs the refresh_token to tokens POST against the
// IdP's token endpoint. Critical and non-obvious: this function
// exists ONLY because golang.org/x/oauth2's refresh path wraps
// client_id and client_secret in url.QueryEscape() before Basic
// auth (internal/token.go:202). RFC 6749 §2.3.1 specifies that
// encoding, and the library follows it strictly. Many production
// IdPs do not. Salesforce, GitHub, and similar token endpoints
// store the raw secret and compare against the raw secret in the
// inbound Basic auth header; a URL-encoded secret in the header
// looks like a literally different password and the IdP returns
// invalid_client.
//
// Symptom this fixes: a client_secret containing `+`, `/`, `=`
// mid-string, ` `, or any other URL-significant character has
// the secret value silently rewritten on every refresh. Exchange
// works (our own exchange code uses plain SetBasicAuth, no
// encoding) but the very first refresh after Connect fails with
// invalid_client. The connection then dies on a one-hour cycle
// because each new Connect lasts until its first refresh tick.
//
// This implementation mirrors the Exchange path: plain
// SetBasicAuth (no URL-encoding) when EndpointAuthStyle is
// AuthStyleInHeader, params-in-body when AuthStyleInParams. The
// body-encoding path is unaffected by the URL-encoding bug
// because url.Values.Encode() is standard form encoding (which
// the IdP URL-decodes back to the raw secret server-side).
func Refresh(ctx context.Context, in RefreshInput) (*RefreshResult, error) {
	if err := validateRefreshInput(in); err != nil {
		return nil, err
	}
	req, err := buildRefreshRequest(ctx, in)
	if err != nil {
		return nil, err
	}
	client, err := newTokenExchangeClient(in.Config)
	if err != nil {
		return nil, err
	}
	body, err := postRefresh(client, req, in.Config.TokenURL)
	if err != nil {
		return nil, err
	}
	return decodeRefreshResponse(body)
}

// validateRefreshInput rejects malformed RefreshInput before any
// network call.
func validateRefreshInput(in RefreshInput) error {
	if in.Config.TokenURL == "" {
		return errors.New("connoauth: refresh: token_url is required")
	}
	if in.Config.ClientID == "" {
		return errors.New("connoauth: refresh: client_id is required")
	}
	if in.RefreshToken == "" {
		return errors.New("connoauth: refresh: refresh_token is required")
	}
	return nil
}

// buildRefreshRequest assembles the refresh-bearing POST. Mirrors
// buildExchangeRequest but for grant_type=refresh_token. The
// critical difference from golang.org/x/oauth2's refresh path is
// the plain http.Request.SetBasicAuth call (no URL-encoding of
// the credentials).
func buildRefreshRequest(ctx context.Context, in RefreshInput) (*http.Request, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", in.RefreshToken)
	form.Set("client_id", in.Config.ClientID)
	if in.Config.EndpointAuthStyle == oauth2.AuthStyleInParams {
		form.Set("client_secret", in.Config.ClientSecret)
	}

	// #nosec G107 G704 -- TokenURL is operator-authored connection config
	// (admin endpoint, validated by per-kind ParseConfig before reaching
	// here). Same sink shape as the exchange path.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, in.Config.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("connoauth: refresh: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if in.Config.EndpointAuthStyle != oauth2.AuthStyleInParams {
		// Plain SetBasicAuth: no URL-encoding of the credentials.
		// See the Refresh godoc for the full rationale.
		req.SetBasicAuth(in.Config.ClientID, in.Config.ClientSecret)
	}
	return req, nil
}

// postRefresh performs the POST, drains and caps the body, and
// translates non-2xx into a structured *RetrieveError that
// classifyRefreshError can detect. Mirrors postExchange but with
// the additional responsibility of returning an
// oauth2.RetrieveError-shaped error on non-200 so the existing
// classify/sanitize pipeline downstream continues to work
// unchanged.
func postRefresh(client *http.Client, req *http.Request, tokenURL string) ([]byte, error) {
	start := time.Now()
	tokenHost := urlHost(tokenURL)
	// #nosec G107 G704 -- request URL is the operator-authored OAuth token
	// endpoint from a validated connection config; client is the locally
	// constructed newTokenExchangeClient with CheckRedirect refusing 3xx
	// and a hard timeout.
	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("connoauth: refresh: transport error",
			logKeyTokenURLHost, tokenHost, "error", err)
		return nil, fmt.Errorf("connoauth: refresh: token request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("connoauth: refresh: read body: %w", err)
	}
	if int64(len(body)) > maxTokenResponseBytes {
		slog.Warn("connoauth: refresh: response exceeds size cap",
			logKeyTokenURLHost, tokenHost, "limit_bytes", maxTokenResponseBytes)
		return nil, fmt.Errorf("connoauth: refresh: response exceeds %d-byte cap (likely misbehaving IdP)", maxTokenResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		slog.Warn("connoauth: refresh: non-200 from token endpoint",
			logKeyTokenURLHost, tokenHost,
			"status", resp.StatusCode,
			"duration", time.Since(start),
			"body_excerpt", trimBody(body))
		return nil, retrieveErrorFromBody(resp, body)
	}
	slog.Info("connoauth: refresh: success",
		logKeyTokenURLHost, tokenHost,
		"duration", time.Since(start),
		"body_len", len(body))
	return body, nil
}

// retrieveErrorFromBody builds an *oauth2.RetrieveError so the
// downstream classifyRefreshError pipeline (which uses errors.As
// to inspect StatusCode + ErrorCode + ErrorDescription) keeps
// working without modification. The classify pipeline pre-dates
// this file by several releases and is depended on by
// handleRevoked, idpErrorCodeOf, and the auth-event detail
// emitters.
func retrieveErrorFromBody(resp *http.Response, body []byte) error {
	var raw struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &raw) // best-effort; empty fields are fine
	return &oauth2.RetrieveError{
		Response:         resp,
		Body:             body,
		ErrorCode:        raw.Error,
		ErrorDescription: raw.ErrorDescription,
	}
}

// decodeRefreshResponse parses the IdP's token-endpoint JSON.
// Mirrors decodeExchangeResponse with the access_token-required
// invariant. refresh_token is OPTIONAL: per RFC 6749 §6, the IdP
// may omit it ("the prior one is still valid"); the caller's
// persistRefreshed preserves the prior token in that case.
func decodeRefreshResponse(body []byte) (*RefreshResult, error) {
	var raw struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		TokenType        string `json:"token_type"`
		ExpiresIn        int64  `json:"expires_in"`
		RefreshExpiresIn int64  `json:"refresh_expires_in"`
		Scope            string `json:"scope"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, errors.New("connoauth: refresh: malformed JSON response")
	}
	if raw.Error != "" {
		// Mirrors decodeExchangeResponse: a 200 with an error field
		// is rare but not unheard of for misbehaving IdPs.
		return nil, fmt.Errorf("connoauth: refresh: idp returned error: %s (%s)",
			sanitizeOAuthErrorField(raw.Error), sanitizeOAuthErrorField(raw.ErrorDescription))
	}
	if raw.AccessToken == "" {
		return nil, errors.New("connoauth: refresh: response missing access_token")
	}
	res := &RefreshResult{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		Scope:        raw.Scope,
	}
	if raw.ExpiresIn > 0 {
		res.ExpiresAt = time.Now().Add(time.Duration(raw.ExpiresIn) * time.Second)
	}
	if raw.RefreshExpiresIn > 0 {
		res.RefreshExpiresAt = time.Now().Add(time.Duration(raw.RefreshExpiresIn) * time.Second)
	}
	return res, nil
}
