// Package totp is the time-based one-time code an authenticator app shows
// (RFC 6238), computed from the seed the provider's QR code carries (#2065).
// A stored secret of kind totp holds the seed; the api gateway fills
// {{totp:<name>}} with the code for the moment the request is sent.
//
// It knows nothing about storage or who may use a code: it reads the seed
// and its parameters out of what an administrator pastes, and computes a
// code for a period.
package totp

import (
	"crypto/hmac"
	"crypto/sha1" // #nosec G505 -- RFC 6238's default algorithm, which every authenticator app uses
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The algorithms a code may be computed with.
const (
	AlgorithmSHA1   = "SHA1"
	AlgorithmSHA256 = "SHA256"
	AlgorithmSHA512 = "SHA512"
)

// The defaults a bare seed takes, which are an otpauth URI's when it names
// none: SHA1, six digits, thirty seconds.
const (
	DefaultDigits = 6
	DefaultPeriod = 30
)

// MinSeedBytes is the shortest seed accepted: 80 bits, the shortest any
// provider issues. RFC 4226 recommends 160.
const MinSeedBytes = 10

// MaxPeriod bounds the period an otpauth URI may name.
const MaxPeriod = 300

// The code lengths RFC 4226 allows that providers issue.
const (
	digitsSix   = 6
	digitsEight = 8
)

// RFC 4226 5.3 dynamic truncation: the low nibble of the last byte picks the
// offset, and the top bit of the four bytes there is cleared.
const (
	offsetMask = 0x0f
	signMask   = 0x7fffffff
	decimal    = 10
)

// ErrInvalid is wrapped by every refusal of what was pasted.
var ErrInvalid = errors.New("invalid authenticator seed")

// Params are what a code is computed with besides the seed.
type Params struct {
	Algorithm string `json:"algorithm" example:"SHA1"`
	Digits    int    `json:"digits" example:"6"`
	Period    int    `json:"period" example:"30"`
}

// Defaults are the parameters a bare seed takes.
func Defaults() Params {
	return Params{Algorithm: AlgorithmSHA1, Digits: DefaultDigits, Period: DefaultPeriod}
}

// Parse reads an authenticator seed as an administrator pastes it: the
// otpauth://totp/... URI the provider's QR code encodes, or the bare base32
// seed. It returns the seed in canonical base32 (upper case, no padding or
// spaces), which is what is stored, and the code's parameters. Every refusal
// names what is wrong.
func Parse(input string) (seed string, p Params, err error) {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(strings.ToLower(input), "otpauth://") {
		return parseURI(input)
	}
	seed, err = canonicalSeed(input)
	return seed, Defaults(), err
}

// parseURI reads an otpauth URI.
func parseURI(raw string) (string, Params, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", Params{}, fmt.Errorf("the otpauth URI does not parse: %w", ErrInvalid)
	}
	switch strings.ToLower(u.Host) {
	case "totp":
	case "hotp":
		return "", Params{}, fmt.Errorf("an hotp (counter-based) seed is not supported, only totp: the counter would have to be kept in step with the provider: %w", ErrInvalid)
	default:
		return "", Params{}, fmt.Errorf("the otpauth URI's type is %q, want totp: %w", u.Host, ErrInvalid)
	}
	q := u.Query()
	seed, err := canonicalSeed(q.Get("secret"))
	if err != nil {
		return "", Params{}, err
	}
	p := Defaults()
	if a := q.Get("algorithm"); a != "" {
		p.Algorithm = strings.ToUpper(a)
	}
	if p.Digits, err = intParam(q, "digits", DefaultDigits); err != nil {
		return "", Params{}, err
	}
	if p.Period, err = intParam(q, "period", DefaultPeriod); err != nil {
		return "", Params{}, err
	}
	return seed, p, p.Validate()
}

// intParam reads an integer query parameter, or def when it is absent.
func intParam(q url.Values, key string, def int) (int, error) {
	raw := q.Get(key)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("the otpauth URI's %s is %q, not a number: %w", key, raw, ErrInvalid)
	}
	return n, nil
}

// Validate refuses parameters a code cannot be computed with.
func (p Params) Validate() error {
	switch p.Algorithm {
	case AlgorithmSHA1, AlgorithmSHA256, AlgorithmSHA512:
	default:
		return fmt.Errorf("algorithm %q is not SHA1, SHA256 or SHA512: %w", p.Algorithm, ErrInvalid)
	}
	if p.Digits != digitsSix && p.Digits != digitsEight {
		return fmt.Errorf("digits is %d, want 6 or 8: %w", p.Digits, ErrInvalid)
	}
	if p.Period < 1 || p.Period > MaxPeriod {
		return fmt.Errorf("period is %d seconds, want 1 to %d: %w", p.Period, MaxPeriod, ErrInvalid)
	}
	return nil
}

// canonicalSeed reads a base32 seed the way authenticator apps accept one:
// any case, with spaces, with or without padding.
func canonicalSeed(raw string) (string, error) {
	s := strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "=", "").Replace(raw))
	if s == "" {
		return "", fmt.Errorf("the seed is empty: %w", ErrInvalid)
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("the seed is not base32 (letters A-Z and digits 2-7): %w", ErrInvalid)
	}
	if len(key) < MinSeedBytes {
		return "", fmt.Errorf("the seed is %d bits, shorter than the %d any provider issues: %w", len(key)*8, MinSeedBytes*8, ErrInvalid)
	}
	return s, nil
}

// Counter is the period t falls in.
func (p Params) Counter(t time.Time) int64 {
	return t.Unix() / int64(p.Period)
}

// Start is when period counter begins.
func (p Params) Start(counter int64) time.Time {
	return time.Unix(counter*int64(p.Period), 0)
}

// Code is the code for period counter, from a seed Parse returned.
func Code(seed string, p Params, counter int64) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(seed)
	if err != nil {
		return "", fmt.Errorf("the stored seed is not base32: %w", ErrInvalid)
	}
	if err := p.Validate(); err != nil {
		return "", err
	}
	mac := hmac.New(hasher(p.Algorithm), key)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter)) // #nosec G115 -- a period count since 1970 is never negative
	_, _ = mac.Write(msg[:])
	sum := mac.Sum(nil)
	// RFC 4226 5.3 dynamic truncation.
	off := sum[len(sum)-1] & offsetMask
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & signMask
	mod := uint32(1)
	for range p.Digits {
		mod *= decimal
	}
	return fmt.Sprintf("%0*d", p.Digits, bin%mod), nil
}

// hasher is the hash an algorithm names; Validate has refused any other.
func hasher(algorithm string) func() hash.Hash {
	switch algorithm {
	case AlgorithmSHA256:
		return sha256.New
	case AlgorithmSHA512:
		return sha512.New
	default:
		return sha1.New
	}
}
