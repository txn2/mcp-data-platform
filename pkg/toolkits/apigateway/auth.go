package apigateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// Authenticator applies a connection's authentication scheme to an
// outbound HTTP request before it is sent. Implementations must be
// safe for concurrent use — a single Authenticator is shared across
// all in-flight invocations of a connection.
//
// Implementations MUST NOT log credential material. The toolkit's
// audit pipeline expects no Authorization or X-API-Key value to ever
// appear in slog output, error messages, or audit rows; carelessly
// formatted error strings are the most common leak path.
type Authenticator interface {
	Apply(req *http.Request) error
}

// NewAuthenticator returns the Authenticator implementation for a
// validated Config. ParseConfig has already rejected unknown auth
// modes, so the default branch only fires if a future mode is added
// without a matching case here.
func NewAuthenticator(c Config) (Authenticator, error) {
	switch c.AuthMode {
	case AuthModeNone:
		return noneAuth{}, nil
	case AuthModeBearer:
		return bearerAuth{credential: c.Credential}, nil
	case AuthModeAPIKey:
		return newAPIKeyAuth(c)
	case AuthModeOAuth2ClientCredentials:
		return newOAuth2ClientCredentialsAuth(c), nil
	default:
		return nil, fmt.Errorf("apigateway: no authenticator for auth_mode %q", c.AuthMode)
	}
}

// noneAuth applies no credential. Distinct from a nil Authenticator so
// the toolkit's invocation path can call Apply unconditionally.
type noneAuth struct{}

// Apply is a no-op; the connection requested no outbound auth.
func (noneAuth) Apply(_ *http.Request) error { return nil }

// bearerAuth sets the Authorization header to "Bearer <credential>".
type bearerAuth struct {
	credential string
}

// Apply attaches the bearer token as the Authorization header.
func (b bearerAuth) Apply(req *http.Request) error {
	if b.credential == "" {
		return errors.New("apigateway: bearer credential is empty")
	}
	req.Header.Set(authorizationHeader, "Bearer "+b.credential)
	return nil
}

// apiKeyAuth attaches the credential as either a header or a query
// parameter, per the connection's APIKeyPlacement setting.
type apiKeyAuth struct {
	credential string
	placement  string
	header     string
	param      string
}

func newAPIKeyAuth(c Config) (apiKeyAuth, error) {
	a := apiKeyAuth{
		credential: c.Credential,
		placement:  c.APIKeyPlacement,
		header:     c.APIKeyHeader,
		param:      c.APIKeyParam,
	}
	if a.credential == "" {
		return apiKeyAuth{}, errors.New("apigateway: api_key credential is empty")
	}
	switch a.placement {
	case APIKeyPlacementHeader:
		if a.header == "" {
			return apiKeyAuth{}, errors.New("apigateway: api_key_header is empty")
		}
	case APIKeyPlacementQuery:
		if a.param == "" {
			return apiKeyAuth{}, errors.New("apigateway: api_key_param is empty")
		}
	default:
		return apiKeyAuth{}, fmt.Errorf("apigateway: invalid api_key_placement %q", a.placement)
	}
	return a, nil
}

// Apply attaches the API key as either a header or a query parameter,
// per the connection's APIKeyPlacement.
func (a apiKeyAuth) Apply(req *http.Request) error {
	switch a.placement {
	case APIKeyPlacementHeader:
		req.Header.Set(a.header, a.credential)
	case APIKeyPlacementQuery:
		q := req.URL.Query()
		q.Set(a.param, a.credential)
		req.URL.RawQuery = q.Encode()
	default:
		return fmt.Errorf("apigateway: invalid api_key_placement %q", a.placement)
	}
	return nil
}

// oauth2ClientCredentialsAuth applies an OAuth 2.1 access token
// acquired via the client_credentials grant. Token caching and
// refresh-on-expiry are delegated to the standard
// golang.org/x/oauth2 library: the underlying TokenSource fetches
// once, caches in memory, and re-fetches transparently when the
// token expires. No DB state is required because the authoritative
// inputs (client_id + client_secret) survive process restarts as
// part of the connection's encrypted credentials blob.
//
// SECURITY: this struct intentionally does NOT carry the client
// secret in its log/error string output. tokenFetchError below
// scrubs the token URL's query string and userinfo before
// surfacing transport errors to the model — without this, a bad
// network path or DNS failure during a token fetch would expose
// any credential the operator embedded in the URL (an unfortunate
// pattern but one that exists in the wild).
type oauth2ClientCredentialsAuth struct {
	src oauth2.TokenSource
}

// oauth2TokenFetchTimeout caps how long a single token-endpoint
// request can take. Without this the default Go http.Client has no
// timeout and an unreachable IdP would hang every Apply call until
// the OS-level dial timeout fires (minutes). 30 seconds matches
// the order of magnitude of the upstream call_timeout while
// staying generous for IdPs with slow first-token issuance.
const oauth2TokenFetchTimeout = 30 * time.Second

func newOAuth2ClientCredentialsAuth(c Config) oauth2ClientCredentialsAuth {
	authStyle := oauth2.AuthStyleInHeader
	if c.OAuth2.EndpointAuthStyle == OAuth2AuthStyleParams {
		authStyle = oauth2.AuthStyleInParams
	}
	cfg := &clientcredentials.Config{
		ClientID:     c.OAuth2.ClientID,
		ClientSecret: c.OAuth2.ClientSecret,
		TokenURL:     c.OAuth2.TokenURL,
		Scopes:       c.OAuth2.Scopes,
		AuthStyle:    authStyle,
	}
	// Bound token-endpoint requests via a custom http.Client. The
	// oauth2 library reads ctx-value oauth2.HTTPClient and falls
	// back to http.DefaultClient (no timeout) otherwise.
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{
		Timeout: oauth2TokenFetchTimeout,
	})
	// The token source caches in-memory and re-fetches when expired.
	// Wrap with oauth2.ReuseTokenSource so the cache is reused
	// across Apply calls; clientcredentials.Config.TokenSource
	// already does this internally but the wrap is explicit
	// defense against future library changes.
	src := oauth2.ReuseTokenSource(nil, cfg.TokenSource(ctx))
	return oauth2ClientCredentialsAuth{src: src}
}

// Apply fetches (or returns the cached) access token and attaches
// it as the Authorization header. Errors from the token source —
// network failures, IdP rejections, expired credentials — are
// scrubbed via tokenFetchError before being returned, so the
// caller's error pipeline cannot leak the OAuth client secret or
// the token URL's userinfo.
func (a oauth2ClientCredentialsAuth) Apply(req *http.Request) error {
	tok, err := a.src.Token()
	if err != nil {
		return tokenFetchError(err)
	}
	if tok == nil || tok.AccessToken == "" {
		return errors.New("apigateway: oauth2 token source returned no access token")
	}
	req.Header.Set(authorizationHeader, "Bearer "+tok.AccessToken)
	return nil
}

// tokenFetchError sanitizes errors from oauth2.TokenSource.Token().
// The library's *oauth2.RetrieveError includes the IdP response
// body, which can contain sensitive material (e.g., a refresh
// token from a partial grant exchange). It also includes the
// token URL — if the operator embedded credentials in the URL
// (e.g., https://user:secret@idp.example/token), those would
// leak. We rebuild the message keeping only the non-sensitive
// pieces.
func tokenFetchError(err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		return fmt.Errorf("apigateway: oauth2 token fetch failed: status=%d", re.Response.StatusCode)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		parsed, perr := url.Parse(ue.URL)
		if perr != nil {
			return fmt.Errorf("apigateway: oauth2 token fetch %s: %w", ue.Op, ue.Err)
		}
		parsed.RawQuery = ""
		parsed.User = nil
		return fmt.Errorf("apigateway: oauth2 token fetch %s %q: %w", ue.Op, parsed.String(), ue.Err)
	}
	// Fallback: redact anything that looks URL-shaped just in case
	// a future library version wraps in a different error type.
	msg := err.Error()
	if strings.Contains(msg, "://") {
		return errors.New("apigateway: oauth2 token fetch failed (details redacted)")
	}
	return fmt.Errorf("apigateway: oauth2 token fetch failed: %s", msg)
}
