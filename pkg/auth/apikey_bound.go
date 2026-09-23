package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// BoundPrincipal is the person a key is issued against: the id their own
// sessions authenticate as, their address, and the roles the platform last
// recorded for them.
//
// Subject is what makes one identity out of two credentials. A person signed
// in through the identity provider stamps their subject on everything they
// own; a key bound to them stamps the same one, so what they do through a
// client that speaks only bearer tokens is theirs and is there when they open
// the portal (#1759).
type BoundPrincipal struct {
	Subject string
	Email   string
	Roles   []string
}

// ErrNoBoundPrincipal is the answer for an address the platform cannot speak
// for: no account, or nobody it has seen sign in, so there is no subject to
// present and no roles to carry. It is a verdict, not a failure -- a lookup
// that could not run reports its own error -- and every caller refuses the key
// either way. Callers that only want to know what a key reaches (a listing)
// tell the two apart to decide whether to say anything at all.
var ErrNoBoundPrincipal = errors.New("no account the platform can speak for")

// PrincipalSource resolves the person a key is bound to.
type PrincipalSource interface {
	// BoundPrincipal returns the person recorded at email, or
	// ErrNoBoundPrincipal when the platform has no record of them signing in.
	BoundPrincipal(ctx context.Context, email string) (*BoundPrincipal, error)
}

// SetPrincipalSource attaches the resolver for keys bound to a user account.
// It may be called while requests are being authenticated. Unattached, a bound
// key is refused rather than falling back to a key identity: a key that cannot
// resolve to the person it names must not authenticate as something else.
func (a *APIKeyAuthenticator) SetPrincipalSource(source PrincipalSource) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.principals = source
}

// Principals is the resolver bound keys are answered through, or nil when this
// deployment has none, including on a nil authenticator. The route that issues
// a bound key reads it so that the
// account it accepts is one that will resolve when the key is presented: the
// check and the behavior ask one source, and cannot drift apart.
func (a *APIKeyAuthenticator) Principals() PrincipalSource {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.principals
}

// userInfo is the identity a matched key authenticates as: the person it is
// bound to, or the key itself.
func (a *APIKeyAuthenticator) userInfo(ctx context.Context, key *APIKey) (*middleware.UserInfo, error) {
	if key.IsExpired() {
		return nil, fmt.Errorf("api key %q has expired", key.Name)
	}
	if key.UserEmail == "" {
		return keyUserInfo(key), nil
	}
	return a.boundUserInfo(ctx, key)
}

// boundUserInfo resolves the person a bound key names. Every failure refuses
// the key. A key that says whose it is and cannot be shown to be theirs must
// not authenticate at all: falling back to the key's own identity would hand
// the caller a second, unaudited principal at exactly the moment the platform
// has lost track of the first.
func (a *APIKeyAuthenticator) boundUserInfo(ctx context.Context, key *APIKey) (*middleware.UserInfo, error) {
	a.mu.RLock()
	source := a.principals
	a.mu.RUnlock()
	if source == nil {
		return nil, fmt.Errorf("api key %q is bound to a user account, which this deployment cannot resolve", key.Name)
	}
	person, err := source.BoundPrincipal(ctx, key.UserEmail)
	if err != nil {
		return nil, fmt.Errorf("api key %q: resolving the account it is bound to: %w", key.Name, err)
	}
	if person.Subject == "" {
		return nil, fmt.Errorf("api key %q is bound to an account the platform has no record of", key.Name)
	}
	return &middleware.UserInfo{
		UserID:   person.Subject,
		Email:    person.Email,
		Claims:   attributeClaims(key.Attributes),
		Roles:    boundRoles(key, person),
		AuthType: middleware.AuthTypeAPIKey,
	}, nil
}

// boundRoles is what a bound key may reach: the roles the person currently
// holds, or the set an administrator put on the key.
//
// A key carrying no roles of its own follows the person, which is what a key
// somebody issued for themselves always does -- they cannot widen it, and a
// role the provider stops granting stops reaching the key on their next sign-in.
// A non-empty set replaces theirs and is used verbatim: it is not intersected
// with what they hold, so an administrator can hand a key a role its person
// does not have. That is deliberate -- an administrator already decides what
// every key may reach -- but it means the key's access is the administrator's
// answer, not a narrowing of its person's.
func boundRoles(key *APIKey, person *BoundPrincipal) []string {
	if len(key.Roles) > 0 {
		return key.Roles
	}
	return person.Roles
}
