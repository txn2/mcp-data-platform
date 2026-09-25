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
)

// HMAC settings a source chooses between.
const (
	AlgorithmSHA256 = "sha256"
	AlgorithmSHA1   = "sha1"

	EncodingHex    = "hex"
	EncodingBase64 = "base64"

	// SignedBody signs the raw body.
	SignedBody = "body"
	// SignedTimestampBody signs the timestamp header's value, a ".", and
	// the raw body, which is what makes a replayed request with an old
	// timestamp fail even though its signature once verified.
	SignedTimestampBody = "timestamp.body"
)

// Handshakes a source can answer.
const (
	HandshakeNone = "none"
	// HandshakeCloudEvents answers the CloudEvents HTTP webhook
	// abuse-protection OPTIONS request, which a sender that implements it
	// sends before it delivers anything.
	HandshakeCloudEvents = "cloudevents"
)

// Defaults a source gets for every setting it leaves at zero.
const (
	DefaultMaxBodyBytes        int64 = 1 << 20
	DefaultFlushMaxEvents            = 5000
	DefaultFlushMaxInterval          = time.Second
	DefaultFlushMaxBytes       int64 = 8 << 20
	DefaultBufferLimit               = 50000
	DefaultRawRetentionDays          = 7
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
	Mode           string    `json:"mode"`
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

	// HeaderToken.
	Header string `json:"header,omitempty"`

	// Basic.
	Username string `json:"username,omitempty"`
}

// Config is everything about a source other than its authentication.
type Config struct {
	Handshake     string `json:"handshake,omitempty"`
	MaxBodyBytes  int64  `json:"max_body_bytes,omitempty"`
	Split         string `json:"split,omitempty"`
	EventIDPath   string `json:"event_id_path,omitempty"`
	EventTypePath string `json:"event_type_path,omitempty"`
	KeyPath       string `json:"key_path,omitempty"`

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
	// belongs to is compacted.
	RawRetentionDays int `json:"raw_retention_days,omitempty"`
	// CompactedRetentionDays is how long a compacted window is kept. Nil is
	// the default; zero keeps it forever.
	CompactedRetentionDays *int `json:"compacted_retention_days,omitempty"`
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
	s.Config = s.Config.withDefaults()
	s.Auth = s.Auth.withDefaults()
	return s
}

// withDefaults fills the zero settings of a config.
func (c Config) withDefaults() Config {
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
	if a.Mode != AuthHMAC {
		return a
	}
	if a.Algorithm == "" {
		a.Algorithm = AlgorithmSHA256
	}
	if a.Encoding == "" {
		a.Encoding = EncodingHex
	}
	if a.Signed == "" {
		a.Signed = SignedBody
	}
	if a.TimestampHeader != "" && a.ToleranceSeconds == 0 {
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
