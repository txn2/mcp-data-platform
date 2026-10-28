// Package whsource defines an inbound webhook source: the administrator-managed
// record that says who may post to /hooks/{name}, how a request proves it, how
// the body becomes events, and how long what lands is kept (#1870).
//
// It holds the model, its defaults and validation, and the PostgreSQL store.
// The receiver, the compactor and the admin API all read a Source through this
// package; none of them owns its shape.
package whsource

import (
	"regexp"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/hmacsig"
)

// Auth modes a source authenticates a request with.
const (
	// AuthHMAC checks a signature over the body, or over timestamp + "." +
	// body, carried in a header.
	AuthHMAC = "hmac"
	// AuthHeaderToken compares a header's value with the secret.
	AuthHeaderToken = "header_token"
	// AuthBasic checks HTTP Basic credentials: a username and the secret as
	// the password.
	AuthBasic = "basic"
	// AuthPathToken takes the secret as an extra path segment,
	// /hooks/{name}/{token}, for a sender that can set nothing but a URL.
	AuthPathToken = "path_token"
	// AuthEd25519 checks an Ed25519 signature over the signed bytes with the
	// sender's public key, which the source stores as its secret, in hex
	// (#2223: Discord).
	AuthEd25519 = "ed25519"
	// AuthBodyToken compares a value read from the body, at TokenPath, with
	// the secret (#2223: Mattermost, Microsoft Graph's clientState).
	AuthBodyToken = "body_token"
	// AuthJWT checks an HS256 JSON Web Token in a header, signed with the
	// secret, honoring its exp (#2223: monday.com).
	AuthJWT = "jwt"
)

// SecretBase64 reads an hmac source's secret as base64, the key being the
// bytes it decodes to.
const SecretBase64 = hmacsig.SecretBase64

// DefaultJWTHeader is the header a jwt source reads its token from.
const DefaultJWTHeader = "Authorization"

// HMAC settings a source chooses between. They are hmacsig's, which the
// platform's outbound hmac connections sign with too, so a source and a
// connection configured with the same values agree (#1996).
const (
	AlgorithmSHA256 = hmacsig.AlgorithmSHA256
	AlgorithmSHA1   = hmacsig.AlgorithmSHA1
	AlgorithmSHA512 = hmacsig.AlgorithmSHA512

	EncodingHex    = hmacsig.EncodingHex
	EncodingBase64 = hmacsig.EncodingBase64

	// SignedBody signs the raw body.
	SignedBody = hmacsig.SignedBody
	// SignedTimestampBody signs the timestamp header's value, a ".", and
	// the raw body, which is what makes a replayed request with an old
	// timestamp fail even though its signature once verified.
	SignedTimestampBody = hmacsig.SignedTimestampBody
	// SignedIDTimestampBody signs the id header's value, a ".", the
	// timestamp, a ".", and the body: the Standard Webhooks form.
	SignedIDTimestampBody = hmacsig.SignedIDTimestampBody

	// HeaderFormatStripe reads the signature header as Stripe writes it,
	// "t=<timestamp>,v1=<signature>", with the timestamp inside it.
	HeaderFormatStripe = hmacsig.FormatStripe
)

// Handshakes a source can answer.
const (
	HandshakeNone = "none"
	// HandshakeCloudEvents answers the CloudEvents HTTP webhook
	// abuse-protection OPTIONS request, which a sender that implements it
	// sends before it delivers anything.
	HandshakeCloudEvents = "cloudevents"
	// HandshakeChallengeEcho answers a JSON body carrying a challenge with
	// that challenge, before verification and before anything is stored
	// (#2223: Slack's url_verification, monday.com).
	HandshakeChallengeEcho = "challenge_echo"
	// HandshakeZoom answers Zoom's endpoint.url_validation with the plain
	// token and its HMAC under the source's secret. The request is verified
	// first: an unverified answer would hand anyone the HMAC of any text.
	HandshakeZoom = "zoom"
	// HandshakeDiscord answers a verified PING with a PONG.
	HandshakeDiscord = "discord"
	// HandshakeGraphValidation answers a request carrying a validationToken
	// query parameter with the token, as plain text (Microsoft Graph).
	HandshakeGraphValidation = "graph_validation"
)

// DefaultChallengeField is the body field a challenge_echo source reads.
const DefaultChallengeField = "challenge"

// Defaults a source gets for every setting it leaves at zero.
const (
	DefaultMaxBodyBytes        int64 = 1 << 20
	DefaultFlushMaxEvents            = 5000
	DefaultFlushMaxInterval          = time.Second
	DefaultFlushMaxBytes       int64 = 8 << 20
	DefaultBufferLimit               = 50000
	DefaultRawRetentionDays          = 1
	DefaultCompactEveryMinutes       = 60
	DefaultCompactedDays             = 400
	DefaultTolerance                 = 5 * time.Minute
)

// day is what a retention setting counts in.
const day = 24 * time.Hour

// Bounds a setting is refused outside of. They keep one source from
// configuring the receiver into holding more than a replica can.
const (
	maxBodyBytesCeiling     int64 = 64 << 20
	maxFlushBytesCeiling    int64 = 256 << 20
	maxBufferLimitCeiling         = 1_000_000
	maxFlushIntervalCeiling       = time.Minute
	maxNameLength                 = 63
)

// nameRe is the shape of a source name: the path segment it is posted to, and
// the suffix of its table's name with each "-" read as "_". Underscores are
// not allowed in the name, so two names can never map to one table.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Source is one inbound webhook source.
type Source struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Auth    Auth   `json:"auth"`
	Config  Config `json:"config"`
	// Connection is the Trino connection whose scratch catalog holds the
	// source's table. That catalog must read the managed-resources store,
	// because both the raw segments and the compacted windows are written
	// there.
	Connection string    `json:"connection"`
	CreatedBy  string    `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Auth is how a request to the source proves it came from the sender.
//
// Secret and PreviousSecret are plaintext in memory and encrypted at rest; the
// admin API never returns either. PreviousSecret is accepted until
// PreviousUntil, which is how a secret is rotated without a window in which
// the sender's requests fail.
type Auth struct {
	Mode string `json:"mode"`
	// Preset names a sender's whole convention (#2223); the settings it
	// leaves unset are filled from it.
	Preset         string    `json:"preset,omitempty"`
	Secret         string    `json:"secret,omitempty"`
	PreviousSecret string    `json:"previous_secret,omitempty"`
	PreviousUntil  time.Time `json:"previous_until,omitzero"`

	// HMAC.
	Algorithm        string `json:"algorithm,omitempty"`
	SignatureHeader  string `json:"signature_header,omitempty"`
	Encoding         string `json:"encoding,omitempty"`
	Prefix           string `json:"prefix,omitempty"`
	TimestampHeader  string `json:"timestamp_header,omitempty"`
	ToleranceSeconds int    `json:"tolerance_seconds,omitempty"`
	Signed           string `json:"signed,omitempty"`
	// IDHeader carries the delivery id id.timestamp.body signs.
	IDHeader string `json:"id_header,omitempty"`
	// HeaderFormat is empty for a prefix and a signature, or "stripe".
	HeaderFormat string `json:"header_format,omitempty"`
	// HeaderPattern lays the signature header out over {signature} and
	// {timestamp}, as in "{timestamp}|{signature}" (#2223).
	HeaderPattern string `json:"header_pattern,omitempty"`
	// SecretEncoding is empty for the secret as written, or "base64".
	SecretEncoding string `json:"secret_encoding,omitempty"`
	// TimestampPath reads the timestamp from the body rather than a header,
	// in seconds or milliseconds (Linear's webhookTimestamp).
	TimestampPath string `json:"timestamp_path,omitempty"`

	// BodyToken: where in the body the token is. A path through "[*]"
	// requires every element to carry it.
	TokenPath string `json:"token_path,omitempty"`

	// HeaderToken.
	Header string `json:"header,omitempty"`

	// Basic.
	Username string `json:"username,omitempty"`
}

// Config is everything about a source other than its authentication.
type Config struct {
	// Input is "http" (the default, posted to), "imap" (a polled mailbox,
	// configured under IMAP) or "form" (a form's submissions, under Form).
	Input string `json:"input,omitempty"`
	IMAP  *IMAP  `json:"imap,omitempty"`
	Form  *Form  `json:"form,omitempty"`

	Handshake string `json:"handshake,omitempty"`
	// ChallengeField is the body field a challenge_echo handshake reads;
	// empty is "challenge".
	ChallengeField string `json:"challenge_field,omitempty"`
	MaxBodyBytes   int64  `json:"max_body_bytes,omitempty"`
	Split          string `json:"split,omitempty"`
	EventIDPath    string `json:"event_id_path,omitempty"`
	EventTypePath  string `json:"event_type_path,omitempty"`
	KeyPath        string `json:"key_path,omitempty"`

	FlushMaxEvents     int   `json:"flush_max_events,omitempty"`
	FlushMaxIntervalMS int64 `json:"flush_max_interval_ms,omitempty"`
	FlushMaxBytes      int64 `json:"flush_max_bytes,omitempty"`
	BufferLimit        int   `json:"buffer_limit,omitempty"`

	// RateLimitPerMinute is zero for no limit. The limit is on the source,
	// not on a client address: a sender's requests arrive from whatever
	// addresses its infrastructure has.
	RateLimitPerMinute int `json:"rate_limit_per_minute,omitempty"`
	RateLimitBurst     int `json:"rate_limit_burst,omitempty"`

	// Persona is the persona whose members see the source's compacted windows
	// in Resources and search. Empty is the administrator persona, which keeps
	// them to administrators. Querying
	// the table is governed by the Trino connection, not by this. It is set
	// when the source is created and does not change: windows already written
	// stay where they were written.
	Persona string `json:"persona,omitempty"`

	// CompactEveryMinutes is the length of the window a source's events are
	// partitioned and compacted by: 60, the default, compacts each hour once
	// it has ended; a shorter window compacts sooner, into more files. It
	// divides an hour evenly, so windows start on the hour and an hour is
	// always a whole number of them.
	CompactEveryMinutes int `json:"compact_every_minutes,omitempty"`

	// RawRetentionDays is how long a raw segment is kept once the window it
	// belongs to is compacted. A compacted window's events are all in its
	// Parquet file, and a recompaction reads that file rather than the
	// segments, so they are a safety margin and nothing else; every day kept
	// is objects the raw table and retention list (#2134).
	RawRetentionDays int `json:"raw_retention_days,omitempty"`
	// CompactedRetentionDays is how long a compacted window is kept. Nil is
	// the default; zero keeps it forever.
	CompactedRetentionDays *int `json:"compacted_retention_days,omitempty"`

	// Index is how search indexes the source's compacted windows (#2111):
	// "full", "metadata" or "none". Empty leaves it to the operator's
	// resources.index rules, which can match these writes by origin webhook.
	// It applies to windows written from the next compaction on.
	Index string `json:"index,omitempty"`
}

// TableName is the name readers query the source by, in the scratch schema of
// its connection.
func TableName(source string) string {
	return "webhook_" + strings.ReplaceAll(source, "-", "_")
}

// TableName is the view readers name for this source.
func (s Source) TableName() string { return TableName(s.Name) }

// RawTableName is the table over the raw segments, underneath the view.
func (s Source) RawTableName() string { return s.TableName() + "_raw" }

// CompactedTableName is the table over the compacted windows, underneath the view.
func (s Source) CompactedTableName() string { return s.TableName() + "_compacted" }

// WithDefaults returns the source with every zero setting replaced by its
// default.
func (s Source) WithDefaults() Source {
	s = s.withPreset()
	s.Config = s.Config.withDefaults()
	s.Auth = s.Auth.withDefaults()
	return s
}

// withDefaults fills the zero settings of a config.
func (c Config) withDefaults() Config {
	if c.Input == "" {
		c.Input = InputHTTP
	}
	if c.IMAP != nil {
		m := c.IMAP.withDefaults()
		c.IMAP = &m
	}
	if c.Handshake == "" {
		c.Handshake = HandshakeNone
	}
	if c.MaxBodyBytes == 0 {
		c.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if c.FlushMaxEvents == 0 {
		c.FlushMaxEvents = DefaultFlushMaxEvents
	}
	if c.FlushMaxIntervalMS == 0 {
		c.FlushMaxIntervalMS = DefaultFlushMaxInterval.Milliseconds()
	}
	if c.FlushMaxBytes == 0 {
		c.FlushMaxBytes = DefaultFlushMaxBytes
	}
	if c.BufferLimit == 0 {
		c.BufferLimit = DefaultBufferLimit
	}
	if c.CompactEveryMinutes == 0 {
		c.CompactEveryMinutes = DefaultCompactEveryMinutes
	}
	if c.RawRetentionDays == 0 {
		c.RawRetentionDays = DefaultRawRetentionDays
	}
	if c.CompactedRetentionDays == nil {
		d := DefaultCompactedDays
		c.CompactedRetentionDays = &d
	}
	return c
}

// withDefaults fills the zero settings of an HMAC source's auth. The other
// modes have no settings that default.
func (a Auth) withDefaults() Auth {
	switch a.Mode {
	case AuthJWT:
		if a.Header == "" {
			a.Header = DefaultJWTHeader
		}
		return a
	case AuthEd25519:
		if a.Encoding == "" {
			a.Encoding = EncodingHex
		}
		if a.ToleranceSeconds == 0 && (a.TimestampHeader != "" || a.TimestampPath != "") {
			a.ToleranceSeconds = int(DefaultTolerance / time.Second)
		}
		return a
	case AuthHMAC:
	default:
		return a
	}
	if a.Algorithm == "" {
		a.Algorithm = AlgorithmSHA256
	}
	if a.Encoding == "" {
		a.Encoding = EncodingHex
	}
	if a.HeaderFormat == HeaderFormatStripe {
		a.Signed = SignedTimestampBody
	}
	if a.Signed == "" {
		a.Signed = SignedBody
	}
	if a.ChecksTimestamp() && a.ToleranceSeconds == 0 {
		a.ToleranceSeconds = int(DefaultTolerance / time.Second)
	}
	return a
}

// FlushInterval is the longest an event waits in the buffer before its
// segment is written.
func (c Config) FlushInterval() time.Duration {
	return time.Duration(c.FlushMaxIntervalMS) * time.Millisecond
}

// Window is the length of the window the source's events are partitioned and
// compacted by.
func (c Config) Window() time.Duration {
	if c.CompactEveryMinutes <= 0 {
		return time.Duration(DefaultCompactEveryMinutes) * time.Minute
	}
	return time.Duration(c.CompactEveryMinutes) * time.Minute
}

// RawRetention is how long a compacted window's raw segments are kept.
func (c Config) RawRetention() time.Duration {
	return time.Duration(c.RawRetentionDays) * day
}

// CompactedRetention is how long a compacted window is kept, and zero for
// forever.
func (c Config) CompactedRetention() time.Duration {
	if c.CompactedRetentionDays == nil {
		return time.Duration(DefaultCompactedDays) * day
	}
	return time.Duration(*c.CompactedRetentionDays) * day
}

// Scheme is the signing convention an hmac source verifies.
func (a Auth) Scheme() hmacsig.Scheme {
	return hmacsig.Scheme{
		Algorithm: a.Algorithm, Encoding: a.Encoding, Prefix: a.Prefix, Signed: a.Signed, Format: a.HeaderFormat,
		HeaderPattern: a.HeaderPattern, SecretEncoding: a.SecretEncoding,
	}
}

// ChecksTimestamp reports whether a signed request's timestamp is checked
// against the tolerance window: one read from a header, from the body, or
// from the signature header itself (the Stripe format, or a header pattern
// naming {timestamp}).
func (a Auth) ChecksTimestamp() bool {
	return a.TimestampHeader != "" || a.TimestampPath != "" || a.HeaderFormat == HeaderFormatStripe ||
		strings.Contains(a.HeaderPattern, hmacsig.PlaceholderTimestamp)
}

// Challenge is the body field a challenge_echo handshake reads.
func (c Config) Challenge() string {
	if c.ChallengeField == "" {
		return DefaultChallengeField
	}
	return c.ChallengeField
}

// Tolerance is how far a signed timestamp may be from the receiver's clock.
func (a Auth) Tolerance() time.Duration {
	return time.Duration(a.ToleranceSeconds) * time.Second
}

// Secrets returns the secrets a request may be verified with at now: the
// current one, and the previous one while its overlap lasts.
func (a Auth) Secrets(now time.Time) []string {
	out := []string{a.Secret}
	if a.PreviousSecret != "" && now.Before(a.PreviousUntil) {
		out = append(out, a.PreviousSecret)
	}
	return out
}

// Rotate replaces the secret, keeping the old one valid for overlap. An
// overlap of zero ends the old secret now.
func (a Auth) Rotate(secret string, overlap time.Duration, now time.Time) Auth {
	if secret == "" || secret == a.Secret {
		return a
	}
	if overlap > 0 && a.Secret != "" {
		a.PreviousSecret = a.Secret
		a.PreviousUntil = now.Add(overlap)
	} else {
		a.PreviousSecret = ""
		a.PreviousUntil = time.Time{}
	}
	a.Secret = secret
	return a
}
