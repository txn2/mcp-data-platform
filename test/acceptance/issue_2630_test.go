//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2630: the environments between dev and prod can be renamed
// and removed. A rename moves every reference to the name in one transaction;
// a removal is refused while something is attached and never widens a share.
// The criteria use a site with dev, stage, uat and prod.
//
// Wire forms: PUT /api/v1/portal/sites/{site}/environments/{name} takes
// rename_to as a string; manage_site set_environment takes rename_to and
// remove_environment takes environment, each as a literal tools/call string
// param; DELETE /api/v1/portal/sites/{site}/environments/{name} has no body.

// fourEnvironments2630 is a site with dev, stage, uat and prod, uat serving
// "uat".
func fourEnvironments2630(t *testing.T) *site2604 {
	t.Helper()
	s := newSite2604(t, keyOwner2604, "acc2630-")
	s.setEnv(t, s.key, "stage", map[string]any{"after": "dev"})
	s.setEnv(t, s.key, "uat", map[string]any{"after": "stage"})
	s.publish(t, s.key, "uat")
	s.promote(t, "dev", "stage")
	s.promote(t, "stage", "uat")
	return s
}

// order2630 is the site's environments in order, as its record answers.
func order2630(t *testing.T, s *site2604) []string {
	t.Helper()
	status, got := s.rest(t, s.key, http.MethodGet, "", nil)
	want2559(t, "reading the site", status, http.StatusOK, got)
	var out []string
	envs, _ := got["environments"].([]any)
	for _, e := range envs {
		m, _ := e.(map[string]any)
		out = append(out, fmt.Sprint(m["name"]))
	}
	return out
}

// TestIssue2630_RenameUatToQa: renaming uat to qa through the portal keeps its
// place and release, moves its domain, a share that named it and its address;
// the old address answers nothing published.
func TestIssue2630_RenameUatToQa(t *testing.T) {
	s := fourEnvironments2630(t)
	domain := "uat." + s.name + ".example.test"
	status, got := s.rest(t, keyAdmin2604, http.MethodPost, "/domains", map[string]any{"hostname": domain, "environment": "uat"})
	want2559(t, "adding uat's domain", status, http.StatusCreated, got)
	s.share(t, s.key, map[string]any{"persona": personaAnalyst2604, "level": "view", "environments": []string{"uat"}})

	s.setEnv(t, s.key, "uat", map[string]any{"rename_to": "qa"})
	if got := strings.Join(order2630(t, s), ","); got != "dev,stage,qa,prod" {
		t.Errorf("the order after the rename is %s", got)
	}
	tool := connectAs(t, s.key).call("manage_site", map[string]any{"command": "get", "site": s.name})
	if !strings.Contains(fmt.Sprint(tool["site"]), "qa") {
		t.Errorf("manage_site get does not show qa: %v", tool["site"])
	}
	served2518For(t, s, "qa", "uat")
	pollUntil2518(t, func() (pageResponse, bool) {
		r := onHost(t, s.host("uat"), http.MethodGet, "/", nil, "")
		return r, strings.Contains(r.body, "Nothing is published here")
	})
	member := signIn2604(t, s.host("qa"), emailKCAnalyst, passKCAnalyst)
	opens2604(t, s.host("qa"), member, "uat")
	status, domains := s.rest(t, s.key, http.MethodGet, "", nil)
	want2559(t, "reading the site", status, http.StatusOK, domains)
	if !strings.Contains(fmt.Sprint(domains["domains"]), "qa") {
		t.Errorf("the domain did not move to qa: %v", domains["domains"])
	}
}

// TestIssue2630_RenameThroughTheToolAndItsRefusals: manage_site renames qa to
// review; renaming dev or prod, and renaming to dev, prod, default, a name in
// use, bad--name or a name past the label limit are refused naming the rule.
func TestIssue2630_RenameThroughTheToolAndItsRefusals(t *testing.T) {
	s := fourEnvironments2630(t)
	agent := connectAs(t, s.key)
	agent.call("manage_site", map[string]any{"command": "set_environment", "site": s.name, "environment": "uat", "rename_to": "review"})
	if got := strings.Join(order2630(t, s), ","); got != "dev,stage,review,prod" {
		t.Errorf("the order after the tool's rename is %s", got)
	}
	for _, tc := range []struct{ from, to, want string }{
		{"dev", "x", "cannot be renamed"},
		{"prod", "x", "cannot be renamed"},
		{"review", "dev", "the platform keeps"},
		{"review", "prod", "the platform keeps"},
		{"review", "default", "the platform keeps"},
		{"review", "stage", "already has an environment"},
		{"review", "bad--name", "environment name"},
		{"review", strings.Repeat("a", 33), "at most 32"},
	} {
		status, got := s.rest(t, s.key, http.MethodPut, "/environments/"+tc.from, map[string]any{"rename_to": tc.to})
		if status != http.StatusUnprocessableEntity || !strings.Contains(asString(got["detail"]), tc.want) {
			t.Errorf("rename %s to %s: %d %v", tc.from, tc.to, status, got["_raw"])
		}
	}
}

// TestIssue2630_RemoveStage: removing stage (no domain) takes it out of every
// list, deletes a share that named only it and narrows one that named it and
// another; uat then takes releases from dev.
func TestIssue2630_RemoveStage(t *testing.T) {
	s := fourEnvironments2630(t)
	s.share(t, s.key, map[string]any{"email": emailPeer2604, "level": "view", "environments": []string{"stage"}})
	s.share(t, s.key, map[string]any{"persona": personaAnalyst2604, "level": "view", "environments": []string{"stage", "uat"}})
	status, got := s.rest(t, s.key, http.MethodDelete, "/environments/stage", nil)
	want2559(t, "removing stage", status, http.StatusOK, got)
	if got := strings.Join(order2630(t, s), ","); got != "dev,uat,prod" {
		t.Errorf("the order after the removal is %s", got)
	}
	status, access := s.rest(t, s.key, http.MethodGet, "/access", nil)
	want2559(t, "reading access", status, http.StatusOK, access)
	raw := fmt.Sprint(access["grants"])
	if strings.Contains(raw, emailPeer2604) {
		t.Errorf("a share that named only stage is still there: %s", raw)
	}
	if !strings.Contains(raw, personaAnalyst2604) || strings.Contains(raw, "stage") {
		t.Errorf("the share naming stage and uat is not narrowed to uat: %s", raw)
	}
	s.publish(t, s.key, "next")
	s.promote(t, "dev", "uat")
}

// TestIssue2630_RemoveRefusals: removing an environment with a domain, the one
// holding the main address, an application part, dev and prod is refused; one
// that requires approval is refused to the owner and removed by an
// administrator, through the portal and manage_site.
func TestIssue2630_RemoveRefusals(t *testing.T) {
	s := fourEnvironments2630(t)
	status, got := s.rest(t, keyAdmin2604, http.MethodPost, "/domains", map[string]any{"hostname": "uat." + s.name + ".example.test", "environment": "uat"})
	want2559(t, "adding uat's domain", status, http.StatusCreated, got)
	for env, want := range map[string]string{"uat": "domain", "dev": "cannot be removed", "prod": "cannot be removed"} {
		if status, got := s.rest(t, s.key, http.MethodDelete, "/environments/"+env, nil); status != http.StatusUnprocessableEntity || !strings.Contains(asString(got["detail"]), want) {
			t.Errorf("removing %s: %d %v", env, status, got["_raw"])
		}
	}
	status, got = s.rest(t, s.key, http.MethodPut, "/main-environment", map[string]any{"environment": "stage"})
	want2559(t, "moving the main address", status, http.StatusOK, got)
	if _, text, _ := connectAs(t, s.key).callRaw("manage_site", map[string]any{"command": "remove_environment", "site": s.name, "environment": "stage"}); !strings.Contains(text, "main address") {
		t.Errorf("removing the main address's environment: %s", text)
	}
	s.rest(t, s.key, http.MethodPut, "/main-environment", map[string]any{"environment": "prod"})
	s.setEnv(t, keyAdmin2604, "stage", map[string]any{"requires_approval": true})
	if status, got := s.rest(t, s.key, http.MethodDelete, "/environments/stage", nil); status != http.StatusForbidden {
		t.Errorf("the owner removed an environment that requires approval: %d %v", status, got["_raw"])
	}
	status, got = s.rest(t, keyAdmin2604, http.MethodDelete, "/environments/stage", nil)
	want2559(t, "an administrator removing it", status, http.StatusOK, got)
}

// TestIssue2630_AnApplicationPartFollowsTheRename: an application part
// <site>/uat becomes <site>/qa when uat is renamed, and an environment that is
// a part is refused removal until the part is removed.
func TestIssue2630_AnApplicationPartFollowsTheRename(t *testing.T) {
	s := fourEnvironments2630(t)
	admin := connectAs(t, keyAdmin2604)
	app := "acc2630app-" + unique1579()
	admin.call("manage_application", map[string]any{"command": "create", "name": app, "description": "holds a site part"})
	t.Cleanup(func() {
		_, _, _ = connectAs(t, keyAdmin2604).callRaw("manage_application", map[string]any{"command": "delete", "application": app})
	})
	admin.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "site", "part_id": s.name + "/uat", "release_mode": "coupled"})
	if status, got := s.rest(t, s.key, http.MethodDelete, "/environments/uat", nil); status != http.StatusUnprocessableEntity || !strings.Contains(asString(got["detail"]), "part of an application") {
		t.Errorf("removing a part: %d %v", status, got["_raw"])
	}
	s.setEnv(t, s.key, "uat", map[string]any{"rename_to": "qa"})
	read := admin.call("manage_application", map[string]any{"command": "get", "application": app})
	raw := fmt.Sprint(read)
	if !strings.Contains(raw, s.name+"/qa") || strings.Contains(raw, s.name+"/uat") {
		t.Errorf("the part did not follow the rename: %s", raw)
	}
}
