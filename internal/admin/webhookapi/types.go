package webhookapi

import (
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whimap"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// The shapes the routes read and write.

// AuthView is a source's authentication without its secrets.
type AuthView struct {
	Mode             string     `json:"mode"`
	Preset           string     `json:"preset,omitempty"`
	SecretSet        bool       `json:"secret_set"`
	PreviousUntil    *time.Time `json:"previous_secret_until,omitempty"`
	Algorithm        string     `json:"algorithm,omitempty"`
	SignatureHeader  string     `json:"signature_header,omitempty"`
	Encoding         string     `json:"encoding,omitempty"`
	Prefix           string     `json:"prefix,omitempty"`
	TimestampHeader  string     `json:"timestamp_header,omitempty"`
	ToleranceSeconds int        `json:"tolerance_seconds,omitempty"`
	Signed           string     `json:"signed,omitempty"`
	IDHeader         string     `json:"id_header,omitempty"`
	HeaderFormat     string     `json:"header_format,omitempty"`
	HeaderPattern    string     `json:"header_pattern,omitempty"`
	SecretEncoding   string     `json:"secret_encoding,omitempty"`
	TimestampPath    string     `json:"timestamp_path,omitempty"`
	TokenPath        string     `json:"token_path,omitempty"`
	Header           string     `json:"header,omitempty"`
	Username         string     `json:"username,omitempty"`
}

// SourceView is the read shape.
type SourceView struct {
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	Connection string `json:"connection"`
	// Path is where the sender posts, relative to the platform's address:
	// /hooks/{name}, with the token as a further segment for path_token.
	Path string `json:"path" example:"/hooks/esp-events"`
	// Table is what readers query, in the connection's scratch schema.
	Table     string          `json:"table" example:"webhook_esp_events"`
	Auth      AuthView        `json:"auth"`
	Config    whsource.Config `json:"config"`
	CreatedBy string          `json:"created_by,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	// Warnings is what a create or change answers with besides the source,
	// without refusing it: a slow read of its partitions, which an uncached
	// file metastore causes (#2134). Absent from a list or a read.
	Warnings []string `json:"warnings,omitempty"`
}

// SourceDetail is one source with its status.
type SourceDetail struct {
	Source SourceView     `json:"source"`
	Status whstore.Status `json:"status"`
	// IMAP is each folder of an imap source as its polls left it: the
	// watermark, the messages stored and withheld, the last poll's error.
	IMAP []whimap.Mark `json:"imap,omitempty"`
}

// SourceList is the collection response.
type SourceList struct {
	Sources []SourceView `json:"sources"`
}
