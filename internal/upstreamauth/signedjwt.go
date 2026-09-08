package upstreamauth

import (
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/txn2/mcp-data-platform/internal/cfgmap"
)

// AuthModeSignedJWT mints the short-lived JWT the upstream validates,
// from an identifier and a signing key the operator was issued out of
// band, and sends it as "Authorization: Bearer <jwt>".
//
// The distinguishing property of this class of upstream is that there
// is no token endpoint: nothing is exchanged, the client is the issuer,
// and the token is valid for minutes. That is why neither "bearer" (a
// fixed string cannot carry a 300-second expiry) nor "oauth" (there is
// no endpoint to exchange against) can reach one. Sage X3's connected
// applications, Snowflake key-pair authentication, and Apple's App
// Store Connect and APNs keys are all this shape, as is the long tail
// of internal services that issue a client id and a shared secret.
const AuthModeSignedJWT = "signed_jwt"

// The signing algorithms the mode supports. The algorithm selects both
// the JWA signing method and the key material the config must carry:
// HS256 signs with the shared secret, RS256 and ES256 with the PEM
// private key.
const (
	// SignedJWTAlgHS256 signs with jwt_client_secret (HMAC-SHA256).
	SignedJWTAlgHS256 = "HS256"
	// SignedJWTAlgRS256 signs with an RSA jwt_private_key_pem.
	SignedJWTAlgRS256 = "RS256"
	// SignedJWTAlgES256 signs with an ECDSA P-256 jwt_private_key_pem.
	SignedJWTAlgES256 = "ES256"
)

const (
	// DefaultSignedJWTTokenLifetime is exp - iat when the connection
	// sets no jwt_token_lifetime. Five minutes matches the default
	// lifetime the upstreams in this class hand out.
	DefaultSignedJWTTokenLifetime = 5 * time.Minute
	// DefaultSignedJWTIssuedAtSkew is how far back iat is set when the
	// connection sets no jwt_issued_at_skew. An upstream whose clock
	// runs behind the platform's refuses a token whose iat is in its
	// future, so the claim is backdated by default rather than on
	// request.
	DefaultSignedJWTIssuedAtSkew = 30 * time.Second
)

// cfgKeyJWT* name the connection-config keys this mode reads. They
// carry the jwt_ prefix for the same reason the OAuth and mTLS blocks
// carry theirs: the config map is shared by every HTTP-based connection
// kind, and "issuer" or "audience" as a bare top-level key would claim
// a name another concern will want.
const (
	cfgKeyJWTAlgorithm     = "jwt_algorithm"
	cfgKeyJWTClientSecret  = "jwt_client_secret"   // #nosec G101 -- map key, not a credential
	cfgKeyJWTPrivateKeyPEM = "jwt_private_key_pem" // #nosec G101 -- map key, not a credential
	cfgKeyJWTKeyID         = "jwt_key_id"
	cfgKeyJWTIssuer        = "jwt_issuer"
	cfgKeyJWTSubject       = "jwt_subject"
	cfgKeyJWTAudience      = "jwt_audience"
	cfgKeyJWTTokenLifetime = "jwt_token_lifetime" // #nosec G101 -- map key, not a credential
	cfgKeyJWTIssuedAtSkew  = "jwt_issued_at_skew"
)

// SignedJWTConfig describes the assertion the platform mints for an
// upstream that validates a client-minted JWT.
//
// The claims are stated rather than derived because each upstream
// registered exact values out of band and checks them literally: a
// connected application in Sage X3 pins the issuer to the client id it
// generated and the audience to the API URL byte for byte, and answers
// a mismatch with a 401 naming the claim.
type SignedJWTConfig struct {
	// Algorithm is one of SignedJWTAlgHS256 (the default),
	// SignedJWTAlgRS256 or SignedJWTAlgES256. It selects the signing
	// method and which key field is required.
	Algorithm string
	// ClientSecret is the HMAC shared secret. Required for HS256 and
	// refused for the asymmetric algorithms. Encrypted at rest via the
	// platform's FieldEncryptor.
	ClientSecret string
	// PrivateKeyPEM is the PEM-encoded private key (PKCS#1 or PKCS#8
	// for RSA, SEC 1 or PKCS#8 for ECDSA). Required for RS256 and
	// ES256 and refused for HS256. Encrypted at rest via the
	// platform's FieldEncryptor.
	PrivateKeyPEM string
	// KeyID is emitted as the token's "kid" header when set. Upstreams
	// that hold several registered keys for one account select by it.
	// Optional; no header is emitted when empty.
	KeyID string
	// Issuer is the "iss" claim, and Subject the "sub" claim. Each is
	// omitted from the token when empty, because an upstream that
	// registered no value for one rejects a token that carries it: a
	// Sage X3 connected application checks both, an Apple App Store
	// Connect team key checks iss alone, and an Apple individual key
	// checks sub alone. At least one must be set — a token that
	// identifies nobody is not one any upstream in this class accepts.
	Issuer  string
	Subject string
	// Audience is the "aud" claim. Defaults to the connection's
	// endpoint URL at parse time; the value must match what the
	// upstream registered, byte for byte.
	Audience string
	// TokenLifetime is exp - iat. Defaults to
	// DefaultSignedJWTTokenLifetime.
	TokenLifetime time.Duration
	// IssuedAtSkew is how far back iat is set to absorb clock drift
	// between the platform and the upstream. It is also the renewal
	// margin: a cached assertion is abandoned once it is this close to
	// expiry, so one is never presented so near exp that the upstream's
	// clock could have passed it in flight. Setting it to zero is an
	// operator stating that the two clocks agree, and gives up the
	// margin as well as the backdating. Defaults to
	// DefaultSignedJWTIssuedAtSkew and must be under TokenLifetime —
	// a skew at or past the lifetime mints a token that is already
	// expired.
	IssuedAtSkew time.Duration
}

// parseSignedJWT reads the mode's keys out of a connection's config map
// and applies the defaults. audience falls back to the connection's
// endpoint URL, which is what the upstreams in this class register when
// the operator does not choose something else.
func parseSignedJWT(endpointURL string, cfg map[string]any) SignedJWTConfig {
	return SignedJWTConfig{
		Algorithm:     cfgmap.StringDefault(cfg, cfgKeyJWTAlgorithm, SignedJWTAlgHS256),
		ClientSecret:  cfgmap.String(cfg, cfgKeyJWTClientSecret),
		PrivateKeyPEM: cfgmap.String(cfg, cfgKeyJWTPrivateKeyPEM),
		KeyID:         cfgmap.String(cfg, cfgKeyJWTKeyID),
		Issuer:        cfgmap.String(cfg, cfgKeyJWTIssuer),
		Subject:       cfgmap.String(cfg, cfgKeyJWTSubject),
		Audience:      cfgmap.StringDefault(cfg, cfgKeyJWTAudience, endpointURL),
		TokenLifetime: cfgmap.Duration(cfg, cfgKeyJWTTokenLifetime, DefaultSignedJWTTokenLifetime),
		IssuedAtSkew:  cfgmap.Duration(cfg, cfgKeyJWTIssuedAtSkew, DefaultSignedJWTIssuedAtSkew),
	}
}

// validateSignedJWTAuth enforces the mode's config rules. Every refusal
// names the offending key, because the operator's next action is to fix
// that field in the connection form.
func (c Config) validateSignedJWTAuth() error {
	j := c.SignedJWT
	if _, err := c.signedJWTSigningKey(); err != nil {
		return err
	}
	if j.Issuer == "" && j.Subject == "" {
		return c.errf("one of %s or %s is required when auth_mode is %q: which one depends on what the upstream registered",
			cfgKeyJWTIssuer, cfgKeyJWTSubject, AuthModeSignedJWT)
	}
	if j.Audience == "" {
		return c.errf("%s is required when auth_mode is %q and the connection has no endpoint URL to default it from", cfgKeyJWTAudience, AuthModeSignedJWT)
	}
	if j.TokenLifetime <= 0 {
		return c.errf("%s must be positive", cfgKeyJWTTokenLifetime)
	}
	if j.IssuedAtSkew < 0 {
		return c.errf("%s must not be negative", cfgKeyJWTIssuedAtSkew)
	}
	if j.IssuedAtSkew >= j.TokenLifetime {
		return c.errf("%s (%s) must be under %s (%s): backdating iat by the whole lifetime mints a token that is already expired",
			cfgKeyJWTIssuedAtSkew, j.IssuedAtSkew, cfgKeyJWTTokenLifetime, j.TokenLifetime)
	}
	return nil
}

// signedJWTSigningKey resolves the algorithm to its signing method and
// the key material it requires, refusing key material that does not
// match the algorithm and a PEM the algorithm cannot parse.
//
// It is the one place the algorithm-to-key-material rule is written, so
// validation at connection-save time and construction at first call
// cannot come to disagree about which config is usable.
func (c Config) signedJWTSigningKey() (signedJWTSigner, error) {
	j := c.SignedJWT
	switch j.Algorithm {
	case SignedJWTAlgHS256:
		if j.ClientSecret == "" {
			return signedJWTSigner{}, c.errf("%s is required when %s is %q", cfgKeyJWTClientSecret, cfgKeyJWTAlgorithm, SignedJWTAlgHS256)
		}
		if j.PrivateKeyPEM != "" {
			return signedJWTSigner{}, c.errf("%s is not used when %s is %q; %s carries the shared secret", cfgKeyJWTPrivateKeyPEM, cfgKeyJWTAlgorithm, SignedJWTAlgHS256, cfgKeyJWTClientSecret)
		}
		return signedJWTSigner{method: jwt.SigningMethodHS256, key: []byte(j.ClientSecret)}, nil
	case SignedJWTAlgRS256, SignedJWTAlgES256:
		return c.signedJWTAsymmetricKey()
	default:
		return signedJWTSigner{}, c.errf("invalid %s %q (want %s, %s or %s)",
			cfgKeyJWTAlgorithm, j.Algorithm, SignedJWTAlgHS256, SignedJWTAlgRS256, SignedJWTAlgES256)
	}
}

// signedJWTAsymmetricKey is the RS256/ES256 half of signedJWTSigningKey,
// split out so each half stays within the cyclomatic-complexity ceiling.
func (c Config) signedJWTAsymmetricKey() (signedJWTSigner, error) {
	j := c.SignedJWT
	if j.PrivateKeyPEM == "" {
		return signedJWTSigner{}, c.errf("%s is required when %s is %q", cfgKeyJWTPrivateKeyPEM, cfgKeyJWTAlgorithm, j.Algorithm)
	}
	if j.ClientSecret != "" {
		return signedJWTSigner{}, c.errf("%s is not used when %s is %q; %s carries the signing key", cfgKeyJWTClientSecret, cfgKeyJWTAlgorithm, j.Algorithm, cfgKeyJWTPrivateKeyPEM)
	}
	if j.Algorithm == SignedJWTAlgRS256 {
		key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(j.PrivateKeyPEM))
		if err != nil {
			return signedJWTSigner{}, c.errf("%s is not a usable %s private key: %v", cfgKeyJWTPrivateKeyPEM, SignedJWTAlgRS256, err)
		}
		return signedJWTSigner{method: jwt.SigningMethodRS256, key: key}, nil
	}
	key, err := jwt.ParseECPrivateKeyFromPEM([]byte(j.PrivateKeyPEM))
	if err != nil {
		return signedJWTSigner{}, c.errf("%s is not a usable %s private key: %v", cfgKeyJWTPrivateKeyPEM, SignedJWTAlgES256, err)
	}
	return signedJWTSigner{method: jwt.SigningMethodES256, key: key}, nil
}

// signedJWTSigner pairs the JWA signing method with the key material it
// signs with.
type signedJWTSigner struct {
	method jwt.SigningMethod
	key    any
}

// signedJWTAuth mints and caches the assertion. The cached token is
// reused until it is within IssuedAtSkew of its own expiry, so a token
// is never presented so close to exp that the upstream's clock could
// have passed it in flight.
//
// SECURITY: no field of this struct is ever formatted into a message.
// The signing key and the shared secret are held only to sign with, and
// the errors Apply returns name the failing step, never the material.
type signedJWTAuth struct {
	cfg    Config
	signer signedJWTSigner

	// now is time.Now in production and a stub in tests, which is the
	// only way to observe the cache boundary without sleeping through a
	// real lifetime.
	now func() time.Time

	// mu guards the cached token against the concurrent Apply calls one
	// shared Authenticator serves.
	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// newSignedJWTAuth re-runs the mode's validation and resolves the
// signing material once, so a connection whose config cannot mint a
// usable assertion fails at construction rather than on every outbound
// call. ValidateAuth has already applied the same rules at
// connection-save time; they are repeated here as defense in depth
// against a caller that hand-builds a Config and bypasses Parse.
func newSignedJWTAuth(c Config) (*signedJWTAuth, error) {
	if err := c.validateSignedJWTAuth(); err != nil {
		return nil, err
	}
	signer, err := c.signedJWTSigningKey()
	if err != nil {
		return nil, err
	}
	return &signedJWTAuth{cfg: c, signer: signer, now: time.Now}, nil
}

// Apply mints (or reuses) the assertion and attaches it as the
// Authorization header.
func (a *signedJWTAuth) Apply(req *http.Request) error {
	token, err := a.assertion()
	if err != nil {
		return err
	}
	req.Header.Set(AuthorizationHeader, "Bearer "+token)
	return nil
}

// assertion returns the cached token when it is still comfortably
// valid, and mints a new one otherwise.
func (a *signedJWTAuth) assertion() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if a.token != "" && now.Add(a.cfg.SignedJWT.IssuedAtSkew).Before(a.expiresAt) {
		return a.token, nil
	}
	token, expiresAt, err := a.mint(now)
	if err != nil {
		return "", err
	}
	a.token, a.expiresAt = token, expiresAt
	return token, nil
}

// mint builds and signs one assertion. iat is set back by IssuedAtSkew
// and exp is IssuedAtSkew + TokenLifetime ahead of it, so exp - iat is
// exactly the configured lifetime whatever the skew.
func (a *signedJWTAuth) mint(now time.Time) (token string, expiresAt time.Time, err error) {
	j := a.cfg.SignedJWT
	issuedAt := now.Add(-j.IssuedAtSkew)
	expiry := issuedAt.Add(j.TokenLifetime)
	claims := jwt.MapClaims{
		"aud": j.Audience,
		"iat": issuedAt.Unix(),
		"exp": expiry.Unix(),
	}
	// An upstream that registered no issuer or no subject refuses a
	// token carrying the claim, so an unset field omits it rather than
	// sending it empty. Validation has already refused a config that
	// sets neither.
	if j.Issuer != "" {
		claims["iss"] = j.Issuer
	}
	if j.Subject != "" {
		claims["sub"] = j.Subject
	}
	t := jwt.NewWithClaims(a.signer.method, claims)
	if j.KeyID != "" {
		t.Header["kid"] = j.KeyID
	}
	signed, signErr := t.SignedString(a.signer.key)
	if signErr != nil {
		// The error is stated without the library's message: a future
		// signing method could format the key into it, and this path
		// reaches the model.
		return "", time.Time{}, a.cfg.errf("%s: signing the assertion failed", AuthModeSignedJWT)
	}
	return signed, expiry, nil
}
