package webhookapi

import "github.com/txn2/mcp-data-platform/internal/webhook/whsource"

// The shapes the write routes read.

// AuthInput is how a request proves it came from the sender. secret is
// write-only; on an update, an empty secret keeps the stored one.
type AuthInput struct {
	// Mode is hmac, header_token, basic, path_token, ed25519, body_token or
	// jwt. A preset sets it.
	Mode string `json:"mode" example:"hmac"`
	// Preset names a sender's whole convention: standard_webhooks, github,
	// stripe, platform, slack, zoom, mattermost, teams, discord, monday,
	// shopify, linear, gitlab, jira or microsoft_graph (#2223). The settings
	// left unset are filled from it.
	Preset string `json:"preset,omitempty" example:"slack"`
	// Secret is write-only. For ed25519 it is the sender's public key in hex.
	Secret           string `json:"secret,omitempty" example:"whsec_example"`
	Algorithm        string `json:"algorithm,omitempty" example:"sha256"`
	SignatureHeader  string `json:"signature_header,omitempty" example:"X-Signature"`
	Encoding         string `json:"encoding,omitempty" example:"hex"`
	Prefix           string `json:"prefix,omitempty" example:"sha256="`
	TimestampHeader  string `json:"timestamp_header,omitempty" example:"X-Timestamp"`
	ToleranceSeconds int    `json:"tolerance_seconds,omitempty" example:"300"`
	Signed           string `json:"signed,omitempty" example:"body"`
	IDHeader         string `json:"id_header,omitempty" example:"webhook-id"`
	HeaderFormat     string `json:"header_format,omitempty" example:"stripe"`
	// HeaderPattern lays the signature header out over {signature} and
	// {timestamp}, in place of a prefix or a header format.
	HeaderPattern string `json:"header_pattern,omitempty" example:"{timestamp}|{signature}"`
	// SecretEncoding is empty for the secret as written, or base64.
	SecretEncoding string `json:"secret_encoding,omitempty" example:"base64"`
	// TimestampPath reads the signed timestamp from the body, in seconds or
	// milliseconds.
	TimestampPath string `json:"timestamp_path,omitempty" example:"webhookTimestamp"`
	// TokenPath is where in the body a body_token source's token is; a path
	// through [*] requires every element to carry it.
	TokenPath string `json:"token_path,omitempty" example:"value[*].clientState"`
	Header    string `json:"header,omitempty" example:"X-Webhook-Token"`
	Username  string `json:"username,omitempty" example:"sender"`
}

// SourceInput is the write shape. name and connection are read on create
// only: the table was created on that connection under a name derived from
// the source's.
type SourceInput struct {
	Name       string          `json:"name,omitempty" example:"esp-events"`
	Enabled    *bool           `json:"enabled,omitempty" example:"true"`
	Connection string          `json:"connection,omitempty" example:"scratch"`
	Auth       AuthInput       `json:"auth"`
	Config     whsource.Config `json:"config"`
	// RotationOverlapSeconds keeps the previous secret valid this long when
	// auth.secret replaces it. Zero ends it at once.
	RotationOverlapSeconds int `json:"rotation_overlap_seconds,omitempty" example:"86400"`
}
