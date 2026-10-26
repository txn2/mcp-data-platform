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

// Challenge is what a page is handed to answer before it calls a bot_check
// endpoint.
type Challenge struct {
	// Check is the name of the check the endpoint names.
	Check string `json:"check" example:"default"`
	// Provider is pow, turnstile or hcaptcha.
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

// Checker issues and verifies the named checks.
type Checker struct {
	Checks  Store
	Answers AnswerStore
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

// NewChecker is the checker over db: its checks and its answer table. key
// signs proof-of-work challenges (Key); secret reads a provider's key.
func NewChecker(db *sql.DB, key []byte, secret func(ctx context.Context, name string) (string, error)) *Checker {
	return &Checker{
		Checks: NewStore(db), Answers: NewAnswerStore(db), Key: key, Secret: secret, Verifier: NewVerifier(),
	}
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Usable reports why an endpoint cannot be bound to the check named name: no
// check has the name, or it is a proof of work and the deployment has no key
// to sign one with. Nil when it can.
func (c *Checker) Usable(ctx context.Context, name string) error {
	chk, err := c.Checks.Get(ctx, name)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("no bot check is named %q: an administrator configures them under Admin > Settings > Bot checks", name)
	}
	if err != nil {
		return fmt.Errorf("reading bot check %q: %w", name, err)
	}
	if chk.Provider == ProviderPoW && c.Key == nil {
		return fmt.Errorf("bot check %q is a proof of work, and this deployment cannot sign one: it needs "+
			"auth.browser_session.signing_key", name)
	}
	return nil
}

// Issue is the challenge a page answers before it calls endpoint, under the
// check named check. A proof of work asks for more as the endpoint's recent
// rate rises.
func (c *Checker) Issue(ctx context.Context, check, endpoint string) (Challenge, error) {
	chk, err := c.Checks.Get(ctx, check)
	if err != nil {
		return Challenge{}, err //nolint:wrapcheck // the store names the read
	}
	if chk.NeedsBrowser() {
		return Challenge{Check: chk.Name, Provider: chk.Provider, SiteKey: chk.SiteKey}, nil
	}
	if c.Key == nil {
		return Challenge{}, ErrUnavailable
	}
	now := c.now()
	rate, err := c.Answers.Recent(ctx, endpoint, now.Add(-rateWindow))
	if err != nil {
		return Challenge{}, fmt.Errorf("reading the endpoint's recent rate: %w", err)
	}
	difficulty := difficultyFor(chk, rate)
	expires := now.Add(time.Duration(chk.ChallengeTTLSeconds) * time.Second).Truncate(time.Second)
	token, err := issuePoW(c.Key, chk.Name, endpoint, difficulty, expires)
	if err != nil {
		return Challenge{}, err
	}
	return Challenge{Check: chk.Name, Provider: ProviderPoW, Token: token, Difficulty: difficulty, ExpiresAt: expires}, nil
}

// Verify accepts answer for endpoint under the check named check, or returns
// why it is refused, and records an accepted answer as used.
func (c *Checker) Verify(ctx context.Context, check, endpoint, answer string) error {
	chk, err := c.Checks.Get(ctx, check)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	if answer == "" {
		return ErrMissing
	}
	if chk.Provider == ProviderPoW {
		return c.verifyPoW(ctx, chk.Name, endpoint, answer)
	}
	return c.verifyProvider(ctx, chk, endpoint, answer)
}

func (c *Checker) verifyPoW(ctx context.Context, check, endpoint, answer string) error {
	if c.Key == nil {
		return ErrUnavailable
	}
	claims, err := checkPoW(c.Key, check, endpoint, answer, c.now())
	if err != nil {
		return err
	}
	return c.use(ctx, "pow:"+claims.Nonce, endpoint, time.Unix(claims.Expires, 0))
}

// verifyProvider asks the provider with the check's own secret key, so a
// token a widget of another check produced is refused by the provider unless
// both checks hold one key.
func (c *Checker) verifyProvider(ctx context.Context, chk Check, endpoint, token string) error {
	if len(token) > maxAnswerLen {
		return ErrInvalid
	}
	secret, err := c.Secret(ctx, chk.SecretName)
	if err != nil {
		return fmt.Errorf("reading the %s secret key: %w", chk.Provider, err)
	}
	if err := c.Verifier.Verify(ctx, chk.Provider, secret, token); err != nil {
		return err //nolint:wrapcheck // ErrInvalid and ErrReplayed are matched by the caller as they are
	}
	sum := sha256.Sum256([]byte(token))
	return c.use(ctx, chk.Provider+":"+hex.EncodeToString(sum[:]), endpoint, c.now().Add(providerTokenTTL))
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
	for _, r := range []error{ErrMissing, ErrInvalid, ErrExpired, ErrReplayed, ErrOtherEndpoint, ErrOtherCheck} {
		if errors.Is(err, r) {
			return true
		}
	}
	return false
}
