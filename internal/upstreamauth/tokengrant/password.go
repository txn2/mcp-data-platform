package tokengrant

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/oauth2"
)

// Password is what the RFC 6749 section 4.3 resource owner password grant
// posts: the client and the API user (#2689). Any of the four credentials
// may name a stored secret; Fill reads each for the request it is sent on.
type Password struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	Username     string
	Password     string
	Scopes       []string
	// InParams sends the client credentials as form parameters rather than
	// HTTP Basic. With no client secret they are always parameters, which
	// omit what is empty: the header style would send "Basic Og==".
	InParams bool
	// Fill returns a value as it is sent: a stored-secret placeholder read,
	// anything else unchanged.
	Fill func(ctx context.Context, value string) (string, error)
}

// PasswordSource is the oauth2.TokenSource behind a password grant
// connection's token cache. It holds the refresh token the last response
// carried, in memory only, and renews with it before falling back to the
// password; nothing it holds is stored.
type PasswordSource struct {
	p   Password
	ctx context.Context

	mu      sync.Mutex
	refresh string
	user    string
	scopes  []string
	// sent are the password and client secret as the last request sent
	// them, a stored secret filled, so an upstream repeating one in its
	// error is redacted whichever way the connection names it.
	sent []string
}

// NewPasswordSource returns the source. ctx carries the token-request client
// (oauth2.HTTPClient); oauth2.TokenSource has no per-call context.
func NewPasswordSource(ctx context.Context, p Password) *PasswordSource {
	return &PasswordSource{p: p, ctx: ctx}
}

// Token renews the access token: with the held refresh token when there is
// one, and with the password when there is none or the upstream refuses it.
// A refresh that fails for any other reason (network, a 5xx) is returned and
// the refresh token kept, since the password would not reach the upstream
// either. The token returned carries no refresh token.
func (s *PasswordSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conf, err := s.client()
	if err != nil {
		return nil, err
	}
	if s.refresh != "" {
		tok, err := conf.TokenSource(s.ctx, &oauth2.Token{RefreshToken: s.refresh}).Token()
		if err == nil {
			return s.hold(tok, true), nil
		}
		if !refreshRefused(err) {
			return nil, err //nolint:wrapcheck // the authenticator classifies and scrubs the library's error
		}
		s.refresh = ""
	}
	username, err := s.p.Fill(s.ctx, s.p.Username)
	if err != nil {
		return nil, err
	}
	password, err := s.p.Fill(s.ctx, s.p.Password)
	if err != nil {
		return nil, err
	}
	s.sent = []string{password, conf.ClientSecret}
	tok, err := conf.PasswordCredentialsToken(s.ctx, username, password)
	if err != nil {
		return nil, err //nolint:wrapcheck // the authenticator classifies and scrubs the library's error
	}
	s.user, s.scopes = username, s.p.Scopes
	if granted, ok := tok.Extra("scope").(string); ok && strings.TrimSpace(granted) != "" {
		s.scopes = strings.Fields(granted)
	}
	return s.hold(tok, false), nil
}

// Redact removes from text the password and client secret the last request
// sent.
func (s *PasswordSource) Redact(text string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return WithoutSecrets(text, s.sent...)
}

// Granted reports the user the endpoint last issued a token for and the
// scopes it carries, and false before the first.
func (s *PasswordSource) Granted() (user string, scopes []string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.user, s.scopes, s.user != ""
}

// hold keeps the refresh token a response carried and returns the token
// without it. A refresh response with no refresh token leaves the one it was
// made with in place (RFC 6749 section 6); a password response with none
// leaves nothing, and the next renewal asks with the password.
func (s *PasswordSource) hold(tok *oauth2.Token, refreshed bool) *oauth2.Token {
	switch {
	case tok.RefreshToken != "":
		s.refresh = tok.RefreshToken
	case !refreshed:
		s.refresh = ""
	}
	out := *tok
	out.RefreshToken = ""
	return &out
}

// client is the OAuth client the requests are made as, with a client id or
// secret that names a stored secret read for this request.
func (s *PasswordSource) client() (*oauth2.Config, error) {
	id, err := s.p.Fill(s.ctx, s.p.ClientID)
	if err != nil {
		return nil, err
	}
	secret, err := s.p.Fill(s.ctx, s.p.ClientSecret)
	if err != nil {
		return nil, err
	}
	style := oauth2.AuthStyleInHeader
	if secret == "" || s.p.InParams {
		style = oauth2.AuthStyleInParams
	}
	return &oauth2.Config{
		ClientID:     id,
		ClientSecret: secret,
		Endpoint:     oauth2.Endpoint{TokenURL: s.p.TokenURL, AuthStyle: style},
		Scopes:       s.p.Scopes,
	}, nil
}

// refreshRefused reports whether a refresh failed because the upstream
// answered and would not honor the refresh token: any 4xx, since legacy
// servers answer an expired or revoked one with 400 invalid_grant, 401, or a
// bare 400 with no error code.
func refreshRefused(err error) bool {
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) || re.Response == nil {
		return false
	}
	return re.Response.StatusCode >= http.StatusBadRequest && re.Response.StatusCode < http.StatusInternalServerError
}
