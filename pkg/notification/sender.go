package notification

import (
	"bytes"
	"context"
	"fmt"

	mail "github.com/wneessen/go-mail"
)

// Sender delivers one rendered email using the given SMTP settings. Settings
// are passed per call so admin changes apply to the next send without any
// worker restart.
type Sender interface {
	Send(ctx context.Context, settings SMTPSettings, email Email) error
}

// SMTPSender implements Sender over SMTP via go-mail. A fresh connection is
// dialed per send: notification volume is human-scale and a stateless dial
// keeps credential and TLS changes immediate.
type SMTPSender struct{}

// NewSMTPSender creates the production SMTP sender.
func NewSMTPSender() *SMTPSender {
	return &SMTPSender{}
}

// Send renders no content itself; it wraps email into a multipart message
// (plaintext body + HTML alternative) and delivers it.
func (*SMTPSender) Send(ctx context.Context, settings SMTPSettings, email Email) error {
	msg, err := buildMessage(settings, email)
	if err != nil {
		return err
	}
	client, err := buildClient(settings)
	if err != nil {
		return err
	}
	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("sending email to %s: %w", email.To, err)
	}
	return nil
}

// buildMessage assembles the multipart mail message.
func buildMessage(settings SMTPSettings, email Email) (*mail.Msg, error) {
	msg := mail.NewMsg()
	if settings.FromName != "" {
		if err := msg.FromFormat(settings.FromName, settings.From); err != nil {
			return nil, fmt.Errorf("invalid from address %q: %w", settings.From, err)
		}
	} else if err := msg.From(settings.From); err != nil {
		return nil, fmt.Errorf("invalid from address %q: %w", settings.From, err)
	}
	if err := msg.To(email.To); err != nil {
		return nil, fmt.Errorf("invalid recipient address %q: %w", email.To, err)
	}
	msg.Subject(email.Subject)
	msg.SetBodyString(mail.TypeTextPlain, email.Text)
	msg.AddAlternativeString(mail.TypeTextHTML, email.HTML)
	// Embed rather than link the logo: an inline part renders on first open,
	// where a remote <img> is suppressed by the image blocking Outlook and
	// Gmail apply by default. The HTML already points at this Content-ID.
	// EmbedReadSeeker over EmbedReader: a *bytes.Reader cannot fail, so the
	// reader-based call would only add an error branch no input can reach.
	if len(email.LogoPNG) > 0 {
		msg.EmbedReadSeeker(logoContentID, bytes.NewReader(email.LogoPNG))
	}
	return msg, nil
}

// buildClient constructs a go-mail client from the admin SMTP settings.
func buildClient(settings SMTPSettings) (*mail.Client, error) {
	opts := []mail.Option{mail.WithPort(settings.Port)}
	switch settings.TLSMode {
	case TLSModeImplicit:
		opts = append(opts, mail.WithSSLPort(false))
	case TLSModeNone:
		opts = append(opts, mail.WithTLSPolicy(mail.NoTLS))
	default: // TLSModeStartTLS
		opts = append(opts, mail.WithTLSPortPolicy(mail.TLSMandatory))
	}
	if settings.Username != "" {
		// AutoDiscover negotiates the strongest mechanism the server
		// advertises (SCRAM, LOGIN, PLAIN, ...), so LOGIN-only and
		// SCRAM-preferring servers work without an auth-mode setting.
		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover),
			mail.WithUsername(settings.Username),
			mail.WithPassword(settings.Password))
	}
	client, err := mail.NewClient(settings.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("building smtp client for %s: %w", settings.Host, err)
	}
	return client, nil
}

// Verify interface compliance.
var _ Sender = (*SMTPSender)(nil)
