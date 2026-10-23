package whsource

import (
	"crypto/x509"
	"strings"
)

// The inputs a source takes events from (#2247). An HTTP source is posted to;
// an IMAP source is a mailbox the platform polls, each message one event.
// Everything after the input (segments, compaction, the table, retention,
// alerts) is the same for both.
const (
	InputHTTP = "http"
	InputIMAP = "imap"
)

// AuthIMAP is the auth mode of an IMAP source: Username and Secret are the
// mailbox's login, Secret encrypted at rest like every source secret.
const AuthIMAP = "imap"

// The ways an IMAP source reaches its server. Plain IMAP is not one of them:
// the password would cross the network in the clear.
const (
	IMAPTLS      = "tls"
	IMAPStartTLS = "starttls"
)

// Defaults an IMAP source gets for every setting it leaves at zero.
const (
	DefaultIMAPPollSeconds        = 60
	DefaultIMAPMaxMessageBytes    = 10 << 20
	DefaultIMAPMaxAttachmentBytes = 25 << 20
	defaultIMAPSPort              = 993
	defaultIMAPPort               = 143
)

// Bounds an IMAP setting is refused outside of.
const (
	minIMAPPollSeconds = 15
	maxIMAPPollSeconds = 86400
	maxPort            = 65535
)

// IMAP is what an IMAP source polls: a server, the folders it reads, and whose
// mail becomes events.
type IMAP struct {
	Host string `json:"host"`
	Port int    `json:"port,omitempty"`
	// TLS is "tls" (IMAPS, the default) or "starttls".
	TLS string `json:"tls,omitempty"`
	// Folders are the folders read, each with its own watermark. Required:
	// there is no all-folders default.
	Folders []string `json:"folders"`
	// AllowSenders are the addresses and @domain entries whose mail becomes
	// an event. Mail from anyone else is counted and never stored.
	AllowSenders []string `json:"allow_senders"`
	// RequireAuthPass trusts an allowed sender only when the receiving
	// server recorded an aligned SPF, DKIM or DMARC pass. Nil is true.
	RequireAuthPass *bool `json:"require_auth_pass,omitempty"`
	// TrustedAuthServID is the receiving server's authserv-id; only an
	// Authentication-Results header carrying it is read.
	TrustedAuthServID string `json:"trusted_authserv_id,omitempty"`
	// TLSCABundlePEM are certificates to trust for a private CA.
	TLSCABundlePEM     string `json:"tls_ca_bundle_pem,omitempty"`
	PollSeconds        int    `json:"poll_seconds,omitempty"`
	MaxMessageBytes    int64  `json:"max_message_bytes,omitempty"`
	MaxAttachmentBytes int64  `json:"max_attachment_bytes,omitempty"`
}

// IsIMAP reports whether the source polls a mailbox rather than being posted to.
func (s Source) IsIMAP() bool { return s.Config.Input == InputIMAP }

// AuthRequired reports whether an IMAP source requires an authenticated sender.
func (m IMAP) AuthRequired() bool { return m.RequireAuthPass == nil || *m.RequireAuthPass }

func (m IMAP) withDefaults() IMAP {
	if m.TLS == "" {
		m.TLS = IMAPTLS
	}
	if m.Port == 0 {
		m.Port = defaultIMAPSPort
		if m.TLS == IMAPStartTLS {
			m.Port = defaultIMAPPort
		}
	}
	if m.PollSeconds == 0 {
		m.PollSeconds = DefaultIMAPPollSeconds
	}
	if m.MaxMessageBytes == 0 {
		m.MaxMessageBytes = DefaultIMAPMaxMessageBytes
	}
	if m.MaxAttachmentBytes == 0 {
		m.MaxAttachmentBytes = DefaultIMAPMaxAttachmentBytes
	}
	return m
}

// validateIMAP checks an IMAP source's server, folders and sender rules.
func validateIMAP(m *IMAP, a Auth) error {
	if m == nil {
		return invalid("config.imap is required for an imap source")
	}
	if a.Mode != AuthIMAP || a.Username == "" {
		return invalid("an imap source's auth is mode imap with the mailbox's username, and its password as auth.secret")
	}
	if err := validateIMAPServer(*m); err != nil {
		return err
	}
	if err := validateIMAPLimits(*m); err != nil {
		return err
	}
	return validateIMAPReads(*m)
}

func validateIMAPServer(m IMAP) error {
	switch {
	case strings.TrimSpace(m.Host) == "":
		return invalid("config.imap.host is required")
	case m.TLS != IMAPTLS && m.TLS != IMAPStartTLS:
		return invalid("config.imap.tls must be tls or starttls; plain IMAP would send the password in the clear")
	case m.Port < 1 || m.Port > maxPort:
		return invalid("config.imap.port %d is not a port", m.Port)
	case m.TLSCABundlePEM != "" && !x509.NewCertPool().AppendCertsFromPEM([]byte(m.TLSCABundlePEM)):
		return invalid("config.imap.tls_ca_bundle_pem holds no PEM certificate")
	}
	return nil
}

func validateIMAPLimits(m IMAP) error {
	switch {
	case m.PollSeconds < minIMAPPollSeconds || m.PollSeconds > maxIMAPPollSeconds:
		return invalid("config.imap.poll_seconds must be between %d and %d", minIMAPPollSeconds, maxIMAPPollSeconds)
	case m.MaxMessageBytes < 1 || m.MaxAttachmentBytes < 1:
		return invalid("config.imap.max_message_bytes and max_attachment_bytes must be positive")
	}
	return nil
}

func validateIMAPReads(m IMAP) error {
	switch {
	case len(m.Folders) == 0:
		return invalid("config.imap.folders is required: name the folders this source reads; there is no all-folders default")
	case len(m.AllowSenders) == 0:
		return invalid("config.imap.allow_senders is required: name the addresses and @domains whose mail becomes events")
	case m.AuthRequired() && strings.TrimSpace(m.TrustedAuthServID) == "":
		return invalid("config.imap.trusted_authserv_id is required while require_auth_pass is on: the receiving server's id in " +
			"its Authentication-Results header, the only one read, since a sender can write that header too")
	}
	for _, s := range m.AllowSenders {
		s = strings.TrimSpace(s)
		if s == "@" || !strings.Contains(s, "@") {
			return invalid("config.imap.allow_senders entry %q is neither an address nor an @domain", s)
		}
	}
	return nil
}
