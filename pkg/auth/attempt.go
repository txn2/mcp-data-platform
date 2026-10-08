package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/txn2/mcp-data-platform/internal/opsobs"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// AttemptObserver is told the outcome of every credential validation the
// chained authenticator makes (#1898): the method that validated it or refused
// it (observability.AuthMethod*), the result and the reason class
// (observability.AuthReason*). It runs on the authentication path and must not
// block.
type AttemptObserver func(ctx context.Context, method, result, reason string)

// The failure classes the authenticators' errors carry, so a reason is read
// from the error's identity and never from its text. Each keeps the message
// the authenticator returned before it existed.
var (
	errTokenExpired = errors.New("token expired")
	errKeyExpired   = errors.New("has expired")
	// errKeyRevoked is the refusal of a key the key store no longer holds:
	// still "invalid API key" to the caller, a revocation to the metric.
	errKeyRevoked error = revokedKeyError{}
)

// revokedKeyError reads as errInvalidAPIKey to every caller.
type revokedKeyError struct{}

func (revokedKeyError) Error() string { return errInvalidAPIKey.Error() }

// Is reports the refusal as the invalid key it is to the caller.
func (revokedKeyError) Is(target error) bool { return target == errInvalidAPIKey }

// unknownKeyError is a token signed with a key the platform does not hold: a
// kid the JWKS or the signing-key ring does not carry.
type unknownKeyError string

func (e unknownKeyError) Error() string { return string(e) }

// claimError is a token whose claims are well formed and say it is not for
// this platform, or not now: the wrong issuer or audience, not yet valid, too
// old.
type claimError string

func (e claimError) Error() string { return string(e) }

const (
	errInvalidIssuer   claimError = "invalid issuer"
	errInvalidAudience claimError = "invalid audience"
	errNotYetValid     claimError = "token not yet valid"
	errTokenTooOld     claimError = "token too old"
)

// attemptMethod is implemented by the authenticators the chain can attribute
// an attempt to.
type attemptMethod interface {
	authMethod() string
	// claimsCredential reports whether token is the kind of credential this
	// authenticator owns, so a refusal is attributed to it rather than to the
	// last authenticator that happened to try.
	claimsCredential(token string) bool
}

// AttemptReason is the reason class for a validation outcome: none for a
// success, otherwise the class of err.
func AttemptReason(err error) string {
	var (
		claim   claimError
		unknown unknownKeyError
	)
	switch {
	case err == nil:
		return opsobs.AuthReasonNone
	case errors.Is(err, middleware.ErrValidationUnavailable):
		return opsobs.AuthReasonIDPUnavailable
	case errors.Is(err, jwt.ErrTokenExpired), errors.Is(err, errTokenExpired), errors.Is(err, errKeyExpired):
		return opsobs.AuthReasonExpired
	case errors.Is(err, errKeyRevoked):
		return opsobs.AuthReasonRevoked
	case errors.Is(err, jwt.ErrTokenSignatureInvalid):
		return opsobs.AuthReasonBadSignature
	case errors.Is(err, errInvalidAPIKey), errors.As(err, &unknown):
		return opsobs.AuthReasonUnknownKey
	case errors.As(err, &claim), errors.Is(err, jwt.ErrTokenNotValidYet),
		errors.Is(err, jwt.ErrTokenInvalidIssuer), errors.Is(err, jwt.ErrTokenInvalidAudience):
		return opsobs.AuthReasonInvalidClaims
	default:
		return opsobs.AuthReasonMalformed
	}
}

// unverifiedIssuer is the iss claim of a JWT read without verifying it. It
// decides only which authenticator a refusal is attributed to; nothing is
// granted on it.
func unverifiedIssuer(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != jwtPartCount {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Issuer string `json:"iss"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return claims.Issuer
}

// chainAttempt is what one chained authentication saw: the authenticator that
// succeeded, or the refusals of the ones that tried.
type chainAttempt struct {
	token     string
	succeeded middleware.Authenticator
	refusals  []refusal
}

// refusal is one authenticator's error.
type refusal struct {
	by  middleware.Authenticator
	err error
}

// outcome is the method, result and reason the attempt is recorded under. A
// refusal is attributed to the authenticator that claims the credential, else
// to the last one that refused it; a chain no authenticator tried is a
// malformed credential of unknown method.
func (c chainAttempt) outcome(final error) (method, result, reason string) {
	if c.succeeded != nil {
		return methodOf(c.succeeded), opsobs.AuthResultSuccess, opsobs.AuthReasonNone
	}
	if c.token == "" {
		// No credential at all: no authenticator owns the refusal.
		return "", opsobs.AuthResultFailure, opsobs.AuthReasonMalformed
	}
	if len(c.refusals) == 0 {
		return "", opsobs.AuthResultFailure, AttemptReason(final)
	}
	chosen := c.refusals[len(c.refusals)-1]
	for _, r := range c.refusals {
		if am, ok := r.by.(attemptMethod); ok && am.claimsCredential(c.token) {
			chosen = r
			break
		}
	}
	return methodOf(chosen.by), opsobs.AuthResultFailure, AttemptReason(chosen.err)
}

// methodOf is an authenticator's method, or "" (recorded as unknown) for one
// that does not name it.
func methodOf(a middleware.Authenticator) string {
	if am, ok := a.(attemptMethod); ok {
		return am.authMethod()
	}
	return ""
}

func (*OIDCAuthenticator) authMethod() string { return opsobs.AuthMethodOIDC }

func (a *OIDCAuthenticator) claimsCredential(token string) bool {
	return LooksLikeJWT(token) && unverifiedIssuer(token) == a.cfg.Issuer
}

func (*OAuthJWTAuthenticator) authMethod() string { return opsobs.AuthMethodOAuth }

func (a *OAuthJWTAuthenticator) claimsCredential(token string) bool {
	return LooksLikeJWT(token) && unverifiedIssuer(token) == a.cfg.Issuer
}

func (*APIKeyAuthenticator) authMethod() string { return opsobs.AuthMethodAPIKey }

func (*APIKeyAuthenticator) claimsCredential(token string) bool { return !LooksLikeJWT(token) }
