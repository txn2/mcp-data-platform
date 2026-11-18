package upstreamauth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"golang.org/x/oauth2"

	"github.com/txn2/mcp-data-platform/internal/connstate"
	"github.com/txn2/mcp-data-platform/internal/upstreamauth/tokenexpiry"
	"github.com/txn2/mcp-data-platform/internal/upstreamauth/tokengrant"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// passwordMode names the password grant in the errors it returns.
const passwordMode = "oauth password" // #nosec G101 -- mode name, not a credential

// ErrPasswordRefused is the token endpoint refusing the grant (RFC 6749
// section 5.2, invalid_grant for a wrong password). The next call asks again.
var ErrPasswordRefused = errors.New("the upstream token endpoint refused the username and password")

// validateOAuth2Password requires a token endpoint and the API user. The client
// is optional (as for jwt_bearer), but a secret with no client id is refused.
func (c Config) validateOAuth2Password() error {
	for _, f := range [][2]string{{connoauth.ConfigKeyTokenURL, c.OAuth2.TokenURL}, {cfgKeyOAuthUsername, c.OAuth2.Username}, {cfgKeyOAuthPassword, c.OAuth2.Password}} {
		if f[1] == "" {
			return c.errf(errOAuthFieldRequired, f[0], c.oauthRequirement())
		}
	}
	if c.OAuth2.ClientSecret != "" && c.OAuth2.ClientID == "" {
		return c.errf("%s is required when %s is set and %s", connoauth.ConfigKeyClientID, connoauth.ConfigKeyClientSecret, c.oauthRequirement())
	}
	return c.validateEndpointAuthStyle()
}

// passwordAuth applies a password-grant token, cached in memory and renewed by
// tokengrant.PasswordSource. Each replica holds its own, so the refresh token
// reaches no database row. No field is ever formatted into a message.
type passwordAuth struct {
	cfg    Config
	src    oauth2.TokenSource
	source *tokengrant.PasswordSource
}

func newPasswordAuth(c Config) (*passwordAuth, error) {
	if err := c.validateOAuth2Password(); err != nil {
		return nil, err
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, newTokenExchangeClient(c))
	source := tokengrant.NewPasswordSource(ctx, tokengrant.Password{
		TokenURL: c.OAuth2.TokenURL, ClientID: c.OAuth2.ClientID, ClientSecret: c.OAuth2.ClientSecret,
		Username: c.OAuth2.Username, Password: c.OAuth2.Password, Scopes: c.OAuth2.Scopes,
		InParams: c.OAuth2.EndpointAuthStyle == OAuth2AuthStyleParams, Fill: c.fill,
	})
	src := oauth2.ReuseTokenSource(nil, tokenexpiry.Bounded(source, time.Now, c.OAuth2.UnstatedTokenLifetime))
	return &passwordAuth{cfg: c, src: src, source: source}, nil
}

// Granted reports whose token the endpoint last issued, for the connection
// test.
func (a *passwordAuth) Granted() (Grant, bool) {
	user, scopes, ok := a.source.Granted()
	return Grant{Identity: user, Scopes: scopes}, ok
}

// Apply attaches the cached access token, renewing it when needed.
func (a *passwordAuth) Apply(req *http.Request) error {
	tok, err := a.src.Token()
	if err != nil {
		return a.fetchError(err)
	}
	if tok == nil || tok.AccessToken == "" {
		return a.cfg.errf("%s: the token endpoint returned no access token", passwordMode)
	}
	req.Header.Set(AuthorizationHeader, "Bearer "+tok.AccessToken)
	return nil
}

// fetchError records a refusal as auth_failed and returns the upstream's code
// and description without the password or client secret; else it scrubs.
func (a *passwordAuth) fetchError(err error) error {
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) || !tokengrant.IsRefusal(re.ErrorCode) {
		return tokenFetchError(a.cfg, err)
	}
	connstate.Observe(a.cfg.Kind, a.cfg.ConnectionName, connstate.AuthFailed)
	if d := a.source.Redact(tokengrant.Description(re.ErrorDescription)); d != "" {
		return a.cfg.errf("%s: %w: %s: %s", passwordMode, ErrPasswordRefused, re.ErrorCode, d)
	}
	return a.cfg.errf("%s: %w: %s", passwordMode, ErrPasswordRefused, re.ErrorCode)
}
