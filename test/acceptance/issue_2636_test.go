//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2636: the name a person types for a new site becomes the
// address name on the server, which keeps the one copy of the rule and answers
// the New site dialog with the name, both addresses, the room left for an
// environment name and the reason a name is refused.
//
// Wire forms: POST /api/v1/portal/sites/check-name and POST /api/v1/portal/sites
// take a JSON object whose name is a string; manage_site create takes command
// and name as literal tools/call strings.

// TestIssue2636_TypingMyWebsiteShowsItsAddress: "My Website" becomes
// my-website with its dev and prod addresses, and Create site creates
// my-website.
func TestIssue2636_TypingMyWebsiteShowsItsAddress(t *testing.T) {
	typed := "My Website " + unique1579()
	status, got := rest2559(t, keyOwner2604, http.MethodPost, portal2559+"/check-name", map[string]any{"name": typed})
	want2559(t, "checking the name", status, http.StatusOK, got)
	name, _ := got["name"].(string)
	if !strings.HasPrefix(name, "my-website-") || got["refusal"] != nil {
		t.Fatalf("%q became %v (refusal %v)", typed, got["name"], got["refusal"])
	}
	addresses, _ := got["addresses"].(map[string]any)
	if addresses["dev"] != "https://"+name+"--dev."+pagesDomain2517 && !strings.Contains(asString(addresses["dev"]), name+"--dev."+pagesDomain2517) {
		t.Errorf("dev's address: %v", addresses)
	}
	if !strings.Contains(asString(addresses["prod"]), "//"+name+"."+pagesDomain2517) {
		t.Errorf("prod's address: %v", addresses)
	}
	if room, _ := got["environment_name_room"].(float64); room < 4 {
		t.Errorf("no room for an environment name: %v", got["environment_name_room"])
	}

	status, created := rest2559(t, keyOwner2604, http.MethodPost, portal2559, map[string]any{"name": typed})
	want2559(t, "creating the site", status, http.StatusCreated, created)
	t.Cleanup(func() { _, _ = rest2559(t, keyAdmin2604, http.MethodDelete, portal2559+"/"+name, nil) })
	if created["name"] != name {
		t.Errorf("Create site made %v, not %s", created["name"], name)
	}
}

// TestIssue2636_ARefusedNameSaysWhy: a name nothing can be made of, one past
// the length limit and one already taken are each refused with the server's
// reason, and manage_site create applies the same rule.
func TestIssue2636_ARefusedNameSaysWhy(t *testing.T) {
	for typed, want := range map[string]string{
		"!!!":                   "at least one letter",
		strings.Repeat("a", 60): "at most 57",
		"ab":                    "two characters",
	} {
		status, got := rest2559(t, keyOwner2604, http.MethodPost, portal2559+"/check-name", map[string]any{"name": typed})
		want2559(t, "checking "+typed, status, http.StatusOK, got)
		if !strings.Contains(asString(got["refusal"]), want) {
			t.Errorf("%q: refusal %v, want %q", typed, got["refusal"], want)
		}
	}
	s := newSite2604(t, keyOwner2604, "acc2636-")
	status, got := rest2559(t, keyOwner2604, http.MethodPost, portal2559+"/check-name", map[string]any{"name": s.name})
	want2559(t, "checking a taken name", status, http.StatusOK, got)
	if !strings.Contains(asString(got["refusal"]), "exists") {
		t.Errorf("a taken name: %v", got["refusal"])
	}
	_, text, _ := connectAs(t, keyOwner2604).callRaw("manage_site", map[string]any{"command": "create", "name": strings.Repeat("b", 60)})
	if !strings.Contains(text, "at most 57") {
		t.Errorf("manage_site create of a name past the limit: %s", text)
	}
}
