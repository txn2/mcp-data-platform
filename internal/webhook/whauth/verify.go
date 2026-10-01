// Package whauth decides whether a request to a webhook source came from its
// sender (#1870). It holds no state and writes nothing: the receiver hands it
// the source, the request's headers, the path token if there was one, and the
// body, and it answers with nil or with the reason the request is refused.
//
// Every comparison of a secret, or of something computed from one, is
// constant-time.
package whauth

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// Reasons a request is refused. They are what the source's page lists beside
// a rejected request, so they name what was wrong without echoing what was
// sent.
var (
	ErrMissingSignature = errors.New("the signature header is missing")
	ErrBadSignature     = errors.New("the signature does not match")
	ErrMissingTimestamp = errors.New("the timestamp header is missing")
	ErrMissingID        = errors.New("the delivery id header is missing")
	ErrBadTimestamp     = errors.New("the timestamp is not a Unix time in seconds or milliseconds")
	ErrStaleTimestamp   = errors.New("the timestamp is outside the tolerance window")
	ErrMissingToken     = errors.New("the token is missing")
	ErrBadToken         = errors.New("the token does not match")
	ErrBadCredentials   = errors.New("the basic credentials do not match")
	ErrUnknownMode      = errors.New("the source's auth mode is not recognized")
)

// Request is what verification reads from one request.
type Request struct {
	Header http.Header
	// PathToken is the segment after the source name, /hooks/{name}/{token},
	// and empty when the request had none.
	PathToken string
	Body      []byte
}

// Verify reports whether req is authentic for src at now.
func Verify(src whsource.Source, req Request, now time.Time) error {
	secrets := src.Auth.Secrets(now)
	switch src.Auth.Mode {
	case whsource.AuthHMAC:
		return verifyHMAC(src.Auth, secrets, req, now)
	case whsource.AuthHeaderToken:
		return matchAny(req.Header.Get(src.Auth.Header), secrets, ErrMissingToken, ErrBadToken)
	case whsource.AuthPathToken:
		return matchAny(req.PathToken, secrets, ErrMissingToken, ErrBadToken)
	case whsource.AuthBasic:
		return verifyBasic(src.Auth, secrets, req.Header)
	}
	return ErrUnknownMode
}

// matchAny compares got with each secret in constant time.
func matchAny(got string, secrets []string, missing, mismatch error) error {
	if got == "" {
		return missing
	}
	for _, s := range secrets {
		if subtle.ConstantTimeCompare([]byte(got), []byte(s)) == 1 {
			return nil
		}
	}
	return mismatch
}

// verifyBasic checks HTTP Basic credentials.
func verifyBasic(a whsource.Auth, secrets []string, h http.Header) error {
	r := http.Request{Header: h}
	user, pass, ok := r.BasicAuth()
	if !ok {
		return ErrBadCredentials
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(a.Username)) == 1
	if err := matchAny(pass, secrets, ErrBadCredentials, ErrBadCredentials); err != nil || !userOK {
		return ErrBadCredentials
	}
	return nil
}

// verifyHMAC checks the timestamp window first, then the signature over the
// bytes the source's scheme signs. The scheme is hmacsig's, the one the
// platform's outbound hmac connections sign with.
func verifyHMAC(a whsource.Auth, secrets []string, req Request, now time.Time) error {
	sent := strings.TrimSpace(req.Header.Get(a.SignatureHeader))
	if sent == "" {
		return ErrMissingSignature
	}
	scheme := a.Scheme()
	sig, err := scheme.Parse(sent)
	if err != nil {
		return ErrBadSignature
	}
	ts := sig.Timestamp
	if a.TimestampHeader != "" {
		ts = strings.TrimSpace(req.Header.Get(a.TimestampHeader))
	}
	if a.TimestampHeader != "" || a.HeaderFormat == whsource.HeaderFormatStripe {
		if err := checkTimestamp(ts, a.Tolerance(), now); err != nil {
			return err
		}
	}
	var id string
	if scheme.UsesID() {
		id = strings.TrimSpace(req.Header.Get(a.IDHeader))
		if id == "" {
			return ErrMissingID
		}
	}
	if !scheme.Matches(sig, secrets, id, ts, req.Body) {
		return ErrBadSignature
	}
	return nil
}

// millisecondsFrom is the value above which a Unix timestamp is read as
// milliseconds: in seconds it is a date past the year 33000.
const millisecondsFrom = 1_000_000_000_000

// checkTimestamp refuses a timestamp further than tolerance from now. A value
// above 1e12 is read as milliseconds, which is what senders that sign in
// milliseconds send.
func checkTimestamp(ts string, tolerance time.Duration, now time.Time) error {
	if ts == "" {
		return ErrMissingTimestamp
	}
	n, err := strconv.Atoi(ts)
	if err != nil || n <= 0 {
		return ErrBadTimestamp
	}
	var sent time.Time
	if n > millisecondsFrom {
		sent = time.UnixMilli(int64(n))
	} else {
		sent = time.Unix(int64(n), 0)
	}
	if d := now.Sub(sent); d > tolerance || d < -tolerance {
		return ErrStaleTimestamp
	}
	return nil
}
