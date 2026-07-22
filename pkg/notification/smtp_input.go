package notification

import (
	"net/mail"
	"time"
)

// defaultSMTPPort is the STARTTLS submission port applied when unset.
const defaultSMTPPort = 587

// implicitTLSPort is the SMTPS port. A server here expects a TLS handshake as
// the first bytes on the connection and never emits a plaintext greeting.
const implicitTLSPort = 465

// maxTCPPort is the highest valid TCP port number.
const maxTCPPort = 65535

// SMTPSettingsInput is the write shape for the admin SMTP configuration. An
// empty password keeps the currently stored one.
type SMTPSettingsInput struct {
	Enabled  bool   `json:"enabled" example:"true"`
	Host     string `json:"host" example:"smtp.example.com"`
	Port     int    `json:"port" example:"587"`
	Username string `json:"username,omitempty" example:"mailer@example.com"`
	Password string `json:"password,omitempty" example:"app-password"`
	From     string `json:"from" example:"platform@example.com"`
	FromName string `json:"from_name,omitempty" example:"Data Platform"`
	TLSMode  string `json:"tls_mode,omitempty" example:"starttls"`
}

// Validate normalizes defaults in place and returns a non-empty message when
// the input is invalid. Omitted fields take defaults (port 587, STARTTLS) so
// a minimal `{"enabled": false}` disable call works.
func (in *SMTPSettingsInput) Validate() string {
	if msg := in.normalizeTransport(); msg != "" {
		return msg
	}
	return in.validateSender()
}

// normalizeTransport defaults and validates tls_mode and port.
func (in *SMTPSettingsInput) normalizeTransport() string {
	if in.TLSMode == "" {
		in.TLSMode = TLSModeStartTLS
	}
	if in.TLSMode != TLSModeStartTLS && in.TLSMode != TLSModeImplicit && in.TLSMode != TLSModeNone {
		return "tls_mode must be starttls, implicit, or none"
	}
	if in.Port == 0 {
		in.Port = defaultSMTPPort
	}
	if in.Port < 0 || in.Port > maxTCPPort {
		return "port must be between 1 and 65535"
	}
	// Reject at save time rather than letting the dial hang: on port 465 the
	// server waits for a TLS ClientHello while a STARTTLS or plaintext client
	// waits for a greeting, so the connection stalls until the send timeout
	// and surfaces as an opaque i/o timeout far from the setting at fault.
	if in.Port == implicitTLSPort && in.TLSMode != TLSModeImplicit {
		return "port 465 requires tls_mode implicit; use port 587 for starttls"
	}
	return ""
}

// validateSender checks the fields required only for an enabled config.
func (in *SMTPSettingsInput) validateSender() string {
	if !in.Enabled {
		return ""
	}
	if in.Host == "" {
		return "host is required when enabled"
	}
	if _, err := mail.ParseAddress(in.From); err != nil {
		return "from must be a valid email address"
	}
	return ""
}

// SMTPSettingsView is the read shape for the admin SMTP configuration. The
// password is write-only: the view reports only whether one is stored.
type SMTPSettingsView struct {
	Enabled     bool      `json:"enabled" example:"true"`
	Host        string    `json:"host" example:"smtp.example.com"`
	Port        int       `json:"port" example:"587"`
	Username    string    `json:"username" example:"mailer@example.com"`
	PasswordSet bool      `json:"password_set" example:"true"`
	From        string    `json:"from" example:"platform@example.com"`
	FromName    string    `json:"from_name" example:"Data Platform"`
	TLSMode     string    `json:"tls_mode" example:"starttls"`
	UpdatedBy   string    `json:"updated_by,omitempty" example:"admin@example.com"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// View maps stored settings to the password-free read shape.
func (s *SMTPSettings) View() SMTPSettingsView {
	return SMTPSettingsView{
		Enabled:     s.Enabled,
		Host:        s.Host,
		Port:        s.Port,
		Username:    s.Username,
		PasswordSet: s.Password != "",
		From:        s.From,
		FromName:    s.FromName,
		TLSMode:     s.TLSMode,
		UpdatedBy:   s.UpdatedBy,
		UpdatedAt:   s.UpdatedAt,
	}
}

// UnconfiguredSMTPView is the read shape served before SMTP has ever been
// configured: disabled, with the transport defaults prefilled.
func UnconfiguredSMTPView() SMTPSettingsView {
	return SMTPSettingsView{Port: defaultSMTPPort, TLSMode: TLSModeStartTLS}
}

// TestEmailRequest is the body for the admin send-test-email action.
type TestEmailRequest struct {
	To string `json:"to" example:"admin@example.com"`
}

// Validate returns a non-empty message when the recipient is invalid.
func (r *TestEmailRequest) Validate() string {
	if _, err := mail.ParseAddress(r.To); err != nil {
		return "to must be a valid email address"
	}
	return ""
}

// Settings maps the validated input to store settings.
func (in *SMTPSettingsInput) Settings() SMTPSettings {
	return SMTPSettings{
		Enabled:  in.Enabled,
		Host:     in.Host,
		Port:     in.Port,
		Username: in.Username,
		Password: in.Password,
		From:     in.From,
		FromName: in.FromName,
		TLSMode:  in.TLSMode,
	}
}
