//go:build integration

package acceptance

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #2579: the Sign in button on a site's own address did nothing in
// Chromium, whose form-action applies to where a form submission is redirected.
// A GET or HEAD with no valid site cookie now goes straight into the sign-in.
// The browser criteria run in a real Chromium (ui/e2e/site-signin), because a
// Go HTTP client enforces no Content Security Policy.
//
// Wire forms: the browser's own navigations and a form POST; HTTP GET, HEAD and
// POST naming the hostname in Host, with and without Cookie, and a request line
// whose target is absolute or protocol-relative.

// siteURL2579 is the address the platform advertises for the site, which is
// where a browser ends up after the handoff: the portal's public port.
func siteURL2579(t *testing.T, s *site2559) string {
	t.Helper()
	status, got := rest2559(t, keyEngineer2559, http.MethodGet, s.path(""), nil)
	want2559(t, "reading the site", status, http.StatusOK, got)
	address, _ := got["address"].(string)
	if !strings.Contains(address, "://"+s.host()) {
		t.Fatalf("the site's address is %q", address)
	}
	return strings.TrimSuffix(address, "/")
}

func browser2579(t *testing.T, grep string, extra ...string) {
	t.Helper()
	root, _ := filepath.Abs("../..")
	cmd := exec.Command("npx", "playwright", "test", "--config", "e2e/site-signin/playwright.config.ts", "--grep", grep)
	cmd.Dir = filepath.Join(root, "ui")
	cmd.Env = append(os.Environ(), "SITE_SIGNIN_BASE_URL="+baseURL(), "SITE_SIGNIN_EMAIL="+emailKCAnalyst, "SITE_SIGNIN_PASSWORD="+passKCAnalyst)
	cmd.Env = append(cmd.Env, extra...)
	out, err := cmd.CombinedOutput()
	t.Logf("playwright against %s:\n%s", baseURL(), out)
	if err != nil {
		t.Fatalf("the browser case %q failed: %v", grep, err)
	}
}

func TestIssue2579_InChromiumTheSiteOpensWithNoClickAndNoViolation(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	s.publish(t, keyEngineer2559, "", "signin2579")
	s.grant(t, keyEngineer2559, emailKCAnalyst, "view")
	browser2579(t, "criterion 1:", "SITE_SIGNIN_OPEN_URL="+siteURL2579(t, s))
}

func TestIssue2579_InChromiumSomeoneNotSignedInGoesThroughTheLoginAndArrives(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	s.publish(t, keyEngineer2559, "", "login2579")
	s.grant(t, keyEngineer2559, emailKCAnalyst, "view")
	browser2579(t, "criterion 2:", "SITE_SIGNIN_OPEN_URL="+siteURL2579(t, s))
}

func TestIssue2579_InChromiumNoAccessReachesThePortalSignOut(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	s.publish(t, keyEngineer2559, "", "closed2579")
	browser2579(t, "criterion 3:", "SITE_SIGNIN_CLOSED_URL="+siteURL2579(t, s))
}

func TestIssue2579_InChromiumTheSignInLinkOnThePageWorks(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	s.publish(t, keyEngineer2559, "", "link2579")
	s.grant(t, keyEngineer2559, emailKCAnalyst, "view")
	browser2579(t, "criterion 4:", "SITE_SIGNIN_OPEN_URL="+siteURL2579(t, s))
}

func TestIssue2579_NoFileIsServedWithoutASiteCookie(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	s.publish(t, keyEngineer2559, "", "guarded2579")
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		for _, path := range []string{"/", "/index.html", "/about/a.css", "/logo.3f2a9c1b.svg", "/moved", "/no-such-file"} {
			r := onHost(t, s.host(), method, path, nil, "")
			want := http.StatusUnauthorized
			if method == http.MethodGet || method == http.MethodHead {
				want = http.StatusFound
			}
			refusedNoContent2559(t, r, want, method+" "+path)
			if want != http.StatusFound {
				continue
			}
			state := cookieOf2517(r.header, "site_state")
			loc := r.header.Get("Location")
			if state == "" || !strings.Contains(loc, "/sites/handoff") {
				t.Errorf("%s %s did not start the sign-in: %v", method, path, r.header)
			}
			if state != "" && strings.Contains(loc, state) {
				t.Errorf("%s %s: the raw state is in the address the browser is sent to", method, path)
			}
			if r.header.Get("Referrer-Policy") != "no-referrer" {
				t.Errorf("%s %s: Referrer-Policy is %q", method, path, r.header.Get("Referrer-Policy"))
			}
		}
	}
}

func TestIssue2579_TheSignInTargetStaysOnTheSite(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	s.publish(t, keyEngineer2559, "", "target2579")
	cases := []struct{ name, target, wantPath string }{
		{"a path on the site is kept", "/about/a.css?x=1", "/about/a.css?x=1"},
		{"a protocol-relative request line", "//evil.example/", "/"},
		{"the sign-in route, absolute", "/_signin?path=https%3A%2F%2Fevil.example%2F", "/"},
		{"the sign-in route, protocol-relative", "/_signin?path=%2F%2Fevil.example%2F", "/"},
		{"the sign-in route, backslash", "/_signin?path=%2F%5Cevil.example", "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := onHost(t, s.host(), http.MethodGet, tc.target, nil, "")
			if r.status != http.StatusFound {
				t.Fatalf("%d %s", r.status, r.body)
			}
			loc, err := url.Parse(r.header.Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			if got := loc.Query().Get("path"); got != tc.wantPath {
				t.Errorf("the target is %q, want %q", got, tc.wantPath)
			}
		})
	}
}
