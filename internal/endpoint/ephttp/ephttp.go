// Package ephttp is the HTTP surface of endpoints (#2174): every request to
// /endpoints/{name}/... and the bot check a bot_check one is answered behind
// (/botcheck/challenge and /botcheck/solver.js).
//
// An authenticated endpoint answers a platform API key or a browser session
// its callers list names, and the identity the platform found is what the
// handler reads as req.caller; it never comes from the request body. A public
// endpoint answers anyone. A bot_check endpoint answers a request carrying an
// answer to the bot check it names in the X-Bot-Check header, or one with
// none to a method it does not check (#2259).
//
// These routes answer their own cross-origin requests from each binding's
// allowed origins; the platform's CORS layer leaves them alone. No limit here
// depends on the client's address, which a request behind an ingress and a
// load balancer does not carry.
package ephttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/botcheck"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprefusal"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/httpobs"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/ratelimit"
)

// The routes.
const (
	// PathPrefix is every endpoint's prefix; a request's path is
	// /endpoints/{name} and what follows it.
	PathPrefix = "/endpoints/"
	// BotCheckPrefix is the bot check's routes.
	BotCheckPrefix = "/botcheck/"

	challengePath = BotCheckPrefix + "challenge"
	solverPath    = BotCheckPrefix + "solver.js"
)

// challengeKey is the bucket an endpoint's challenges are counted in.
const challengeKey = "challenge"

// HeaderBotCheck carries a public request's bot check answer.
const HeaderBotCheck = "X-Bot-Check"

// allowedRequestHeaders are the headers a cross-origin request may send.
const allowedRequestHeaders = "Content-Type, Authorization, X-API-Key, " + HeaderBotCheck + ", " + epform.ViewerHeader

// originOpaque is the Origin a sandboxed frame's requests carry. A form
// embedded in an asset is framed with the asset's sandbox, so its page's
// requests to the form carry it (#2177); a form answers it, never with
// credentials.
const originOpaque = "null"

const (
	headerCacheControl = "Cache-Control"
	logKeyError        = "error"
)

// contentPolicy is what every endpoint answer is held to: whatever the
// handler returns renders as an opaque document that runs no script against
// the platform's origin.
const contentPolicy = "default-src 'none'; sandbox"

// Runner is what serves a resolved endpoint.
type Runner interface {
	Resolve(ctx context.Context, name string) (*eprun.Target, error)
	Serve(ctx context.Context, t *eprun.Target, req eprun.Request) eprun.Response
}

// Checker issues and verifies the named bot checks.
type Checker interface {
	Issue(ctx context.Context, check, endpoint string) (botcheck.Challenge, error)
	Verify(ctx context.Context, check, endpoint, answer string) error
}

// ErrForbidden is what Authenticate returns for a credential that was
// presented and refused for a reason other than its absence (a browser
// session without its CSRF token), which is answered 403 rather than 401.
var ErrForbidden = errors.New("the request's credential was refused")

// Handler answers the routes.
type Handler struct {
	Runner  Runner
	Checker Checker
	// Authenticate is who sent the request: a browser session or an API key,
	// with the persona its roles resolve to and the key's name.
	// Nil caller and nil error is nobody.
	Authenticate func(r *http.Request) (*eprun.Caller, error)
	// Refused counts a request refused before its handler ran, by endpoint
	// and reason (#2208). Nil counts nothing.
	Refused func(endpointID, reason string)
	// ViewerKey opens the viewer token an authenticated form embedded in an
	// asset is sent with (epform.ViewerKey, #2177); nil honors none.
	ViewerKey []byte
	// SiteOrigins are the origins of the sites an application holds (#2518),
	// which its public and bot_check endpoints answer without listing them;
	// nil adds none.
	SiteOrigins func(ctx context.Context, applicationID string) []string

	endpointLimits limiters
	callerLimits   limiters
}

// Mount registers the routes on mux.
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.Handle(PathPrefix+"{name}", h)
	mux.Handle(PathPrefix+"{name}/{rest...}", h)
	mux.HandleFunc("GET "+challengePath, h.challenge)
	mux.HandleFunc("OPTIONS "+challengePath, h.challenge)
	mux.HandleFunc("GET "+solverPath, solver)
}

// PageRoute is a request to an application's endpoint on its page origin
// (#2517): the endpoint, the application it must belong to, and the identity
// that origin carries in place of the portal's.
type PageRoute struct {
	Application string
	Name        string
	Rest        string
	// Nonce is this response's inline-script nonce.
	Nonce string
	// Caller is who the page origin's viewer cookie names; nil and nil is
	// nobody. Neither a portal session nor an API key is read there.
	Caller func(r *http.Request) (*eprun.Caller, error)
	// SignIn sends a browser with no viewer to sign in, for a page.
	SignIn func(w http.ResponseWriter, r *http.Request)
}

type pageKey struct{}

func pageOf(r *http.Request) *PageRoute {
	p, _ := r.Context().Value(pageKey{}).(*PageRoute)
	return p
}

// ServePage answers one request on an application's page origin. An endpoint
// of another application answers as if it did not exist.
func (h *Handler) ServePage(w http.ResponseWriter, r *http.Request, route PageRoute) {
	h.serve(w, r.WithContext(context.WithValue(r.Context(), pageKey{}, &route)), route.Name, route.Rest)
}

// ServeHTTP answers one request to an endpoint.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := ""
	if v := r.PathValue("rest"); v != "" {
		rest = "/" + v
	}
	h.serve(w, r, r.PathValue("name"), rest)
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request, name, rest string) {
	t, ok := h.resolve(w, r, name)
	if !ok {
		return
	}
	page := pageOf(r)
	if page != nil && t.ApplicationID() != page.Application {
		httpjson.WriteError(w, http.StatusNotFound, "no endpoint answers at this path")
		return
	}
	b := t.Binding
	params, ok := h.routed(w, r, t, rest)
	if !ok {
		return
	}
	caller, ok := h.admit(w, r, t)
	if !ok {
		return
	}
	body, status := readBody(w, r, b.MaxBodyBytes)
	if status == http.StatusRequestEntityTooLarge {
		h.refused(b, eprefusal.ReasonBodyLimit)
	}
	if status != 0 {
		return
	}
	req := eprun.Request{
		Method: r.Method, Path: r.URL.Path, Params: params, Query: r.URL.Query(),
		Header: r.Header.Clone(), Body: body, Caller: caller,
	}
	policy := contentPolicy
	if page != nil && b.ResponseMode == epbinding.ResponsePage {
		req.CSPNonce, policy = page.Nonce, b.PagePolicy(page.Nonce)
	}
	write(w, r, h.Runner.Serve(r.Context(), t, req), policy)
}

// routed runs what comes before the caller is looked at: CORS, the preflight
// answer, the method, the path and the endpoint's own rate limit. It writes
// the answer and reports false when the request stops here.
func (h *Handler) routed(w http.ResponseWriter, r *http.Request, t *eprun.Target, rest string) (map[string]string, bool) {
	b := t.Binding
	if !cors(w, r, b, t.Form, h.siteOrigin(r, t)) {
		h.refused(b, eprefusal.ReasonOrigin)
		return nil, false
	}
	if r.Method == http.MethodOptions {
		w.Header().Set("Allow", allow(b))
		w.WriteHeader(http.StatusNoContent)
		return nil, false
	}
	if !b.Answers(r.Method) {
		w.Header().Set("Allow", allow(b))
		httpjson.WriteError(w, http.StatusMethodNotAllowed, "this endpoint does not answer "+r.Method)
		return nil, false
	}
	params, ok := b.Match(rest)
	if !ok {
		httpjson.WriteError(w, http.StatusNotFound, "no endpoint answers at this path")
		return nil, false
	}
	if !h.endpointLimits.allow(b.Name, b.RateLimit, "") {
		h.refused(b, eprefusal.ReasonRateLimit)
		tooMany(w, r, b.RateLimit)
		return nil, false
	}
	return params, true
}

// resolve is the enabled endpoint name serves, writing the error response
// when there is none.
func (h *Handler) resolve(w http.ResponseWriter, r *http.Request, name string) (*eprun.Target, bool) {
	t, err := h.Runner.Resolve(r.Context(), name)
	switch {
	case errors.Is(err, epbinding.ErrNotFound) || (err == nil && !t.Binding.Enabled):
		httpjson.WriteError(w, http.StatusNotFound, "no endpoint answers at this path")
		return nil, false
	case err != nil:
		slog.ErrorContext(r.Context(), "endpoint: resolving the endpoint failed", logKeyError, logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusInternalServerError, "the endpoint could not be read")
		return nil, false
	}
	return t, true
}

// admit decides whether the request may run: the authenticated caller among
// the endpoint's callers and within their own limit, anyone on a public
// endpoint, or the bot check a bot_check endpoint names.
func (h *Handler) admit(w http.ResponseWriter, r *http.Request, t *eprun.Target) (*eprun.Caller, bool) {
	b := t.Binding
	switch b.AuthMode {
	case epbinding.AuthAuthenticated:
		return h.authenticated(w, r, t)
	case epbinding.AuthPublic:
		return h.optional(r, t), true
	case epbinding.AuthBotCheck:
		if !h.checked(w, r, b) {
			return nil, false
		}
		return h.optional(r, t), true
	}
	httpjson.WriteError(w, http.StatusServiceUnavailable, "the endpoint's access mode is not one this platform answers")
	return nil, false
}

// checked reports whether a bot_check endpoint's request passed its check,
// writing the refusal when it did not.
func (h *Handler) checked(w http.ResponseWriter, r *http.Request, b *epbinding.Binding) bool {
	if !b.Checks(r.Method) {
		return true
	}
	err := h.Checker.Verify(r.Context(), b.BotCheck, b.Name, r.Header.Get(HeaderBotCheck))
	switch {
	case botcheck.Refusal(err):
		h.refused(b, eprefusal.ReasonBotCheck)
		httpjson.WriteError(w, http.StatusForbidden, err.Error())
		return false
	case err != nil:
		slog.WarnContext(r.Context(), "endpoint: the bot check could not be made", logKeyError, logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusServiceUnavailable, "the bot check could not be made")
		return false
	}
	return true
}

func (h *Handler) authenticated(w http.ResponseWriter, r *http.Request, t *eprun.Target) (*eprun.Caller, bool) {
	b := t.Binding
	caller, err := h.caller(r, t)
	switch {
	case errors.Is(err, ErrForbidden):
		h.refused(b, eprefusal.ReasonCredential)
		httpjson.WriteError(w, http.StatusForbidden, "the request's credential was refused")
		return nil, false
	case err != nil || caller == nil:
		h.refused(b, eprefusal.ReasonCredential)
		if page := pageOf(r); page != nil && page.SignIn != nil && b.ResponseMode == epbinding.ResponsePage && r.Method == http.MethodGet {
			page.SignIn(w, r)
			return nil, false
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="endpoints"`)
		httpjson.WriteError(w, http.StatusUnauthorized, "this endpoint answers an authenticated caller: a platform API key or a browser session")
		return nil, false
	}
	if !b.Admits(epbinding.Caller{Admin: caller.Admin, Persona: caller.Persona, APIKey: caller.APIKey}) {
		h.refused(b, eprefusal.ReasonCaller)
		httpjson.WriteError(w, http.StatusForbidden, "this endpoint does not admit this caller")
		return nil, false
	}
	key := caller.UserID
	if key == "" {
		key = caller.Email
	}
	if !h.callerLimits.allow(b.Name, b.CallerRateLimit, key) {
		h.refused(b, eprefusal.ReasonCallerRateLimit)
		tooMany(w, r, b.CallerRateLimit)
		return nil, false
	}
	return caller, true
}

// optional is who sent a request to a public or bot_check form when they say
// (#2494): a signed-in person is served the version the script runs, a caller
// with no session the version approved for them. A credential that does not
// open is no caller, as on any public endpoint, and an endpoint that is not a
// form never asks.
func (h *Handler) optional(r *http.Request, t *eprun.Target) *eprun.Caller {
	if !t.ServesPublicly() || h.Authenticate == nil {
		return nil
	}
	caller, err := h.caller(r, t)
	if err != nil {
		return nil
	}
	return caller
}

// caller is who sent the request: the browser session or API key it carries,
// or on a form with neither, the viewer token it carries (#2177).
func (h *Handler) caller(r *http.Request, t *eprun.Target) (*eprun.Caller, error) {
	if page := pageOf(r); page != nil {
		return page.Caller(r)
	}
	caller, err := h.Authenticate(r)
	if err == nil && caller == nil && t.Form && r.Header.Get(epform.ViewerHeader) != "" {
		return h.viewer(r, t.Binding.Name)
	}
	return caller, err
}

// viewer is the caller a form's viewer token names (#2177): who a signed-in
// person was when the platform served them the asset the form is embedded
// in. A token that does not open is no caller.
func (h *Handler) viewer(r *http.Request, endpoint string) (*eprun.Caller, error) {
	v, err := epform.OpenViewer(h.ViewerKey, endpoint, r.Header.Get(epform.ViewerHeader), time.Now())
	if err != nil {
		return nil, err //nolint:wrapcheck // answered 401 like any absent credential
	}
	return &eprun.Caller{UserID: v.UserID, Email: v.Email, AuthType: v.AuthType, Admin: v.Admin, Persona: v.Persona}, nil
}

// refused counts a refusal of a request to b.
func (h *Handler) refused(b *epbinding.Binding, reason string) {
	if h.Refused != nil {
		h.Refused(b.ID, reason)
	}
}

// readBody reads the request body up to limit, answering 413 past it. The
// status is the error it answered, zero when it read the body.
func readBody(w http.ResponseWriter, r *http.Request, limit int) (body []byte, status int) {
	if r.ContentLength > int64(limit) {
		httpjson.WriteError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the request body is larger than the %d bytes this endpoint takes", limit))
		return nil, http.StatusRequestEntityTooLarge
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, int64(limit)))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		httpjson.WriteError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the request body is larger than the %d bytes this endpoint takes", limit))
		return nil, http.StatusRequestEntityTooLarge
	case err != nil:
		httpjson.WriteError(w, http.StatusBadRequest, "the request body could not be read")
		return nil, http.StatusBadRequest
	}
	return body, 0
}

// siteOrigin reports whether the request comes from a site its application
// holds. Only a public or bot_check endpoint answers one unlisted, and never
// with credentials.
func (h *Handler) siteOrigin(r *http.Request, t *eprun.Target) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || h.SiteOrigins == nil || t.Binding.AuthMode == epbinding.AuthAuthenticated {
		return false
	}
	return slices.Contains(h.SiteOrigins(r.Context(), t.ApplicationID()), origin)
}

// write sends the endpoint's response under the policy every endpoint answer
// carries. The headers are the ones the handler returned, which the runner
// has already refused the platform's own from (eprun.headersFrom: framing,
// cookies, content policy, CORS), and whatever the body is, the sandbox
// policy below serves it as an opaque document, as pkg/blobserve serves
// stored content.
func write(w http.ResponseWriter, r *http.Request, resp eprun.Response, policy string) {
	for name, values := range resp.Header {
		for _, v := range values {
			// nosemgrep: relayed-response-headers -- the handler's own response, filtered by eprun.headersFrom; see above
			w.Header().Add(name, v)
		}
	}
	w.Header().Set("Content-Security-Policy", policy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if w.Header().Get(headerCacheControl) == "" {
		w.Header().Set(headerCacheControl, "no-store")
	}
	w.WriteHeader(resp.Status)
	if r.Method == http.MethodHead || resp.Status == http.StatusNoContent || resp.Status == http.StatusNotModified {
		return
	}
	_, _ = w.Write(resp.Body)
}

// cors answers the request's cross-origin part from the binding's allowed
// origins, and reports whether to go on: a preflight from an origin the
// binding does not list is refused, and an allowed one is answered here.
// Credentials are never allowed across origins, so a browser session is
// never sent from another site; a cross-origin caller signs in with an API
// key.
func cors(w http.ResponseWriter, r *http.Request, b *epbinding.Binding, form, site bool) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	w.Header().Add("Vary", "Origin")
	listed := slices.Contains(b.AllowedOrigins, origin) || (form && origin == originOpaque) || site
	preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
	if !listed {
		if preflight {
			httpjson.WriteError(w, http.StatusForbidden, "this endpoint does not answer requests from "+logsan.SanitizeForLog(origin))
			return false
		}
		return true
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	if preflight {
		w.Header().Set("Access-Control-Allow-Methods", allow(b))
		w.Header().Set("Access-Control-Allow-Headers", allowedRequestHeaders)
		w.Header().Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	return true
}

// allow lists the methods the binding answers.
func allow(b *epbinding.Binding) string {
	methods := append([]string{}, b.Methods...)
	if slices.Contains(methods, http.MethodGet) && !slices.Contains(methods, http.MethodHead) {
		methods = append(methods, http.MethodHead)
	}
	return strings.Join(append(methods, http.MethodOptions), ", ")
}

func tooMany(w http.ResponseWriter, r *http.Request, rpm int) {
	httpobs.MarkRateLimited(r, httpobs.LimiterEndpoint)
	w.Header().Set("Retry-After", strconv.Itoa(max(1, (60+rpm-1)/rpm)))
	httpjson.WriteError(w, http.StatusTooManyRequests, "too many requests to this endpoint; wait and send it again")
}

// challenge issues the challenge of the bot check a bot_check endpoint names,
// answering the cross-origin request a page on an allowed origin sends for
// it.
func (h *Handler) challenge(w http.ResponseWriter, r *http.Request) {
	t, ok := h.resolve(w, r, r.URL.Query().Get("endpoint"))
	if !ok {
		return
	}
	b := t.Binding
	if b.AuthMode != epbinding.AuthBotCheck {
		httpjson.WriteError(w, http.StatusNotFound, "no bot_check endpoint has this name")
		return
	}
	if !cors(w, r, b, t.Form, h.siteOrigin(r, t)) {
		return
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// A challenge is a bucket of its own at the endpoint's rate, so fetching
	// one does not spend the request it guards.
	if !h.endpointLimits.allow(b.Name, b.RateLimit, challengeKey) {
		tooMany(w, r, b.RateLimit)
		return
	}
	ch, err := h.Checker.Issue(r.Context(), b.BotCheck, b.Name)
	if err != nil {
		slog.WarnContext(r.Context(), "endpoint: issuing a bot check challenge failed", logKeyError, logsan.SanitizeForLog(err.Error()))
		httpjson.WriteError(w, http.StatusServiceUnavailable, "a bot check challenge could not be issued")
		return
	}
	w.Header().Set(headerCacheControl, "no-store")
	httpjson.WriteJSON(w, http.StatusOK, ch)
}

// solver serves the browser script a page answers a bot check with.
func solver(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set(headerCacheControl, "public, max-age=3600")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write(botcheck.SolverJS)
}

// limiters is one token-bucket limiter per endpoint, rebuilt when the
// endpoint's limit changes. A key within one limiter is a caller.
type limiters struct {
	mu sync.Mutex
	m  map[string]*limiter
}

type limiter struct {
	l   *ratelimit.Limiter
	rpm int
}

// allow takes one request from key's bucket in name's limiter at rpm a
// minute, which a full minute's worth may arrive at once.
func (ls *limiters) allow(name string, rpm int, key string) bool {
	ls.mu.Lock()
	if ls.m == nil {
		ls.m = map[string]*limiter{}
	}
	l := ls.m[name]
	if l == nil || l.rpm != rpm {
		if l != nil {
			l.l.Close()
		}
		l = &limiter{l: ratelimit.New(ratelimit.Config{RequestsPerMinute: rpm, BurstSize: rpm}), rpm: rpm}
		ls.m[name] = l
	}
	ls.mu.Unlock()
	return l.l.Allow(key)
}
