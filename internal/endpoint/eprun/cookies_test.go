package eprun

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

const sessionCookie = "sess-7f3a9c2e41d8b6"

// cookieSource reads the session cookie, passes it to a platform call, and
// sets a cart cookie, failing with the session value when asked to.
const cookieSource = `
def handle(req):
    """Keeps a cart."""
    sid = req["cookies"].get("sid")
    if req["query"].get("fail"):
        fail("no cart for session " + sid)
    platform.call("lookup", {"id": sid})
    return {"json": {"sid": sid, "ip": req["client_ip"], "cookies": len(req["cookies"])},
            "cookies": [{"name": "cart", "value": "c-42", "max_age": 3600}, {"name": "sid", "value": "", "max_age": 0, "same_site": "strict"}]}
`

// TestServe_CookiesReachTheHandlerAndComeBack is #2555's round trip through a
// real run: req["cookies"] and req["client_ip"] hold what the HTTP surface
// read, and the cookies the handler returns are the response's.
func TestServe_CookiesReachTheHandlerAndComeBack(t *testing.T) {
	f := newFixture(t, cookieSource)
	resp := f.serve(t, Request{Method: "GET", Params: map[string]string{"id": "1"}, Cookies: map[string]string{"sid": sessionCookie}, ClientIP: "203.0.113.7"})
	require.Equal(t, http.StatusOK, resp.Status, string(resp.Body))
	out := decode(t, resp.Body)
	assert.Equal(t, sessionCookie, out["sid"])
	assert.Equal(t, "203.0.113.7", out["ip"], "req client_ip is the resolved address (#2556)")
	require.Len(t, resp.Cookies, 2)
	assert.Equal(t, "cart", resp.Cookies[0].Name)
	assert.Equal(t, "c-42", resp.Cookies[0].Value)
	assert.Equal(t, 3600, *resp.Cookies[0].MaxAge)
	assert.Equal(t, SameSiteLax, resp.Cookies[0].SameSite, "Lax by default")
	assert.Equal(t, 0, *resp.Cookies[1].MaxAge, "0 deletes")
	assert.Equal(t, SameSiteStrict, resp.Cookies[1].SameSite)

	resp = f.serve(t, Request{Method: "GET", Params: map[string]string{"id": "1"}})
	require.Equal(t, http.StatusOK, resp.Status, string(resp.Body))
	assert.EqualValues(t, 0, decode(t, resp.Body)["cookies"], "a request with none has an empty cookies dict")
}

// TestServe_ACookieValueReachesNoAuditRowAndNoFailure is #2555's sinks: the
// session value the handler passes to a platform call is replaced in that
// call's audit row, and the request's own row and a kept failure that quotes
// it hold it in no form.
func TestServe_ACookieValueReachesNoAuditRowAndNoFailure(t *testing.T) {
	f := newFixture(t, cookieSource)
	calls := &memAudit{}
	f.runner.Server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(calls))

	resp := f.serve(t, Request{Method: "GET", Params: map[string]string{"id": "1"}, Cookies: map[string]string{"sid": sessionCookie}})
	require.Equal(t, http.StatusOK, resp.Status, string(resp.Body))
	require.NotEmpty(t, calls.events, "the handler's platform call is audited")
	for _, e := range append(calls.events, f.audit.events...) {
		assert.NotContains(t, fmt.Sprint(e.Parameters), sessionCookie, e.ToolName)
		assert.NotContains(t, e.ErrorMessage, sessionCookie, e.ToolName)
	}
	assert.Contains(t, fmt.Sprint(calls.events[0].Parameters), "[REDACTED:cookie]", "the value is replaced, not dropped")

	resp = f.serve(t, Request{Method: "GET", Params: map[string]string{"id": "1"}, Query: map[string][]string{"fail": {"1"}}, Cookies: map[string]string{"sid": sessionCookie}})
	require.Equal(t, http.StatusInternalServerError, resp.Status)
	require.NotEmpty(t, f.bindings.failures)
	failure := f.bindings.failures[len(f.bindings.failures)-1]
	assert.NotContains(t, failure.Error, sessionCookie, "a kept failure holds no cookie value")
	assert.Contains(t, failure.Error, "[REDACTED:cookie]")
	last := f.audit.events[len(f.audit.events)-1]
	assert.NotContains(t, last.ErrorMessage, sessionCookie)
	assert.NotContains(t, string(resp.Body), sessionCookie)
}

// TestCookiesFrom_Refusals is #2555's bounds: each is refused naming what is
// wrong.
func TestCookiesFrom_Refusals(t *testing.T) {
	cookie := func(fields string) string { return "[{" + fields + "}]" }
	many := make([]string, MaxCookiesSet+1)
	for i := range many {
		many[i] = fmt.Sprintf(`{"name": "c%d", "value": "v"}`, i)
	}
	for src, want := range map[string]string{
		cookie(`"name": "sid", "value": "x", "same_site": "None"`):            `same_site "None"`,
		cookie(`"name": "sid", "value": "` + strings.Repeat("a", 1025) + `"`): "a value is at most 1024",
		"[" + strings.Join(many, ",") + "]":                                   "a response sets at most 10",
		cookie(`"name": "a b", "value": "x"`):                                 `cookie name "a b"`,
		cookie(`"name": "` + strings.Repeat("n", 33) + `", "value": "x"`):     "1 to 32 letters",
		cookie(`"name": "", "value": "x"`):                                    `cookie name ""`,
		cookie(`"name": "sid", "value": "a;b"`):                               "encode it first",
		cookie(`"name": "sid", "value": "a b"`):                               "encode it first",
		cookie(`"name": "sid", "value": "x", "max_age": -1`):                  "cookie max_age -1",
		cookie(`"name": "sid", "value": "x", "max_age": 34560001`):            "400 days",
		cookie(`"name": "sid", "value": "x", "max_age": "1"`):                 "cookie max_age is string",
		cookie(`"name": "sid", "value": "x", "domain": "a.example"`):          `the key "domain"`,
		cookie(`"name": "sid", "value": 3`):                                   "cookie value is int",
		`[{"name": "sid", "value": "1"}, {"name": "sid", "value": "2"}]`:      `"sid" is set twice`,
		`["sid=1"]`:    "a cookie is string",
		`{"sid": "1"}`: "cookies is dict",
	} {
		v, err := starlark.EvalOptions(&syntax.FileOptions{}, &starlark.Thread{}, "c", src, nil)
		require.NoError(t, err, src)
		_, err = cookiesFrom(v)
		require.ErrorContains(t, err, want, src)
	}
}

// TestReadCookies is #2555's read side: only a name with the exact prefix is
// the application's, so the platform's cookies are never among them in any
// casing, and at most twenty are read, the first in name order.
func TestReadCookies(t *testing.T) {
	names := []string{"__Host-app-sid", "__Host-page_viewer", "page_state", "__host-app-sid2", "__HOST-APP-x", "__Host-app-page_viewer", "session", "__Host-app-", "__Host-app-a b"}
	values := []string{"s1", "v", "st", "no", "no", "mine", "portal", "empty", "space"}
	got := ReadCookies(CookiePrefixSecure, names, values)
	assert.Equal(t, map[string]string{"sid": "s1", "page_viewer": "mine"}, got,
		"page_viewer here is the application's own cookie of that name, written __Host-app-page_viewer")

	many, vals := make([]string, 0, MaxCookiesRead+5), make([]string, 0, MaxCookiesRead+5)
	for i := range MaxCookiesRead + 5 {
		many = append(many, fmt.Sprintf("app-c%02d", i))
		vals = append(vals, "v")
	}
	read := ReadCookies(CookiePrefixPlain, many, vals)
	assert.Len(t, read, MaxCookiesRead)
	assert.Contains(t, read, "c00")
	assert.NotContains(t, read, fmt.Sprintf("c%02d", MaxCookiesRead), "the first twenty by name")
}

func TestCookieRedactor_LeavesShortValues(t *testing.T) {
	red := cookieRedactor(map[string]string{"theme": "dark", "sid": sessionCookie})
	assert.Equal(t, "dark [REDACTED:cookie]", red.String("dark "+sessionCookie))
	assert.True(t, cookieRedactor(nil).Empty())
	_ = context.Background()
}
