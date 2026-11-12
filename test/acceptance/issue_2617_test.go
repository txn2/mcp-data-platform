//go:build integration

package acceptance

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// Acceptance for #2617: a new site starts with dev and prod (prod holding the
// main address), an environment is placed after another between them when it
// is created and can be moved, the order is stored and every list follows it, and a release
// moves one environment at a time, from the environment immediately before,
// with the release that one serves. An application release pins each site part
// at what the environment before it serves.
//
// Wire forms: POST /api/v1/portal/sites takes a JSON object whose name and
// description are strings; PUT .../environments/{name} takes after as a
// string; POST .../promote takes from and to as strings and release as a
// number. manage_site takes command, name, site, environment, after, from and
// to as strings and release as a number. manage_application takes
// command, application, kind and part_id as strings. Every field is sent as
// its schema's one type, and a field left out is sent absent.

// order2617 is a site's environments by name, as an answer lists them.
func order2617(t *testing.T, site map[string]any) []string {
	t.Helper()
	envs, _ := site["environments"].([]any)
	out := make([]string, 0, len(envs))
	for _, e := range envs {
		m, _ := e.(map[string]any)
		name, _ := m["name"].(string)
		out = append(out, name)
	}
	return out
}

// get2617 is manage_site get's site.
func get2617(t *testing.T, key, name string) map[string]any {
	t.Helper()
	got := connectAs(t, key).call("manage_site", map[string]any{"command": "get", "site": name})
	s, _ := got["site"].(map[string]any)
	if s == nil {
		t.Fatalf("manage_site get answered no site: %v", got)
	}
	return s
}

// promoteRefused2617 asks for a promote through the portal and through
// manage_site and returns both refusals.
func promoteRefused2617(t *testing.T, s *site2604, body map[string]any) (rest, tool string) {
	t.Helper()
	status, got := s.rest(t, s.key, http.MethodPost, "/promote", body)
	if status == http.StatusOK {
		t.Fatalf("the portal promoted %v: %v", body, got["_raw"])
	}
	rest, _ = got["detail"].(string)
	args := map[string]any{"command": "promote", "site": s.name}
	for k, v := range body {
		args[k] = v
	}
	res, text, err := connectAs(t, s.key).callRaw("manage_site", args)
	if err != nil || !res.IsError {
		t.Fatalf("manage_site promoted %v: %v %s", body, err, text)
	}
	return rest, text
}

// TestIssue2617_ANewSiteStartsWithDevAndProd: creating d4l gives dev at
// d4l--dev and prod at d4l--prod, with prod serving d4l's main address, and no
// domain added or asked for, through the portal API and manage_site create.
func TestIssue2617_ANewSiteStartsWithDevAndProd(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2617-new-")
	status, view := s.rest(t, s.key, http.MethodGet, "", nil)
	want2559(t, "reading the site", status, http.StatusOK, view)
	if got := order2617(t, view); !slices.Equal(got, []string{"dev", "prod"}) {
		t.Errorf("a new site's environments are %v, want dev and prod", got)
	}
	if view["main_environment"] != "prod" {
		t.Errorf("the main address is given to %v, want prod", view["main_environment"])
	}
	pages, _ := view["pages_addresses"].(map[string]any)
	if got := hostname2617(asString(pages["dev"])); got != s.host("dev") {
		t.Errorf("dev answers at %v, want %s", pages["dev"], s.host("dev"))
	}
	if got := hostname2617(asString(pages["prod"])); got != s.host("") {
		t.Errorf("prod answers at %v, want the main address %s", pages["prod"], s.host(""))
	}
	if domains, _ := view["domains"].([]any); len(domains) != 0 {
		t.Errorf("a new site has domains: %v", domains)
	}

	name := "acc2617-tool-" + unique1579()
	owner := connectAs(t, keyOwner2604)
	t.Cleanup(func() {
		_, _, _ = connectAs(t, keyAdmin2604).callRaw("manage_site", map[string]any{"command": "delete", "site": name})
	})
	created := owner.call("manage_site", map[string]any{"command": "create", "name": name, "description": "Acceptance 2617"})
	if got := order2617(t, created); !slices.Equal(got, []string{"dev", "prod"}) {
		t.Errorf("manage_site create: environments %v, want dev and prod", got)
	}
	if created["main_environment"] != "prod" {
		t.Errorf("manage_site create: the main address is given to %v", created["main_environment"])
	}
}

// TestIssue2617_EnvironmentsArePlacedAndMoved: on dev -> prod a new stage goes
// between them; a new test is offered after dev or after stage and, placed
// after dev, gives dev, test, stage, prod in manage_site get, the portal view
// and the Sites list; dev and prod cannot be moved and nothing goes after
// prod; a moved environment keeps its place, read from each replica.
func TestIssue2617_EnvironmentsArePlacedAndMoved(t *testing.T) {
	s := newSite2604(t, keyOwner2604, "acc2617-order-")
	s.setEnv(t, s.key, "stage", map[string]any{})
	if got := order2617(t, get2617(t, s.key, s.name)); !slices.Equal(got, []string{"dev", "stage", "prod"}) {
		t.Fatalf("stage went to %v", got)
	}

	status, got := s.rest(t, s.key, http.MethodPut, "/environments/test", map[string]any{})
	if status != http.StatusUnprocessableEntity || !strings.Contains(asString(got["detail"]), "say which environment test goes after: dev or stage") {
		t.Errorf("a new environment with two places was not asked where: %d %v", status, got["_raw"])
	}
	s.setEnv(t, s.key, "test", map[string]any{"after": "dev"})
	want := []string{"dev", "test", "stage", "prod"}
	if got := order2617(t, get2617(t, s.key, s.name)); !slices.Equal(got, want) {
		t.Errorf("manage_site get lists %v, want %v", got, want)
	}
	_, view := s.rest(t, s.key, http.MethodGet, "", nil)
	if got := order2617(t, view); !slices.Equal(got, want) {
		t.Errorf("the portal view lists %v, want %v", got, want)
	}
	status, list := rest2559(t, s.key, http.MethodGet, portal2559, nil)
	want2559(t, "listing sites", status, http.StatusOK, list)
	sites, _ := list["sites"].([]any)
	found := false
	for _, item := range sites {
		m, _ := item.(map[string]any)
		if m["name"] == s.name {
			found = true
			if got := order2617(t, m); !slices.Equal(got, want) {
				t.Errorf("the Sites list lists %v, want %v", got, want)
			}
		}
	}
	if !found {
		t.Errorf("the Sites list does not hold %s", s.name)
	}

	for _, fixed := range []struct{ env, place, value, want string }{
		{"prod", "after", "dev", "prod is always last, so it cannot be moved"},
		{"dev", "after", "stage", "dev is always first, so it cannot be moved"},
		{"test", "after", "prod", "prod is always last: nothing goes after it"},
	} {
		status, got := s.rest(t, s.key, http.MethodPut, "/environments/"+fixed.env, map[string]any{fixed.place: fixed.value})
		if status != http.StatusUnprocessableEntity || !strings.Contains(asString(got["detail"]), fixed.want) {
			t.Errorf("%s %s %s: %d %v", fixed.env, fixed.place, fixed.value, status, got["_raw"])
		}
		_, text, _ := connectAs(t, s.key).callRaw("manage_site", map[string]any{"command": "set_environment", "site": s.name, "environment": fixed.env, fixed.place: fixed.value})
		if !strings.Contains(text, fixed.want) {
			t.Errorf("manage_site %s %s %s: %s", fixed.env, fixed.place, fixed.value, text)
		}
	}

	connectAs(t, s.key).call("manage_site", map[string]any{"command": "set_environment", "site": s.name, "environment": "test", "after": "stage"})
	moved := []string{"dev", "stage", "test", "prod"}
	forEachReplica(t, func(t *testing.T, c *client) {
		got := c.call("manage_site", map[string]any{"command": "get", "site": s.name})
		site, _ := got["site"].(map[string]any)
		if order := order2617(t, site); !slices.Equal(order, moved) {
			t.Errorf("this replica reads %v, want %v", order, moved)
		}
	})
}

// TestIssue2617_PromotionGoesOneStep: with dev -> stage -> prod, promoting prod
// from dev is refused through the portal and manage_site, naming stage;
// promoting prod with a release stage does not serve is refused; promoting prod
// from stage succeeds.
func TestIssue2617_PromotionGoesOneStep(t *testing.T) {
	s := newSite2604(t, keyAdmin2604, "acc2617-promote-")
	s.setEnv(t, s.key, "stage", map[string]any{})
	s.publish(t, s.key, "one")
	s.promote(t, "dev", "stage")
	s.publish(t, s.key, "two")
	stage := s.release2617(t, "stage")
	dev := s.release2617(t, "dev")
	if stage == dev {
		t.Fatalf("dev and stage both serve %d", dev)
	}

	rest, tool := promoteRefused2617(t, s, map[string]any{"from": "dev", "to": "prod"})
	const fromStage = "prod takes releases only from stage, the environment before it: promote it from stage"
	if rest != fromStage || !strings.Contains(tool, fromStage) {
		t.Errorf("promoting prod from dev:\n portal: %q\n tool:   %q", rest, tool)
	}
	rest, tool = promoteRefused2617(t, s, map[string]any{"to": "prod", "release": dev})
	if !strings.Contains(rest, "prod takes only the release stage serves") || !strings.Contains(tool, "prod takes only the release stage serves") {
		t.Errorf("promoting prod a release stage does not serve:\n portal: %q\n tool:   %q", rest, tool)
	}
	if got := s.release2617(t, "prod"); got != 0 {
		t.Errorf("prod moved to %d on a refused promote", got)
	}

	s.promote(t, "stage", "prod")
	if got := s.release2617(t, "prod"); got != stage {
		t.Errorf("prod serves %d, want stage's %d", got, stage)
	}
	got := connectAs(t, s.key).call("manage_site", map[string]any{"command": "promote", "site": s.name, "to": "stage"})
	if got["status"] != "promoted" || asInt(got["release"]) != dev {
		t.Errorf("manage_site promote to stage, from left out: %v", got)
	}
}

// release2617 is the release an environment serves, 0 for none.
func (s *site2604) release2617(t *testing.T, env string) int {
	t.Helper()
	for _, e := range get2617(t, s.key, s.name)["environments"].([]any) {
		m, _ := e.(map[string]any)
		if m["name"] == env {
			return asInt(m["release"])
		}
	}
	t.Fatalf("%s has no environment %s", s.name, env)
	return 0
}

// TestIssue2617_AnApplicationReleaseMovesEachSitePartOneStep: an application
// holding a site's stage and prod pins stage at what dev serves and prod at
// what stage served before the release, as promoting each would.
func TestIssue2617_AnApplicationReleaseMovesEachSitePartOneStep(t *testing.T) {
	admin := connectAs(t, keyAdmin2604)
	s := newSite2604(t, keyAdmin2604, "acc2617-app-")
	s.setEnv(t, s.key, "stage", map[string]any{})
	s.publish(t, s.key, "one")
	s.promote(t, "dev", "stage")
	s.publish(t, s.key, "two")
	stageBefore, devNow := s.release2617(t, "stage"), s.release2617(t, "dev")

	app := app2539(t, admin, admin, "2617")
	for _, env := range []string{"stage", "prod"} {
		admin.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "site", "part_id": sitePart2613(s.name, env)})
	}
	admin.call("manage_application", map[string]any{"command": "release", "application": app, "note": "acceptance 2617"})
	if got := s.release2617(t, "stage"); got != devNow {
		t.Errorf("stage serves %d after the release, want dev's %d", got, devNow)
	}
	if got := s.release2617(t, "prod"); got != stageBefore {
		t.Errorf("prod serves %d after the release, want %d, what stage served before it", got, stageBefore)
	}
}

// hostname2617 is an address's host name, without scheme or port.
func hostname2617(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// asInt is a JSON number as an int, 0 for anything else.
func asInt(v any) int {
	f, _ := v.(float64)
	return int(f)
}
