//go:build integration

package acceptance

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #2587: the whole row of the Sites list opens the site, and one Share
// dialog offers Can view and Can manage. Both are what a person sees and
// clicks, so the criteria run in Chromium (ui/e2e/sites-list) against the dev
// stack, signed in as a site's owner.
//
// Wire forms: the browser's own clicks, key presses and navigations. The spec
// creates and deletes its site through the portal API, whose bodies are JSON
// objects of strings; no parameter admits a second form.

// browser2587 runs the Sites list's browser cases whose names match grep.
func browser2587(t *testing.T, grep string) {
	t.Helper()
	root, _ := filepath.Abs("../..")
	cmd := exec.Command("npx", "playwright", "test", "--config", "e2e/sites-list/playwright.config.ts", "--grep", grep)
	cmd.Dir = filepath.Join(root, "ui")
	cmd.Env = append(os.Environ(), "SITES_BASE_URL="+baseURL(), "SITES_API_KEY="+keyOwner2604)
	out, err := cmd.CombinedOutput()
	t.Logf("playwright against %s:\n%s", baseURL(), out)
	if err != nil {
		t.Fatalf("the browser case %q failed: %v", grep, err)
	}
}

// TestIssue2587_TheWholeRowOpensTheSite is criteria 1 and 2: a click anywhere
// on a row but its address, or Enter on the focused row, opens the site's
// page, and the address still opens the live site in a new tab.
func TestIssue2587_TheWholeRowOpensTheSite(t *testing.T) {
	browser2587(t, "click anywhere on a row|Enter on the focused row|the address opens the live site")
}

// TestIssue2587_ShareOffersTheOwnerCanManage is criterion 3: the owner's
// Share offers Can manage beside Can view.
func TestIssue2587_ShareOffersTheOwnerCanManage(t *testing.T) {
	browser2587(t, "Share offers the owner Can manage")
}

// TestIssue2587_TheServerStillDecidesWhoMayGrantManage is criteria 5 and 6:
// someone who manages the site without owning it is refused manage, and an
// external person cannot be given it, whatever a dialog offers.
func TestIssue2587_TheServerStillDecidesWhoMayGrantManage(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2587-")
	s.share(t, keyOwner2604, map[string]any{"email": emailPeer2604, "level": "manage"})

	status, got := s.rest(t, keyPeer2604, http.MethodPut, "/access", map[string]any{"email": "someone@example.com", "level": "manage"})
	if status == http.StatusOK {
		t.Errorf("someone who manages without owning granted manage: %v", got["_raw"])
	}

	// The external person signs in once, so the directory records their persona.
	_ = browserSignIn(t, baseURL(), emailKCClient, passKCClient)
	status, got = s.rest(t, keyOwner2604, http.MethodPut, "/access", map[string]any{"email": emailKCClient, "level": "manage"})
	raw, _ := got["_raw"].(string)
	if status == http.StatusOK || !strings.Contains(strings.ToLower(raw), "external") {
		t.Errorf("an external person given manage: %d %s", status, raw)
	}
	status, got = s.rest(t, keyOwner2604, http.MethodGet, "/access", nil)
	want2559(t, "reading access", status, http.StatusOK, got)
	if strings.Contains(got["_raw"].(string), emailKCClient) {
		t.Error("a refused grant was written")
	}
}

// TestIssue2587_NothingOverflowsAtPhoneWidth is criterion 7.
func TestIssue2587_NothingOverflowsAtPhoneWidth(t *testing.T) {
	browser2587(t, "at 375px")
}
