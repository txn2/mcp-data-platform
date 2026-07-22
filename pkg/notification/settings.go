package notification

import (
	"context"
	"time"
)

// SMTP TLS modes.
const (
	// TLSModeStartTLS negotiates STARTTLS on a plaintext connection (port 587).
	TLSModeStartTLS = "starttls"
	// TLSModeImplicit opens a TLS connection directly (port 465).
	TLSModeImplicit = "implicit"
	// TLSModeNone sends in plaintext. Only for closed-network relays.
	TLSModeNone = "none"
)

// SettingsSectionSMTP is the platform_settings section key for SMTP.
const SettingsSectionSMTP = "smtp"

// SMTPSettings is the admin-configured mail server connection. The password
// is encrypted at rest and never returned by the admin API.
type SMTPSettings struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	// From is the sender address, e.g. "platform@example.com".
	From string `json:"from"`
	// FromName is the optional display name for the From address.
	FromName string `json:"from_name"`
	// ReplyTo, when set, is applied as the Reply-To header on every outgoing
	// message so recipient replies reach a monitored mailbox instead of
	// bouncing off a no-reply From address (#1023).
	ReplyTo string `json:"reply_to,omitempty"`
	// AboutText is an optional sentence or two describing the platform,
	// rendered as a footer block on all outgoing mail (#1023). It gives
	// first-contact recipients context and lifts short notifications out of
	// the image-heavy/low-text band content filters penalize.
	AboutText string `json:"about_text,omitempty"`
	// SupportContact is an optional email address or URL where recipients can
	// get help, rendered with AboutText in the footer (#1023).
	SupportContact string `json:"support_contact,omitempty"`
	// TLSMode is one of the TLSMode* constants.
	TLSMode string `json:"tls_mode"`
	// UpdatedBy and UpdatedAt describe the last admin write.
	UpdatedBy string    `json:"updated_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// StringEncryptor encrypts and decrypts a single string secret. Satisfied by
// fieldcrypt.RestFieldEncryptor; a nil-safe passthrough is acceptable when
// encryption is disabled.
type StringEncryptor interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// SettingsStore persists admin-editable platform settings sections. SMTP is
// the first section; future global settings reuse the same table.
type SettingsStore interface {
	// GetSMTP returns the stored SMTP settings with the password decrypted,
	// or ErrNotFound when SMTP has never been configured.
	GetSMTP(ctx context.Context) (*SMTPSettings, error)
	// SetSMTP upserts the SMTP settings, encrypting the password at rest.
	// An empty incoming password preserves the previously stored one so the
	// admin UI can stay write-only without re-sending the secret.
	SetSMTP(ctx context.Context, s SMTPSettings, author string) error
}
