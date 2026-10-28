package whsource

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/net/http/httpguts"

	"github.com/txn2/mcp-data-platform/internal/hmacsig"
	"github.com/txn2/mcp-data-platform/internal/indexpolicy"
	"github.com/txn2/mcp-data-platform/internal/webhook/jsonpath"
)

// ErrInvalid wraps every refusal Validate returns, so a surface can answer 400
// for any of them and show the sentence as written.
var ErrInvalid = errors.New("invalid webhook source")

// refusal is a setting the operator has to change. Its text is the sentence
// that says which, and it is ErrInvalid to errors.Is.
type refusal struct{ msg string }

func (r refusal) Error() string { return r.msg }

func (refusal) Unwrap() error { return ErrInvalid }

// invalid builds a refusal naming the field the operator has to change.
func invalid(format string, args ...any) error {
	return refusal{msg: fmt.Sprintf(format, args...)}
}

// Refusal is invalid for a caller that refuses a source on grounds this
// package cannot see, such as a persona the platform does not define.
func Refusal(format string, args ...any) error {
	return invalid(format, args...)
}

// Validate refuses a source that could not be served as configured. It is
// called on the source with its defaults applied, so a zero setting has
// already been replaced and what is checked is what the receiver would use.
func Validate(s Source) error {
	if err := validateName(s.Name); err != nil {
		return err
	}
	if strings.TrimSpace(s.Connection) == "" {
		return invalid("connection is required: the Trino connection whose scratch catalog holds the source's table")
	}
	if s.Config.Input != InputForm {
		if err := refuseFormBlock(s); err != nil {
			return err
		}
	}
	switch s.Config.Input {
	case InputForm:
		if err := validateForm(s); err != nil {
			return err
		}
	case InputIMAP:
		if s.Auth.Secret == "" {
			return invalid("auth.secret is required: the mailbox's password")
		}
		if err := validateIMAP(s.Config.IMAP, s.Auth); err != nil {
			return err
		}
	case InputHTTP:
		if err := validateAuth(s.Auth); err != nil {
			return err
		}
	default:
		return invalid("config.input must be http or imap")
	}
	return validateConfig(s.Config)
}

// reservedName is the address the portal creates a source at,
// /admin/webhooks/new, which a source of that name could not be opened past.
const reservedName = "new"

// validateName checks the name is a path segment and a table-name suffix.
func validateName(name string) error {
	if len(name) > maxNameLength || !nameRe.MatchString(name) {
		return invalid("name must start with a lowercase letter and hold only lowercase letters, digits and \"-\", at most %d characters", maxNameLength)
	}
	if name == reservedName {
		return invalid("name %q is reserved", reservedName)
	}
	return nil
}

// validateAuth checks the mode and the fields that mode needs.
func validateAuth(a Auth) error {
	if a.Preset != "" && !KnownPreset(a.Preset) {
		return invalid("auth.preset must be one of %s", strings.Join(PresetNames(), ", "))
	}
	if a.Secret == "" {
		return invalid("auth.secret is required")
	}
	check, ok := modeChecks[a.Mode]
	if !ok {
		return invalid("auth.mode must be one of hmac, header_token, basic, path_token, ed25519, body_token or jwt")
	}
	return check(a)
}

// modeChecks are the settings each auth mode needs.
var modeChecks = map[string]func(Auth) error{
	AuthHMAC: validateHMAC,
	AuthHeaderToken: func(a Auth) error {
		if !validHeaderName(a.Header) {
			return invalid("auth.header must name the header the token is sent in")
		}
		return nil
	},
	AuthBasic: func(a Auth) error {
		if a.Username == "" || strings.Contains(a.Username, ":") {
			return invalid("auth.username is required and cannot contain \":\"")
		}
		return nil
	},
	AuthPathToken: func(a Auth) error {
		if strings.Contains(a.Secret, "/") {
			return invalid("auth.secret cannot contain \"/\" for path_token: it is sent as one path segment")
		}
		return nil
	},
	AuthEd25519: validateEd25519,
	AuthBodyToken: func(a Auth) error {
		if _, err := jsonpath.Parse(a.TokenPath); err != nil {
			return invalid("auth.token_path must name where in the body the token is: %v", err)
		}
		return nil
	},
	AuthJWT: func(a Auth) error {
		if !validHeaderName(a.Header) {
			return invalid("auth.header must name the header the token is sent in")
		}
		return nil
	},
}

// ed25519PublicKeyBytes is the length of an Ed25519 public key.
const ed25519PublicKeyBytes = 32

// validateEd25519 checks the public key and the settings the signature is read
// and checked with.
func validateEd25519(a Auth) error {
	if k, err := hex.DecodeString(a.Secret); err != nil || len(k) != ed25519PublicKeyBytes {
		return invalid("auth.secret must be the sender's Ed25519 public key, %d bytes in hex", ed25519PublicKeyBytes)
	}
	if !validHeaderName(a.SignatureHeader) {
		return invalid("auth.signature_header must name the header the signature is sent in")
	}
	if !hmacsig.ValidEncoding(a.Encoding) {
		return invalid("auth.encoding must be hex or base64")
	}
	tmpl, err := hmacsig.CompileTemplate(signedOrBody(a.Signed))
	if err != nil {
		return invalid("auth.signed: %v", err)
	}
	return validateTimestampSource(a, tmpl)
}

// validateHMAC checks the settings an HMAC source signs with.
func validateHMAC(a Auth) error {
	if !hmacsig.ValidAlgorithm(a.Algorithm) {
		return invalid("auth.algorithm must be sha256, sha1 or sha512")
	}
	if !validHeaderName(a.SignatureHeader) {
		return invalid("auth.signature_header must name the header the signature is sent in")
	}
	if !hmacsig.ValidEncoding(a.Encoding) {
		return invalid("auth.encoding must be hex or base64")
	}
	if !hmacsig.ValidFormat(a.HeaderFormat) {
		return invalid("auth.header_format must be empty or stripe")
	}
	if err := validateSecretEncoding(a); err != nil {
		return err
	}
	if err := validatePattern(a); err != nil {
		return err
	}
	if a.HeaderFormat == HeaderFormatStripe {
		if a.TimestampHeader != "" || a.IDHeader != "" || a.TimestampPath != "" {
			return invalid("auth.header_format stripe carries the timestamp in the signature header; " +
				"leave auth.timestamp_header, auth.timestamp_path and auth.id_header empty")
		}
		return validateTolerance(a)
	}
	tmpl, err := hmacsig.CompileTemplate(signedOrBody(a.Signed))
	if err != nil {
		return invalid("auth.signed must be body, timestamp.body, id.timestamp.body or a template over "+
			"{id}, {timestamp} and {body}: %v", err)
	}
	return validateTimestampSource(a, tmpl)
}

// validateSecretEncoding checks how the secret is read, and that a base64
// secret decodes.
func validateSecretEncoding(a Auth) error {
	if !hmacsig.ValidSecretEncoding(a.SecretEncoding) {
		return invalid("auth.secret_encoding must be empty or base64")
	}
	if a.SecretEncoding == SecretBase64 {
		if _, err := base64.StdEncoding.DecodeString(a.Secret); err != nil {
			return invalid("auth.secret is not base64, which auth.secret_encoding base64 reads it as")
		}
	}
	return nil
}

// validatePattern checks a header pattern, which replaces a prefix and a
// format.
func validatePattern(a Auth) error {
	if a.HeaderPattern == "" {
		return nil
	}
	if a.HeaderFormat != "" || a.Prefix != "" {
		return invalid("auth.header_pattern lays out the whole signature header; leave auth.header_format and auth.prefix empty")
	}
	if _, err := hmacsig.CompilePattern(a.HeaderPattern); err != nil {
		return invalid("auth.header_pattern: %v", err)
	}
	return nil
}

// validateTimestampSource checks that what the signed bytes name can be read
// from the request: the timestamp from exactly one of a header, the body or
// the signature header, and the delivery id from a header.
func validateTimestampSource(a Auth, tmpl hmacsig.Template) error {
	if err := validateSignedPlaces(a); err != nil {
		return err
	}
	sources := timestampSources(a)
	if sources > 1 {
		return invalid("the timestamp is read from one place: set one of auth.timestamp_header, " +
			"auth.timestamp_path or a header_pattern naming {timestamp}")
	}
	if tmpl.UsesTimestamp() && sources == 0 {
		return invalid("auth.signed %q signs a timestamp; set auth.timestamp_header, auth.timestamp_path "+
			"or a header_pattern naming {timestamp}", a.Signed)
	}
	if tmpl.UsesID() && a.IDHeader == "" {
		return invalid("auth.signed %q signs a delivery id; set auth.id_header", a.Signed)
	}
	return validateTolerance(a)
}

// validateSignedPlaces checks the headers and body path a signed timestamp
// and delivery id are read from.
func validateSignedPlaces(a Auth) error {
	if a.TimestampHeader != "" && !validHeaderName(a.TimestampHeader) {
		return invalid("auth.timestamp_header is not a valid header name")
	}
	if a.IDHeader != "" && !validHeaderName(a.IDHeader) {
		return invalid("auth.id_header is not a valid header name")
	}
	if a.TimestampPath != "" {
		p, err := jsonpath.Parse(a.TimestampPath)
		if err != nil || p.HasWildcard() {
			return invalid("auth.timestamp_path must name one place in the body")
		}
	}
	return nil
}

// timestampSources counts the places a source names to read its timestamp
// from: a header, the body, and the signature header's pattern.
func timestampSources(a Auth) int {
	n := 0
	for _, set := range []bool{
		a.TimestampHeader != "", a.TimestampPath != "", strings.Contains(a.HeaderPattern, hmacsig.PlaceholderTimestamp),
	} {
		if set {
			n++
		}
	}
	return n
}

// validateTolerance checks the replay window.
func validateTolerance(a Auth) error {
	if a.ToleranceSeconds < 0 {
		return invalid("auth.tolerance_seconds cannot be negative")
	}
	return nil
}

// signedOrBody is what a source signs, the body when it names nothing.
func signedOrBody(v string) string {
	if v == "" {
		return SignedBody
	}
	return v
}

// validHeaderName reports whether name is a header a request can carry.
func validHeaderName(name string) bool {
	return httpguts.ValidHeaderFieldName(name)
}

// validateConfig checks the body, buffer, rate and retention settings.
func validateConfig(c Config) error {
	switch c.Handshake {
	case HandshakeNone, HandshakeCloudEvents, HandshakeChallengeEcho, HandshakeZoom, HandshakeDiscord, HandshakeGraphValidation:
	default:
		return invalid("config.handshake must be none, cloudevents, challenge_echo, zoom, discord or graph_validation")
	}
	if strings.ContainsAny(c.ChallengeField, ".[]$") {
		return invalid("config.challenge_field must name a top-level field of the body")
	}
	if strings.TrimSpace(c.Persona) != c.Persona || strings.ContainsAny(c.Persona, "/ \t") {
		return invalid("config.persona must be a persona name")
	}
	if c.Index != "" {
		if _, err := indexpolicy.Parse(c.Index); err != nil {
			return invalid("config.index: %v", err)
		}
	}
	if c.MaxBodyBytes < 1 || c.MaxBodyBytes > maxBodyBytesCeiling {
		return invalid("config.max_body_bytes must be between 1 and %d", maxBodyBytesCeiling)
	}
	for field, path := range map[string]string{
		"split": c.Split, "event_id_path": c.EventIDPath,
		"event_type_path": c.EventTypePath, "key_path": c.KeyPath,
	} {
		if path == "" {
			continue
		}
		p, err := jsonpath.Parse(path)
		if err != nil {
			return invalid("config.%s: %v", field, err)
		}
		if p.HasWildcard() {
			return invalid("config.%s must name one place in the body; [*] is not allowed here", field)
		}
	}
	return validateLimits(c)
}

// minutesPerHour is what a compaction window must divide evenly.
const minutesPerHour = 60

// validateLimits checks the numeric settings against their bounds, in the
// order they are listed so the first broken one is the one named.
func validateLimits(c Config) error {
	for _, l := range []struct {
		broken bool
		msg    string
	}{
		{c.FlushMaxEvents < 1, "config.flush_max_events must be at least 1"},
		{
			c.FlushInterval() <= 0 || c.FlushInterval() > maxFlushIntervalCeiling,
			fmt.Sprintf("config.flush_max_interval_ms must be between 1 and %d", maxFlushIntervalCeiling.Milliseconds()),
		},
		{
			c.FlushMaxBytes < 1 || c.FlushMaxBytes > maxFlushBytesCeiling,
			fmt.Sprintf("config.flush_max_bytes must be between 1 and %d", maxFlushBytesCeiling),
		},
		{
			c.BufferLimit < 1 || c.BufferLimit > maxBufferLimitCeiling,
			fmt.Sprintf("config.buffer_limit must be between 1 and %d", maxBufferLimitCeiling),
		},
		{
			c.RateLimitPerMinute < 0 || c.RateLimitBurst < 0,
			"config.rate_limit_per_minute and rate_limit_burst cannot be negative",
		},
		{
			c.CompactEveryMinutes < 1 || c.CompactEveryMinutes > minutesPerHour || minutesPerHour%c.CompactEveryMinutes != 0,
			"config.compact_every_minutes must divide an hour evenly: 1, 2, 3, 4, 5, 6, 10, 12, 15, 20, 30 or 60",
		},
		{c.RawRetentionDays < 1, "config.raw_retention_days must be at least 1"},
		{
			c.CompactedRetentionDays != nil && *c.CompactedRetentionDays < 0,
			"config.compacted_retention_days cannot be negative; 0 keeps compacted windows forever",
		},
	} {
		if l.broken {
			return invalid("%s", l.msg)
		}
	}
	return nil
}
