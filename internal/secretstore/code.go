package secretstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/txn2/mcp-data-platform/internal/totp"
)

// issueCode is the one-time code a fill of sec sends, issued once per period
// (#2065). Many providers refuse a code already accepted in its period, so
// the period a code is issued for is claimed in the secret's row, which
// every replica shares: the first fill in a period claims it and sends its
// code; a later fill in the same period claims the next period, waits for it
// to begin, and sends that code. Two fills in one period from concurrent
// calls therefore get one code and one wait, never one code twice. A fill
// that finds the next period claimed as well is refused, naming when to try
// again, rather than waiting a second period.
func (s *Store) issueCode(ctx context.Context, sec Secret, seed string) (string, error) {
	p := *sec.TOTP
	now := s.now()
	counter := p.Counter(now)
	claimed, err := s.claimPeriod(ctx, sec.Name, counter)
	if err != nil {
		return "", err
	}
	if !claimed {
		counter++
		if claimed, err = s.claimPeriod(ctx, sec.Name, counter); err != nil {
			return "", err
		}
		if !claimed {
			return "", fmt.Errorf("secret %q has issued its one-time codes for this period and the next, and a provider refuses a code it already accepted; try again after %s",
				sec.Name, p.Start(counter+1).UTC().Format(time.RFC3339))
		}
		if err := s.sleep(ctx, p.Start(counter).Sub(now)); err != nil {
			return "", err
		}
	}
	return totp.Code(seed, p, counter) //nolint:wrapcheck // the totp refusal names what is wrong with the stored seed
}

// claimPeriod records counter as the last period sec issued a code for, when
// no later or equal one is recorded, and reports whether it did.
func (s *Store) claimPeriod(ctx context.Context, name string, counter int64) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE gateway_secrets SET totp_last_period = $2 WHERE name = $1 AND kind = 'totp' AND totp_last_period < $2`,
		name, counter)
	if err != nil {
		return false, fmt.Errorf("recording the one-time code period of secret %q: %w", name, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("recording the one-time code period of secret %q: %w", name, err)
	}
	return n == 1, nil
}

// CurrentCode is a totp secret's code for this moment and how many seconds
// of its period are left, for an administrator to compare with the phone app
// before an automation depends on the secret. Showing it issues nothing: the
// next fill may send the same code.
type CurrentCode struct {
	Code        string      `json:"code" example:"287082"`
	SecondsLeft int         `json:"seconds_left" example:"17"`
	Params      totp.Params `json:"totp"`
}

// ErrNotTOTP is returned for a current code of a value secret.
var ErrNotTOTP = errors.New("not an authenticator seed")

// Code returns the current code of the totp secret name.
func (s *Store) Code(ctx context.Context, name string) (CurrentCode, error) {
	sec, seed, err := s.withValue(ctx, name)
	if err != nil {
		return CurrentCode{}, err
	}
	if sec.Kind != KindTOTP {
		return CurrentCode{}, fmt.Errorf("secret %q holds a value: %w", name, ErrNotTOTP)
	}
	now := s.now()
	counter := sec.TOTP.Counter(now)
	code, err := totp.Code(seed, *sec.TOTP, counter)
	if err != nil {
		return CurrentCode{}, fmt.Errorf("computing the code of secret %q: %w", name, err)
	}
	left := int(math.Ceil(sec.TOTP.Start(counter + 1).Sub(now).Seconds()))
	return CurrentCode{Code: code, SecondsLeft: left, Params: *sec.TOTP}, nil
}
