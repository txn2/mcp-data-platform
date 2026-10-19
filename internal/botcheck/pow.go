package botcheck

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/portal/shareguest"
)

// KeyLabel domain-separates the challenge signing key from the master key it
// is derived from.
const KeyLabel = "bot-check-challenge-v1"

// Key is the challenge signing key, derived from the browser-session signing
// key (base64, auth.browser_session.signing_key) that every replica holds, so
// a challenge one replica issues verifies on another. Nil when browser
// sessions are off or the key does not decode: such a deployment cannot
// issue a proof-of-work challenge.
func Key(enabled bool, signingKey string) []byte {
	if !enabled || signingKey == "" {
		return nil
	}
	master, err := base64.StdEncoding.DecodeString(signingKey)
	if err != nil || len(master) == 0 {
		return nil
	}
	return shareguest.DeriveKey(master, KeyLabel)
}

// answerSeparator joins a challenge to the counter that answers it.
const answerSeparator = "~"

// maxAnswerLen bounds an answer before anything is decoded from it.
const maxAnswerLen = 2048

// counterBase is the base an answer's counter is written in, and bitsPerByte
// what a byte of the hash counts toward its leading zero bits.
const (
	counterBase = 10
	counterBits = 64
	bitsPerByte = 8
)

// Refusals a check answers with. Each is the reason a request was refused,
// and none says more about the deployment than that.
var (
	ErrMissing       = errors.New("the request carries no bot check answer")
	ErrInvalid       = errors.New("the bot check answer is not valid")
	ErrExpired       = errors.New("the bot check challenge has expired")
	ErrReplayed      = errors.New("the bot check answer was already used")
	ErrOtherEndpoint = errors.New("the bot check challenge was issued for another endpoint")
	ErrUnavailable   = errors.New("this deployment cannot issue a bot check challenge")
)

// claims is what a proof-of-work challenge carries, signed.
type claims struct {
	Endpoint   string `json:"e"`
	Nonce      string `json:"n"`
	Difficulty int    `json:"d"`
	Expires    int64  `json:"x"`
}

// issuePoW signs a challenge for endpoint at difficulty, answerable until
// expires.
func issuePoW(key []byte, endpoint string, difficulty int, expires time.Time) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("reading a nonce: %w", err)
	}
	payload, err := json.Marshal(claims{Endpoint: endpoint, Nonce: hex.EncodeToString(nonce), Difficulty: difficulty, Expires: expires.Unix()})
	if err != nil {
		return "", fmt.Errorf("encoding a challenge: %w", err)
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(sign(key, body)), nil
}

func sign(key []byte, body string) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(body)) // a hash.Hash never returns an error
	return m.Sum(nil)
}

// checkPoW verifies an answer to a challenge issuePoW signed: the signature,
// the endpoint, the expiry and the work. It returns the challenge's nonce and
// expiry, which the caller records as used.
func checkPoW(key []byte, endpoint, answer string, now time.Time) (claims, error) {
	if len(answer) > maxAnswerLen {
		return claims{}, ErrInvalid
	}
	token, counter, ok := strings.Cut(answer, answerSeparator)
	if !ok {
		return claims{}, ErrInvalid
	}
	c, err := signedClaims(key, token)
	if err != nil {
		return claims{}, err
	}
	switch {
	case c.Endpoint != endpoint:
		return claims{}, ErrOtherEndpoint
	case now.Unix() >= c.Expires:
		return claims{}, ErrExpired
	case !worked(token, counter, c.Difficulty):
		return claims{}, ErrInvalid
	}
	return c, nil
}

// signedClaims reads the claims of a challenge token whose signature key
// made, refusing any other.
func signedClaims(key []byte, token string) (claims, error) {
	body, sig, ok := strings.Cut(token, ".")
	if !ok {
		return claims{}, ErrInvalid
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, sign(key, body)) {
		return claims{}, ErrInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return claims{}, ErrInvalid
	}
	var c claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return claims{}, ErrInvalid
	}
	return c, nil
}

// worked reports whether counter answers token at difficulty: the SHA-256 of
// token~counter has at least difficulty leading zero bits.
func worked(token, counter string, difficulty int) bool {
	if _, err := strconv.ParseUint(counter, counterBase, counterBits); err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(token + answerSeparator + counter))
	return leadingZeroBits(sum[:]) >= difficulty
}

func leadingZeroBits(b []byte) int {
	n := 0
	for _, x := range b {
		if x != 0 {
			return n + bits.LeadingZeros8(x)
		}
		n += bitsPerByte
	}
	return n
}

// difficultyFor is the work a challenge asks for when the endpoint answered
// rate challenges in the last minute: the base while the rate is at or under
// the threshold, and one bit more for each doubling past it, up to the most.
func difficultyFor(s Settings, rate int) int {
	if rate <= s.RateThreshold {
		return s.Difficulty
	}
	extra := int(math.Ceil(math.Log2(float64(rate) / float64(s.RateThreshold))))
	return min(s.Difficulty+extra, s.MaxDifficulty)
}
