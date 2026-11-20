//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

// Acceptance for #2735: a site that an application holds as one of its parts
// names that application, with the site's environments it uses, on the site's
// page; a site in no application names none, as an empty list.
//
// Wire forms: manage_site create and manage_application create, add_environment
// and add_part take their arguments as the literal tools/call params (kind and
// part_id as strings); GET /api/v1/portal/sites/{site} takes no body.

// site2735 reads a site as the portal's site page does.
func site2735(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/portal/sites/"+url.PathEscape(name), http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET site %s: %d %v", name, status, body)
	}
	return body
}

// TestIssue2735_ASiteNamesTheApplicationThatUsesIt: the site read names the
// application holding the site's prod environment, with its id and the
// environment, and that environment names it too.
func TestIssue2735_ASiteNamesTheApplicationThatUsesIt(t *testing.T) {
	admin := connect(t)
	site := "acc2735-" + unique1579()
	admin.call("manage_site", map[string]any{"command": "create", "name": site, "description": "Acceptance 2735"})
	t.Cleanup(func() { _, _, _ = connect(t).callRaw("manage_site", map[string]any{"command": "delete", "site": site}) })
	app := "acc2735-app-" + unique1579()
	created := admin.call("manage_application", map[string]any{"command": "create", "name": app, "description": "uses a site"})
	t.Cleanup(func() {
		_, _, _ = connect(t).callRaw("manage_application", map[string]any{"command": "delete", "application": app})
	})
	_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "add_environment", "application": app, "environment": "prod"})
	admin.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "site", "part_id": site + "/prod", "release_mode": "coupled"})

	body := site2735(t, admin, site)
	apps, _ := body["applications"].([]any)
	if len(apps) != 1 {
		t.Fatalf("applications = %v; want the one application", body["applications"])
	}
	a, _ := apps[0].(map[string]any)
	if a["name"] != app || a["id"] == "" || fmt.Sprint(a["environments"]) != "[prod]" {
		t.Fatalf("application = %v; want %s holding prod", a, app)
	}
	if id := fmt.Sprint(created["id"]); id != "" && id != "<nil>" && a["id"] != id {
		t.Errorf("the application's id is %v; manage_application created %s", a["id"], id)
	}
	envs, _ := body["environments"].([]any)
	for _, e := range envs {
		env, _ := e.(map[string]any)
		named, _ := env["application"].(map[string]any)
		switch env["name"] {
		case "prod":
			if named == nil || named["name"] != app {
				t.Errorf("prod names %v; want %s", env["application"], app)
			}
		default:
			if named != nil {
				t.Errorf("%v names %v, which holds only prod", env["name"], named)
			}
		}
	}
}

// TestIssue2735_ASiteInNoApplicationNamesNone: a site no application uses
// answers applications as an empty list, never null.
func TestIssue2735_ASiteInNoApplicationNamesNone(t *testing.T) {
	admin := connect(t)
	site := "acc2735-alone-" + unique1579()
	admin.call("manage_site", map[string]any{"command": "create", "name": site, "description": "Acceptance 2735"})
	t.Cleanup(func() { _, _, _ = connect(t).callRaw("manage_site", map[string]any{"command": "delete", "site": site}) })
	status, raw := admin.restText("/api/v1/portal/sites/" + url.PathEscape(site))
	if status != http.StatusOK {
		t.Fatalf("GET site: %d %.300s", status, raw)
	}
	body := site2735(t, admin, site)
	apps, ok := body["applications"].([]any)
	if !ok || len(apps) != 0 {
		t.Fatalf("applications = %#v; want []", body["applications"])
	}
}
