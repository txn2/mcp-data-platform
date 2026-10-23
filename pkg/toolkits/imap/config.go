// Package imap provides the platform's IMAP connection kind (#2247): a
// toolkit that reads one dedicated platform mailbox per connection, so mail
// cc'd to the platform's address (client threads, vendor notices, automated
// reports) can be asked about and processed by managed scripts.
//
// Mail is the most hostile input the platform takes, and most of this kind is
// what it refuses. A connection reads only the folders it names. A message is
// shown only when its From address is on the connection's allowlist and,
// unless the operator turned it off, the receiving server recorded an SPF,
// DKIM or DMARC pass aligned with that address; any other message is never
// returned, summarized or quoted, only counted. Everything a sender wrote is
// cleaned (hidden HTML, zero-width and bidirectional characters) and kept
// apart from the platform's own fields. And it only reads: folders are opened
// with EXAMINE and bodies fetched with BODY.PEEK, so nothing marks a message
// seen, and there is no STORE, COPY, MOVE, EXPUNGE, APPEND or send.
//
// The protocol and the sender rules are internal/imapmail; this package is the
// connection kind around them.
package imap

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/cfgmap"
	"github.com/txn2/mcp-data-platform/internal/imapmail"
	"github.com/txn2/mcp-data-platform/internal/mailtrust"
	"github.com/txn2/mcp-data-platform/internal/secretref"
)

const (
	// Kind is the connection-instance kind discriminator.
	Kind = "imap"

	// DefaultMaxMessageBytes is the largest message mail_message reads.
	DefaultMaxMessageBytes = 10 << 20
	// DefaultMaxAttachmentBytes is the largest attachment mail_attachment saves.
	DefaultMaxAttachmentBytes = 25 << 20
	// DefaultMaxResults is how many messages one call returns at most.
	DefaultMaxResults = 50
	// maxResultsCeiling bounds what an operator may raise max_results to.
	maxResultsCeiling = 500
	// DefaultConnectTimeout bounds connecting and logging in.
	DefaultConnectTimeout = 15 * time.Second
	// DefaultCallTimeout bounds one tool call's whole session.
	DefaultCallTimeout = 60 * time.Second

	// portIMAPS and portIMAP are the defaults for tls and starttls.
	portIMAPS = 993
	portIMAP  = 143
	maxPort   = 65535
)

// SecretKeys are the keys whose value may name a stored secret. The password
// is filled each time a session is opened, so a rotated secret is read on the
// next call.
var SecretKeys = []string{cfgKeyPassword} //nolint:gochecknoglobals // the kind's fixed key list

const (
	cfgKeyHost               = "host"
	cfgKeyPort               = "port"
	cfgKeyTLS                = "tls"
	cfgKeyUsername           = "username"
	cfgKeyPassword           = "password" // #nosec G101 -- a config key, not a credential
	cfgKeyFolders            = "folders"
	cfgKeyAllowSenders       = "allow_senders"
	cfgKeyRequireAuthPass    = "require_auth_pass" // #nosec G101 -- a config key, not a credential
	cfgKeyTrustedAuthServID  = "trusted_authserv_id"
	cfgKeyMaxMessageBytes    = "max_message_bytes"
	cfgKeyMaxAttachmentBytes = "max_attachment_bytes"
	cfgKeyMaxResults         = "max_results"
	cfgKeyTLSCABundlePEM     = "tls_ca_bundle_pem"
	cfgKeyConnectTimeout     = "connect_timeout"
	cfgKeyCallTimeout        = "call_timeout"
	cfgKeyDescription        = "description"
)

// Config is one IMAP connection.
type Config struct {
	ConnectionName string
	Host           string
	Port           int
	TLS            string
	Username       string
	// Password is as stored: a {{secret:<name>}} reference is filled when a
	// session is opened.
	Password           string
	Folders            []string
	AllowSenders       []string
	RequireAuthPass    bool
	TrustedAuthServID  string
	MaxMessageBytes    int64
	MaxAttachmentBytes int64
	MaxResults         int
	TLSCABundlePEM     string
	ConnectTimeout     time.Duration
	CallTimeout        time.Duration
	Description        string
}

// ParseConfig reads a connection from the map it is stored as, applying
// defaults, and validates it.
func ParseConfig(cfg map[string]any) (Config, error) {
	c := Config{
		Host:               strings.TrimSpace(cfgmap.String(cfg, cfgKeyHost)),
		TLS:                cfgmap.StringDefault(cfg, cfgKeyTLS, imapmail.TLSImplicit),
		Username:           cfgmap.String(cfg, cfgKeyUsername),
		Password:           cfgmap.String(cfg, cfgKeyPassword),
		Folders:            stringList(cfg[cfgKeyFolders]),
		AllowSenders:       stringList(cfg[cfgKeyAllowSenders]),
		RequireAuthPass:    cfgmap.BoolDefault(cfg, cfgKeyRequireAuthPass, true),
		TrustedAuthServID:  strings.TrimSpace(cfgmap.String(cfg, cfgKeyTrustedAuthServID)),
		MaxMessageBytes:    cfgmap.Int64(cfg, cfgKeyMaxMessageBytes, DefaultMaxMessageBytes),
		MaxAttachmentBytes: cfgmap.Int64(cfg, cfgKeyMaxAttachmentBytes, DefaultMaxAttachmentBytes),
		MaxResults:         int(cfgmap.Int64(cfg, cfgKeyMaxResults, DefaultMaxResults)),
		TLSCABundlePEM:     cfgmap.String(cfg, cfgKeyTLSCABundlePEM),
		ConnectTimeout:     cfgmap.Duration(cfg, cfgKeyConnectTimeout, DefaultConnectTimeout),
		CallTimeout:        cfgmap.Duration(cfg, cfgKeyCallTimeout, DefaultCallTimeout),
		Description:        cfgmap.String(cfg, cfgKeyDescription),
	}
	defaultPort := portIMAPS
	if c.TLS == imapmail.TLSStartTLS {
		defaultPort = portIMAP
	}
	c.Port = int(cfgmap.Int64(cfg, cfgKeyPort, int64(defaultPort)))
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate refuses a configuration the kind cannot serve safely.
func (c Config) Validate() error {
	for _, check := range []func() error{c.validateServer, c.validateReads, c.validateLimits} {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

func (c Config) validateServer() error {
	switch {
	case c.Host == "":
		return errors.New("imap: host is required")
	case c.TLS != imapmail.TLSImplicit && c.TLS != imapmail.TLSStartTLS:
		return fmt.Errorf("imap: tls must be %q (IMAPS, port 993) or %q (port 143); plain IMAP would send the password in the clear", imapmail.TLSImplicit, imapmail.TLSStartTLS)
	case c.Port < 1 || c.Port > maxPort:
		return fmt.Errorf("imap: port %d is not a port", c.Port)
	case c.Username == "" || c.Password == "":
		return errors.New("imap: username and password are required; use an app password, stored as a secret")
	case c.TLSCABundlePEM != "" && !x509.NewCertPool().AppendCertsFromPEM([]byte(c.TLSCABundlePEM)):
		return errors.New("imap: tls_ca_bundle_pem holds no PEM certificate")
	}
	return nil
}

// validateReads checks what the connection may read and whose mail it shows.
func (c Config) validateReads() error {
	switch {
	case len(c.Folders) == 0:
		return errors.New("imap: folders is required: name the folders this connection may read; there is no all-folders default")
	case len(c.AllowSenders) == 0:
		return errors.New("imap: allow_senders is required: name the addresses and @domains whose mail is visible; mail from anyone else is never shown")
	case c.RequireAuthPass && c.TrustedAuthServID == "":
		return errors.New("imap: trusted_authserv_id is required while require_auth_pass is on: it is the receiving mail server's id " +
			"in its Authentication-Results header, and the only one read, since a sender can write that header too")
	}
	for _, s := range c.AllowSenders {
		s = strings.TrimSpace(s)
		if s == "@" || !strings.Contains(s, "@") {
			return fmt.Errorf("imap: allow_senders entry %q is neither an address nor an @domain", s)
		}
	}
	return nil
}

func (c Config) validateLimits() error {
	if c.MaxMessageBytes < 1 || c.MaxAttachmentBytes < 1 {
		return errors.New("imap: max_message_bytes and max_attachment_bytes must be positive")
	}
	if c.MaxResults < 1 || c.MaxResults > maxResultsCeiling {
		return fmt.Errorf("imap: max_results must be between 1 and %d", maxResultsCeiling)
	}
	return nil
}

// Policy is the connection's sender rules.
func (c Config) Policy() mailtrust.Policy {
	return mailtrust.Policy{
		Allow:       mailtrust.NewAllowlist(c.AllowSenders),
		RequireAuth: c.RequireAuthPass,
		AuthServID:  c.TrustedAuthServID,
	}
}

// account is how a session reaches the server, the password filled from the
// secret store when the configuration names one.
func (c Config) account(ctx context.Context) (imapmail.Account, error) {
	pw := c.Password
	if secretref.HasPlaceholder(pw) {
		filled, err := secretref.FillConnection(ctx, c.ConnectionName, pw, func(v string) string { return v })
		if err != nil {
			return imapmail.Account{}, fmt.Errorf("imap: connection %q password: %w", c.ConnectionName, err)
		}
		pw = filled
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: c.Host}
	if c.TLSCABundlePEM != "" {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM([]byte(c.TLSCABundlePEM))
		tlsCfg.RootCAs = pool
	}
	return imapmail.Account{
		Host: c.Host, Port: c.Port, TLS: c.TLS, Username: c.Username, Password: pw,
		TLSConfig: tlsCfg, DialTimeout: c.ConnectTimeout,
	}, nil
}

// allowsFolder reports whether the connection may read folder.
func (c Config) allowsFolder(folder string) bool {
	return slices.Contains(c.Folders, folder)
}

// stringList reads a list key stored as a JSON array or as one
// comma-separated string.
func stringList(v any) []string {
	var out []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				add(s)
			}
		}
	case []string:
		for _, s := range t {
			add(s)
		}
	case string:
		for s := range strings.SplitSeq(t, ",") {
			add(s)
		}
	}
	return out
}

// MultiConfig is every configured imap connection.
type MultiConfig struct {
	DefaultName string
	Instances   map[string]Config
}

// ParseMultiConfig reads the instances the registry hands the kind's factory,
// skipping one that does not parse with a warning.
func ParseMultiConfig(defaultName string, raw map[string]map[string]any) MultiConfig {
	parsed := make(map[string]Config, len(raw))
	for name, r := range raw {
		c, err := ParseConfig(r)
		if err != nil {
			slog.Warn("skipping invalid connection instance", "kind", Kind, "instance", name, "error", err)
			continue
		}
		c.ConnectionName = name
		parsed[name] = c
	}
	return MultiConfig{DefaultName: defaultName, Instances: parsed}
}

// ConfigSchemaJSON declares what an imap connection's config takes.
const ConfigSchemaJSON = `{
  "type": "object",
  "required": ["host", "username", "password", "folders", "allow_senders"],
  "properties": {
    "host": {"type": "string", "description": "The mail server's IMAP host. Required."},
    "port": {"type": "integer", "description": "Defaults to 993 for tls and 143 for starttls."},
    "tls": {"type": "string", "enum": ["tls", "starttls"], "description": "IMAPS (tls, the default) or STARTTLS. Plain IMAP is refused."},
    "username": {"type": "string", "description": "The mailbox's login. Required."},
    "password": {"type": "string", "description": "The mailbox's password or app password; name a stored secret as {{secret:<name>}}. Required."},
    "folders": {"type": "array", "items": {"type": "string"}, "description": "The folders this connection may read. Required; there is no all-folders default."},
    "allow_senders": {"type": "array", "items": {"type": "string"}, "description": "Addresses (ap@vendor.example) and domains (@vendor.example) whose mail is visible. Mail from anyone else is never shown, only counted. Required."},
    "require_auth_pass": {"type": "boolean", "description": "Trust an allowed sender only when the receiving server recorded an SPF, DKIM or DMARC pass aligned with From. Defaults to true."},
    "trusted_authserv_id": {"type": "string", "description": "The receiving mail server's authserv-id: the first word of the Authentication-Results header it adds. Only that header is read. Required while require_auth_pass is on."},
    "max_message_bytes": {"type": "integer", "description": "Largest message mail_message reads. Defaults to 10 MiB."},
    "max_attachment_bytes": {"type": "integer", "description": "Largest attachment mail_attachment saves. Defaults to 25 MiB."},
    "max_results": {"type": "integer", "description": "Most messages one call returns (1-500). Defaults to 50."},
    "tls_ca_bundle_pem": {"type": "string", "description": "PEM certificates to trust for the server, for a private CA."},
    "connect_timeout": {"type": "string", "description": "Bound on connecting and logging in, as a duration. Defaults to 15s."},
    "call_timeout": {"type": "string", "description": "Bound on one call's whole session, as a duration. Defaults to 60s."},
    "description": {"type": "string", "description": "What this connection is, as list_connections and search report it."}
  }
}`
