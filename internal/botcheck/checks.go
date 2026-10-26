// Package botcheck is the bot check a bot_check endpoint requires (#2173,
// #2259): a named check an administrator configures and an endpoint picks,
// either the platform's own proof-of-work challenge or Cloudflare Turnstile or
// hCaptcha verified server side. A challenge is signed, short-lived and bound
// to one check and one endpoint, and an answer is recorded as used in
// PostgreSQL the moment it is accepted, so it cannot be replayed on any
// replica. The work a proof-of-work challenge asks for rises with the
// endpoint's recent request rate. The checks are the bot_checks table, which
// administrators edit under Admin > Settings > Bot checks.
package botcheck

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// SecretUse is what a stored secret's allow_connections names for the bot
// check to read it: the provider's secret key goes to the provider through
// the platform, never through a connection a call names.
const SecretUse = "bot_check"

// Providers.
const (
	// ProviderPoW is the platform's own proof-of-work challenge: it calls no
	// third party. It makes each request cost work, which any program can
	// pay; it slows an automated caller rather than stopping one.
	ProviderPoW = "pow"
	// ProviderTurnstile and ProviderHCaptcha need a browser to answer.
	ProviderTurnstile = "turnstile"
	ProviderHCaptcha  = "hcaptcha"
)

// DefaultName is the check migration 000221 made from the deployment's one
// bot check setting.
const DefaultName = "default"

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
	maxNameLen               = 63
	providerSecretRequiredFm = "provider %s needs %s"
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Errors a check is looked up, written or deleted with.
var (
	// ErrNotFound reports a name no check has.
	ErrNotFound = errors.New("no bot check has this name")
)

// InUseError refuses the deletion of a check endpoints name.
type InUseError struct {
	Name      string
	Endpoints []string
}

func (e *InUseError) Error() string {
	return fmt.Sprintf("bot check %q is used by the endpoints %s: bind them to another check first",
		e.Name, strings.Join(e.Endpoints, ", "))
}

// Check is one named bot check.
type Check struct {
	// Name is how an endpoint names the check.
	Name string `json:"name" example:"default"`
	// Provider is pow, turnstile or hcaptcha.
	Provider string `json:"provider" example:"pow"`
	// SiteKey is the Turnstile or hCaptcha site key a page renders the
	// widget with. It is public.
	SiteKey string `json:"site_key"`
	// SecretName is the stored secret (Admin > Secrets) holding the
	// provider's secret key, whose allow_connections names SecretUse and
	// which lists no personas. The key never leaves the server.
	SecretName string `json:"secret_name"`
	// Difficulty is the proof-of-work's leading zero bits while an endpoint
	// is quiet, and MaxDifficulty the most a flood raises it to.
	Difficulty    int `json:"difficulty"`
	MaxDifficulty int `json:"max_difficulty"`
	// RateThreshold is the answers a minute an endpoint takes before each
	// doubling of its rate adds a bit of difficulty.
	RateThreshold int `json:"rate_threshold"`
	// ChallengeTTLSeconds is how long a challenge may be answered.
	ChallengeTTLSeconds int       `json:"challenge_ttl_seconds"`
	CreatedBy           string    `json:"created_by,omitempty"`
	UpdatedBy           string    `json:"updated_by,omitempty"`
	CreatedAt           time.Time `json:"created_at,omitzero"`
	UpdatedAt           time.Time `json:"updated_at,omitzero"`
}

// New is a check named name at the proof of work's defaults.
func New(name string) Check {
	return Check{
		Name: name, Provider: ProviderPoW, Difficulty: DefaultDifficulty, MaxDifficulty: DefaultMaxDifficulty,
		RateThreshold: DefaultRateThreshold, ChallengeTTLSeconds: DefaultChallengeTTL,
	}
}

// Input is the write shape. An absent field keeps the stored value, or the
// default on a new check.
type Input struct {
	Provider            *string `json:"provider,omitempty"`
	SiteKey             *string `json:"site_key,omitempty"`
	SecretName          *string `json:"secret_name,omitempty"`
	Difficulty          *int    `json:"difficulty,omitempty"`
	MaxDifficulty       *int    `json:"max_difficulty,omitempty"`
	RateThreshold       *int    `json:"rate_threshold,omitempty"`
	ChallengeTTLSeconds *int    `json:"challenge_ttl_seconds,omitempty"`
}

// Apply returns stored with in written over it, or the first reason the
// result is invalid. Whether the named secret exists is the route's to check.
func (in Input) Apply(stored Check) (Check, error) {
	out := stored
	setString(&out.Provider, in.Provider)
	setString(&out.SiteKey, in.SiteKey)
	setString(&out.SecretName, in.SecretName)
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

// Validate reports the first thing wrong with the check.
func (c Check) Validate() error {
	if err := ValidateName(c.Name); err != nil {
		return err
	}
	if err := c.validateProvider(); err != nil {
		return err
	}
	return c.validateWork()
}

// ValidateName refuses a name a check cannot have.
func ValidateName(name string) error {
	if name == "" || len(name) > maxNameLen || !namePattern.MatchString(name) {
		return fmt.Errorf("bot check name %q must be 1 to %d lowercase letters, digits, hyphens and underscores, "+
			"starting with a letter or digit", name, maxNameLen)
	}
	return nil
}

// NeedsBrowser reports whether answering the check takes a browser, which
// the proof of work does not.
func (c Check) NeedsBrowser() bool { return c.Provider != ProviderPoW }

func (c Check) validateProvider() error {
	switch c.Provider {
	case ProviderPoW:
	case ProviderTurnstile, ProviderHCaptcha:
		if c.SiteKey == "" {
			return fmt.Errorf(providerSecretRequiredFm, c.Provider, "a site_key")
		}
		if c.SecretName == "" {
			return fmt.Errorf(providerSecretRequiredFm, c.Provider, "secret_name, the stored secret holding its secret key")
		}
	default:
		return fmt.Errorf("provider %q is not pow, turnstile or hcaptcha", c.Provider)
	}
	switch {
	case len(c.SiteKey) > maxSiteKeyLen:
		return fmt.Errorf("site_key is longer than %d characters", maxSiteKeyLen)
	case len(c.SecretName) > maxSecretNameLen:
		return fmt.Errorf("secret_name is longer than %d characters", maxSecretNameLen)
	}
	return nil
}

func (c Check) validateWork() error {
	switch {
	case c.Difficulty < minDifficulty || c.Difficulty > maxDifficulty:
		return fmt.Errorf("difficulty must be from %d to %d bits", minDifficulty, maxDifficulty)
	case c.MaxDifficulty < c.Difficulty || c.MaxDifficulty > maxDifficulty:
		return fmt.Errorf("max_difficulty must be from difficulty (%d) to %d bits", c.Difficulty, maxDifficulty)
	case c.RateThreshold < 1 || c.RateThreshold > maxRateThreshold:
		return fmt.Errorf("rate_threshold must be from 1 to %d answers a minute", maxRateThreshold)
	case c.ChallengeTTLSeconds < minChallengeTTL || c.ChallengeTTLSeconds > maxChallengeTTL:
		return fmt.Errorf("challenge_ttl_seconds must be from %d to %d", minChallengeTTL, maxChallengeTTL)
	}
	return nil
}

// Store keeps the checks.
type Store interface {
	// List is every check, by name.
	List(ctx context.Context) ([]Check, error)
	// Get is one check, ErrNotFound when no check has the name.
	Get(ctx context.Context, name string) (Check, error)
	// Put creates or replaces a check, keeping the creation stamp of the one
	// it replaces.
	Put(ctx context.Context, c Check, author string) (Check, error)
	// Delete removes a check, ErrNotFound when none has the name and an
	// *InUseError naming the endpoints when any names it.
	Delete(ctx context.Context, name string) error
	// UsedBy is each check's endpoints, by check name.
	UsedBy(ctx context.Context) (map[string][]string, error)
}
