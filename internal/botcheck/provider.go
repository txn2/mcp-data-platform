package botcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/outbound"
)

// The providers' server-side verification endpoints.
const (
	TurnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	HCaptchaVerifyURL  = "https://api.hcaptcha.com/siteverify"
)

// verifyTimeout bounds one call to a provider, and maxVerifyBody what is read
// of its answer.
const (
	verifyTimeout = 10 * time.Second
	maxVerifyBody = 64 << 10
)

// Verifier asks a provider whether a token its widget produced is genuine.
type Verifier struct {
	client *http.Client
	// urls maps a provider to its verification endpoint; a test points it at
	// its own server.
	urls map[string]string
}

// NewVerifier is a Verifier over the platform's outbound client.
func NewVerifier() *Verifier {
	return &Verifier{
		client: outbound.NewClient(outbound.Options{Kind: outbound.KindBotCheck, Timeout: verifyTimeout}),
		urls:   map[string]string{ProviderTurnstile: TurnstileVerifyURL, ProviderHCaptcha: HCaptchaVerifyURL},
	}
}

// verifyAnswer is the part of a siteverify answer the platform reads.
type verifyAnswer struct {
	Success bool     `json:"success"`
	Codes   []string `json:"error-codes"`
}

// Verify posts token and secret to provider's verification endpoint, and
// returns nil only when the provider says the token is genuine. A provider
// that did not answer is an error distinct from ErrInvalid: the request was
// not shown to be a bot, the check could not be made.
func (v *Verifier) Verify(ctx context.Context, provider, secret, token string) error {
	endpoint, ok := v.urls[provider]
	if !ok {
		return fmt.Errorf("provider %q has no verification endpoint", provider)
	}
	form := url.Values{"secret": {secret}, "response": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("building the %s verification: %w", provider, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("the %s provider did not answer the verification: %w", provider, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxVerifyBody))
	if err != nil {
		return fmt.Errorf("reading the %s verification: %w", provider, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the %s provider answered the verification with HTTP %d", provider, resp.StatusCode)
	}
	var answer verifyAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		return fmt.Errorf("the %s provider answered the verification with something other than JSON: %w", provider, err)
	}
	if !answer.Success {
		if slices.Contains(answer.Codes, "timeout-or-duplicate") {
			return ErrReplayed
		}
		return ErrInvalid
	}
	return nil
}
