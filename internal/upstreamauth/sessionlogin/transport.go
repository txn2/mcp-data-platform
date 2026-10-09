package sessionlogin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpguts"

	"github.com/txn2/mcp-data-platform/internal/secretref"
	"github.com/txn2/mcp-data-platform/internal/useragent"
)

// ErrSessionLogin is the error a call returns when the platform could not
// sign in to the upstream: the sign-in was refused, unreachable, or answered
// without a token where the connection says one is.
var ErrSessionLogin = errors.New("session sign-in failed")

// ErrSessionRejected is the error a call returns when the upstream rejected
// a session the platform had just signed in for. Signing in again would be
// answered the same way, so the connection's credential is what needs
// attention, as with an OAuth connection that needs reconnecting.
var ErrSessionRejected = errors.New("the upstream rejected a fresh session; check the connection's sign-in credential")

// ErrUnknownSessionValue is the error a call returns when its path names a
// {session.<name>} the connection's sign-in does not capture.
var ErrUnknownSessionValue = errors.New("the path names a session value this connection's sign-in does not capture")

// IsSessionMessage reports whether an error's text, as a tool reports it to a
// caller, is one of the session's own: a sign-in that failed, a fresh session
// refused, or a session value not captured. Each is the connection's or the
// caller's to fix, so a tool classifying a failed call by its text reports it
// as the upstream's refusal rather than as an upstream that could not be
// reached, which a caller would retry.
func IsSessionMessage(msg string) bool {
	for _, err := range []error{ErrSessionLogin, ErrSessionRejected, ErrUnknownSessionValue} {
		if strings.Contains(msg, err.Error()) {
			return true
		}
	}
	return false
}

// IsSessionFailure reports whether err is a session_login connection failing
// to sign in or having a fresh session rejected, which a connection test
// reports as the sign-in failing rather than the upstream being unreachable.
func IsSessionFailure(err error) bool {
	return errors.Is(err, ErrSessionLogin) || errors.Is(err, ErrSessionRejected)
}

const (
	// sessionLoginTimeout bounds one sign-in, as an OAuth token fetch is
	// bounded.
	sessionLoginTimeout = 30 * time.Second
	// sessionLogoutTimeout bounds one sign-out. A sign-out is a courtesy to
	// the upstream and is not waited on past this.
	sessionLogoutTimeout = 5 * time.Second
	// maxSessionResponse bounds how much of a sign-in response is read.
	maxSessionResponse = 1 << 20
	// maxMarkerPeek bounds how much of a call's response is read to look
	// for session_expired_marker before the body is handed on.
	maxMarkerPeek = 64 << 10
	// maxExcerpt bounds the upstream text an error quotes.
	maxExcerpt = 240
	// redacted replaces a credential in quoted upstream text.
	redacted = "[REDACTED]"
)

// sessionTransport carries a session_login connection's session on every
// request: it signs in on first use, puts the token and any captured values
// on the request, and signs in once more and replays when the upstream says
// the session is no longer accepted.
//
// It sits inside the connection's *http.Client, below the User-Agent and the
// metrics wrappers, so every caller of the client -- the api gateway's
// calls and page walks, the graphql kind's queries and introspection, a
// notification channel's delivery -- holds one session and none of them has
// a sign-in path of its own.
type sessionTransport struct {
	cfg    Config
	prefix string
	// next carries a call; login carries the sign-in and sign-out, which go
	// to their own URLs and so take nothing next adds to a call's path.
	next  http.RoundTripper
	login http.RoundTripper
	now   func() time.Time

	// signing is the one sign-in slot. Holding it across a sign-in is what
	// makes one sign-in serve every call waiting for it: an upstream that
	// allows one live session per credential would otherwise have each
	// concurrent sign-in end the session the one before it opened. A caller
	// waits for it under its own context, so its call timeout still holds.
	signing chan struct{}
	// signOuts tracks the sign-outs in flight, for a test to wait on.
	signOuts sync.WaitGroup

	// mu guards the session state. It is never held across a network call.
	mu       sync.Mutex
	token    string
	values   map[string]string
	gen      uint64
	signedIn time.Time
	// failErr is the last sign-in's failure, answered without signing in
	// again until failureHold has passed since failedAt: a wrong secret
	// would otherwise be a failed sign-in at the upstream on every call,
	// which locks some vendors' accounts.
	failErr  error
	failedAt time.Time
	// rejectedUntil is when a fresh session's rejection stops being taken
	// as the credential's: until then an expired answer is returned as it
	// is rather than signed in again for.
	rejectedUntil time.Time
}

// failureHold is how long a failed sign-in, or a fresh session the upstream
// rejected, is answered without signing in again.
const failureHold = 30 * time.Second

// NewTransport wraps next with cfg's session. login carries the sign-in and
// sign-out requests. prefix names the calling kind in every error the session
// produces.
func NewTransport(cfg Config, prefix string, next, login http.RoundTripper) http.RoundTripper {
	return newSessionTransport(cfg, prefix, next, login)
}

func newSessionTransport(cfg Config, prefix string, next, login http.RoundTripper) *sessionTransport {
	return &sessionTransport{
		cfg: cfg, prefix: prefix, next: next, login: login, now: time.Now,
		signing: make(chan struct{}, 1),
	}
}

// errf builds an error in the calling kind's voice.
func (t *sessionTransport) errf(format string, a ...any) error {
	return fmt.Errorf(t.prefix+": "+format, a...)
}

// sessionState is the session a request was sent with.
type sessionState struct {
	token  string
	values map[string]string
	gen    uint64
}

// RoundTrip sends req with the session on it, signing in first when there is
// none, and signs in once more and replays when the answer says the session
// is no longer accepted.
func (t *sessionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	state, err := t.current(req.Context())
	if err != nil {
		closeBody(req)
		return nil, err
	}
	resp, err := t.send(req, state)
	if err != nil {
		return nil, err
	}
	expired, resp := t.expired(resp)
	if !expired || t.rejectedRecently() {
		return resp, nil
	}
	t.invalidate(state.gen)
	// A body that cannot be read twice cannot be replayed. The rejection
	// goes back as the upstream sent it, and the next call signs in.
	if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
		return resp, nil
	}
	discard(resp)
	return t.replay(req)
}

// replay signs in again and sends req a second time. A second rejection is
// the credential's, not the session's, and is reported as such.
func (t *sessionTransport) replay(req *http.Request) (*http.Response, error) {
	state, err := t.current(req.Context())
	if err != nil {
		return nil, err
	}
	again := req
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, t.errf("replaying the request after signing in again: %w", err)
		}
		again = req.Clone(req.Context())
		again.Body = body
	}
	resp, err := t.send(again, state)
	if err != nil {
		return nil, err
	}
	expired, resp := t.expired(resp)
	if !expired {
		return resp, nil
	}
	// The session was signed in for this call and is kept: it is the
	// credential, or this endpoint, the upstream refuses, and signing in
	// again on every call would not change that.
	t.mu.Lock()
	t.rejectedUntil = t.now().Add(failureHold)
	t.mu.Unlock()
	excerpt := t.excerpt(resp.Body, state.token, nil)
	_ = resp.Body.Close()
	return nil, t.errf("%w (HTTP %d%s)", ErrSessionRejected, resp.StatusCode, excerpt)
}

// send transmits a copy of req carrying the session.
func (t *sessionTransport) send(req *http.Request, state sessionState) (*http.Response, error) {
	out := req.Clone(req.Context())
	u, err := substituteSessionValues(*req.URL, state.values)
	if err != nil {
		closeBody(req)
		return nil, t.errf("%w", err)
	}
	out.URL = &u
	out.Header.Set(t.cfg.TokenHeader, t.cfg.TokenPrefix+state.token)
	return t.next.RoundTrip(out) //nolint:wrapcheck // the transport's own error, as any other round trip returns it
}

// current returns the session to send with, signing in when there is none or
// the configured lifetime has passed. Callers that arrive during a sign-in
// wait for it and use its session; a sign-in that failed within failureHold
// is answered with its failure.
func (t *sessionTransport) current(ctx context.Context) (sessionState, error) {
	if st, ok, err := t.held(); ok || err != nil {
		return st, err
	}
	select {
	case t.signing <- struct{}{}:
	case <-ctx.Done():
		return sessionState{}, t.errf("waiting for the session sign-in: %w", ctx.Err())
	}
	defer func() { <-t.signing }()
	// Another caller may have signed in while this one waited.
	if st, ok, err := t.held(); ok || err != nil {
		return st, err
	}
	token, values, err := t.signIn(ctx)
	t.mu.Lock()
	if err != nil {
		// A caller that gave up did not learn anything about the
		// credential, so its failure is not held against the next one.
		if ctx.Err() == nil {
			t.failErr, t.failedAt = err, t.now()
		}
		t.mu.Unlock()
		return sessionState{}, err
	}
	replaced := t.token
	t.token, t.values, t.signedIn, t.failErr = token, values, t.now(), nil
	t.gen++
	st := sessionState{token: token, values: values, gen: t.gen}
	t.mu.Unlock()
	// A session the lifetime retired is still live at the upstream.
	t.endSession(replaced)
	return st, nil
}

// held returns the session in hand, when there is one inside its lifetime,
// or the failure of a sign-in made within failureHold.
func (t *sessionTransport) held() (sessionState, bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ttl := t.cfg.TTL
	if t.token != "" && (ttl <= 0 || t.now().Sub(t.signedIn) < ttl) {
		return sessionState{token: t.token, values: t.values, gen: t.gen}, true, nil
	}
	if t.failErr != nil && t.now().Sub(t.failedAt) < failureHold {
		return sessionState{}, false, t.failErr
	}
	return sessionState{}, false, nil
}

// rejectedRecently reports whether a fresh session was rejected within
// failureHold.
func (t *sessionTransport) rejectedRecently() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.now().Before(t.rejectedUntil)
}

// invalidate drops the session a request was sent with. A session another
// caller has already replaced is left alone, so a burst of rejections of one
// session produces one sign-in.
func (t *sessionTransport) invalidate(gen uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.gen == gen {
		t.token, t.values = "", nil
	}
}

// signIn sends the sign-in request and reads the token and the captured
// values from its answer.
func (t *sessionTransport) signIn(ctx context.Context) (token string, values map[string]string, err error) {
	s := t.cfg
	ctx, cancel := context.WithTimeout(ctx, sessionLoginTimeout)
	defer cancel()
	// The stored secrets the sign-in names are read now, so a rotated one
	// is sent from the next sign-in on (#2066), and recorded so an error
	// quoting the upstream's answer cannot quote them.
	sent := &secretref.Redactor{}
	lookup := sent.Recording(secretref.ConnectionLookup(ctx, s.Connection))
	var body io.Reader = http.NoBody
	if s.LoginBody != "" {
		rendered, err := s.signInBody(lookup)
		if err != nil {
			return "", nil, t.errf("%w: %w", ErrSessionLogin, err)
		}
		body = strings.NewReader(rendered)
	}
	req, err := http.NewRequestWithContext(ctx, s.LoginMethod, s.LoginURL, body)
	if err != nil {
		return "", nil, t.errf("%w: building the sign-in request: %v", ErrSessionLogin, err)
	}
	if s.LoginBody != "" {
		req.Header.Set("Content-Type", s.LoginContentType)
	}
	req.Header.Set("Accept", "application/json")
	headers, err := secretref.FillStrings(s.LoginHeaders, lookup)
	if err != nil {
		return "", nil, t.errf("%w: %w", ErrSessionLogin, err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	// #nosec G107 G704 -- the sign-in URL is the operator's configured
	// session_login_url, validated as an http(s) URL at save.
	resp, err := useragent.Transport(t.login).RoundTrip(req)
	if err != nil {
		return "", nil, t.errf("%w: %s: %v", ErrSessionLogin, scrubbedURL(s.LoginURL), unwrapURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", nil, t.errf("%w: %s answered HTTP %d%s",
			ErrSessionLogin, scrubbedURL(s.LoginURL), resp.StatusCode, t.excerpt(resp.Body, "", sent))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSessionResponse))
	if err != nil {
		return "", nil, t.errf("%w: reading the sign-in response: %v", ErrSessionLogin, err)
	}
	return t.readSession(resp.Header, raw)
}

// readSession reads the token and the captured values out of a sign-in
// response.
func (t *sessionTransport) readSession(header http.Header, raw []byte) (token string, values map[string]string, err error) {
	answer := sessionAnswer{header: header, raw: raw}
	token, err = answer.read(t.cfg.TokenSource)
	if err != nil {
		return "", nil, t.errf("%w: the token was not at %s: %v", ErrSessionLogin, t.cfg.TokenSource, err)
	}
	if strings.ContainsAny(token, "\r\n\x00") {
		return "", nil, t.errf("%w: the token at %s contains CR/LF/NUL", ErrSessionLogin, t.cfg.TokenSource)
	}
	values = make(map[string]string, len(t.cfg.Capture))
	for name, source := range t.cfg.Capture {
		v, err := answer.read(source)
		if err != nil {
			return "", nil, t.errf("%w: the value %q was not at %s: %v", ErrSessionLogin, name, source, err)
		}
		values[name] = v
	}
	return token, values, nil
}

// expired reports whether resp says the session is no longer accepted, and
// returns the response to hand on: when the marker had to be looked for, the
// body read to find it is put back in front of the rest.
func (t *sessionTransport) expired(resp *http.Response) (bool, *http.Response) {
	if slices.Contains(t.cfg.ExpiredStatuses, resp.StatusCode) {
		return true, resp
	}
	marker := t.cfg.ExpiredMarker
	if marker == "" || resp.Body == nil {
		return false, resp
	}
	peek, err := io.ReadAll(io.LimitReader(resp.Body, maxMarkerPeek))
	resp.Body = readCloser{Reader: io.MultiReader(bytes.NewReader(peek), resp.Body), Closer: resp.Body}
	if err != nil {
		return false, resp
	}
	return bytes.Contains(peek, []byte(marker)), resp
}

// excerpt is a short, credential-free quote of an upstream's error body, as
// the tail of an error message. sent holds the stored secrets the request
// carried, which are redacted before the quote is cut.
func (t *sessionTransport) excerpt(body io.Reader, token string, sent *secretref.Redactor) string {
	raw, _ := io.ReadAll(io.LimitReader(body, maxExcerpt*4))
	text := strings.Join(strings.Fields(sent.String(string(raw))), " ")
	for _, secret := range []string{t.cfg.Secret, token, escapeFor(t.cfg.LoginContentType, t.cfg.Secret)} {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, redacted)
		}
	}
	text = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, text)
	if text == "" {
		return ""
	}
	if len(text) > maxExcerpt {
		text = text[:maxExcerpt] + "..."
	}
	return ": " + text
}

// CloseIdleConnections ends the session at the upstream, when the connection
// names a sign-out, and releases the idle connections below. A connection's
// client is told to close its idle connections when the connection is
// removed, replaced or shut down, which is when its session is no longer
// wanted. It does not wait for a sign-in in progress or for the sign-out,
// which is sent in the background within sessionLogoutTimeout: a toolkit
// closes its connections under its own lock. A call still in flight keeps
// working; one made afterwards signs in again.
func (t *sessionTransport) CloseIdleConnections() {
	t.mu.Lock()
	token := t.token
	t.token, t.values, t.failErr, t.rejectedUntil = "", nil, nil, time.Time{}
	t.gen++
	t.mu.Unlock()
	t.endSession(token)
	if closer, ok := t.next.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// endSession signs token out in the background, when there is one and the
// connection names a sign-out.
func (t *sessionTransport) endSession(token string) {
	if token == "" || t.cfg.LogoutURL == "" {
		return
	}
	t.signOuts.Go(func() { t.signOut(token) })
}

// signOut ends one session at the upstream. Its outcome is not reported: the
// session is abandoned either way, and an upstream that did not hear the
// sign-out ends it at its own expiry.
func (t *sessionTransport) signOut(token string) {
	ctx, cancel := context.WithTimeout(context.Background(), sessionLogoutTimeout)
	defer cancel()
	s := t.cfg
	req, err := http.NewRequestWithContext(ctx, s.LogoutMethod, s.LogoutURL, http.NoBody)
	if err != nil {
		return
	}
	req.Header.Set(s.TokenHeader, s.TokenPrefix+token)
	// #nosec G107 G704 -- the sign-out URL is the operator's configured
	// session_logout_url, validated as an http(s) URL at save.
	resp, err := useragent.Transport(t.login).RoundTrip(req)
	if err != nil {
		return
	}
	discard(resp)
}

// sessionAnswer is a sign-in response, read for values.
type sessionAnswer struct {
	header http.Header
	raw    []byte
	doc    any
	parsed bool
	err    error
}

// read returns the value at source.
func (a *sessionAnswer) read(source string) (string, error) {
	where, what, err := parseSessionSource(source)
	if err != nil {
		return "", err
	}
	if where == sessionSourceHeader {
		if v := a.header.Get(what); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("the response has no %s header", what)
	}
	if !a.parsed {
		a.parsed = true
		dec := json.NewDecoder(bytes.NewReader(a.raw))
		dec.UseNumber()
		a.err = dec.Decode(&a.doc)
	}
	if a.err != nil {
		return "", errors.New("the response body is not JSON; a sign-in that answers XML by default usually answers JSON " +
			"when session_login_headers sets Accept: application/json")
	}
	return jsonPathString(a.doc, what)
}

// jsonPathString walks a dotted path through a decoded JSON document, reading
// a numeric segment as an array index, and returns the scalar it ends at.
func jsonPathString(doc any, path string) (string, error) {
	cur := doc
	for seg := range strings.SplitSeq(path, ".") {
		next, err := jsonStep(cur, seg)
		if err != nil {
			return "", err
		}
		cur = next
	}
	return jsonScalar(cur)
}

// jsonStep is one segment of a path: an object's member or a list's item.
func jsonStep(cur any, seg string) (any, error) {
	switch node := cur.(type) {
	case map[string]any:
		next, ok := node[seg]
		if !ok {
			return nil, fmt.Errorf("the response has no %q", seg)
		}
		return next, nil
	case []any:
		i, err := strconv.Atoi(seg)
		if err != nil || i < 0 || i >= len(node) {
			return nil, fmt.Errorf("segment %q is not an index of a %d-item list", seg, len(node))
		}
		return node[i], nil
	default:
		return nil, fmt.Errorf("segment %q is below a value that is not an object or a list", seg)
	}
}

// jsonScalar is the value a path ended at, as text.
func jsonScalar(v any) (string, error) {
	switch v := v.(type) {
	case string:
		if v == "" {
			return "", errors.New("the value is empty")
		}
		return v, nil
	case json.Number:
		return v.String(), nil
	case bool:
		return strconv.FormatBool(v), nil
	default:
		return "", errors.New("the value is not a string or a number")
	}
}

// substituteSessionValues writes each {session.<name>} a URL's path names
// with the value the sign-in captured under that name. A name the session
// does not hold is refused, naming the ones it does.
func substituteSessionValues(u url.URL, values map[string]string) (url.URL, error) {
	if !strings.Contains(u.Path, "session.") && !strings.Contains(u.RawPath, "session.") {
		return u, nil
	}
	var missing string
	replace := func(escape bool) func(string) string {
		return func(m string) string {
			name := strings.ToLower(sessionValuePattern.FindStringSubmatch(m)[1])
			v, ok := values[name]
			if !ok {
				missing = name
				return m
			}
			if escape {
				return url.PathEscape(v)
			}
			return v
		}
	}
	u.Path = sessionValuePattern.ReplaceAllStringFunc(u.Path, replace(false))
	if u.RawPath != "" {
		u.RawPath = sessionValuePattern.ReplaceAllStringFunc(u.RawPath, replace(true))
	}
	if missing != "" {
		return u, fmt.Errorf("session value {session.%s}: %w%s", missing, ErrUnknownSessionValue, heldNames(values))
	}
	return u, nil
}

// heldNames lists the values a session holds, as the tail of an error.
func heldNames(values map[string]string) string {
	if len(values) == 0 {
		return " (it captures none)"
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, "{session."+name+"}")
	}
	sort.Strings(names)
	return " (it captures " + strings.Join(names, ", ") + ")"
}

// scrubbedURL is a URL without its query or user info, for an error message.
func scrubbedURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "the sign-in URL"
	}
	u.RawQuery, u.User = "", nil
	return u.String()
}

// unwrapURLError drops the *url.Error wrapper a transport may add, whose
// message repeats the URL with its query.
func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// closeBody closes a request's body, which a RoundTripper owns even when it
// does not send the request.
func closeBody(req *http.Request) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
}

// discard reads what is left of a response and closes it, so its connection
// can be reused.
func discard(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxMarkerPeek))
	_ = resp.Body.Close()
}

// readCloser is a body put back together from what was read and the rest.
type readCloser struct {
	io.Reader
	io.Closer
}

// authorizationHeader is the header the token goes in when the connection
// names none.
const authorizationHeader = "Authorization"

// validHeaderName reports whether name is an HTTP header field name.
func validHeaderName(name string) bool {
	return httpguts.ValidHeaderFieldName(name)
}

// reservedHopHeader names the headers net/http manages on a request itself or
// that mean nothing per request, which a session cannot be carried in.
func reservedHopHeader(name string) bool {
	switch strings.ToLower(name) {
	case "host", "content-length", "connection", "transfer-encoding",
		"upgrade", "keep-alive", "proxy-authenticate",
		"proxy-authorization", "te", "trailer":
		return true
	}
	return false
}
