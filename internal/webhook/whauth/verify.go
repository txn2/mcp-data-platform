// Package whauth decides whether a request to a webhook source came from its
// sender (#1870). It holds no state and writes nothing: the receiver hands it
// the source, the request's headers, the path token if there was one, and the
// body, and it answers with nil or with the reason the request is refused.
//
// Every comparison of a secret, or of something computed from one, is
// constant-time.
package whauth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/txn2/mcp-data-platform/internal/hmacsig"
	"github.com/txn2/mcp-data-platform/internal/webhook/jsonpath"
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
	ErrExpiredToken     = errors.New("the token has expired")
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

// Verifier verifies requests for one source. Its signed-bytes template,
// header pattern and body paths are compiled once, when the source is loaded,
// so nothing about them is parsed per request (#2223).
type Verifier struct {
	auth whsource.Auth
	// checksTimestamp is the source's ChecksTimestamp, read once.
	checksTimestamp bool
	scheme          hmacsig.Compiled
	tokenPath       jsonpath.Path
	tsPath          jsonpath.Path
	hasTSPath       bool
}

// Compile builds the verifier for a source with its defaults applied. It
// refuses a source whose settings do not compile, which Validate refuses too.
// A mode it does not know compiles to a verifier that refuses every request:
// a form's source has none, and is never posted to.
func Compile(src whsource.Source) (*Verifier, error) {
	a := src.Auth
	v := &Verifier{auth: a, checksTimestamp: a.ChecksTimestamp()}
	var err error
	switch a.Mode {
	case whsource.AuthHMAC, whsource.AuthEd25519:
		if v.scheme, err = a.Scheme().Compile(); err != nil {
			return nil, fmt.Errorf("compiling the signature scheme: %w", err)
		}
	case whsource.AuthBodyToken:
		if v.tokenPath, err = jsonpath.Parse(a.TokenPath); err != nil {
			return nil, fmt.Errorf("compiling the token path: %w", err)
		}
	}
	if a.TimestampPath != "" {
		if v.tsPath, err = jsonpath.Parse(a.TimestampPath); err != nil {
			return nil, fmt.Errorf("compiling the timestamp path: %w", err)
		}
		v.hasTSPath = true
	}
	return v, nil
}

// Verify reports whether req is authentic at now.
func (v *Verifier) Verify(req Request, now time.Time) error {
	a := v.auth
	secrets := a.Secrets(now)
	switch a.Mode {
	case whsource.AuthHMAC:
		return v.verifyHMAC(secrets, req, now)
	case whsource.AuthEd25519:
		return v.verifyEd25519(secrets, req, now)
	case whsource.AuthHeaderToken:
		return matchAny(req.Header.Get(a.Header), secrets, ErrMissingToken, ErrBadToken)
	case whsource.AuthPathToken:
		return matchAny(req.PathToken, secrets, ErrMissingToken, ErrBadToken)
	case whsource.AuthBasic:
		return verifyBasic(a, secrets, req.Header)
	case whsource.AuthBodyToken:
		return v.verifyBodyToken(secrets, req)
	case whsource.AuthJWT:
		return verifyJWT(a, secrets, req.Header, now)
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

// verifyHMAC checks the timestamp window first, so a stale request costs no
// hashing, then the signature over the bytes the source's scheme signs. The
// scheme is hmacsig's, the one the platform's outbound hmac connections sign
// with.
func (v *Verifier) verifyHMAC(secrets []string, req Request, now time.Time) error {
	sent := strings.TrimSpace(req.Header.Get(v.auth.SignatureHeader))
	if sent == "" {
		return ErrMissingSignature
	}
	sig, err := v.scheme.Parse(sent)
	if err != nil {
		return ErrBadSignature
	}
	ts, id, err := v.signedValues(req, sig.Timestamp, now)
	if err != nil {
		return err
	}
	if !v.scheme.Matches(sig, secrets, id, ts, req.Body) {
		return ErrBadSignature
	}
	return nil
}

// verifyEd25519 checks the timestamp window, then the Ed25519 signature over
// the signed bytes with each public key the source holds.
func (v *Verifier) verifyEd25519(keys []string, req Request, now time.Time) error {
	sent := strings.TrimSpace(req.Header.Get(v.auth.SignatureHeader))
	if sent == "" {
		return ErrMissingSignature
	}
	sig, err := decodeSignature(v.auth.Encoding, sent)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return ErrBadSignature
	}
	ts, id, err := v.signedValues(req, "", now)
	if err != nil {
		return err
	}
	msg := v.scheme.Template().Bytes(id, ts, req.Body)
	for _, k := range keys {
		pub, err := hex.DecodeString(k)
		if err == nil && len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, msg, sig) {
			return nil
		}
	}
	return ErrBadSignature
}

// signedValues reads the timestamp and the delivery id the signed bytes name,
// checking the timestamp against the tolerance window. inHeader is the
// timestamp the signature header carried, if its layout carries one.
func (v *Verifier) signedValues(req Request, inHeader string, now time.Time) (ts, id string, err error) {
	a := v.auth
	switch {
	case a.TimestampHeader != "":
		ts = strings.TrimSpace(req.Header.Get(a.TimestampHeader))
	case v.hasTSPath:
		ts = v.bodyTimestamp(req)
	default:
		ts = inHeader
	}
	if v.checksTimestamp {
		if err := checkTimestamp(ts, a.Tolerance(), now); err != nil {
			return "", "", err
		}
	}
	if v.scheme.UsesID() {
		id = strings.TrimSpace(req.Header.Get(a.IDHeader))
		if id == "" {
			return "", "", ErrMissingID
		}
	}
	return ts, id, nil
}

// bodyTimestamp is the timestamp read from the body, empty when the body has
// none there.
func (v *Verifier) bodyTimestamp(req Request) string {
	doc, ok := bodyDocument(req)
	if !ok {
		return ""
	}
	got, ok := v.tsPath.Lookup(doc)
	if !ok {
		return ""
	}
	return jsonpath.Scalar(got)
}

// verifyBodyToken compares the token read from the body with the secrets. A
// path through "[*]" requires every element to carry a matching token: one
// element without it refuses the whole request.
func (v *Verifier) verifyBodyToken(secrets []string, req Request) error {
	doc, ok := bodyDocument(req)
	if !ok {
		return ErrMissingToken
	}
	values, ok := v.tokenPath.LookupAll(doc)
	if !ok {
		return ErrMissingToken
	}
	for _, got := range values {
		s, isString := got.(string)
		if !isString {
			return ErrMissingToken
		}
		if err := matchAny(s, secrets, ErrMissingToken, ErrBadToken); err != nil {
			return err
		}
	}
	return nil
}

// Redact removes the source's secret from doc, the decoded document a
// request's events are built from, so no source stores it (#2353, #2354):
// every object member, at any depth, whose value is one of the secrets valid
// at now is deleted, and every such array element is set to null so the
// elements after it keep their indexes. That is the token a body_token sender
// puts at token_path, in each element of a "[*]" path, and a token any other
// sender repeats in its body. A value that is not the secret is the sender's
// own and is kept. An ed25519 source's secret is the sender's public key,
// which is no secret, and leaves doc as it is.
func (v *Verifier) Redact(doc any, now time.Time) {
	if v.auth.Mode == whsource.AuthEd25519 {
		return
	}
	secrets := v.auth.Secrets(now)
	removeSecrets(doc, func(s string) bool { return matchAny(s, secrets, ErrMissingToken, ErrBadToken) == nil })
}

// removeSecrets deletes from doc every string isSecret accepts.
func removeSecrets(doc any, isSecret func(string) bool) {
	switch t := doc.(type) {
	case map[string]any:
		for k, inner := range t {
			if s, ok := inner.(string); ok && isSecret(s) {
				delete(t, k)
				continue
			}
			removeSecrets(inner, isSecret)
		}
	case []any:
		for i, inner := range t {
			if s, ok := inner.(string); ok && isSecret(s) {
				t[i] = nil
				continue
			}
			removeSecrets(inner, isSecret)
		}
	}
}

// formMediaType is the content type a form body is posted with.
const formMediaType = "application/x-www-form-urlencoded"

// bodyDocument decodes the body a body-read setting looks in: a form's fields
// as an object of their first values, or the JSON document.
func bodyDocument(req Request) (any, bool) {
	mediaType, _, _ := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if mediaType == formMediaType {
		form, err := url.ParseQuery(string(req.Body))
		if err != nil {
			return nil, false
		}
		doc := make(map[string]any, len(form))
		for k := range form {
			doc[k] = form.Get(k)
		}
		return doc, true
	}
	dec := json.NewDecoder(bytes.NewReader(req.Body))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, false
	}
	return doc, true
}

// decodeSignature reads a signature written in hex or base64.
func decodeSignature(encoding, v string) ([]byte, error) {
	if encoding == whsource.EncodingBase64 {
		if b, err := base64.StdEncoding.DecodeString(v); err == nil {
			return b, nil
		}
		return base64.URLEncoding.DecodeString(v) //nolint:wrapcheck // the caller answers ErrBadSignature
	}
	return hex.DecodeString(strings.ToLower(v)) //nolint:wrapcheck // as above
}

// bearerPrefix is the scheme a token in an Authorization header may carry.
const bearerPrefix = "bearer "

// verifyJWT checks an HS256 JSON Web Token signed with one of the secrets. An
// exp the token carries is honored; a token with none does not expire.
func verifyJWT(a whsource.Auth, secrets []string, h http.Header, now time.Time) error {
	tok := strings.TrimSpace(h.Get(a.Header))
	if len(tok) >= len(bearerPrefix) && strings.EqualFold(tok[:len(bearerPrefix)], bearerPrefix) {
		tok = strings.TrimSpace(tok[len(bearerPrefix):])
	}
	if tok == "" {
		return ErrMissingToken
	}
	parser := jwt.NewParser(jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithTimeFunc(func() time.Time { return now }))
	expired := false
	for _, secret := range secrets {
		key := []byte(secret)
		_, err := parser.Parse(tok, func(*jwt.Token) (any, error) { return key, nil })
		if err == nil {
			return nil
		}
		if errors.Is(err, jwt.ErrTokenExpired) {
			expired = true
		}
	}
	if expired {
		return ErrExpiredToken
	}
	return ErrBadToken
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
