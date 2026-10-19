package botcheck

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// rateWindow is how far back an endpoint's recent rate is counted.
const rateWindow = time.Minute

// providerTokenTTL is how long a provider token's answer is kept: longer
// than any provider lets a token be verified, so a token replayed late is
// still found.
const providerTokenTTL = 10 * time.Minute

// Challenge is what a page is handed to answer before it calls a public
// endpoint.
type Challenge struct {
	// Provider is pow, turnstile, hcaptcha or none.
	Provider string `json:"provider" example:"pow"`
	// Token is the proof-of-work challenge; the answer is Token~counter, the
	// counter whose SHA-256 of Token~counter has Difficulty leading zero bits.
	Token      string `json:"token,omitempty"`
	Difficulty int    `json:"difficulty,omitempty"`
	// SiteKey is the Turnstile or hCaptcha widget's site key; the answer is
	// the token the widget produces.
	SiteKey   string    `json:"site_key,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

// Checker issues and verifies bot checks under the deployment's settings.
type Checker struct {
	Settings SettingsStore
	Answers  AnswerStore
	// Key signs proof-of-work challenges; nil leaves the deployment unable to
	// issue one.
	Key []byte
	// Secret reads the stored secret holding a provider's secret key.
	Secret func(ctx context.Context, name string) (string, error)
	// Verifier asks a provider about a token.
	Verifier interface {
		Verify(ctx context.Context, provider, secret, token string) error
	}
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// New is the checker over db: its settings section and its answer table.
// key signs proof-of-work challenges (Key); secret reads a provider's key.
func New(db *sql.DB, key []byte, secret func(ctx context.Context, name string) (string, error)) *Checker {
	return &Checker{
		Settings: NewSettingsStore(db), Answers: NewAnswerStore(db), Key: key, Secret: secret, Verifier: NewVerifier(),
	}
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Current is the deployment's settings as they stand.
func (c *Checker) Current(ctx context.Context) (Settings, error) {
	v, err := Read(ctx, c.Settings)
	if err != nil {
		return Settings{}, err
	}
	return v.Settings, nil
}

// PublicAllowed reports whether a public endpoint may be bound and answered
// (Settings.PublicAllowed). Proof of work counts only where there is a key to
// sign challenges with.
func (c *Checker) PublicAllowed(ctx context.Context) (bool, error) {
	s, err := c.Current(ctx)
	if err != nil {
		return false, err
	}
	return s.PublicAllowedWhere(c.Key != nil), nil
}

// Issue is the challenge a page answers before it calls endpoint. A proof of
// work asks for more as the endpoint's recent rate rises.
func (c *Checker) Issue(ctx context.Context, endpoint string) (Challenge, error) {
	s, err := c.Current(ctx)
	if err != nil {
		return Challenge{}, err
	}
	switch s.Provider {
	case ProviderNone:
		return Challenge{Provider: ProviderNone}, nil
	case ProviderTurnstile, ProviderHCaptcha:
		return Challenge{Provider: s.Provider, SiteKey: s.SiteKey}, nil
	}
	if c.Key == nil {
		return Challenge{}, ErrUnavailable
	}
	now := c.now()
	rate, err := c.Answers.Recent(ctx, endpoint, now.Add(-rateWindow))
	if err != nil {
		return Challenge{}, fmt.Errorf("reading the endpoint's recent rate: %w", err)
	}
	difficulty := difficultyFor(s, rate)
	expires := now.Add(time.Duration(s.ChallengeTTLSeconds) * time.Second).Truncate(time.Second)
	token, err := issuePoW(c.Key, endpoint, difficulty, expires)
	if err != nil {
		return Challenge{}, err
	}
	return Challenge{Provider: ProviderPoW, Token: token, Difficulty: difficulty, ExpiresAt: expires}, nil
}

// Verify accepts answer for endpoint, or returns why it is refused, and
// records an accepted answer as used. With the provider none it accepts
// everything; whether a public endpoint may run without a check at all is
// the binding's question (PublicAllowed), asked before this.
func (c *Checker) Verify(ctx context.Context, endpoint, answer string) error {
	s, err := c.Current(ctx)
	if err != nil {
		return err
	}
	if s.Provider == ProviderNone {
		return nil
	}
	if answer == "" {
		return ErrMissing
	}
	if s.Provider == ProviderPoW {
		return c.verifyPoW(ctx, endpoint, answer)
	}
	return c.verifyProvider(ctx, s, endpoint, answer)
}

func (c *Checker) verifyPoW(ctx context.Context, endpoint, answer string) error {
	if c.Key == nil {
		return ErrUnavailable
	}
	claims, err := checkPoW(c.Key, endpoint, answer, c.now())
	if err != nil {
		return err
	}
	return c.use(ctx, "pow:"+claims.Nonce, endpoint, time.Unix(claims.Expires, 0))
}

func (c *Checker) verifyProvider(ctx context.Context, s Settings, endpoint, token string) error {
	if len(token) > maxAnswerLen {
		return ErrInvalid
	}
	secret, err := c.Secret(ctx, s.SecretName)
	if err != nil {
		return fmt.Errorf("reading the %s secret key: %w", s.Provider, err)
	}
	if err := c.Verifier.Verify(ctx, s.Provider, secret, token); err != nil {
		return err //nolint:wrapcheck // ErrInvalid and ErrReplayed are matched by the caller as they are
	}
	sum := sha256.Sum256([]byte(token))
	return c.use(ctx, s.Provider+":"+hex.EncodeToString(sum[:]), endpoint, c.now().Add(providerTokenTTL))
}

// use records the answer, refusing one recorded before.
func (c *Checker) use(ctx context.Context, id, endpoint string, expires time.Time) error {
	first, err := c.Answers.Use(ctx, id, endpoint, expires)
	if err != nil {
		return err //nolint:wrapcheck // the store's error names what it was doing
	}
	if !first {
		return ErrReplayed
	}
	return nil
}

// Refusal reports whether err is a refusal of the answer, which is the
// caller's (403), rather than the check being unable to run (503).
func Refusal(err error) bool {
	for _, r := range []error{ErrMissing, ErrInvalid, ErrExpired, ErrReplayed, ErrOtherEndpoint} {
		if errors.Is(err, r) {
			return true
		}
	}
	return false
}
