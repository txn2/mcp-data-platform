package webhookapi

import "github.com/txn2/mcp-data-platform/internal/webhook/whsource"

// The shapes the write routes read.

// AuthInput is how a request proves it came from the sender. secret is
// write-only; on an update, an empty secret keeps the stored one.
type AuthInput struct {
	Mode             string `json:"mode" example:"hmac"`
	Secret           string `json:"secret,omitempty" example:"whsec_example"`
	Algorithm        string `json:"algorithm,omitempty" example:"sha256"`
	SignatureHeader  string `json:"signature_header,omitempty" example:"X-Signature"`
	Encoding         string `json:"encoding,omitempty" example:"hex"`
	Prefix           string `json:"prefix,omitempty" example:"sha256="`
	TimestampHeader  string `json:"timestamp_header,omitempty" example:"X-Timestamp"`
	ToleranceSeconds int    `json:"tolerance_seconds,omitempty" example:"300"`
	Signed           string `json:"signed,omitempty" example:"body"`
	Header           string `json:"header,omitempty" example:"X-Webhook-Token"`
	Username         string `json:"username,omitempty" example:"sender"`
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
