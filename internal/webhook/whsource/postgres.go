package whsource

import (
	"regexp"
	"strings"
)

// InputPostgres is a source whose events come from an application database
// (#2408): the notifications sent on its channels, or the rows of an outbox
// table its triggers write. Configured under Postgres; it takes no auth, since
// nothing is posted to it.
const InputPostgres = "postgres"

// The modes a postgres source reads its database in.
const (
	// PostgresNotify files each notification's payload as one event. A
	// notification sent while no replica listens is lost: at most once.
	PostgresNotify = "notify"
	// PostgresOutbox drains an outbox table: each row is filed and deleted in
	// one transaction, and a poll drains rows whose notification was missed,
	// so a missed notification delays an event and never loses it.
	PostgresOutbox = "outbox"
)

// Defaults and bounds of a postgres source's settings.
const (
	DefaultPostgresPollSeconds     = 30
	DefaultPostgresMaxPayloadBytes = 64 << 10
	minPostgresPollSeconds         = 1
	maxPostgresPollSeconds         = 3600
	maxPostgresPayloadBytes        = 1 << 20
	maxPostgresChannels            = 20
	maxChannelBytes                = 63
)

// Postgres is what a postgres source reads.
type Postgres struct {
	// Connection is the application database (a connection of kind appdb)
	// the source listens on, as that connection's role.
	Connection string `json:"connection"`
	// Mode is "notify" or "outbox".
	Mode string `json:"mode"`
	// Channels are the channels a notify source listens on.
	Channels []string `json:"channels,omitempty"`
	// Table is an outbox source's outbox, as schema.table: the table the
	// manage_schema outbox template created, whose channel is its name.
	Table string `json:"table,omitempty"`
	// PollSeconds is how often an outbox source drains with no
	// notification.
	PollSeconds int `json:"poll_seconds,omitempty"`
	// MaxPayloadBytes bounds one notification's payload or one outbox row;
	// a larger one is refused, recorded as a rejection, and never stored.
	MaxPayloadBytes int `json:"max_payload_bytes,omitempty"`
}

// IsPostgres reports whether the source reads an application database.
func (s Source) IsPostgres() bool { return s.Config.Input == InputPostgres }

func (p Postgres) withDefaults() Postgres {
	if p.PollSeconds == 0 {
		p.PollSeconds = DefaultPostgresPollSeconds
	}
	if p.MaxPayloadBytes == 0 {
		p.MaxPayloadBytes = DefaultPostgresMaxPayloadBytes
	}
	return p
}

// outboxTable is what an outbox's name may be: two lowercase identifiers,
// the form the outbox template writes and the channel is named by.
var outboxTable = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}\.[a-z_][a-z0-9_]{0,62}$`)

// validatePostgres checks a postgres source's database, mode and bounds.
func validatePostgres(s Source) error {
	p := s.Config.Postgres
	if p == nil {
		return invalid("config.postgres is required for a postgres source")
	}
	if s.Auth.Mode != "" || s.Auth.Secret != "" {
		return invalid("a postgres source takes no auth: nothing is posted to it, and it reads its database as the connection's role")
	}
	if err := validatePostgresBounds(*p); err != nil {
		return err
	}
	switch p.Mode {
	case PostgresNotify:
		return validateChannels(*p)
	case PostgresOutbox:
		return validateOutbox(*p)
	default:
		return invalid("config.postgres.mode must be notify or outbox")
	}
}

// validatePostgresBounds checks the connection and the numeric settings.
func validatePostgresBounds(p Postgres) error {
	switch {
	case strings.TrimSpace(p.Connection) == "":
		return invalid("config.postgres.connection is required: the application database the source reads")
	case p.PollSeconds < minPostgresPollSeconds || p.PollSeconds > maxPostgresPollSeconds:
		return invalid("config.postgres.poll_seconds must be between %d and %d", minPostgresPollSeconds, maxPostgresPollSeconds)
	case p.MaxPayloadBytes < 1 || p.MaxPayloadBytes > maxPostgresPayloadBytes:
		return invalid("config.postgres.max_payload_bytes must be between 1 and %d", maxPostgresPayloadBytes)
	}
	return nil
}

// validateOutbox checks an outbox source's table.
func validateOutbox(p Postgres) error {
	if len(p.Channels) > 0 {
		return invalid("config.postgres.channels is for mode notify; an outbox source listens on its table's channel")
	}
	if !outboxTable.MatchString(p.Table) || len(p.Table) > maxChannelBytes {
		return invalid("config.postgres.table must be the outbox as schema.table, in lowercase, at most %d bytes", maxChannelBytes)
	}
	return nil
}

// validateChannels checks a notify source's channels.
func validateChannels(p Postgres) error {
	if p.Table != "" {
		return invalid("config.postgres.table is for mode outbox")
	}
	if len(p.Channels) == 0 || len(p.Channels) > maxPostgresChannels {
		return invalid("config.postgres.channels must name between 1 and %d channels", maxPostgresChannels)
	}
	for _, ch := range p.Channels {
		if strings.TrimSpace(ch) == "" || len(ch) > maxChannelBytes {
			return invalid("config.postgres.channels names a channel that is empty or longer than %d bytes", maxChannelBytes)
		}
	}
	return nil
}

// refusePostgresBlock refuses a postgres block on a source of another input.
func refusePostgresBlock(s Source) error {
	if s.Config.Postgres != nil && !s.IsPostgres() {
		return invalid("config.postgres is only for a source whose input is postgres; this source's input is %s", s.Config.Input)
	}
	return nil
}
