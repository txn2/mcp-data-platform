package notification

import (
	"context"
	"time"
)

// Prefs is one user's notification preferences. Absence of a stored row means
// DefaultPrefs applies (immediate delivery, all categories on), per the
// platform's important-features-default-on convention.
type Prefs struct {
	Email           string    `json:"email"`
	Mode            string    `json:"mode"`
	SharesEnabled   bool      `json:"shares_enabled"`
	CommentsEnabled bool      `json:"comments_enabled"`
	MentionsEnabled bool      `json:"mentions_enabled"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// DefaultPrefs returns the preferences applied to a user with no stored row.
func DefaultPrefs(email string) Prefs {
	return Prefs{
		Email:           email,
		Mode:            ModeImmediate,
		SharesEnabled:   true,
		CommentsEnabled: true,
		MentionsEnabled: true,
	}
}

// PrefsUpdate carries the fields of a preferences write; nil fields keep the
// current (or default) value.
type PrefsUpdate struct {
	Mode            *string `json:"mode,omitempty"`
	SharesEnabled   *bool   `json:"shares_enabled,omitempty"`
	CommentsEnabled *bool   `json:"comments_enabled,omitempty"`
	MentionsEnabled *bool   `json:"mentions_enabled,omitempty"`
}

// Apply overlays the update's set fields onto p, leaving the rest untouched.
// Which fields a partial write may leave alone is a property of the preference
// model rather than of any one backend, so every store applies an update the
// same way by calling this.
func (u PrefsUpdate) Apply(p *Prefs) {
	if u.Mode != nil {
		p.Mode = *u.Mode
	}
	if u.SharesEnabled != nil {
		p.SharesEnabled = *u.SharesEnabled
	}
	if u.CommentsEnabled != nil {
		p.CommentsEnabled = *u.CommentsEnabled
	}
	if u.MentionsEnabled != nil {
		p.MentionsEnabled = *u.MentionsEnabled
	}
}

// ValidMode reports whether m is one of the delivery modes.
func ValidMode(m string) bool {
	return m == ModeOff || m == ModeImmediate || m == ModeDaily
}

// PrefsStore persists per-user notification preferences.
type PrefsStore interface {
	// Get returns the user's preferences, falling back to DefaultPrefs when
	// no row exists. It never returns an error for an unknown user.
	Get(ctx context.Context, email string) (Prefs, error)
	// Set upserts the user's preferences, applying u over the current values.
	Set(ctx context.Context, email string, u PrefsUpdate) (Prefs, error)
}
