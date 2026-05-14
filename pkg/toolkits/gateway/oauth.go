package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// tokenExchangeTimeout caps any single token-endpoint POST. The
// underlying golang.org/x/oauth2 library defaults to http.DefaultClient
// (no timeout) unless the caller injects oauth2.HTTPClient on ctx; an
// unreachable IdP would otherwise hang every Token() call until the OS
// dial timeout fires. 30s matches connoauth.tokenFetchTimeout.
const tokenExchangeTimeout = 30 * time.Second

// newTokenExchangeClient is the http.Client used for credential-bearing
// POSTs to the token endpoint. CheckRedirect refuses 3xx so a
// misconfigured (or compromised) IdP cannot redirect this form body —
// which carries client_secret — to an attacker URL.
func newTokenExchangeClient() *http.Client {
	return &http.Client{
		Timeout: tokenExchangeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// tokenProvider yields a fresh access token for an outbound upstream
// request. Two implementations:
//
//   - clientCredentialsTokenProvider wraps a
//     golang.org/x/oauth2/clientcredentials.Config.TokenSource (cached
//     in-memory via oauth2.ReuseTokenSource). Server-to-server grant;
//     no DB row required.
//   - connoauthTokenProvider builds an ephemeral connoauth.Source per
//     call from the toolkit's current connoauth.Store + audit-event
//     writer. Authorization-code grant; the row in
//     connection_oauth_tokens is the single source of truth.
//
// Decoupling the round-tripper from the concrete OAuth type lets the
// toolkit choose the right strategy at dial time without growing the
// round-tripper's interface every time a new grant is added.
type tokenProvider interface {
	Token(ctx context.Context) (string, error)
}

// clientCredentialsTokenProvider mints (and silently refreshes) an
// access token via the OAuth 2.1 client_credentials grant. The
// in-memory cache is intentional asymmetry with the authorization_code
// path: that path persists rotated refresh tokens to the unified
// store (must not cache across calls because the background refresher
// may rotate underneath); client_credentials carries no rotation
// contract — the same client_id + client_secret always produces an
// equivalent fresh token on demand — so caching the access token
// in-process is correct and reduces IdP load.
//
// The mutex + manual cache pattern (instead of oauth2.ReuseTokenSource)
// is what lets every Token call carry the caller's ctx through to the
// token-endpoint POST: ReuseTokenSource bakes ctx in at construction
// time, which would let a canceled tool call run to the 30s http
// timeout. Here a cancel cancels promptly because the per-call
// TokenSource captures the per-call ctx.
type clientCredentialsTokenProvider struct {
	cc         *clientcredentials.Config
	httpClient *http.Client

	mu     sync.Mutex
	cached *oauth2.Token
}

func newClientCredentialsTokenProvider(cfg OAuthConfig) *clientCredentialsTokenProvider {
	cc := &clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     cfg.TokenURL,
		AuthStyle:    oauth2AuthStyle(cfg.EndpointAuthStyle),
	}
	if cfg.Scope != "" {
		cc.Scopes = splitScopeString(cfg.Scope)
	}
	return &clientCredentialsTokenProvider{
		cc:         cc,
		httpClient: newTokenExchangeClient(),
	}
}

// Token returns the cached access token when still valid; otherwise
// fetches a fresh one via the IdP token endpoint, using the caller's
// ctx so a cancel/deadline propagates to the POST. Goroutine-safe;
// the mutex serializes concurrent first-call fetches so a burst of
// outbound requests produces one IdP roundtrip, not N.
func (p *clientCredentialsTokenProvider) Token(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cached != nil && p.cached.Valid() {
		return p.cached.AccessToken, nil
	}
	return p.fetchLocked(ctx)
}

// Reacquire drops the cached token and forces a fresh fetch. Used by
// the admin "Reacquire" button so an operator can verify a rotated
// client_secret without waiting for the existing token to expire.
func (p *clientCredentialsTokenProvider) Reacquire(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cached = nil
	_, err := p.fetchLocked(ctx)
	return err
}

// fetchLocked runs the IdP token exchange and stores the result in
// the cache. Caller must hold p.mu.
func (p *clientCredentialsTokenProvider) fetchLocked(ctx context.Context) (string, error) {
	bound := context.WithValue(ctx, oauth2.HTTPClient, p.httpClient)
	tok, err := p.cc.TokenSource(bound).Token()
	if err != nil {
		return "", fmt.Errorf("gateway: oauth client_credentials: %w", err)
	}
	if tok == nil || tok.AccessToken == "" {
		return "", fmt.Errorf("gateway: oauth client_credentials: empty access token")
	}
	p.cached = tok
	return tok.AccessToken, nil
}

// Status returns an OAuthStatus snapshot derived from the in-memory
// cache. Used by Toolkit.Status for cc connections — the unified
// connoauth.Store path can't serve cc because cc tokens are
// intentionally not persisted (no rotation contract, fresh fetch on
// demand is the design).
func (p *clientCredentialsTokenProvider) Status() connoauth.OAuthStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := connoauth.OAuthStatus{
		Configured: true,
		Grant:      OAuthGrantClientCredentials,
		TokenURL:   p.cc.TokenURL,
	}
	if p.cached != nil {
		st.TokenAcquired = p.cached.AccessToken != ""
		st.ExpiresAt = p.cached.Expiry
	}
	// cc never needs operator re-auth: a missing access token just
	// triggers a fresh fetch on the next outbound request. Leave
	// NeedsReauth false even when no token has been minted yet.
	return st
}

// connoauthTokenProvider builds a stateless connoauth.Source per
// Token call. The toolkit field reads happen on every Token call so a
// late SetAuthEvents / SetConnOAuthStore wires through immediately on
// the next outbound request.
type connoauthTokenProvider struct {
	tk         *Toolkit
	connection string
	cfg        OAuthConfig
}

// Token reads the toolkit's current connoauth.Store + audit-event
// writer under the toolkit's lock, then constructs a fresh
// connoauth.Source for the single Token call. The Source itself
// re-reads the persisted row, so a background refresher rotation that
// landed between our last call and this one is picked up
// automatically.
func (p connoauthTokenProvider) Token(ctx context.Context) (string, error) {
	p.tk.mu.RLock()
	store := p.tk.connOAuthStore
	events := p.tk.authEvents
	p.tk.mu.RUnlock()
	if store == nil {
		return "", fmt.Errorf("gateway: oauth connection %q has no connoauth.Store wired", p.connection)
	}
	src := connoauthSourceFor(store, events, p.connection, p.cfg)
	tok, err := src.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("gateway: oauth authorization_code: %w", err)
	}
	return tok, nil
}

// URLHost returns the host portion of a URL, falling back to the raw
// input when net/url cannot parse it. Exported because the legacy
// admin gateway_oauth_handler emits structured log lines with the
// same host-only audit field; sharing this helper keeps both call
// sites' log shape identical.
func URLHost(u string) string {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return u
	}
	return parsed.Host
}

// connoauthConfigFor maps a parsed gateway.OAuthConfig to the unified
// connoauth.Config the Source consumes. EndpointAuthStyle is
// operator-configured because some legacy IdPs accept ONLY form-body
// credentials and reject HTTP Basic on the token endpoint (and vice
// versa).
func connoauthConfigFor(cfg OAuthConfig) connoauth.Config {
	out := connoauth.Config{
		Grant:             cfg.Grant,
		AuthorizationURL:  cfg.AuthorizationURL,
		TokenURL:          cfg.TokenURL,
		ClientID:          cfg.ClientID,
		ClientSecret:      cfg.ClientSecret,
		Prompt:            cfg.Prompt,
		EndpointAuthStyle: oauth2AuthStyle(cfg.EndpointAuthStyle),
	}
	if cfg.Scope != "" {
		out.Scopes = splitScopeString(cfg.Scope)
	}
	return out
}

// oauth2AuthStyle maps the operator-facing endpoint-auth-style string
// onto the golang.org/x/oauth2 constant. Empty / unrecognized values
// resolve to AuthStyleInHeader — the OAuth 2.1 default.
func oauth2AuthStyle(s string) oauth2.AuthStyle {
	if s == OAuthAuthStyleParams {
		return oauth2.AuthStyleInParams
	}
	return oauth2.AuthStyleInHeader
}

// connoauthSourceFor builds a connoauth.Source for the named
// connection. The Source is stateless across Token() calls — it
// reads from the unified connection_oauth_tokens row on every
// outbound request — so a fresh Source per dial (or per status look-
// up) is cheap and avoids the in-memory cache divergence that the
// prior in-toolkit token source suffered from when the background
// refresher rotated the persisted refresh token underneath it.
//
// Returns nil when no connoauth.Store has been wired into the
// toolkit; the toolkit treats that as "OAuth not available" and
// surfaces the placeholder needs-reauth state to the admin UI
// rather than constructing a Source with no backing storage.
func connoauthSourceFor(store connoauth.Store, events *authevents.Writer,
	connection string, cfg OAuthConfig,
) *connoauth.Source {
	if store == nil {
		return nil
	}
	return connoauth.
		NewSource(store, connoauth.Key{Kind: connoauth.KindMCP, Name: connection}, connoauthConfigFor(cfg)).
		WithEvents(events).
		WithActor(authevents.SystemToolCall)
}
