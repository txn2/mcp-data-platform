package gatewayhttp

import (
	"context"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// identityOIDC is the one identity label value every signed-in person
// records: a person is not a series (#1892).
const identityOIDC = "oidc"

// apiKeyUserIDPrefix is what the API key authenticator puts in front of the
// key's name to form a UserID.
const apiKeyUserIDPrefix = "apikey:"

// identityResolver is the IdentityResolver over the platform's authenticator:
// the API key's name for key auth, "oidc" for any other authenticated caller,
// "unknown" when nothing resolves. It reuses the authenticator rather than
// forking auth, and it never carries a person's address or subject: the label
// is bounded by the operator's key list, and who the person was is on the
// audit row.
type identityResolver struct {
	authn middleware.Authenticator
}

// NewIdentityResolver builds the resolver from the platform's authenticator. A
// nil authenticator yields a resolver that always answers "unknown".
func NewIdentityResolver(authn middleware.Authenticator) IdentityResolver {
	return identityResolver{authn: authn}
}

// ResolveIdentity authenticates the token already placed on ctx and returns
// the label value.
func (r identityResolver) ResolveIdentity(ctx context.Context) string {
	if r.authn == nil {
		return metricLabelUnknown
	}
	info, err := r.authn.Authenticate(ctx)
	if err != nil || info == nil {
		return metricLabelUnknown
	}
	if info.AuthType == middleware.AuthTypeAPIKey {
		if name := strings.TrimPrefix(info.UserID, apiKeyUserIDPrefix); name != "" && name != info.UserID {
			return name
		}
	}
	return identityOIDC
}
