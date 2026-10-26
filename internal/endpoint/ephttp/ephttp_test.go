package ephttp

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/botcheck"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
)

// runner resolves the bindings it holds and answers each request with the
// request it was handed, so a test reads what the surface passed on.
type runner struct {
	bindings map[string]*epbinding.Binding
	err      error
	served   []eprun.Request
}

func (f *runner) Resolve(_ context.Context, name string) (*eprun.Target, error) {
	if f.err != nil {
		return nil, f.err
	}
	b, ok := f.bindings[name]
	if !ok {
		return nil, epbinding.ErrNotFound
	}
	return &eprun.Target{Binding: b}, nil
}

func (f *runner) Serve(_ context.Context, _ *eprun.Target, req eprun.Request) eprun.Response {
	f.served = append(f.served, req)
	caller := ""
	if req.Caller != nil {
		caller = req.Caller.Email
	}
	body, _ := json.Marshal(map[string]any{"params": req.Params, "caller": caller, "body": string(req.Body)})
	return eprun.Response{Status: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}, "X-From": {"handler"}}, Body: body}
}

// checker accepts the answer "good" to the check "default" and "good:<check>"
// to any other, once per endpoint, and refuses the rest: an answer to one
// check is refused by an endpoint naming another.
type checker struct {
	verifyErr error
	used      map[string]bool
	issued    []string
	issueErr  error
}

func (c *checker) Issue(_ context.Context, check, endpoint string) (botcheck.Challenge, error) {
	c.issued = append(c.issued, check+"/"+endpoint)
	return botcheck.Challenge{Check: check, Provider: botcheck.ProviderPoW, Token: "tok", Difficulty: 8}, c.issueErr
}

func (c *checker) Verify(_ context.Context, check, endpoint, answer string) error {
	if c.verifyErr != nil {
		return c.verifyErr
	}
	want := "good"
	if check != "default" {
		want = "good:" + check
	}
	switch {
	case answer == "":
		return botcheck.ErrMissing
	case strings.HasPrefix(answer, "good") && answer != want:
		return botcheck.ErrOtherCheck
	case answer != want:
		return botcheck.ErrInvalid
	case c.used[check+"/"+endpoint]:
		return botcheck.ErrReplayed
	}
	c.used[check+"/"+endpoint] = true
	return nil
}

func binding(name, mode string) *epbinding.Binding {
	b := &epbinding.Binding{
		ID: name, Name: name, Methods: []string{"GET", "POST"}, Path: "/{id}", AuthMode: mode, Enabled: true,
		MaxBodyBytes: 64, RateLimit: 600, CallerRateLimit: 2, AllowedOrigins: []string{"https://forms.example.com"},
	}
	switch mode {
	case epbinding.AuthAuthenticated:
		b.Callers = []string{epbinding.CallersAll}
	case epbinding.AuthBotCheck:
		b.BotCheck = "default"
	}
	return b
}

type fixture struct {
	mux     *http.ServeMux
	runner  *runner
	checker *checker
	// refused records every refusal counted, as endpoint/reason.
	refused []string
}

func newFixture() *fixture {
	f := &fixture{
		runner: &runner{bindings: map[string]*epbinding.Binding{
			"contact": binding("contact", epbinding.AuthBotCheck),
			"orders":  binding("orders", epbinding.AuthAuthenticated),
			"open":    binding("open", epbinding.AuthPublic),
		}},
		checker: &checker{used: map[string]bool{}},
	}
	h := &Handler{Runner: f.runner, Checker: f.checker, Authenticate: func(r *http.Request) (*eprun.Caller, error) {
		switch r.Header.Get("Authorization") {
		case "Bearer ann-key":
			return &eprun.Caller{UserID: "ann", Email: "ann@example.com", AuthType: "apikey", Persona: "analyst", APIKey: "ann-key"}, nil
		case "Bearer bo-key":
			return &eprun.Caller{UserID: "bo", Email: "bo@example.com", AuthType: "apikey", Persona: "ops", APIKey: "bo-key"}, nil
		case "Bearer admin-key":
			return &eprun.Caller{UserID: "apikey:admin", Email: "admin@example.com", AuthType: "apikey", Admin: true, Persona: "admin", APIKey: "admin"}, nil
		case "Bearer csrf":
			return nil, ErrForbidden
		}
		if c, err := r.Cookie("session"); err == nil && c.Value == "ann" {
			return &eprun.Caller{UserID: "ann", Email: "ann@example.com", AuthType: "browser_session", Persona: "analyst"}, nil
		}
		return nil, nil //nolint:nilnil // nobody: no credential and no refusal
	}}
	h.Refused = func(endpointID, reason string) { f.refused = append(f.refused, endpointID+"/"+reason) }
	f.mux = http.NewServeMux()
	h.Mount(f.mux)
	return f
}

func (f *fixture) do(method, path string, header http.Header, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	maps.Copy(req.Header, header)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func fields(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	out := map[string]any{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

// #2174, #2259: a bot_check endpoint answers an anonymous request with a
// valid answer to its check, and refuses one without.
func TestBotCheck_AnswersOnlyARequestThatPassedTheBotCheck(t *testing.T) {
	f := newFixture()
	rec := f.do("POST", "/endpoints/contact/7", nil, "hi")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "no bot check answer")

	rec = f.do("POST", "/endpoints/contact/7", http.Header{HeaderBotCheck: {"good"}}, "hi")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	out := fields(t, rec)
	assert.Equal(t, map[string]any{"id": "7"}, out["params"])
	assert.Empty(t, out["caller"], "a bot_check request has no caller")
	assert.Equal(t, "hi", out["body"])
	assert.Equal(t, "handler", rec.Header().Get("X-From"))
	assert.Equal(t, contentPolicy, rec.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

	rec = f.do("POST", "/endpoints/contact/7", http.Header{HeaderBotCheck: {"good"}}, "hi")
	assert.Equal(t, http.StatusForbidden, rec.Code, "the same answer sent again")
	assert.Contains(t, rec.Body.String(), "already used")
}

// #2259: a public endpoint answers a request with no X-Bot-Check header while
// a bot_check endpoint on the same deployment refuses one.
func TestPublic_AnswersWithNoCheckWhereABotCheckEndpointRefuses(t *testing.T) {
	f := newFixture()
	rec := f.do("POST", "/endpoints/open/1", nil, "hi")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, fields(t, rec)["caller"])
	assert.Equal(t, http.StatusForbidden, f.do("POST", "/endpoints/contact/1", nil, "hi").Code)
	assert.Empty(t, f.checker.issued, "a public endpoint asks no check")
}

// #2259: a bot_check endpoint checking only POST answers GET without a check
// and refuses POST without one.
func TestBotCheck_CheckedMethodsAreTheOnlyOnesChecked(t *testing.T) {
	f := newFixture()
	assert.Equal(t, http.StatusForbidden, f.do("GET", "/endpoints/contact/1", nil, "").Code, "every method is checked by default")
	f.runner.bindings["contact"].CheckedMethods = []string{"POST"}
	assert.Equal(t, http.StatusOK, f.do("GET", "/endpoints/contact/1", nil, "").Code)
	head := f.do("HEAD", "/endpoints/contact/1", nil, "")
	assert.Equal(t, http.StatusOK, head.Code)
	assert.Empty(t, head.Body.String(), "a HEAD is answered without a body")
	assert.Equal(t, http.StatusForbidden, f.do("POST", "/endpoints/contact/1", nil, "").Code, "a checked method still needs the check")
}

// #2259: two bot_check endpoints naming different checks each refuse the
// other's answer and accept their own.
func TestBotCheck_EachEndpointTakesOnlyItsOwnChecksAnswer(t *testing.T) {
	f := newFixture()
	other := binding("signup", epbinding.AuthBotCheck)
	other.BotCheck = "turnstile"
	f.runner.bindings["signup"] = other

	rec := f.do("POST", "/endpoints/signup/1", http.Header{HeaderBotCheck: {"good"}}, "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "another bot check")
	assert.Equal(t, http.StatusForbidden, f.do("POST", "/endpoints/contact/1", http.Header{HeaderBotCheck: {"good:turnstile"}}, "").Code)
	assert.Equal(t, http.StatusOK, f.do("POST", "/endpoints/signup/1", http.Header{HeaderBotCheck: {"good:turnstile"}}, "").Code)
	assert.Equal(t, http.StatusOK, f.do("POST", "/endpoints/contact/1", http.Header{HeaderBotCheck: {"good"}}, "").Code)
}

func TestBotCheck_ACheckThatCannotBeMadeIs503(t *testing.T) {
	f := newFixture()
	f.checker.verifyErr = errors.New("provider down")
	rec := f.do("POST", "/endpoints/contact/1", http.Header{HeaderBotCheck: {"good"}}, "")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "a check that could not be made is not the caller's refusal")
}

func TestAnUnknownModeIsNotAnswered(t *testing.T) {
	f := newFixture()
	f.runner.bindings["open"].AuthMode = "signed_in"
	assert.Equal(t, http.StatusServiceUnavailable, f.do("GET", "/endpoints/open/1", nil, "").Code)
	assert.Empty(t, f.runner.served)
}

// #2174: an authenticated endpoint answers an API key and a browser session,
// refuses an anonymous request with 401, and the handler reads who the
// credential named.
func TestAuthenticated_AnswersAKeyAndASessionAndRefusesNobody(t *testing.T) {
	f := newFixture()
	rec := f.do("GET", "/endpoints/orders/1", http.Header{"Authorization": {"Bearer ann-key"}}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "ann@example.com", fields(t, rec)["caller"])
	assert.Equal(t, "apikey", f.runner.served[0].Caller.AuthType)

	rec = f.do("GET", "/endpoints/orders/1", http.Header{"Cookie": {"session=ann"}}, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "browser_session", f.runner.served[1].Caller.AuthType)

	rec = f.do("GET", "/endpoints/orders/1", nil, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("WWW-Authenticate"))

	assert.Equal(t, http.StatusForbidden, f.do("POST", "/endpoints/orders/1", http.Header{"Authorization": {"Bearer csrf"}}, "").Code)
}

// #2259: an authenticated endpoint admits the callers its list names: a
// persona, an API key by name, every caller with "*", and no one with an empty
// list. A caller not named is answered 403 before the handler runs and counted
// under its own reason.
func TestAuthenticated_AdmitsOnlyTheCallersItNames(t *testing.T) {
	f := newFixture()
	orders := f.runner.bindings["orders"]
	ann := http.Header{"Authorization": {"Bearer ann-key"}}
	bo := http.Header{"Authorization": {"Bearer bo-key"}}

	orders.Callers = []string{"persona:analyst"}
	assert.Equal(t, http.StatusOK, f.do("GET", "/endpoints/orders/1", ann, "").Code)
	rec := f.do("GET", "/endpoints/orders/1", bo, "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "does not admit this caller")
	assert.Equal(t, []string{"orders/caller"}, f.refused)

	orders.Callers = []string{"api_key:bo-key"}
	assert.Equal(t, http.StatusOK, f.do("GET", "/endpoints/orders/1", bo, "").Code)
	assert.Equal(t, http.StatusForbidden, f.do("GET", "/endpoints/orders/1", http.Header{"Cookie": {"session=ann"}}, "").Code,
		"a browser session carries no API key")

	orders.Callers = []string{}
	assert.Equal(t, http.StatusForbidden, f.do("GET", "/endpoints/orders/1", ann, "").Code, "an empty list admits no one")
	assert.Equal(t, http.StatusForbidden, f.do("GET", "/endpoints/orders/1", bo, "").Code)
	assert.Equal(t, http.StatusOK, f.do("GET", "/endpoints/orders/1", http.Header{"Authorization": {"Bearer admin-key"}}, "").Code,
		"but the administrator, who is unrestricted")

	orders.Callers = []string{"*"}
	assert.Equal(t, http.StatusOK, f.do("GET", "/endpoints/orders/1", bo, "").Code)
	assert.Len(t, f.runner.served, 4, "no refused caller reached the handler")
}

// #2174: an authenticated caller past their own limit gets 429 while another
// caller is still answered.
func TestAuthenticated_ACallerPastTheirLimitIsRefusedAndAnotherIsNot(t *testing.T) {
	f := newFixture()
	ann := http.Header{"Authorization": {"Bearer ann-key"}}
	for range 2 {
		require.Equal(t, http.StatusOK, f.do("GET", "/endpoints/orders/1", ann, "").Code)
	}
	rec := f.do("GET", "/endpoints/orders/1", ann, "")
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "30", rec.Header().Get("Retry-After"))
	assert.Equal(t, http.StatusOK, f.do("GET", "/endpoints/orders/1", http.Header{"Authorization": {"Bearer bo-key"}}, "").Code)
}

func TestEndpointLimitIsTheCeiling(t *testing.T) {
	f := newFixture()
	b := f.runner.bindings["open"]
	b.RateLimit = 1
	assert.Equal(t, http.StatusOK, f.do("GET", "/endpoints/open/1", nil, "").Code)
	assert.Equal(t, http.StatusTooManyRequests, f.do("GET", "/endpoints/open/1", nil, "").Code)
	b.RateLimit = 5
	assert.Equal(t, http.StatusOK, f.do("GET", "/endpoints/open/1", nil, "").Code, "a changed limit starts a new bucket")
}

// #2174: a browser fetch from an allowed origin passes its preflight and can
// read the response; from an origin that is not listed the preflight is
// refused.
func TestCORS_PreflightAndResponseFromTheBindingsOrigins(t *testing.T) {
	f := newFixture()
	pre := http.Header{
		"Origin": {"https://forms.example.com"}, "Access-Control-Request-Method": {"POST"},
		"Access-Control-Request-Headers": {"content-type"},
	}
	rec := f.do("OPTIONS", "/endpoints/orders/1", pre, "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "https://forms.example.com", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "GET, POST, HEAD, OPTIONS", rec.Header().Get("Access-Control-Allow-Methods"))
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), "Content-Type")
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"), "credentials never cross origins")

	rec = f.do("POST", "/endpoints/orders/1", http.Header{
		"Origin":        {"https://forms.example.com"},
		"Authorization": {"Bearer ann-key"}, "Content-Type": {"application/json"},
	}, `{"a":1}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "https://forms.example.com", rec.Header().Get("Access-Control-Allow-Origin"))

	pre["Origin"] = []string{"https://elsewhere.example.com"}
	rec = f.do("OPTIONS", "/endpoints/orders/1", pre, "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))

	rec = f.do("GET", "/endpoints/orders/1", http.Header{"Origin": {"https://elsewhere.example.com"}, "Authorization": {"Bearer ann-key"}}, "")
	assert.Equal(t, http.StatusOK, rec.Code, "a same-site tool sending an Origin is answered; the browser withholds the response")
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))

	rec = f.do("OPTIONS", "/endpoints/orders/1", nil, "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "GET, POST, HEAD, OPTIONS", rec.Header().Get("Allow"))
}

// #2172: a request body past the limit gets 413 without running the script.
func TestBodyPastTheLimitIs413WithoutRunning(t *testing.T) {
	f := newFixture()
	ann := http.Header{"Authorization": {"Bearer ann-key"}}
	rec := f.do("POST", "/endpoints/orders/1", ann, strings.Repeat("x", 65))
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/endpoints/orders/1", strings.NewReader(strings.Repeat("x", 65)))
	req.Header.Set("Authorization", "Bearer ann-key")
	req.ContentLength = -1
	rec = httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, "a body of unknown length is cut at the limit")
	assert.Empty(t, f.runner.served)
}

func TestRouting(t *testing.T) {
	f := newFixture()
	ann := http.Header{"Authorization": {"Bearer ann-key"}}
	assert.Equal(t, http.StatusNotFound, f.do("GET", "/endpoints/nope", ann, "").Code)
	assert.Equal(t, http.StatusNotFound, f.do("GET", "/endpoints/orders", ann, "").Code, "the path template is /{id}")
	assert.Equal(t, http.StatusNotFound, f.do("GET", "/endpoints/orders/1/2", ann, "").Code)
	rec := f.do("DELETE", "/endpoints/orders/1", ann, "")
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "GET, POST, HEAD, OPTIONS", rec.Header().Get("Allow"))

	f.runner.bindings["orders"].Enabled = false
	assert.Equal(t, http.StatusNotFound, f.do("GET", "/endpoints/orders/1", ann, "").Code, "a disabled binding answers nothing")

	f.runner.err = errors.New("boom")
	assert.Equal(t, http.StatusInternalServerError, f.do("GET", "/endpoints/orders/1", ann, "").Code)
}

func TestChallenge(t *testing.T) {
	f := newFixture()
	rec := f.do("GET", "/botcheck/challenge?endpoint=contact", http.Header{"Origin": {"https://forms.example.com"}}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "tok", fields(t, rec)["token"])
	assert.Equal(t, "https://forms.example.com", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "default", fields(t, rec)["check"])
	assert.Equal(t, []string{"default/contact"}, f.checker.issued, "the challenge is the check the endpoint names")

	assert.Equal(t, http.StatusNotFound, f.do("GET", "/botcheck/challenge?endpoint=orders", nil, "").Code, "an authenticated endpoint has no challenge")
	assert.Equal(t, http.StatusNotFound, f.do("GET", "/botcheck/challenge?endpoint=open", nil, "").Code, "a public endpoint has no challenge")
	assert.Equal(t, http.StatusNotFound, f.do("GET", "/botcheck/challenge?endpoint=nope", nil, "").Code)

	pre := http.Header{"Origin": {"https://forms.example.com"}, "Access-Control-Request-Method": {"GET"}}
	assert.Equal(t, http.StatusNoContent, f.do("OPTIONS", "/botcheck/challenge?endpoint=contact", pre, "").Code)
	assert.Equal(t, http.StatusNoContent, f.do("OPTIONS", "/botcheck/challenge?endpoint=contact", nil, "").Code)

	f.checker.issueErr = errors.New("no key")
	assert.Equal(t, http.StatusServiceUnavailable, f.do("GET", "/botcheck/challenge?endpoint=contact", nil, "").Code)

	f.runner.bindings["contact"].RateLimit = 1
	f.checker.issueErr = nil
	assert.Equal(t, http.StatusOK, f.do("GET", "/botcheck/challenge?endpoint=contact", nil, "").Code)
	assert.Equal(t, http.StatusTooManyRequests, f.do("GET", "/botcheck/challenge?endpoint=contact", nil, "").Code)
	assert.Equal(t, http.StatusOK, f.do("POST", "/endpoints/contact/1", http.Header{HeaderBotCheck: {"good"}}, "").Code,
		"a challenge is counted in a bucket of its own, so the request it guards is still answered")
}

func TestSolverIsServed(t *testing.T) {
	rec := newFixture().do("GET", "/botcheck/solver.js", nil, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/javascript; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), "PlatformBotCheck")
}

func TestWriteLeavesTheBodyOffA204(t *testing.T) {
	rec := httptest.NewRecorder()
	write(rec, httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody), eprun.Response{
		Status: http.StatusNoContent, Body: []byte("x"),
		Header: http.Header{"Cache-Control": {"max-age=60"}},
	})
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
	assert.Equal(t, "max-age=60", rec.Header().Get("Cache-Control"), "the handler's own cache policy is kept")
}

// TestRefusalsAreCountedByEndpointAndReason is #2208: each request refused
// before its handler ran is counted under its endpoint and why, and an
// answered one is not.
func TestRefusalsAreCountedByEndpointAndReason(t *testing.T) {
	f := newFixture()
	ann := http.Header{"Authorization": {"Bearer ann-key"}}

	f.do("POST", "/endpoints/contact/1", nil, "")                                         // no bot check answer
	f.do("POST", "/endpoints/contact/1", http.Header{HeaderBotCheck: {"good"}}, "")       // answered
	f.do("GET", "/endpoints/orders/1", nil, "")                                           // no credential
	f.do("GET", "/endpoints/orders/1", http.Header{"Authorization": {"Bearer csrf"}}, "") // refused credential
	f.do("POST", "/endpoints/orders/1", ann, strings.Repeat("x", 65))                     // body limit
	f.do("GET", "/endpoints/orders/1", ann, "")                                           // caller's second request
	f.do("GET", "/endpoints/orders/1", ann, "")                                           // past the caller's limit
	f.do("OPTIONS", "/endpoints/orders/1", http.Header{"Origin": {"https://elsewhere.example.com"}, "Access-Control-Request-Method": {"POST"}}, "")
	f.runner.bindings["open"].RateLimit = 1
	f.do("GET", "/endpoints/open/1", nil, "")
	f.do("GET", "/endpoints/open/1", nil, "") // past the endpoint's limit
	f.runner.bindings["orders"].Callers = []string{"persona:ops"}
	f.do("GET", "/endpoints/orders/2", http.Header{"Authorization": {"Bearer bo-key"}}, "")  // admitted
	f.do("GET", "/endpoints/orders/2", http.Header{"Authorization": {"Bearer ann-key"}}, "") // not a caller it names

	assert.Equal(t, []string{
		"contact/bot_check", "orders/credential", "orders/credential", "orders/body_limit",
		"orders/caller_rate_limit", "orders/origin", "open/rate_limit", "orders/caller",
	}, f.refused)
}

// TestABodyThatCannotBeReadIsNotABodyLimitRefusal keeps the reason honest.
func TestABodyThatCannotBeReadIsNotABodyLimitRefusal(t *testing.T) {
	f := newFixture()
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/endpoints/orders/1", failingReader{})
	req.Header.Set("Authorization", "Bearer ann-key")
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, f.refused)
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
