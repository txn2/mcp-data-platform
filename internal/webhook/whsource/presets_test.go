package whsource

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sender is a valid http source authenticated as a.
func sender(a Auth) Source {
	return Source{Name: "s", Connection: "c", Auth: a}.WithDefaults()
}

func TestEveryPresetValidates(t *testing.T) {
	key := hex.EncodeToString(make([]byte, ed25519PublicKeyBytes))
	for _, name := range PresetNames() {
		secret := "c2VjcmV0"
		if name == PresetDiscord {
			secret = key
		}
		src := sender(Auth{Preset: name, Secret: secret})
		require.NoError(t, Validate(src), name)
		assert.True(t, KnownPreset(name))
		assert.Equal(t, sourcePresets[name].auth.Mode, src.Auth.Mode, name)
	}
	assert.False(t, KnownPreset("svix"))
	assert.ErrorIs(t, Validate(sender(Auth{Preset: "svix", Secret: "s", Mode: AuthHMAC, SignatureHeader: "X"})), ErrInvalid)
}

func TestPresetFillsWhatTheSourceLeavesUnset(t *testing.T) {
	src := sender(Auth{Preset: PresetSlack, Secret: "s", ToleranceSeconds: 60, Mode: AuthBasic})
	assert.Equal(t, AuthHMAC, src.Auth.Mode, "a preset's mode replaces the source's")
	assert.Equal(t, "X-Slack-Signature", src.Auth.SignatureHeader)
	assert.Equal(t, "v0:{timestamp}:{body}", src.Auth.Signed)
	assert.Equal(t, 60, src.Auth.ToleranceSeconds, "a setting the source sets is kept")
	assert.Equal(t, HandshakeChallengeEcho, src.Config.Handshake)

	cloud := Source{Name: "s", Connection: "c", Auth: Auth{Preset: PresetSlack, Secret: "s"}, Config: Config{Handshake: HandshakeCloudEvents}}.WithDefaults()
	assert.Equal(t, HandshakeCloudEvents, cloud.Config.Handshake, "a handshake the source chose is kept")

	linear := sender(Auth{Preset: PresetLinear, Secret: "s"})
	assert.Equal(t, "webhookTimestamp", linear.Auth.TimestampPath)
	assert.Equal(t, linearToleranceSeconds, linear.Auth.ToleranceSeconds)

	graph := sender(Auth{Preset: PresetMicrosoftGraph, Secret: "s"})
	assert.Equal(t, HandshakeGraphValidation, graph.Config.Handshake)
	assert.Equal(t, DefaultJWTHeader, sender(Auth{Preset: PresetMonday, Secret: "s"}).Auth.Header)
	assert.Equal(t, DefaultJWTHeader, sender(Auth{Mode: AuthJWT, Secret: "s"}).Auth.Header)
}

func TestValidateRefusesBadHeaderPatterns(t *testing.T) {
	for pattern, why := range map[string]string{
		"{signature}{timestamp}":  "two placeholders with no text between them",
		"{timestamp}|{timestamp}": "more than once",
		"{nonce}|{signature}":     "{nonce}",
	} {
		err := Validate(sender(Auth{
			Mode: AuthHMAC, Secret: "s", SignatureHeader: "X", HeaderPattern: pattern, Signed: "{body}|{timestamp}",
		}))
		require.ErrorIs(t, err, ErrInvalid, pattern)
		assert.Contains(t, err.Error(), "auth.header_pattern", pattern)
		assert.Contains(t, err.Error(), why, pattern)
	}
}

func TestValidateSenderSettings(t *testing.T) {
	hm := func(mut func(*Auth)) error {
		a := Auth{Mode: AuthHMAC, Secret: "s", SignatureHeader: "X"}
		mut(&a)
		return Validate(sender(a))
	}
	for name, tc := range map[string]struct {
		mut  func(*Auth)
		want string
	}{
		"pattern with prefix":  {func(a *Auth) { a.HeaderPattern = "{signature}"; a.Prefix = "v0=" }, "leave auth.header_format and auth.prefix empty"},
		"bad template":         {func(a *Auth) { a.Signed = "{nonce}.{body}" }, "auth.signed must be"},
		"timestamp unread":     {func(a *Auth) { a.Signed = "v0:{timestamp}:{body}" }, "signs a timestamp"},
		"two timestamp places": {func(a *Auth) { a.TimestampHeader = "X-T"; a.TimestampPath = "ts" }, "read from one place"},
		"id unread":            {func(a *Auth) { a.Signed = "{id}.{body}" }, "set auth.id_header"},
		"wildcard timestamp":   {func(a *Auth) { a.TimestampPath = "items[*].ts" }, "one place in the body"},
		"base64 secret":        {func(a *Auth) { a.SecretEncoding = SecretBase64; a.Secret = "not base64!" }, "is not base64"},
		"secret encoding":      {func(a *Auth) { a.SecretEncoding = "hex" }, "auth.secret_encoding"},
		"stripe with path":     {func(a *Auth) { a.HeaderFormat = HeaderFormatStripe; a.TimestampPath = "t" }, "auth.timestamp_path"},
		"bad timestamp header": {func(a *Auth) { a.TimestampHeader = "bad header"; a.Signed = SignedTimestampBody }, "not a valid header name"},
		"bad id header":        {func(a *Auth) { a.TimestampHeader = "T"; a.IDHeader = "bad id"; a.Signed = SignedIDTimestampBody }, "not a valid header name"},
	} {
		err := hm(tc.mut)
		require.ErrorIs(t, err, ErrInvalid, name)
		assert.Contains(t, err.Error(), tc.want, name)
	}
	assert.NoError(t, hm(func(a *Auth) { a.TimestampPath = "ts"; a.Signed = "{timestamp}.{body}" }))
}

func TestValidateNewModes(t *testing.T) {
	key := hex.EncodeToString(make([]byte, ed25519PublicKeyBytes))
	for name, tc := range map[string]struct {
		auth Auth
		want string
	}{
		"ed25519 short key":   {Auth{Mode: AuthEd25519, Secret: "abcd", SignatureHeader: "X"}, "Ed25519 public key"},
		"ed25519 no header":   {Auth{Mode: AuthEd25519, Secret: key}, "auth.signature_header"},
		"ed25519 encoding":    {Auth{Mode: AuthEd25519, Secret: key, SignatureHeader: "X", Encoding: "base32"}, "auth.encoding"},
		"ed25519 template":    {Auth{Mode: AuthEd25519, Secret: key, SignatureHeader: "X", Signed: "{nonce}"}, "auth.signed"},
		"ed25519 needs ts":    {Auth{Mode: AuthEd25519, Secret: key, SignatureHeader: "X", Signed: "{timestamp}{body}"}, "signs a timestamp"},
		"body token no path":  {Auth{Mode: AuthBodyToken, Secret: "s"}, "auth.token_path"},
		"jwt bad header":      {Auth{Mode: AuthJWT, Secret: "s", Header: "bad header"}, "auth.header"},
		"unknown mode listed": {Auth{Mode: "telepathy", Secret: "s"}, "ed25519, body_token or jwt"},
	} {
		err := Validate(sender(tc.auth))
		require.ErrorIs(t, err, ErrInvalid, name)
		assert.Contains(t, err.Error(), tc.want, name)
	}
	assert.NoError(t, Validate(sender(Auth{Mode: AuthEd25519, Secret: key, SignatureHeader: "X"})))
	assert.NoError(t, Validate(sender(Auth{Mode: AuthBodyToken, Secret: "s", TokenPath: "value[*].clientState"})))
}

func TestValidateHandshakesAndPaths(t *testing.T) {
	base := func(c Config) Source {
		return Source{Name: "s", Connection: "c", Auth: Auth{Mode: AuthHeaderToken, Secret: "s", Header: "X"}, Config: c}.WithDefaults()
	}
	for _, h := range []string{HandshakeChallengeEcho, HandshakeZoom, HandshakeDiscord, HandshakeGraphValidation} {
		assert.NoError(t, Validate(base(Config{Handshake: h})), h)
	}
	err := Validate(base(Config{Handshake: "smoke_signal"}))
	require.ErrorIs(t, err, ErrInvalid)
	assert.True(t, strings.Contains(err.Error(), "graph_validation"))
	assert.ErrorIs(t, Validate(base(Config{ChallengeField: "$.a"})), ErrInvalid)
	err = Validate(base(Config{EventIDPath: "items[*].id"}))
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "[*] is not allowed here")
	assert.Equal(t, DefaultChallengeField, Config{}.Challenge())
	assert.Equal(t, "token", Config{ChallengeField: "token"}.Challenge())
}
