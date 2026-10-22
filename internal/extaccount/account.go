// Package extaccount is the accounts the platform creates for people outside
// the organization (#2092).
//
// An external person does not come from the identity provider. An
// administrator creates the account with an email, a name, an external persona
// and an optional expiry; the platform emails an invitation holding a one-time
// sign-in link, and every later sign-in is another emailed link the person
// asks for on the sign-in screen. Claiming a link opens a portal session for
// the account, signed like any other browser session, whose user id is
// UserIDPrefix and the account id. Holding the mailbox is the proof of
// identity: there is no password.
//
// A session is a signed cookie, so it cannot be recalled. What makes Suspend,
// Remove and expiry take effect at once is Admit: the portal authenticator asks
// it on every request an external session makes, and it answers with the
// persona's current roles only while the account is active.
//
// The first time the same address signs in through the identity provider the
// account converts: it is closed, its links die, and the person is internal
// from then on. An account is never created for an address the platform
// already knows from the identity provider.
package extaccount

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// UserIDPrefix starts the user id of every external account's session.
const UserIDPrefix = "ext:"

// SettingsSection is the platform_settings section holding Settings, which
// administrators edit under Admin > Settings > External accounts.
const SettingsSection = "external_accounts"

// Defaults of Settings: a session as long as a share guest's, an invitation
// that waits a week, and a sign-in link short-lived like a guest link.
const (
	DefaultSessionHours  = 12
	DefaultInviteHours   = 7 * 24
	DefaultLinkMinutes   = 15
	maxSessionHours      = 30 * 24
	maxInviteHours       = 30 * 24
	maxLinkMinutes       = 24 * 60
	maxLinksPerHour      = 5
	maxNameLen           = 200
	maxEmailLen          = 320
	settingsRangeMessage = "%s must be between 1 and %d"
)

// Errors a caller maps to a status.
var (
	// ErrNotFound is an account that does not exist.
	ErrNotFound = errors.New("external account not found")
	// ErrExists is an address that already has an account.
	ErrExists = errors.New("an external account already exists for that address")
	// ErrInternal is an address the platform already knows from the
	// identity provider.
	ErrInternal = errors.New("that address already signs in through the identity provider")
	// ErrPersona is a persona that is not external, or that the persona's
	// own roles do not resolve to.
	ErrPersona = errors.New("the persona is not an external persona its roles resolve to")
	// ErrInvalid is input that fails validation; it wraps the reason.
	ErrInvalid = errors.New("invalid external account")
	// ErrLink is a sign-in link that is unknown, used, expired, or for an
	// account that may not sign in.
	ErrLink = errors.New("that sign-in link cannot be used")
)

// Account is one external person.
type Account struct {
	ID           string     `json:"id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	Persona      string     `json:"persona"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	SuspendedAt  *time.Time `json:"suspended_at,omitempty"`
	CreatedBy    string     `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	LastSignInAt *time.Time `json:"last_sign_in_at,omitempty"`
	// ConvertedAt is when the person first signed in through the identity
	// provider, which closed the account.
	ConvertedAt *time.Time `json:"converted_at,omitempty"`
}

// UserID is the user id the account's sessions carry.
func (a Account) UserID() string { return UserIDPrefix + a.ID }

// Active reports whether the account may sign in and be served at now.
func (a Account) Active(now time.Time) bool {
	return a.SuspendedAt == nil && a.ConvertedAt == nil && (a.ExpiresAt == nil || now.Before(*a.ExpiresAt))
}

// Status is the one word the admin list shows for the account.
func (a Account) Status(now time.Time) string {
	switch {
	case a.ConvertedAt != nil:
		return "converted"
	case a.SuspendedAt != nil:
		return "suspended"
	case a.ExpiresAt != nil && !now.Before(*a.ExpiresAt):
		return "expired"
	}
	return "active"
}

// AccountID is the account id a user id names, and whether it names one.
func AccountID(userID string) (string, bool) {
	id, ok := strings.CutPrefix(userID, UserIDPrefix)
	return id, ok && id != ""
}

// Settings is the stored configuration.
type Settings struct {
	// SessionHours is how long a signed-in session lasts.
	SessionHours int `json:"session_hours" example:"12"`
	// InviteHours is how long the invitation email's link can be used.
	InviteHours int `json:"invite_hours" example:"168"`
	// LinkMinutes is how long a link asked for on the sign-in screen can be
	// used.
	LinkMinutes int `json:"link_minutes" example:"15"`
}

// Defaults is the configuration before an administrator writes the section.
func Defaults() Settings {
	return Settings{SessionHours: DefaultSessionHours, InviteHours: DefaultInviteHours, LinkMinutes: DefaultLinkMinutes}
}

// Validate refuses a duration out of range.
func (s Settings) Validate() error {
	for _, f := range []struct {
		name     string
		got, max int
	}{
		{"session_hours", s.SessionHours, maxSessionHours},
		{"invite_hours", s.InviteHours, maxInviteHours},
		{"link_minutes", s.LinkMinutes, maxLinkMinutes},
	} {
		if f.got < 1 || f.got > f.max {
			return invalid(settingsRangeMessage, f.name, f.max)
		}
	}
	return nil
}

// Session is the session length.
func (s Settings) Session() time.Duration { return time.Duration(s.SessionHours) * time.Hour }

// Invite is the invitation link's lifetime.
func (s Settings) Invite() time.Duration { return time.Duration(s.InviteHours) * time.Hour }

// Link is a requested link's lifetime.
func (s Settings) Link() time.Duration { return time.Duration(s.LinkMinutes) * time.Minute }

// NewAccount is what an administrator gives to create an account.
type NewAccount struct {
	Email     string     `json:"email"`
	Name      string     `json:"name"`
	Persona   string     `json:"persona"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// invalidError is input that fails validation: ErrInvalid with the reason.
type invalidError struct{ reason string }

// Error is ErrInvalid's text with the reason.
func (e *invalidError) Error() string { return ErrInvalid.Error() + ": " + e.reason }

// Is makes errors.Is(err, ErrInvalid) hold.
func (*invalidError) Is(target error) bool { return target == ErrInvalid }

// invalid is ErrInvalid with the reason the format and args give.
func invalid(format string, args ...any) error {
	return &invalidError{reason: fmt.Sprintf(format, args...)}
}

// normalize trims the input and refuses what cannot be stored.
func (n NewAccount) normalize(now time.Time) (NewAccount, error) {
	n.Email = strings.TrimSpace(n.Email)
	n.Name = strings.TrimSpace(n.Name)
	n.Persona = strings.TrimSpace(n.Persona)
	addr, err := mail.ParseAddress(n.Email)
	if err != nil || addr.Address != n.Email || len(n.Email) > maxEmailLen {
		return n, invalid("email must be an address")
	}
	if len(n.Name) > maxNameLen {
		return n, invalid("name is longer than %d characters", maxNameLen)
	}
	if n.Persona == "" {
		return n, invalid("persona is required")
	}
	if n.ExpiresAt != nil && !n.ExpiresAt.After(now) {
		return n, invalid("expires_at must be in the future")
	}
	return n, nil
}
