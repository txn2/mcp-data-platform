package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/txn2/mcp-data-platform/internal/opsobs"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

const (
	attemptOAuthIssuer = "https://platform.example.com"
	attemptIDPIssuer   = "https://idp.example.com/realms/acme"
	attemptAPIKey      = "dp_attempt_test_key_value"
)

type observed struct{ method, result, reason string }

// attemptChain is the chain the platform assembles (OAuth JWT, OIDC, API key)
// with an observer recording every outcome.
func attemptChain(t *testing.T, anonymous bool) (*ChainedAuthenticator, *[]observed) {
	t.Helper()
	oauth, err := NewOAuthJWTAuthenticator(OAuthJWTConfig{
		Issuer: attemptOAuthIssuer, SigningKey: []byte("attempt-signing-key-at-least-32-bytes"),
	})
	if err != nil {
		t.Fatal(err)
	}
	oidc, err := NewOIDCAuthenticator(OIDCConfig{
		Issuer: attemptIDPIssuer, Audience: "mcp-data-platform", SkipSignatureVerification: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	keys := NewAPIKeyAuthenticator(APIKeyConfig{Keys: []APIKey{{Key: attemptAPIKey, Name: "ops", Roles: []string{"admin"}}}})
	var got []observed
	chain := NewChainedAuthenticator(ChainedAuthConfig{
		AllowAnonymous: anonymous,
		Observe: func(_ context.Context, method, result, reason string) {
			got = append(got, observed{method, result, reason})
		},
	}, oauth, oidc, keys)
	return chain, &got
}

func idpToken(exp time.Time, aud string) string {
	return createTestJWT(map[string]any{
		"iss": attemptIDPIssuer, "sub": "user-1", "aud": aud, "exp": float64(exp.Unix()),
	})
}

func oauthToken(t *testing.T, key string, exp time.Time) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": attemptOAuthIssuer, "sub": "user-2", "aud": attemptOAuthIssuer, "exp": exp.Unix(),
	}).SignedString([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestChainedAuthenticator_ObservesEveryAttempt is the attribution table: each
// credential is counted under the method that owns it and the class of its
// refusal, never under whichever authenticator happened to try last.
func TestChainedAuthenticator_ObservesEveryAttempt(t *testing.T) {
	future, past := time.Now().Add(time.Hour), time.Now().Add(-time.Hour)
	tests := []struct {
		name  string
		token string
		want  observed
	}{
		{
			"expired idp token", idpToken(past, "mcp-data-platform"),
			observed{opsobs.AuthMethodOIDC, opsobs.AuthResultFailure, opsobs.AuthReasonExpired},
		},
		{
			"idp token for another audience", idpToken(future, "someone-else"),
			observed{opsobs.AuthMethodOIDC, opsobs.AuthResultFailure, opsobs.AuthReasonInvalidClaims},
		},
		{
			"valid idp token", idpToken(future, "mcp-data-platform"),
			observed{opsobs.AuthMethodOIDC, opsobs.AuthResultSuccess, opsobs.AuthReasonNone},
		},
		{
			"valid api key", attemptAPIKey,
			observed{opsobs.AuthMethodAPIKey, opsobs.AuthResultSuccess, opsobs.AuthReasonNone},
		},
		{
			"unknown api key", "dp_not_a_key",
			observed{opsobs.AuthMethodAPIKey, opsobs.AuthResultFailure, opsobs.AuthReasonUnknownKey},
		},
		{
			"oauth token signed with another key", oauthToken(t, "a-different-key-that-is-32-bytes-long", future),
			observed{opsobs.AuthMethodOAuth, opsobs.AuthResultFailure, opsobs.AuthReasonBadSignature},
		},
		{
			"no credential", "",
			observed{"", opsobs.AuthResultFailure, opsobs.AuthReasonMalformed},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chain, got := attemptChain(t, false)
			ctx := context.Background()
			if tt.token != "" {
				ctx = WithToken(ctx, tt.token)
			}
			_, _ = chain.Authenticate(ctx)
			if len(*got) != 1 {
				t.Fatalf("observed %d attempts, want 1: %+v", len(*got), *got)
			}
			if (*got)[0] != tt.want {
				t.Errorf("observed %+v, want %+v", (*got)[0], tt.want)
			}
		})
	}
}

// TestChainedAuthenticator_AnonymousFallbackIsNotAnAttempt shows a request
// with no credential on an anonymous deployment is not counted.
func TestChainedAuthenticator_AnonymousFallbackIsNotAnAttempt(t *testing.T) {
	chain, got := attemptChain(t, true)
	info, err := chain.Authenticate(context.Background())
	if err != nil || info == nil || info.AuthType != middleware.AuthTypeAnonymous {
		t.Fatalf("anonymous fallback = %+v, %v", info, err)
	}
	if len(*got) != 0 {
		t.Errorf("observed %+v for an anonymous request", *got)
	}
}

// TestChainedAuthenticator_IdPUnavailable attributes a transient validation
// failure to the authenticator that claims the token.
func TestChainedAuthenticator_IdPUnavailable(t *testing.T) {
	var got []observed
	oidc := &OIDCAuthenticator{cfg: OIDCConfig{Issuer: attemptIDPIssuer}}
	chain := NewChainedAuthenticator(ChainedAuthConfig{Observe: func(_ context.Context, m, r, re string) {
		got = append(got, observed{m, r, re})
	}}, &unavailableAuth{OIDCAuthenticator: oidc})
	_, _ = chain.Authenticate(WithToken(context.Background(), idpToken(time.Now().Add(time.Hour), "x")))
	want := observed{opsobs.AuthMethodOIDC, opsobs.AuthResultFailure, opsobs.AuthReasonIDPUnavailable}
	if len(got) != 1 || got[0] != want {
		t.Errorf("observed %+v, want %+v", got, want)
	}
}

// unavailableAuth answers as an OIDC authenticator whose identity provider is
// down.
type unavailableAuth struct{ *OIDCAuthenticator }

func (*unavailableAuth) Authenticate(context.Context) (*middleware.UserInfo, error) {
	return nil, fmt.Errorf("refreshing jwks: %w", middleware.ErrValidationUnavailable)
}

func TestAttemptReason(t *testing.T) {
	for name, tt := range map[string]struct {
		err  error
		want string
	}{
		"none":            {nil, opsobs.AuthReasonNone},
		"idp unavailable": {fmt.Errorf("refreshing jwks: %w", middleware.ErrValidationUnavailable), opsobs.AuthReasonIDPUnavailable},
		"jwt expired":     {fmt.Errorf("verifying token: %w", jwt.ErrTokenExpired), opsobs.AuthReasonExpired},
		"oidc expired":    {errTokenExpired, opsobs.AuthReasonExpired},
		"key expired":     {fmt.Errorf("api key %q %w", "ops", errKeyExpired), opsobs.AuthReasonExpired},
		"key revoked":     {errKeyRevoked, opsobs.AuthReasonRevoked},
		"bad signature":   {fmt.Errorf("verifying token: %w", jwt.ErrTokenSignatureInvalid), opsobs.AuthReasonBadSignature},
		"unknown key":     {errInvalidAPIKey, opsobs.AuthReasonUnknownKey},
		"unknown kid":     {fmt.Errorf("getting public key: %w", unknownKeyError("key not found: k1")), opsobs.AuthReasonUnknownKey},
		"issuer":          {errInvalidIssuer, opsobs.AuthReasonInvalidClaims},
		"not yet valid":   {fmt.Errorf("verifying token: %w", jwt.ErrTokenNotValidYet), opsobs.AuthReasonInvalidClaims},
		"anything else":   {errors.New("missing exp claim"), opsobs.AuthReasonMalformed},
	} {
		if got := AttemptReason(tt.err); got != tt.want {
			t.Errorf("%s: AttemptReason = %q, want %q", name, got, tt.want)
		}
	}
}

// TestAttemptSentinelsKeepTheirMessages pins that classing an error did not
// change what the caller is told.
func TestAttemptSentinelsKeepTheirMessages(t *testing.T) {
	for err, want := range map[error]string{
		errKeyRevoked:                        "invalid API key",
		errInvalidAudience:                   "invalid audience",
		errNotYetValid:                       "token not yet valid",
		errTokenTooOld:                       "token too old",
		unknownKeyError("key not found: k1"): "key not found: k1",
	} {
		if err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	}
	if !errors.Is(errKeyRevoked, errInvalidAPIKey) {
		t.Error("a revoked key no longer reads as an invalid key")
	}
}

func TestUnverifiedIssuer(t *testing.T) {
	if got := unverifiedIssuer(idpToken(time.Now(), "a")); got != attemptIDPIssuer {
		t.Errorf("issuer = %q", got)
	}
	for _, bad := range []string{"", "a.b", "a.!!!.c", "a." + "bm90LWpzb24" + ".c"} {
		if got := unverifiedIssuer(bad); got != "" {
			t.Errorf("unverifiedIssuer(%q) = %q", bad, got)
		}
	}
}

// TestChainedAuthenticator_CountedRequestIsNotCountedAgain: a validation of a
// request the HTTP gate already counted is not reported a second time, while
// it still authenticates.
func TestChainedAuthenticator_CountedRequestIsNotCountedAgain(t *testing.T) {
	var got []observed
	chain := NewChainedAuthenticator(ChainedAuthConfig{Observe: func(_ context.Context, m, r, re string) {
		got = append(got, observed{m, r, re})
	}}, NewAPIKeyAuthenticator(APIKeyConfig{Keys: []APIKey{{Key: "k", Name: "svc", Roles: []string{"analyst"}}}}))

	ctx := WithToken(context.Background(), "k")
	if _, err := chain.Authenticate(ctx); err != nil {
		t.Fatal(err)
	}
	info, err := chain.Authenticate(WithAttemptCounted(ctx))
	if err != nil || info == nil {
		t.Fatalf("a counted request no longer authenticates: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("observed %d attempts for one request, want 1: %+v", len(got), got)
	}
}
