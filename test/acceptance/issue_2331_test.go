//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Acceptance for #2331: a form's hosted page at /forms/<endpoint> and the
// embed snippet a site outside the platform frames it with. An anonymous
// browser opens the page, submits it and sees the confirmation; a page on an
// origin the binding lists embeds the snippet and its submission is stored,
// and the same snippet on an origin it does not list is refused by the
// browser; the page's query string reaches form_spec; a name that is not a
// form answers 404; an authenticated form opened directly sends the browser
// to sign in; and the page in light and dark at desktop and phone width.
//
// Wire forms: manage_script create and endpoint_set take their arguments as
// literal tools/call params (allowed_origins as an array of strings); the
// form page is a plain GET; the browser submits the form as the page does, a
// JSON object; trino_query takes string connection, sql and purpose.
//
// The criteria run against the local stack: the platform, Keycloak, Trino with
// the acme-scratch-resources scratch catalog and the store the receiver writes
// to, with Playwright's Chromium for the browser cases.

const (
	// issue2331Listed is the site the binding lists, and issue2331Unlisted
	// one it does not. Playwright answers both addresses itself.
	issue2331Listed   = "http://site-a.example.org"
	issue2331Unlisted = "http://site-b.example.net"
)

// TestIssue2331_AnAnonymousBrowserSubmitsTheHostedPage is criteria 1 and 3:
// the page at /forms/<name>, opened with a query string, is filled in and
// submitted by an anonymous browser, which sees the confirmation, and the
// stored row carries the query's value form_spec read.
func TestIssue2331_AnAnonymousBrowserSubmitsTheHostedPage(t *testing.T) {
	admin := connect(t)
	form, table := bindForm(t, admin, "acc-2331-hosted", formFields, map[string]any{"auth_mode": "public"})
	formsBrowser(t, "hosted", "FORMS_FORM_PATH=/forms/"+form+"?t=link-2331")
	rows := formRows(t, admin, table, 1)
	if v := rows[0].values(); v["email"] != "hosted@example.com" || v["t"] != "link-2331" {
		t.Fatalf("the hosted page's submission is stored as %v", v)
	}
}

// TestIssue2331_TheSnippetWorksOnlyWhereTheBindingAllowsIt is criterion 2: a
// page at an origin the binding lists frames the snippet, the frame is sized
// to the form, and its submission is stored; the same snippet at an origin it
// does not list is refused by the browser and stores nothing.
func TestIssue2331_TheSnippetWorksOnlyWhereTheBindingAllowsIt(t *testing.T) {
	admin := connect(t)
	form, table := bindForm(t, admin, "acc-2331-embed", formFields, map[string]any{
		"auth_mode": "public", "allowed_origins": []string{issue2331Listed},
	})
	page := endpointRequest(t, baseURL(), http.MethodGet, "/forms/"+form, nil, "")
	if fa := issue2331FrameAncestors(page.header); fa != "'self' "+issue2331Listed {
		t.Fatalf("the page is framed by %q, want 'self' and the listed origin", fa)
	}
	formsBrowser(t, "snippet", "FORMS_FORM_PATH=/forms/"+form, "FORMS_LISTED_ORIGIN="+issue2331Listed,
		"FORMS_UNLISTED_ORIGIN="+issue2331Unlisted)
	rows := formRows(t, admin, table, 1)
	if len(rows) != 1 || rows[0].values()["email"] != "embedded@example.com" {
		t.Fatalf("the listed site's submission, and only it, is stored: %v", rows)
	}
}

// issue2331FrameAncestors is the frame-ancestors a page's policy sends.
func issue2331FrameAncestors(h http.Header) string {
	for _, d := range strings.Split(h.Get("Content-Security-Policy"), ";") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(d), "frame-ancestors "); ok {
			return v
		}
	}
	return ""
}

// TestIssue2331_ANameThatIsNotAFormIs404 is criterion 4.
func TestIssue2331_ANameThatIsNotAFormIs404(t *testing.T) {
	admin := connect(t)
	name := endpointName("acc-2331-lookup")
	saveEndpointScript(t, admin, name, issue2171EchoHandler, `{"params": {}, "caller": None}`)
	bindEndpoint(t, admin, name, "", map[string]any{"endpoint": name, "methods": []string{"GET"}, "auth_mode": "public"})
	for _, path := range []string{"/forms/" + name, "/forms/" + endpointName("acc-2331-nobody")} {
		if got := endpointRequest(t, baseURL(), http.MethodGet, path, nil, ""); got.status != http.StatusNotFound {
			t.Errorf("%s: %d %s, want 404", path, got.status, got.body)
		}
	}
}

// TestIssue2331_AnAuthenticatedFormAsksTheBrowserToSignIn: opened with no
// session, the page sends the browser to sign in and back; opened by a
// signed-in person, it submits as them.
func TestIssue2331_AnAuthenticatedFormAsksTheBrowserToSignIn(t *testing.T) {
	admin := connect(t)
	form, table := bindForm(t, admin, "acc-2331-members", formFields, map[string]any{"auth_mode": "authenticated", "callers": []string{"*"}})
	path := "/forms/" + form + "?t=member"

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	opened := endpointRequestWith(t, noRedirect, baseURL(), http.MethodGet, path, http.Header{"Sec-Fetch-Dest": {"document"}}, "")
	if opened.status != http.StatusSeeOther || opened.header.Get("Location") != "/portal/auth/login?return_to="+url.QueryEscape(path) {
		t.Fatalf("a browser with no session: %d %v, want 303 to the sign-in returning to the page", opened.status, opened.header.Get("Location"))
	}

	session := browserSignIn(t, baseURL(), issue1759AnalystEmail, issue1759AnalystPass)
	page := endpointRequestWith(t, session, baseURL(), http.MethodGet, path, http.Header{"Sec-Fetch-Dest": {"document"}}, "")
	viewer := issue2331Config(t, page.body)["viewer"]
	if page.status != http.StatusOK || viewer == nil || viewer == "" {
		t.Fatalf("a signed-in browser's page: %d, viewer %v", page.status, viewer)
	}
	header := http.Header{"X-Form-Viewer": {fmt.Sprint(viewer)}}
	shown := showForm(t, baseURL(), form, "t=member", header)
	shown.waitToFill()
	if status, out := submitForm(t, baseURL(), form, shown.fields(map[string]string{"email": "member@example.com"}), true, header); status != http.StatusOK {
		t.Fatalf("submitting as the signed-in person: %d %v", status, out)
	}
	rows := formRows(t, admin, table, 1)
	sub, _ := rows[0].payload["submitter"].(map[string]any)
	if sub["email"] != issue1759AnalystEmail {
		t.Fatalf("the stored submitter is %v, want %s", sub, issue1759AnalystEmail)
	}
}

// issue2331Config is the configuration a served page hands its script.
func issue2331Config(t *testing.T, body string) map[string]any {
	t.Helper()
	_, rest, ok := strings.Cut(body, `<script type="application/json" id="form-config">`)
	raw, _, _ := strings.Cut(rest, "</script>")
	var cfg map[string]any
	if !ok || json.Unmarshal([]byte(raw), &cfg) != nil {
		t.Fatalf("the page carries no configuration: %.400s", body)
	}
	return cfg
}

// TestIssue2331_ScreensInLightAndDark is criterion 5: the hosted page with a
// field error, in light and dark at desktop and phone width.
func TestIssue2331_ScreensInLightAndDark(t *testing.T) {
	admin := connect(t)
	fields := `[
    {"name": "email", "type": "email", "required": True, "label": "Email", "help": "We reply to this address."},
    {"name": "message", "type": "long_text", "label": "Message"},
    {"name": "topic", "type": "single_choice", "label": "Topic", "choices": [{"value": "billing", "label": "Billing"}, {"value": "data", "label": "Data"}]},
    {"name": "updates", "type": "checkbox", "label": "Send me updates"},
]`
	form, _ := bindForm(t, admin, "acc-2331-screens", fields, map[string]any{"auth_mode": "public"})
	root, _ := filepath.Abs("../..")
	dir := filepath.Join(root, "build", "2331", "screens")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	formsBrowser(t, "screens", "FORMS_FORM_PATH=/forms/"+form, "FORMS_SCREENSHOT_DIR="+dir)
	for _, scheme := range []string{"light", "dark"} {
		for _, size := range []string{"desktop", "phone"} {
			path := filepath.Join(dir, "form-"+scheme+"-"+size+".png")
			if info, err := os.Stat(path); err != nil || info.Size() == 0 {
				t.Errorf("no screenshot at %s: %v", path, err)
			}
		}
	}
}
