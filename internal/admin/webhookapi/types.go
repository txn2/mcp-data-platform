package webhookapi

import (
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// The shapes the routes read and write.

// AuthView is a source's authentication without its secrets.
type AuthView struct {
	Mode             string     `json:"mode"`
	SecretSet        bool       `json:"secret_set"`
	PreviousUntil    *time.Time `json:"previous_secret_until,omitempty"`
	Algorithm        string     `json:"algorithm,omitempty"`
	SignatureHeader  string     `json:"signature_header,omitempty"`
	Encoding         string     `json:"encoding,omitempty"`
	Prefix           string     `json:"prefix,omitempty"`
	TimestampHeader  string     `json:"timestamp_header,omitempty"`
	ToleranceSeconds int        `json:"tolerance_seconds,omitempty"`
	Signed           string     `json:"signed,omitempty"`
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
}

// SourceDetail is one source with its status.
type SourceDetail struct {
	Source SourceView     `json:"source"`
	Status whstore.Status `json:"status"`
}

// SourceList is the collection response.
type SourceList struct {
	Sources []SourceView `json:"sources"`
}
