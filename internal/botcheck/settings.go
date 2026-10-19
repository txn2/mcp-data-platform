// Package botcheck is the bot check a public endpoint requires (#2173): by
// default a proof-of-work challenge the platform issues and verifies itself,
// or Cloudflare Turnstile or hCaptcha verified server side. A challenge is
// signed, short-lived and bound to one endpoint, and an answer is recorded as
// used in PostgreSQL the moment it is accepted, so it cannot be replayed on
// any replica. The work a challenge asks for rises with the endpoint's recent
// request rate. The configuration is the platform_settings section "bot_check",
// which administrators edit under Admin > Settings > Bot check.
package botcheck

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
)

// SettingsSection is the platform_settings section holding the settings.
const SettingsSection = "bot_check"

// SecretUse is what a stored secret's allow_connections names for the bot
// check to read it: the provider's secret key goes to the provider through
// the platform, never through a connection a call names.
const SecretUse = "bot_check"

// Providers.
const (
	// ProviderPoW is the platform's own proof-of-work challenge, the default:
	// it calls no third party.
	ProviderPoW       = "pow"
	ProviderTurnstile = "turnstile"
	ProviderHCaptcha  = "hcaptcha"
	// ProviderNone checks nothing. A public endpoint can then be bound only
	// when an administrator allows it (AllowPublicWithoutCheck).
	ProviderNone = "none"
)

// Defaults and bounds of the proof-of-work challenge. Difficulty is the
// number of leading zero bits the answer's SHA-256 must have: 16 is about
// 65,000 hashes, a fraction of a second in a browser.
const (
	DefaultDifficulty        = 16
	DefaultMaxDifficulty     = 22
	DefaultRateThreshold     = 30
	DefaultChallengeTTL      = 300
	minDifficulty            = 8
	maxDifficulty            = 28
	minChallengeTTL          = 5
	maxChallengeTTL          = 3600
	maxRateThreshold         = 100000
	maxSiteKeyLen            = 200
	maxSecretNameLen         = 128
	providerSecretRequiredFm = "provider %s needs %s"
)

// Settings is the stored configuration.
type Settings struct {
	// Provider is pow, turnstile, hcaptcha or none.
	Provider string `json:"provider" example:"pow"`
	// SiteKey is the Turnstile or hCaptcha site key a page renders the
	// widget with. It is public.
	SiteKey string `json:"site_key"`
	// SecretName is the stored secret (Admin > Secrets) holding the
	// provider's secret key, whose allow_connections names SecretUse and
	// which lists no personas. The key never leaves the server.
	SecretName string `json:"secret_name"`
	// AllowPublicWithoutCheck lets a public endpoint be bound and answered
	// while the provider is none.
	AllowPublicWithoutCheck bool `json:"allow_public_without_check"`
	// Difficulty is the proof-of-work's leading zero bits while an endpoint
	// is quiet, and MaxDifficulty the most a flood raises it to.
	Difficulty    int `json:"difficulty"`
	MaxDifficulty int `json:"max_difficulty"`
	// RateThreshold is the answers a minute an endpoint takes before each
	// doubling of its rate adds a bit of difficulty.
	RateThreshold int `json:"rate_threshold"`
	// ChallengeTTLSeconds is how long a challenge may be answered.
	ChallengeTTLSeconds int `json:"challenge_ttl_seconds"`
}

// Defaults is the configuration before an administrator writes the section:
// the proof-of-work challenge, which needs no setup.
func Defaults() Settings {
	return Settings{
		Provider: ProviderPoW, Difficulty: DefaultDifficulty, MaxDifficulty: DefaultMaxDifficulty,
		RateThreshold: DefaultRateThreshold, ChallengeTTLSeconds: DefaultChallengeTTL,
	}
}

// Checks reports whether the provider checks anything.
func (s Settings) Checks() bool { return s.Provider != ProviderNone }

// PublicAllowed reports whether a public endpoint may be bound and answered:
// there is a bot check, or an administrator allowed public endpoints without
// one.
func (s Settings) PublicAllowed() bool { return s.Checks() || s.AllowPublicWithoutCheck }

// PublicAllowedWhere is PublicAllowed on a deployment that can, or cannot,
// sign a proof-of-work challenge: one that cannot has no proof of work.
func (s Settings) PublicAllowedWhere(canSign bool) bool {
	if s.Provider == ProviderPoW && !canSign {
		return s.AllowPublicWithoutCheck
	}
	return s.PublicAllowed()
}

// Input is the write shape. An absent field keeps the stored value.
type Input struct {
	Provider                *string `json:"provider,omitempty"`
	SiteKey                 *string `json:"site_key,omitempty"`
	SecretName              *string `json:"secret_name,omitempty"`
	AllowPublicWithoutCheck *bool   `json:"allow_public_without_check,omitempty"`
	Difficulty              *int    `json:"difficulty,omitempty"`
	MaxDifficulty           *int    `json:"max_difficulty,omitempty"`
	RateThreshold           *int    `json:"rate_threshold,omitempty"`
	ChallengeTTLSeconds     *int    `json:"challenge_ttl_seconds,omitempty"`
}

// Apply returns stored with in written over it, or the first reason the
// result is invalid. Whether the named secret exists is the route's to check.
func (in Input) Apply(stored Settings) (Settings, error) {
	out := stored
	setString(&out.Provider, in.Provider)
	setString(&out.SiteKey, in.SiteKey)
	setString(&out.SecretName, in.SecretName)
	if in.AllowPublicWithoutCheck != nil {
		out.AllowPublicWithoutCheck = *in.AllowPublicWithoutCheck
	}
	setInt(&out.Difficulty, in.Difficulty)
	setInt(&out.MaxDifficulty, in.MaxDifficulty)
	setInt(&out.RateThreshold, in.RateThreshold)
	setInt(&out.ChallengeTTLSeconds, in.ChallengeTTLSeconds)
	return out, out.Validate()
}

func setString(dst, v *string) {
	if v != nil {
		*dst = strings.TrimSpace(*v)
	}
}

func setInt(dst, v *int) {
	if v != nil {
		*dst = *v
	}
}

// Validate reports the first thing wrong with the settings.
func (s Settings) Validate() error {
	if err := s.validateProvider(); err != nil {
		return err
	}
	return s.validateWork()
}

func (s Settings) validateProvider() error {
	switch s.Provider {
	case ProviderPoW, ProviderNone:
	case ProviderTurnstile, ProviderHCaptcha:
		if s.SiteKey == "" {
			return fmt.Errorf(providerSecretRequiredFm, s.Provider, "a site_key")
		}
		if s.SecretName == "" {
			return fmt.Errorf(providerSecretRequiredFm, s.Provider, "secret_name, the stored secret holding its secret key")
		}
	default:
		return fmt.Errorf("provider %q is not pow, turnstile, hcaptcha or none", s.Provider)
	}
	switch {
	case len(s.SiteKey) > maxSiteKeyLen:
		return fmt.Errorf("site_key is longer than %d characters", maxSiteKeyLen)
	case len(s.SecretName) > maxSecretNameLen:
		return fmt.Errorf("secret_name is longer than %d characters", maxSecretNameLen)
	}
	return nil
}

func (s Settings) validateWork() error {
	switch {
	case s.Difficulty < minDifficulty || s.Difficulty > maxDifficulty:
		return fmt.Errorf("difficulty must be from %d to %d bits", minDifficulty, maxDifficulty)
	case s.MaxDifficulty < s.Difficulty || s.MaxDifficulty > maxDifficulty:
		return fmt.Errorf("max_difficulty must be from difficulty (%d) to %d bits", s.Difficulty, maxDifficulty)
	case s.RateThreshold < 1 || s.RateThreshold > maxRateThreshold:
		return fmt.Errorf("rate_threshold must be from 1 to %d answers a minute", maxRateThreshold)
	case s.ChallengeTTLSeconds < minChallengeTTL || s.ChallengeTTLSeconds > maxChallengeTTL:
		return fmt.Errorf("challenge_ttl_seconds must be from %d to %d", minChallengeTTL, maxChallengeTTL)
	}
	return nil
}

// SettingsStore reads and writes the section.
type SettingsStore interface {
	Get(ctx context.Context) (settingsjson.Stored[Settings], error)
	Set(ctx context.Context, v Settings, author string) error
}

// NewSettingsStore is the section over db.
func NewSettingsStore(db *sql.DB) SettingsStore {
	return settingsjson.New[Settings](db, SettingsSection)
}

// View is the settings as an administrator reads them.
type View struct {
	Settings
	UpdatedBy string `json:"updated_by,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// Read returns the stored settings, or the defaults when none were written.
func Read(ctx context.Context, store SettingsStore) (View, error) {
	stored, err := store.Get(ctx)
	if errors.Is(err, settingsjson.ErrNotFound) {
		return View{Settings: Defaults()}, nil
	}
	if err != nil {
		return View{}, fmt.Errorf("reading the bot check settings: %w", err)
	}
	v := View{Settings: stored.Value, UpdatedBy: stored.UpdatedBy}
	if !stored.UpdatedAt.IsZero() {
		v.UpdatedAt = stored.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return v, nil
}
