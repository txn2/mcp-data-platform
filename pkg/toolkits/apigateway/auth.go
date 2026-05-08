package apigateway

import (
	"errors"
	"fmt"
	"net/http"
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
