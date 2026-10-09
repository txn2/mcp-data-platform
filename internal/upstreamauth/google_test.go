package upstreamauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/txn2/mcp-data-platform/internal/upstreamauth/googlekey"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

const (
	testGoogleEmail = "reporting@acme-analytics.iam.gserviceaccount.com"
	testGoogleScope = "https://www.googleapis.com/auth/display-video"
)

// googleKeyFile is a service account key file as Google issues it, signing
// with the test RSA key and exchanging at tokenURI.
func googleKeyFile(t *testing.T, tokenURI, keyID string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "acme-analytics", "private_key_id": keyID,
		"private_key": testRSAKeyPEM(), "client_email": testGoogleEmail, "client_id": "1234567890",
		"token_uri": tokenURI, "auth_uri": "https://accounts.google.com/o/oauth2/auth",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// assertionOf decodes the assertion one exchange sent, unverified: the test
// reads what the platform put in it.
func assertionOf(t *testing.T, req tokenRequest) (claims jwt.MapClaims, header map[string]any) {
	t.Helper()
	claims = jwt.MapClaims{}
	tok, _, err := jwt.NewParser().ParseUnverified(req.form.Get("assertion"), claims)
	if err != nil {
		t.Fatalf("the exchange sent no readable assertion: %v", err)
	}
	return claims, tok.Header
}

func parseGoogle(t *testing.T, cfg map[string]any) Config {
	t.Helper()
	c, err := Parse("api", "apigateway", "https://displayvideo.googleapis.com", cfg)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	c.ConnectionName = "dv360"
	return c
}

func TestParseGoogleServiceAccount_Refusals(t *testing.T) {
	file := googleKeyFile(t, googlekey.TokenURL, "k1")
	var fields map[string]any
	if err := json.Unmarshal([]byte(file), &fields); err != nil {
		t.Fatal(err)
	}
	without := func(key string) string {
		m := map[string]any{}
		for k, v := range fields {
			if k != key {
				m[k] = v
			}
		}
		raw, _ := json.Marshal(m)
		return string(raw)
	}
	oauthClient := strings.Replace(file, `"type":"service_account"`, `"type":"authorized_user"`, 1)
	for name, tc := range map[string]struct{ raw, want string }{
		"not JSON":          {"-----BEGIN PRIVATE KEY-----", "is not a JSON key file"},
		"an OAuth client":   {oauthClient, `type is "authorized_user", not "service_account"`},
		"no private key":    {without("private_key"), "has no private_key"},
		"no client email":   {without("client_email"), "has no client_email"},
		"no token endpoint": {without("token_uri"), "has no token_uri"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := googlekey.Parse(tc.raw)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "PRIVATE KEY") || strings.Contains(err.Error(), testGoogleEmail) {
				t.Fatalf("the refusal repeats the file: %v", err)
			}
		})
	}
	// Parse refuses the same file, in the kind's voice.
	_, err := Parse("api", "apigateway", "", map[string]any{googlekey.ConfigKey: without("private_key")})
	if err == nil || !strings.HasPrefix(err.Error(), "apigateway: ") {
		t.Fatalf("Parse err = %v", err)
	}
}

// TestParseGoogleServiceAccount_FillsTheGrant is #2061's convenience path: the
// key file and the scopes are the whole connection, and every derived value
// gives way to one the operator set.
func TestParseGoogleServiceAccount_FillsTheGrant(t *testing.T) {
	file := googleKeyFile(t, googlekey.TokenURL, "k1")
	c := parseGoogle(t, map[string]any{googlekey.ConfigKey: file, connoauth.ConfigKeyScope: testGoogleScope})
	if c.AuthMode != AuthModeOAuth || c.OAuth2.Grant != connoauth.GrantJWTBearer {
		t.Fatalf("mode %q grant %q, want oauth jwt_bearer", c.AuthMode, c.OAuth2.Grant)
	}
	j := c.SignedJWT
	if j.Algorithm != SignedJWTAlgRS256 || j.Issuer != testGoogleEmail || j.KeyID != "k1" || j.Subject != "" ||
		j.Audience != googlekey.TokenURL || c.OAuth2.TokenURL != googlekey.TokenURL || j.PrivateKeyPEM == "" {
		t.Fatalf("derived grant = %+v, token url %q", j, c.OAuth2.TokenURL)
	}
	if c.OAuth2.ScopePlacement != ScopePlacementClaim || !c.Google.Set {
		t.Fatalf("placement %q, google %+v", c.OAuth2.ScopePlacement, c.Google)
	}
	if err := c.ValidateAuth(); err != nil {
		t.Fatalf("ValidateAuth: %v", err)
	}

	explicit := parseGoogle(t, map[string]any{
		googlekey.ConfigKey: file, connoauth.ConfigKeyScope: testGoogleScope,
		cfgKeyJWTSubject: "analyst@acme.example", cfgKeyJWTKeyID: "pinned", cfgKeyOAuthScopePlacement: ScopePlacementBoth,
		connoauth.ConfigKeyTokenURL: "https://token.example/token",
	})
	if explicit.SignedJWT.Subject != "analyst@acme.example" || explicit.SignedJWT.KeyID != "pinned" ||
		explicit.OAuth2.ScopePlacement != ScopePlacementBoth || explicit.OAuth2.TokenURL != "https://token.example/token" {
		t.Fatalf("an explicit value lost to a derived one: %+v %+v", explicit.SignedJWT, explicit.OAuth2)
	}

	_, err := Parse("api", "apigateway", "", map[string]any{
		googlekey.ConfigKey: file, cfgKeyGoogleServiceAccountSecret: "dv360-key",
	})
	if err == nil || !strings.Contains(err.Error(), "set one of") {
		t.Fatalf("both forms of the key were accepted: %v", err)
	}

	secret := parseGoogle(t, map[string]any{cfgKeyGoogleServiceAccountSecret: " dv360-key ", connoauth.ConfigKeyScope: testGoogleScope})
	if secret.Google.Secret != "dv360-key" || secret.OAuth2.TokenURL != googlekey.TokenURL {
		t.Fatalf("secret form = %+v, token url %q", secret.Google, secret.OAuth2.TokenURL)
	}
	if err := secret.ValidateAuth(); err != nil {
		t.Fatalf("a key named by its secret was refused before it was read: %v", err)
	}

	if _, ok := googlekey.IdentityOf(map[string]any{googlekey.ConfigKey: file}); !ok {
		t.Fatal("no identity read from a key file")
	}
	if id, _ := googlekey.IdentityOf(map[string]any{googlekey.ConfigKey: file}); id != (googlekey.Identity{
		ClientEmail: testGoogleEmail, ProjectID: "acme-analytics", PrivateKeyID: "k1",
	}) {
		t.Fatalf("identity = %+v", id)
	}
	for _, cfg := range []map[string]any{{}, {googlekey.ConfigKey: "nope"}} {
		if _, ok := googlekey.IdentityOf(cfg); ok {
			t.Errorf("an identity was read from %v", cfg)
		}
	}
}

// TestGoogleExchange_ScopesRideInTheAssertion is the wire Google accepts
// (#2061): the scopes are a claim in the signed assertion and not a form
// parameter, sub is absent unless the account impersonates a user, and the
// grant the endpoint reports is what the connection test shows.
func TestGoogleExchange_ScopesRideInTheAssertion(t *testing.T) {
	e := newTokenEndpoint(t)
	e.answer(http.StatusOK, `{"access_token":"ya29.a","token_type":"Bearer","expires_in":3599,"scope":"`+testGoogleScope+`"}`)
	file := googleKeyFile(t, e.url(), "k1")
	c := parseGoogle(t, map[string]any{googlekey.ConfigKey: file, connoauth.ConfigKeyScope: testGoogleScope})
	a, err := NewAuthenticator(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := GrantOf(a); ok {
		t.Fatal("a grant is reported before any exchange")
	}
	req, err := applyTo(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get(AuthorizationHeader); got != "Bearer ya29.a" {
		t.Fatalf("Authorization = %q", got)
	}
	sent := e.seen()
	if len(sent) != 1 {
		t.Fatalf("%d exchanges", len(sent))
	}
	if s := sent[0].form.Get("scope"); s != "" {
		t.Fatalf("the scope was sent as a form parameter too: %q", s)
	}
	claims, header := assertionOf(t, sent[0])
	if claims["scope"] != testGoogleScope || claims["iss"] != testGoogleEmail || claims["aud"] != e.url() || header["kid"] != "k1" {
		t.Fatalf("assertion claims %v header %v", claims, header)
	}
	if _, ok := claims["sub"]; ok {
		t.Fatalf("an account acting as itself sent sub: %v", claims["sub"])
	}
	grant, ok := GrantOf(a)
	if !ok || grant.Identity != testGoogleEmail || len(grant.Scopes) != 1 || grant.Scopes[0] != testGoogleScope {
		t.Fatalf("grant = %+v, %v", grant, ok)
	}

	// Under delegation sub names the user, and param placement keeps the
	// scope off the assertion.
	e2 := newTokenEndpoint(t)
	c2 := parseGoogle(t, map[string]any{
		googlekey.ConfigKey: googleKeyFile(t, e2.url(), "k1"), connoauth.ConfigKeyScope: testGoogleScope,
		cfgKeyJWTSubject: "analyst@acme.example", cfgKeyOAuthScopePlacement: ScopePlacementParam,
	})
	a2, err := NewAuthenticator(c2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyTo(t, a2); err != nil {
		t.Fatal(err)
	}
	claims2, _ := assertionOf(t, e2.seen()[0])
	if claims2["sub"] != "analyst@acme.example" || claims2["scope"] != nil || e2.seen()[0].form.Get("scope") != testGoogleScope {
		t.Fatalf("delegation claims %v form %v", claims2, e2.seen()[0].form)
	}
	if g, _ := GrantOf(a2); g.Identity != "analyst@acme.example" || g.Scopes[0] != testGoogleScope {
		t.Fatalf("a delegated grant = %+v", g)
	}
	if _, ok := GrantOf(noneAuth{}); ok {
		t.Fatal("a mode that exchanges nothing reports a grant")
	}
}

// TestGoogleExchange_KeyFromAStoredSecret is section 6 of #2061: the key never
// enters the connection's config. The secret is read at every exchange, so a
// rotated key is the next token's key; without the read wired, or with a
// secret that is not a key file, the call is refused naming the secret.
func TestGoogleExchange_KeyFromAStoredSecret(t *testing.T) {
	e := newTokenEndpoint(t)
	e.answer(http.StatusOK, `{"access_token":"ya29.a","token_type":"Bearer","expires_in":1}`)
	c := parseGoogle(t, map[string]any{
		cfgKeyGoogleServiceAccountSecret: "dv360-key", connoauth.ConfigKeyScope: testGoogleScope,
		connoauth.ConfigKeyTokenURL: e.url(),
	})
	a, err := NewAuthenticator(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyTo(t, a); err == nil || !strings.Contains(err.Error(), `the stored secret "dv360-key"`) {
		t.Fatalf("an unwired secret read was not refused naming it: %v", err)
	}

	value := googleKeyFile(t, e.url(), "k1")
	var asked []string
	if !SetKeySecrets(a, func(_ context.Context, name string) (string, error) {
		asked = append(asked, name)
		return value, nil
	}) {
		t.Fatal("the jwt_bearer authenticator did not take the secret read")
	}
	if _, err := applyTo(t, a); err != nil {
		t.Fatal(err)
	}
	value = googleKeyFile(t, e.url(), "k2") // rotated in the secret
	if _, err := applyTo(t, a); err != nil {
		t.Fatal(err)
	}
	sent := e.seen()
	if len(sent) != 2 || len(asked) != 2 || asked[0] != "dv360-key" {
		t.Fatalf("%d exchanges, secret read %v", len(sent), asked)
	}
	first, h1 := assertionOf(t, sent[0])
	_, h2 := assertionOf(t, sent[1])
	if first["iss"] != testGoogleEmail || h1["kid"] != "k1" || h2["kid"] != "k2" {
		t.Fatalf("issuer %v, kids %v then %v", first["iss"], h1["kid"], h2["kid"])
	}

	SetKeySecrets(a, func(context.Context, string) (string, error) {
		return "", errors.New(`secret "dv360-key" may not be sent through connection "dv360"`)
	})
	if _, err := applyTo(t, a); err == nil || !strings.Contains(err.Error(), "may not be sent through connection") {
		t.Fatalf("a refused secret read = %v", err)
	}
	SetKeySecrets(a, func(context.Context, string) (string, error) { return "not a key", nil })
	if _, err := applyTo(t, a); err == nil || !strings.Contains(err.Error(), "not a JSON key file") {
		t.Fatalf("a secret that is not a key file = %v", err)
	}
	if SetKeySecrets(noneAuth{}, nil) {
		t.Fatal("a mode with no key took a secret read")
	}
}

// TestGoogleExchange_RefusalsCarryTheOperatorsNextStep is the token half of
// #2061's error table.
func TestGoogleExchange_RefusalsCarryTheOperatorsNextStep(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"a bad scope":       {`{"error":"invalid_scope","error_description":"Invalid OAuth scope or ID token audience provided."}`, "check oauth_scope"},
		"a deleted key":     {`{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`, "create a new key"},
		"a removed account": {`{"error":"invalid_grant","error_description":"Account not found"}`, "check the account still exists"},
		"clock skew":        {`{"error":"invalid_grant","error_description":"Invalid JWT: Token must be a short-lived token (60 minutes) and in a reasonable timeframe."}`, "check the host clock"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newTokenEndpoint(t)
			e.answer(http.StatusBadRequest, tc.body)
			a, err := NewAuthenticator(parseGoogle(t, map[string]any{
				googlekey.ConfigKey: googleKeyFile(t, e.url(), "k1"), connoauth.ConfigKeyScope: testGoogleScope,
			}))
			if err != nil {
				t.Fatal(err)
			}
			_, err = applyTo(t, a)
			if !errors.Is(err, ErrAssertionRejected) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want a rejection carrying %q", err, tc.want)
			}
		})
	}
	// The same refusal from an upstream that is not Google carries no Google
	// advice.
	e := newTokenEndpoint(t)
	e.answer(http.StatusBadRequest, `{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`)
	a, err := NewAuthenticator(jwtBearerConfig(e.url(), nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyTo(t, a); err == nil || strings.Contains(err.Error(), "create a new key") {
		t.Fatalf("a non-Google refusal = %v", err)
	}
}

// The key file arrives as a string of JSON or as the object it is; both parse,
// and googlekey.Normalize writes the object back as text, which is the form
// the at-rest encryption encrypts.
func TestNormalizeGoogleKeyFile(t *testing.T) {
	file := googleKeyFile(t, googlekey.TokenURL, "k1")
	var object map[string]any
	if err := json.Unmarshal([]byte(file), &object); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{googlekey.ConfigKey: object, connoauth.ConfigKeyScope: testGoogleScope}
	got, err := googlekey.Normalize(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text, ok := got[googlekey.ConfigKey].(string)
	if !ok || !strings.Contains(text, testGoogleEmail) {
		t.Fatalf("the object was not written back as text: %T", got[googlekey.ConfigKey])
	}
	if _, isMap := cfg[googlekey.ConfigKey].(map[string]any); !isMap {
		t.Fatal("the caller's map was changed")
	}
	if parsed := parseGoogle(t, cfg); parsed.SignedJWT.Issuer != testGoogleEmail {
		t.Fatalf("the object form did not fill the grant: %+v", parsed.SignedJWT)
	}
	if id, ok := googlekey.IdentityOf(cfg); !ok || id.ClientEmail != testGoogleEmail {
		t.Fatalf("no identity from the object form: %+v", id)
	}
	for _, unchanged := range []map[string]any{{}, {googlekey.ConfigKey: nil}, {googlekey.ConfigKey: file}} {
		if out, err := googlekey.Normalize(unchanged); err != nil || len(out) != len(unchanged) {
			t.Fatalf("%v -> %v, %v", unchanged, out, err)
		}
	}
	if _, err := googlekey.Normalize(map[string]any{googlekey.ConfigKey: 42}); err == nil {
		t.Fatal("a number was accepted as a key file")
	}
	if _, err := Parse("api", "apigateway", "", map[string]any{googlekey.ConfigKey: []any{"x"}}); err == nil {
		t.Fatal("Parse accepted a list as a key file")
	}
	if _, ok := googlekey.IdentityOf(map[string]any{googlekey.ConfigKey: 42}); ok {
		t.Fatal("an identity was read from a number")
	}
}

// Google answers an assertion whose scope claim names no access scope with a
// 200 carrying no access token, observed against oauth2.googleapis.com on the
// #2061 acceptance run; the error says to check the scopes.
func TestGoogleExchange_NoAccessTokenSaysCheckTheScopes(t *testing.T) {
	e := newTokenEndpoint(t)
	e.answer(http.StatusOK, `{"id_token":"eyJ.x.y"}`)
	a, err := NewAuthenticator(parseGoogle(t, map[string]any{
		googlekey.ConfigKey: googleKeyFile(t, e.url(), "k1"), connoauth.ConfigKeyScope: "https://www.googleapis.com/auth/no-such-scope",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyTo(t, a); err == nil || !strings.Contains(err.Error(), "check oauth_scope") {
		t.Fatalf("err = %v", err)
	}
	b, err := NewAuthenticator(jwtBearerConfig(e.url(), nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyTo(t, b); err == nil || strings.Contains(err.Error(), "check oauth_scope") {
		t.Fatalf("a non-Google connection = %v", err)
	}
}

func TestGrantDetailAndBindKeySecrets(t *testing.T) {
	if got := GrantDetail(noneAuth{}); got != "" {
		t.Fatalf("a mode that exchanges nothing reports %q", got)
	}
	e := newTokenEndpoint(t)
	c := parseGoogle(t, map[string]any{
		cfgKeyGoogleServiceAccountSecret: "dv360-key", connoauth.ConfigKeyScope: testGoogleScope, connoauth.ConfigKeyTokenURL: e.url(),
	})
	a, err := NewAuthenticator(c)
	if err != nil {
		t.Fatal(err)
	}
	if BindKeySecrets(a, nil, "dv360") {
		t.Fatal("a nil read was bound")
	}
	var bound string
	if !BindKeySecrets(a, func(_ context.Context, name, connection string) (string, error) {
		bound = name + "@" + connection
		return googleKeyFile(t, e.url(), "k1"), nil
	}, "dv360") {
		t.Fatal("the read was not bound")
	}
	if _, err := applyTo(t, a); err != nil {
		t.Fatal(err)
	}
	if bound != "dv360-key@dv360" {
		t.Fatalf("the read was asked for %q", bound)
	}
	// The endpoint reported no scope, so the requested ones are shown.
	if got := GrantDetail(a); got != "the token endpoint issued a token for "+testGoogleEmail+" with scopes "+testGoogleScope+"; " {
		t.Fatalf("GrantDetail = %q", got)
	}
	a2, err := NewAuthenticator(jwtBearerConfig(e.url(), func(c *Config) { c.OAuth2.Scopes = nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyTo(t, a2); err != nil {
		t.Fatal(err)
	}
	if got := GrantDetail(a2); !strings.HasSuffix(got, " with no scope; ") {
		t.Fatalf("GrantDetail with no scope = %q", got)
	}
}
