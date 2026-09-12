// Package connalert tells somebody when a connection's stored OAuth credential
// is discarded, and tells somebody else when nobody has come back to it (#1694).
//
// #395 made the revocation observable: the platform refreshes proactively,
// records a durable auth-event history, and a status card distinguishes revoked
// from never-connected. Every one of those is pull — each answers an operator
// who already knows to ask. This package is the push.
//
// It matters here more than on the platform's other auth surfaces because this
// is the one where the person holding the upstream credential is deliberately
// not the person using the connection day to day. An agent's call comes back
// needing reauthorization and the agent reports that to whoever is watching; on
// a schedule nobody is, and the run's empty answer reads as data rather than as
// an outage.
//
// Three pieces:
//
//	Settings   the operator's escalation window and recipient list, stored in
//	           the platform_settings section the admin API writes
//	AlertStore the open revocations: one row per connection, inserted when the
//	           credential is discarded, deleted when it is authorized again,
//	           and stamped once when the escalation goes out
//	Alerter    the authevents.RevocationSink that opens the row and mails the
//	           person whose authorization lapsed
//	Escalator  the timer that mails the operator's recipients about a
//	           revocation nobody has acted on
//
// It must not import pkg/platform: the HTTP composition root supplies the
// enqueuer and the portal base URL it already holds.
package connalert

import (
	"errors"
	"strings"
	"time"
)

// ErrNotFound is returned by the settings store when the alert has never been
// configured. Callers apply DefaultSettings instead.
var ErrNotFound = errors.New("connalert: settings not found")

// SettingsSection is the platform_settings section holding the operator's
// configuration. It is a stored key: renaming it would strand every
// deployment's configured recipients behind a section nothing reads.
const SettingsSection = "connection_auth_alert"

// DefaultEscalateAfterHours is the delay before a revocation nobody has acted
// on is raised with the operator's recipients.
//
// A day, because that is how long the person who authorized the connection
// plausibly takes to see their mail, and because the premise of the whole
// feature is that they may be unreachable rather than merely slow. A window
// shorter than that mails a second group of people about something the first
// is about to handle.
const DefaultEscalateAfterHours = 24

// Route is the path both alerts link to, relative to the portal base that
// notification.PortalLink prefixes: the Connections page, where the connection
// is reauthorized.
const Route = "/admin/connections"

// Settings is the operator's configuration for connection-revocation alerts.
// It is that feature's section of the platform_settings table.
type Settings struct {
	// Enabled turns both alerts on. Off, a revocation is still recorded in the
	// auth-event history and on the status card exactly as before — what is
	// turned off is telling anyone.
	Enabled bool `json:"enabled"`
	// EscalateAfterHours is how long a revoked connection goes unauthorized
	// before Recipients are told. Zero or less applies
	// DefaultEscalateAfterHours.
	EscalateAfterHours int `json:"escalate_after_hours"`
	// Recipients are the addresses the escalation is delivered to, in
	// notification.NormalizeAddress form. Empty is the default and means no
	// escalation: the person who authorized the connection is still told, and
	// nobody else is.
	//
	// They are addresses rather than a role because the platform stores no
	// directory of administrators — admin is a claim on a token at request
	// time, so there is no set of people to resolve. Naming them is also what
	// makes the escalation a deliberate choice about who is accountable for a
	// connection rather than a broadcast.
	Recipients []string `json:"recipients"`
	// UpdatedBy and UpdatedAt describe the last admin write. They live in the
	// platform_settings audit columns, which are authoritative, so they are
	// excluded from the section value rather than written into it twice.
	UpdatedBy string    `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

// DefaultSettings returns the configuration applied before an operator has
// written one: alerts on, escalating after a day, to nobody yet.
func DefaultSettings() Settings {
	return Settings{Enabled: true, EscalateAfterHours: DefaultEscalateAfterHours}
}

// EscalateAfter returns the configured escalation window, or the default when
// unset or non-positive.
func (s Settings) EscalateAfter() time.Duration {
	if s.EscalateAfterHours > 0 {
		return time.Duration(s.EscalateAfterHours) * time.Hour
	}
	return DefaultEscalateAfterHours * time.Hour
}

// EscalatesTo returns the recipients the escalation is addressed to, with blank
// entries dropped. An empty result means this deployment escalates nowhere.
func (s Settings) EscalatesTo() []string {
	out := make([]string, 0, len(s.Recipients))
	for _, r := range s.Recipients {
		if addr := strings.TrimSpace(r); addr != "" {
			out = append(out, addr)
		}
	}
	return out
}

// Alert is one connection's open revocation: the row this package keeps between
// the credential being discarded and somebody reauthorizing.
type Alert struct {
	// Kind is the connection kind (mcp, api, graphql).
	Kind string
	// Name is the connection name within the kind.
	Name string
	// AuthorizedBy is the identity that authorized the connection, and the
	// address the first alert goes to.
	AuthorizedBy string
	// IDPHost is the host of the upstream that rejected the refresh. Empty
	// when the platform reached the verdict without calling it.
	IDPHost string
	// Reason is the stable short form the auth-event history records.
	Reason string
	// RevokedAt is when the credential was discarded, and the instant the
	// escalation window is measured from.
	RevokedAt time.Time
}
