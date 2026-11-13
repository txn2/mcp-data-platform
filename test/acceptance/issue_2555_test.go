//go:build integration

package acceptance

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2555: a handler on an application's own address reads its
// cookies as req["cookies"] and sets them by returning "cookies". The platform
// writes each under a fixed prefix, HttpOnly, Path=/, with no Domain, Secure
// over https, and the platform's own cookies never reach a handler. The
// platform's own host carries none and sets none; a cross-site write carries
// none; the bounds are refused; and a cookie value reaches no audit row, log,
// kept failure or call record in any form, and is sealed in a recording.
//
// Wire forms: manage_application takes command, name, application, kind and
// part_id as strings; manage_script takes command, name and source as strings;
// endpoint_set takes endpoint, path, auth_mode and response_mode as strings and
// methods and callers as lists of strings. A request is an HTTP GET or POST
// naming the page origin in Host and carrying a Cookie header.

// setHandler2555 sets the cookie the query names (sid by default) to the value
// it gives.
const setHandler2555 = `def handle(req):
    """Sets the cookie a visitor's next request carries."""
    name = req["query"].get("name", "sid")
    return {"json": {"set": name}, "cookies": [{"name": name, "value": req["query"].get("v", ""), "max_age": 3600}]}
`

// readHandler2555 reads the visitor's cookies, passes the session to a call,
// and fails quoting it when asked.
const readHandler2555 = `def handle(req):
    """Reads the visitor's cookies."""
    sid = req["cookies"].get("sid", "")
    if req["query"].get("fail"):
        fail("no cart for session " + sid)
    platform.call("dev-mock__echo", {"message": "session " + sid, "purpose": "Acceptance 2555: a handler passes its session to a call"})
    return {"json": {"sid": sid, "names": sorted(req["cookies"].keys()), "values": req["cookies"]}}
`

// badHandler2555 returns the out-of-bounds cookies the query names.
const badHandler2555 = `def handle(req):
    """Returns the cookies the query names."""
    choices = {
        "none": [{"name": "sid", "value": "x", "same_site": "None"}],
        "big": [{"name": "sid", "value": "v" * 1025}],
        "many": [{"name": "c" + str(i), "value": "v"} for i in range(11)],
    }
    return {"json": {}, "cookies": choices[req["query"]["kind"]]}
`

// app2555 is an application whose root sets cookies and whose second endpoint
// reads them.
type app2555 struct {
	admin *client
	name  string
	read  string
	bad   string
	host  string
}

func newApp2555(t *testing.T) *app2555 {
	t.Helper()
	admin := connect(t)
	name := "acc2555-" + unique1579()
	admin.call("manage_application", map[string]any{"command": "create", "name": name, "description": "Acceptance 2555"})
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": name})
	})
	a := &app2555{admin: admin, name: name, read: name + "-read", bad: name + "-bad", host: name + "." + pagesDomain2517}
	for _, s := range []struct{ name, source, req string }{
		{name, setHandler2555, `{"params": {"route": ""}, "caller": None, "query": {"v": "abc123"}, "cookies": {}}`},
		{a.read, readHandler2555, `{"params": {}, "caller": None, "query": {}, "cookies": {"sid": "abc123"}}`},
		{a.bad, badHandler2555, `{"params": {}, "caller": None, "query": {"kind": "none"}, "cookies": {}}`},
	} {
		saveEndpointScript(t, admin, s.name, s.source, s.req)
		addScriptPart2517(t, admin, name, s.name)
	}
	bindEndpoint(t, admin, name, "", map[string]any{
		"endpoint": name, "methods": []string{"GET"}, "path": "/{route...}", "auth_mode": "public", "callers": []string{}, "response_mode": "page",
	})
	bindEndpoint(t, admin, a.read, "", map[string]any{"endpoint": a.read, "methods": []string{"GET", "POST"}, "path": "/", "auth_mode": "public", "callers": []string{}})
	bindEndpoint(t, admin, a.bad, "", map[string]any{"endpoint": a.bad, "methods": []string{"GET"}, "path": "/", "auth_mode": "public", "callers": []string{}})
	admin.call("manage_application", map[string]any{"command": "release", "application": name, "note": "first"})
	return a
}

func (a *app2555) readCookies(t *testing.T, method, cookie string, headers map[string]string) map[string]any {
	t.Helper()
	h := map[string]string{"Cookie": cookie}
	for k, v := range headers {
		h[k] = v
	}
	res := onHost(t, a.host, method, "/endpoints/"+a.read, h, "")
	if res.status != http.StatusOK {
		t.Fatalf("%s /endpoints/%s answered %d: %s", method, a.read, res.status, res.body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.body), &out); err != nil {
		t.Fatalf("not JSON: %s", res.body)
	}
	return out
}

// Criteria 1 and 2 (http): a handler sets a cookie and another endpoint of
// the application reads it on the next request; the Set-Cookie carries the
// prefix, HttpOnly and Path=/, no Domain, and the answer is private.
func TestIssue2555_AHandlerSetsACookieAnotherEndpointReads(t *testing.T) {
	a := newApp2555(t)
	value := "s-" + unique1579()
	res := onHost(t, a.host, "GET", "/?v="+value, nil, "")
	if res.status != http.StatusOK {
		t.Fatalf("setting the cookie answered %d: %s", res.status, res.body)
	}
	set := res.header.Get("Set-Cookie")
	for _, want := range []string{"app-sid=" + value, "Path=/", "HttpOnly", "Max-Age=3600", "SameSite=Lax"} {
		if !strings.Contains(set, want) {
			t.Errorf("Set-Cookie %q lacks %q", set, want)
		}
	}
	if strings.Contains(strings.ToLower(set), "domain=") || strings.Contains(set, "Secure") {
		t.Errorf("Set-Cookie %q names a Domain, or Secure over http", set)
	}
	if cc := res.header.Get("Cache-Control"); !strings.Contains(cc, "private") {
		t.Errorf("Cache-Control is %q, want private", cc)
	}
	got := a.readCookies(t, "GET", "app-sid="+value, nil)
	if got["sid"] != value {
		t.Fatalf("the next request read sid %v, want %q", got["sid"], value)
	}
}

// Criterion 2 (https): over https the cookie is __Host-app- and Secure.
func TestIssue2555_OverHTTPSTheCookieIsHostPrefixedAndSecure(t *testing.T) {
	a := newApp2555(t)
	certFile, keyFile := cert2557(t)
	p := startPlatform(t, fmt.Sprintf(issue2557Config, certFile, keyFile, devPostgresPort(), devS3Port2616(), false))
	base := strings.Replace(p.base, "http://", "https://", 1)
	hc := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // #nosec G402 -- the test's own self-signed certificate
	value := "t-" + unique1579()
	var set, last string
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline) && set == ""; time.Sleep(500 * time.Millisecond) {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/?v="+value, http.NoBody)
		req.Host = a.host
		res, err := hc.Do(req)
		if err != nil {
			last = err.Error()
			continue
		}
		_ = res.Body.Close()
		if res.StatusCode == http.StatusOK {
			set = res.Header.Get("Set-Cookie")
		}
		last = res.Status
	}
	if set == "" {
		t.Fatalf("the page origin over https set no cookie within 2m: %s", last)
	}
	for _, want := range []string{"__Host-app-sid=" + value, "Path=/", "HttpOnly", "Secure"} {
		if !strings.Contains(set, want) {
			t.Errorf("Set-Cookie %q lacks %q", set, want)
		}
	}
	if strings.Contains(strings.ToLower(set), "domain=") {
		t.Errorf("Set-Cookie %q names a Domain", set)
	}
}

// Criterion 3: with page_viewer, page_state and a portal session on the
// request, in any casing, req["cookies"] holds none of them, and a handler
// setting one of those names writes it under the prefix, changing nothing of
// the platform's.
func TestIssue2555_ThePlatformsCookiesNeverReachAHandler(t *testing.T) {
	a := newApp2555(t)
	got := a.readCookies(t, "GET", "page_viewer=v1; PAGE_VIEWER=v2; page_state=st; __Host-page_viewer=v3; session=portal; App-sid=x; app-page_viewer=mine", nil)
	if names := fmt.Sprint(got["names"]); names != "[page_viewer]" {
		t.Fatalf("the handler read %s, want only the application's own page_viewer", names)
	}
	if values, _ := got["values"].(map[string]any); values["page_viewer"] != "mine" {
		t.Errorf("page_viewer is %v, want the application's own value", values["page_viewer"])
	}
	res := onHost(t, a.host, "GET", "/?name=page_viewer&v=forged", nil, "")
	set := res.header.Values("Set-Cookie")
	if len(set) != 1 || !strings.HasPrefix(set[0], "app-page_viewer=forged") {
		t.Fatalf("a handler setting page_viewer wrote %v, want only app-page_viewer", set)
	}
}

// Criterion 4: the same endpoint on the platform's own host sees no cookies
// and sets none.
func TestIssue2555_ThePlatformsHostSeesAndSetsNone(t *testing.T) {
	a := newApp2555(t)
	res := endpointRequest(t, baseURL(), http.MethodGet, "/endpoints/"+a.read, http.Header{"Cookie": {"app-sid=x; __Host-app-sid=y"}}, "")
	if res.status != http.StatusOK || !strings.Contains(res.body, `"names":[]`) {
		t.Fatalf("on the platform's host the handler read %d: %s", res.status, res.body)
	}
	set := endpointRequest(t, baseURL(), http.MethodGet, "/endpoints/"+a.name+"?v=zzz", nil, "")
	if v := set.header.Values("Set-Cookie"); len(v) != 0 {
		t.Fatalf("on the platform's host a handler set %v", v)
	}
}

// Criterion 5: a POST with no same-origin evidence reaches the handler with
// req["cookies"] empty, and one with Sec-Fetch-Site: same-origin with them.
func TestIssue2555_ACrossSiteWriteCarriesNoCookies(t *testing.T) {
	a := newApp2555(t)
	if got := a.readCookies(t, "POST", "app-sid=s-1", nil); got["sid"] != "" {
		t.Fatalf("a POST with no evidence read sid %v", got["sid"])
	}
	if got := a.readCookies(t, "POST", "app-sid=s-1", map[string]string{"Sec-Fetch-Site": "cross-site"}); got["sid"] != "" {
		t.Fatalf("a cross-site POST read sid %v", got["sid"])
	}
	if got := a.readCookies(t, "POST", "app-sid=s-1", map[string]string{"Sec-Fetch-Site": "same-origin"}); got["sid"] != "s-1" {
		t.Fatalf("a same-origin POST read sid %v, want s-1", got["sid"])
	}
}

// Criterion 6: same_site None, an oversized value and too many cookies are
// each refused, naming what is wrong.
func TestIssue2555_TheBoundsAreRefused(t *testing.T) {
	a := newApp2555(t)
	for kind, want := range map[string]string{"none": `same_site "None"`, "big": "a value is at most 1024", "many": "a response sets at most 10"} {
		res := onHost(t, a.host, "GET", "/endpoints/"+a.bad+"?kind="+kind, nil, "")
		if res.status != http.StatusInternalServerError {
			t.Errorf("kind %s answered %d: %s", kind, res.status, res.body)
		}
		if res.header.Get("Set-Cookie") != "" {
			t.Errorf("kind %s set a cookie", kind)
		}
		found := false
		for range 20 {
			listed := a.admin.call("manage_script", map[string]any{"command": "endpoint_list", "name": a.bad})
			if strings.Contains(fmt.Sprint(listed["failures"]), want) {
				found = true
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		if !found {
			t.Errorf("no kept failure names %q", want)
		}
	}
}

// Criterion 7: a cookie value never appears in the audit rows, the logs, a
// kept failure or the call catalog, as written, base64, hex, URL- or
// JSON-escaped, and is sealed in a recording.
func TestIssue2555_ACookieValueReachesNoSink(t *testing.T) {
	a := newApp2555(t)
	canary := "cookie-canary-" + unique1579()
	if got := a.readCookies(t, "GET", "app-sid="+canary, nil); got["sid"] != canary {
		t.Fatalf("the handler read sid %v", got["sid"])
	}
	failed := onHost(t, a.host, "GET", "/endpoints/"+a.read+"?fail=1", map[string]string{"Cookie": "app-sid=" + canary}, "")
	if failed.status != http.StatusInternalServerError {
		t.Fatalf("the failing request answered %d: %s", failed.status, failed.body)
	}
	forms := forms2363(canary)
	holds := func(text string) string {
		for _, f := range forms {
			if strings.Contains(text, f) {
				return f
			}
		}
		return ""
	}
	var rows []any
	for range 40 {
		rows = a.admin.list("/api/v1/admin/audit/events?user_id=script:" + a.read + "&per_page=50")
		if len(rows) >= 3 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	raw, _ := json.Marshal(rows)
	if len(rows) < 3 {
		t.Fatalf("the requests' audit rows did not arrive: %s", raw)
	}
	if f := holds(string(raw)); f != "" {
		t.Errorf("an audit row holds the cookie value as %q", f)
	}
	if !strings.Contains(string(raw), "[REDACTED:cookie]") {
		t.Errorf("the platform call's row does not show the value replaced: %s", raw)
	}
	listed := a.admin.call("manage_script", map[string]any{"command": "endpoint_list", "name": a.read})
	failures, _ := json.Marshal(listed["failures"])
	if !strings.Contains(string(failures), "no cart for session") {
		t.Fatalf("the failure was not kept: %s", failures)
	}
	if f := holds(string(failures)); f != "" {
		t.Errorf("a kept failure holds the cookie value as %q", f)
	}
	for _, path := range stderrLogFiles(t) {
		data, err := os.ReadFile(path) // #nosec G304 -- the dev stack's own log
		if err == nil {
			if f := holds(string(data)); f != "" {
				t.Errorf("%s holds the cookie value as %q", path, f)
			}
		}
	}
	calls, _ := json.Marshal(a.admin.list("/api/v1/admin/calls?per_page=200"))
	if f := holds(string(calls)); f != "" {
		t.Errorf("the call catalog holds the cookie value as %q", f)
	}

	ran := a.admin.call("manage_script", map[string]any{
		"command": "run_draft", "name": a.read,
		"source": readHandler2555 + draftMain(`{"params": {}, "caller": None, "query": {}, "cookies": {"sid": "`+canary+`"}}`),
	})
	recording, _ := ran["recording"].(string)
	if recording == "" {
		t.Fatalf("the draft named no recording: %v", ran)
	}
	var data []byte
	if err := platformDB2402(t).QueryRowContext(t.Context(), `SELECT data FROM script_recordings WHERE run_id = $1`, recording).Scan(&data); err != nil {
		t.Fatalf("reading recording %s: %v", recording, err)
	}
	if !bytes.HasPrefix(data, []byte("encb:")) || holds(string(data)) != "" {
		t.Errorf("the recording holding the cookie value is not sealed")
	}
}
