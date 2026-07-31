// Package smtp is the admin-configured mail server layer of the notification
// substrate: the stored connection settings, the admin API's write/read shapes
// with their validation, and the store that persists them with the password
// encrypted at rest.
//
// It knows nothing about queues, preferences, or rendering. The send path
// (internal/notification/notifysend) takes a Settings value per call, so an
// admin change applies to the next send without a restart.
package smtp

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors returned by this package.
var (
	// ErrNotFound is returned by the store when SMTP has never been configured.
	ErrNotFound = errors.New("notification: smtp settings not found")
	// ErrNotConfigured is returned by delivery actions when SMTP is absent,
	// disabled, or missing a host; the caller should surface it as a
	// configuration conflict, not a delivery failure.
	ErrNotConfigured = errors.New("notification: smtp is disabled or not configured")
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

// SettingsSection is the platform_settings section key for SMTP.
const SettingsSection = "smtp"

// Settings is the admin-configured mail server connection. The password
// is encrypted at rest and never returned by the admin API.
type Settings struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	// From is the sender address, e.g. "platform@example.com".
	From string `json:"from"`
	// FromName is the optional display name for the From address.
	FromName string `json:"from_name"`
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

// SettingsStore persists the admin's SMTP configuration. It is the SMTP
// section of the platform_settings table; another global setting would add its
// own contract over the same table rather than widen this one.
type SettingsStore interface {
	// Get returns the stored SMTP settings with the password decrypted,
	// or ErrNotFound when SMTP has never been configured.
	Get(ctx context.Context) (*Settings, error)
	// Set upserts the SMTP settings, encrypting the password at rest.
	// An empty incoming password preserves the previously stored one so the
	// admin UI can stay write-only without re-sending the secret.
	Set(ctx context.Context, s Settings, author string) error
}
