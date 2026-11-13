//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// Acceptance for #2554: page_frame_origins, page_connect_origins and
// page_form_origins on a page binding add to its policy's frame-src,
// connect-src and form-action; they and page_cdns take https origins and one
// leading wildcard label of the accepted shape; a binding that sets none
// answers exactly the policy it did; each is refused on an api binding and in
// a bad shape, naming the field; the endpoint on the portal's own origin stays
// sandboxed; and an application export and import carries them.
//
// Wire forms: endpoint_set takes endpoint, path, auth_mode and response_mode
// as strings and methods, callers, page_cdns, page_frame_origins,
// page_connect_origins and page_form_origins as lists of strings (a list is
// the only form each admits); manage_application takes command, name,
// application, kind and part_id as strings, release as a number, and import
// takes bundle as a string and map as an object. A page is an HTTP GET naming
// the page origin in Host.

const (
	frame2554   = "https://pay.example.test"
	connect2554 = "https://api.pay.example.test"
	form2554    = "https://checkout.pay.example.test"
	wild2554    = "https://*.js.pay.example.test"
)

// page2554 is an application with one page endpoint.
type page2554 struct {
	admin *client
	name  string
	host  string
}

func newPage2554(t *testing.T, lists map[string]any) *page2554 {
	t.Helper()
	admin := connect(t)
	name := "acc2554-" + unique1579()
	admin.call("manage_application", map[string]any{"command": "create", "name": name, "description": "Acceptance 2554"})
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": name})
	})
	saveEndpointScript(t, admin, name, `def handle(req):
    """Answers a page."""
    return {"headers": {"Content-Type": "text/html; charset=utf-8"}, "body": "<p>checkout</p>"}
`, `{"params": {}, "caller": None}`)
	addScriptPart2517(t, admin, name, name)
	args := map[string]any{
		"endpoint": name, "methods": []string{"GET"}, "path": "/", "auth_mode": "public", "callers": []string{}, "response_mode": "page",
	}
	for k, v := range lists {
		args[k] = v
	}
	bindEndpoint(t, admin, name, "", args)
	admin.call("manage_application", map[string]any{"command": "release", "application": name, "note": "first"})
	return &page2554{admin: admin, name: name, host: name + "." + pagesDomain2517}
}

func (p *page2554) policy(t *testing.T) string {
	t.Helper()
	res := onHost(t, p.host, "GET", "/", nil, "")
	if res.status != http.StatusOK {
		t.Fatalf("the page answered %d: %s", res.status, res.body)
	}
	return res.header.Get("Content-Security-Policy")
}

// directive2554 is one directive's sources.
func directive2554(policy, name string) []string {
	for part := range strings.SplitSeq(policy, ";") {
		if f := strings.Fields(part); len(f) > 0 && f[0] == name {
			return f[1:]
		}
	}
	return nil
}

// Criteria 1 to 3: each list lands in its directive, a wildcard origin of the
// accepted shape is served in each list and in page_cdns.
func TestIssue2554_EachListLandsInItsDirective(t *testing.T) {
	p := newPage2554(t, map[string]any{
		"page_frame_origins":   []string{frame2554, wild2554},
		"page_connect_origins": []string{connect2554, wild2554},
		"page_form_origins":    []string{form2554, wild2554},
		"page_cdns":            []string{wild2554},
	})
	policy := p.policy(t)
	for name, want := range map[string]string{
		"frame-src":   strings.Join([]string{frame2554, wild2554}, " "),
		"connect-src": strings.Join([]string{"'self'", connect2554, wild2554}, " "),
		"form-action": strings.Join([]string{"'self'", form2554, wild2554}, " "),
	} {
		if got := strings.Join(directive2554(policy, name), " "); got != want {
			t.Errorf("%s is %q, want %q (policy %s)", name, got, want, policy)
		}
	}
	for _, name := range []string{"script-src", "style-src", "img-src", "font-src"} {
		if !strings.Contains(strings.Join(directive2554(policy, name), " "), wild2554) {
			t.Errorf("%s does not carry the wildcard CDN: %s", name, policy)
		}
	}
	if got := directive2554(policy, "frame-ancestors"); strings.Join(got, " ") != "'none'" {
		t.Errorf("frame-ancestors changed: %v", got)
	}
}

// Criterion 4: a binding with none of them answers byte for byte the policy
// it answered before they existed.
func TestIssue2554_ABindingWithNoneAnswersTodaysPolicy(t *testing.T) {
	p := newPage2554(t, nil)
	policy := p.policy(t)
	nonce := regexp.MustCompile(`'nonce-([^']+)'`).FindStringSubmatch(policy)
	if nonce == nil {
		t.Fatalf("no nonce in %s", policy)
	}
	want := fmt.Sprintf("default-src 'none'; script-src 'self' 'nonce-%s'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; "+
		"font-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'", nonce[1])
	if policy != want {
		t.Fatalf("the policy is\n  %s\nwant\n  %s", policy, want)
	}
}

// Criteria 5 and 6: a bad shape or a list over the cap is refused naming the
// field, and on an api binding each field is refused.
func TestIssue2554_RefusalsNameTheField(t *testing.T) {
	p := newPage2554(t, nil)
	over := make([]string, 21)
	for i := range over {
		over[i] = fmt.Sprintf("https://pay%d.example.test", i)
	}
	for _, field := range []string{"page_frame_origins", "page_connect_origins", "page_form_origins", "page_cdns"} {
		for _, bad := range [][]string{{"http://pay.example.test"}, {"https://pay.example.test/checkout"}, {"https://*"}, {"https://*.com"}, over} {
			out, text := endpointSet(t, p.admin, p.name, "", map[string]any{field: bad})
			if out != nil || !strings.Contains(text, field) {
				t.Errorf("%s %v: accepted=%v, refusal %q; want refused naming the field", field, bad[0], out != nil, text)
			}
		}
	}
	api := p.name + "-api"
	saveEndpointScript(t, p.admin, api, `def handle(req):
    """Answers JSON."""
    return {"json": {}}
`, `{"params": {}, "caller": None}`)
	for _, field := range []string{"page_frame_origins", "page_connect_origins", "page_form_origins"} {
		out, text := endpointSet(t, p.admin, api, "", map[string]any{
			"endpoint": api, "auth_mode": "public", "callers": []string{}, field: []string{frame2554},
		})
		if out != nil || !strings.Contains(text, field+" applies to an endpoint with response_mode page") {
			t.Errorf("%s on an api binding: accepted=%v, refusal %q", field, out != nil, text)
		}
	}
}

// Criterion 7: the endpoint on the portal's origin stays sandboxed.
func TestIssue2554_ThePortalsOriginStaysSandboxed(t *testing.T) {
	p := newPage2554(t, map[string]any{"page_frame_origins": []string{frame2554}, "page_connect_origins": []string{connect2554}})
	res := endpointRequest(t, baseURL(), http.MethodGet, "/endpoints/"+p.name, nil, "")
	if got := res.header.Get("Content-Security-Policy"); got != "default-src 'none'; sandbox" {
		t.Fatalf("the endpoint on the portal's origin answered the policy %q", got)
	}
}

// Criterion 8: an application export and import carries the fields.
func TestIssue2554_AnExportAndImportCarryTheFields(t *testing.T) {
	p := newPage2554(t, map[string]any{
		"page_frame_origins": []string{frame2554}, "page_connect_origins": []string{connect2554}, "page_form_origins": []string{form2554},
	})
	out := p.admin.call("manage_application", map[string]any{"command": "export", "application": p.name, "release": 1})
	landing, _ := out["bundle"].(map[string]any)
	id, _ := landing["resource_id"].(string)
	got := endpointRequest(t, p.admin.base, http.MethodGet, "/api/v1/resources/"+id+"/content", bearer(p.admin.apiKey), "")
	if got.status != http.StatusOK {
		t.Fatalf("reading the bundle answered %d: %s", got.status, got.body)
	}
	for _, want := range []string{`"page_frame_origins":["` + frame2554 + `"]`, `"page_connect_origins":["` + connect2554 + `"]`, `"page_form_origins":["` + form2554 + `"]`} {
		if !strings.Contains(got.body, want) {
			t.Errorf("the bundle does not carry %s", want)
		}
	}

	second := startSecond2412(t)
	if ok, text := importMCP2476(t, second, []byte(got.body), map[string]string{}); !ok {
		t.Fatalf("the import was refused: %s", text)
	}
	bound := second.admin.call("manage_script", map[string]any{"command": "endpoint_list", "name": p.name})
	for field, want := range map[string]string{"page_frame_origins": frame2554, "page_connect_origins": connect2554, "page_form_origins": form2554} {
		if !strings.Contains(fmt.Sprint(bound[field]), want) {
			t.Errorf("the imported binding's %s is %v, want %s", field, bound[field], want)
		}
	}
}
