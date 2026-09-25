package whsource

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/net/http/httpguts"

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
	if err := validateAuth(s.Auth); err != nil {
		return err
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
	if a.Secret == "" {
		return invalid("auth.secret is required")
	}
	check, ok := modeChecks[a.Mode]
	if !ok {
		return invalid("auth.mode must be one of hmac, header_token, basic or path_token")
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
}

// validateHMAC checks the settings an HMAC source signs with.
func validateHMAC(a Auth) error {
	if a.Algorithm != AlgorithmSHA256 && a.Algorithm != AlgorithmSHA1 {
		return invalid("auth.algorithm must be sha256 or sha1")
	}
	if !validHeaderName(a.SignatureHeader) {
		return invalid("auth.signature_header must name the header the signature is sent in")
	}
	if a.Encoding != EncodingHex && a.Encoding != EncodingBase64 {
		return invalid("auth.encoding must be hex or base64")
	}
	return validateTimestamp(a)
}

// validateTimestamp checks the replay window an HMAC source may declare.
func validateTimestamp(a Auth) error {
	if a.TimestampHeader != "" && !validHeaderName(a.TimestampHeader) {
		return invalid("auth.timestamp_header is not a valid header name")
	}
	switch a.Signed {
	case SignedBody:
	case SignedTimestampBody:
		if a.TimestampHeader == "" {
			return invalid("auth.signed timestamp.body needs auth.timestamp_header")
		}
	default:
		return invalid("auth.signed must be body or timestamp.body")
	}
	if a.ToleranceSeconds < 0 {
		return invalid("auth.tolerance_seconds cannot be negative")
	}
	return nil
}

// validHeaderName reports whether name is a header a request can carry.
func validHeaderName(name string) bool {
	return httpguts.ValidHeaderFieldName(name)
}

// validateConfig checks the body, buffer, rate and retention settings.
func validateConfig(c Config) error {
	if c.Handshake != HandshakeNone && c.Handshake != HandshakeCloudEvents {
		return invalid("config.handshake must be none or cloudevents")
	}
	if strings.TrimSpace(c.Persona) != c.Persona || strings.ContainsAny(c.Persona, "/ \t") {
		return invalid("config.persona must be a persona name")
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
		if _, err := jsonpath.Parse(path); err != nil {
			return invalid("config.%s: %v", field, err)
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
