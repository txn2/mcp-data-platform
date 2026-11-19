//go:build integration

package acceptance

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

// Issue #2718: an external person finds the site environments shared with
// them. GET /api/v1/portal/shared-sites lists each environment a grant to
// their email or persona opens, with its address, and nothing else of the
// site: not its other environments and not who else it is shared with. The
// Sites area stays closed to them, and the persona editor reports that the
// external limits turned Sites off.
//
// Wire forms: GET /api/v1/portal/shared-sites takes no parameters; the site's
// access is set with PUT /api/v1/portal/sites/{site}/access and a JSON object
// {persona|email, level, environments}, environments a list of strings, the
// one form it admits; the persona is created with a JSON object whose portal
// is an object of booleans.

const other2718 = "other2718@example.com"

// TestIssue2718_AnExternalPersonFindsOnlyTheEnvironmentSharedWithThem: a
// site with dev and prod, dev shared with an external persona and prod with
// someone else. The persona's person is listed dev alone, with its address,
// and the response names neither prod, the other grantee nor who granted it.
func TestIssue2718_AnExternalPersonFindsOnlyTheEnvironmentSharedWithThem(t *testing.T) {
	admin := connect(t)
	p := newPersona2290(t, admin, "sites-2718", map[string]any{"external": true, "portal": map[string]any{"sites": true}})
	person := newPerson2290(t, admin, p, "sites")
	s := newSite2604(t, keyAdmin2604, "acc2718-")
	s.share(t, keyAdmin2604, map[string]any{"persona": p.name, "level": "view", "environments": []string{"dev"}})
	s.share(t, keyAdmin2604, map[string]any{"email": other2718, "level": "view", "environments": []string{"prod"}})

	status, body := person.do(t, http.MethodGet, "/api/v1/portal/shared-sites", nil)
	if status != http.StatusOK {
		t.Fatalf("GET shared-sites as an external person answered %d %v; want 200", status, body)
	}
	envs, _ := body["environments"].([]any)
	var mine []map[string]any
	for _, e := range envs {
		m, _ := e.(map[string]any)
		if m["site"] == s.name {
			mine = append(mine, m)
		}
	}
	if len(mine) != 1 || mine[0]["environment"] != "dev" {
		t.Fatalf("the site's environments listed for the person are %v; want dev alone", mine)
	}
	if addr, _ := mine[0]["address"].(string); !strings.Contains(addr, s.name+"--dev.") {
		t.Errorf("dev's address reads %q; want its address on the pages domain", addr)
	}
	if mine[0]["online"] != true {
		t.Errorf("dev reads online %v; want true", mine[0]["online"])
	}
	raw := asJSON(body)
	for _, leak := range []string{`"prod"`, other2718, "granted_by", "admin@example.com"} {
		if strings.Contains(raw, leak) {
			t.Errorf("the shared-sites answer holds %q: %s", leak, firstRunes(raw, 600))
		}
	}

	if status, _ := person.do(t, http.MethodGet, "/api/v1/portal/sites", nil); status != http.StatusForbidden {
		t.Errorf("the Sites list answered an external person %d; want 403, the area stays closed", status)
	}

	status, view := admin.rest(http.MethodGet, "/api/v1/admin/personas/"+p.name, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the persona: %d %v", status, view)
	}
	capped := toStrings(view["capped_by_external"])
	if !slices.Contains(capped, "sites") {
		t.Errorf("capped_by_external reads %v; want sites, which the persona stores on and External turns off", capped)
	}
}

// TestIssue2718_SomeoneWithNoGrantGetsAnEmptyList: a person whose persona
// and email no grant names gets [], never null and never another person's
// environments.
func TestIssue2718_SomeoneWithNoGrantGetsAnEmptyList(t *testing.T) {
	admin := connect(t)
	p := newPersona2290(t, admin, "none-2718", map[string]any{"external": true})
	person := newPerson2290(t, admin, p, "none")
	s := newSite2604(t, keyAdmin2604, "acc2718n-")
	s.share(t, keyAdmin2604, map[string]any{"email": other2718, "level": "view", "environments": []string{"dev"}})

	status, body := person.do(t, http.MethodGet, "/api/v1/portal/shared-sites", nil)
	if status != http.StatusOK {
		t.Fatalf("GET shared-sites answered %d %v", status, body)
	}
	envs, ok := body["environments"].([]any)
	if !ok {
		t.Fatalf("environments is %v; want a list, never null", body["environments"])
	}
	for _, e := range envs {
		if m, _ := e.(map[string]any); m["site"] == s.name {
			t.Errorf("a site shared with someone else is listed for a person with no grant: %v", m)
		}
	}
}

// toStrings is a JSON list of strings.
func toStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
