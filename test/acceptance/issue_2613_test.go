//go:build integration

package acceptance

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2613: the portal's application routes and manage_application
// read the same site service. A site environment is listed among the parts a
// person may add, added from the portal, and read back on the application's
// page as the part it is, as manage_application reads it.
//
// Wire forms: POST /api/v1/portal/applications/{app}/parts takes a JSON object
// whose kind ("site") and id ("<site>/<environment>") are strings; GET
// .../candidates takes kind and q as query strings. manage_application takes
// command, application, kind and part_id as strings. A site is created with
// POST /api/v1/portal/sites and a JSON object whose name and description are
// strings.

// sitePart2613 is the part id of a site environment.
func sitePart2613(site, env string) string { return site + "/" + env }

// candidates2613 lists the site candidates the holder of c may add to app,
// by id.
func candidates2613(t *testing.T, c *client, app, query string) map[string]map[string]any {
	t.Helper()
	code, out := c.rest(http.MethodGet, apps2527+app+"/candidates?kind=site&q="+url.QueryEscape(query), http.NoBody)
	if code != http.StatusOK {
		t.Fatalf("the candidates route answered %d: %v", code, out)
	}
	found := map[string]map[string]any{}
	list, _ := out["candidates"].([]any)
	for _, raw := range list {
		m, _ := raw.(map[string]any)
		if m["kind"] != "site" {
			t.Errorf("kind=site listed a %v", m["kind"])
		}
		id, _ := m["id"].(string)
		found[id] = m
	}
	return found
}

func TestIssue2613_APortalApplicationTakesASiteEnvironment(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	stamp := unique1579()
	s := newSite2604(t, keyAdmin2604, "acc2613-")
	app := "acc2613-shop-" + stamp
	app2527(t, admin, app)

	found := candidates2613(t, admin, app, s.name)
	dev, ok := found[sitePart2613(s.name, "dev")]
	if !ok {
		t.Fatalf("the site's dev environment is not among the candidates: %v", found)
	}
	if dev["name"] != sitePart2613(s.name, "dev") || dev["application"] != nil {
		t.Errorf("the candidate reads %v", dev)
	}

	status, out := admin.rest(http.MethodPost, apps2527+app+"/parts", jsonBody(t, map[string]any{"kind": "site", "id": sitePart2613(s.name, "dev")}))
	if status != http.StatusOK {
		t.Fatalf("the portal's add part answered %d: %v", status, out)
	}
	if strings.Contains(asJSON(out), "this deployment has no sites") {
		t.Fatalf("the portal refused a site part as if the deployment had none: %v", out)
	}

	status, detail := admin.rest(http.MethodGet, apps2527+app, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("the application page answered %d: %v", status, detail)
	}
	page := asJSON(detail)
	if !strings.Contains(page, `"kind":"site"`) || !strings.Contains(page, sitePart2613(s.name, "dev")) {
		t.Errorf("the application page lists no site part: %v", detail["parts"])
	}
	if strings.Contains(page, "cannot be read") {
		t.Errorf("the application page reads the site part as a problem: %s", page)
	}

	got := admin.application(map[string]any{"command": "get", "application": app})
	if !strings.Contains(asJSON(got), sitePart2613(s.name, "dev")) {
		t.Errorf("manage_application does not read the part the portal added: %v", got)
	}

	after := candidates2613(t, admin, app, s.name)
	if _, still := after[sitePart2613(s.name, "dev")]; still {
		t.Errorf("a part of this application is still listed as a candidate: %v", after)
	}
}

// A person who is not an administrator is offered the environments of their
// own sites and no one else's, by the rule adding one applies.
func TestIssue2613_APersonIsOfferedTheirOwnSites(t *testing.T) {
	owner := connectAs(t, keyOwner2604)
	stamp := unique1579()
	mine := newSite2604(t, keyOwner2604, "acc2613-mine-")
	theirs := newSite2604(t, keyAdmin2604, "acc2613-theirs-")
	app := "acc2613-own-" + stamp
	app2527(t, owner, app)

	found := candidates2613(t, owner, app, "acc2613-")
	if _, ok := found[sitePart2613(mine.name, "dev")]; !ok {
		t.Errorf("their own site's dev is not offered: %v", found)
	}
	if _, ok := found[sitePart2613(theirs.name, "dev")]; ok {
		t.Errorf("another person's site is offered: %v", found)
	}

	status, out := owner.rest(http.MethodPost, apps2527+app+"/parts", jsonBody(t, map[string]any{"kind": "site", "id": sitePart2613(mine.name, "dev")}))
	if status != http.StatusOK {
		t.Errorf("adding their own site from the portal answered %d: %v", status, out)
	}
	status, out = owner.rest(http.MethodPost, apps2527+app+"/parts", jsonBody(t, map[string]any{"kind": "site", "id": sitePart2613(theirs.name, "dev")}))
	if status != http.StatusForbidden {
		t.Errorf("adding another person's site from the portal answered %d: %v", status, out)
	}
}
