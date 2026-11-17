//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
)

// Acceptance for #2679: a site part is linked or coupled. A linked part keeps
// same-origin endpoints and is left alone by an application release and its
// rollback; a release naming it in move_sites moves it one step and its
// rollback returns it. A new part is linked, set_part changes it, and get
// names each site part's mode, served release and compatibility. With a
// contract declared, an application release a linked part's release excludes
// is refused naming the part and range unless forced (recorded), and
// manage_site promote of a release excluding the contract is refused.
//
// Wire forms: manage_application takes command, application, kind, part_id,
// release_mode and note as strings, move_sites as an array of strings,
// contract and contract_version as JSON numbers and force as a JSON boolean;
// manage_site takes command, site, to and files ([{path, content}] of strings);
// every call is sent as literal tools/call params.

// publish2679 publishes one release to dev with an index page and, when given,
// an _application file, and returns the site's dev release.
func publish2679(t *testing.T, admin *client, s *site2604, label, contract string) int {
	t.Helper()
	files := []map[string]any{{"path": "index.html", "content": "<h1>" + label + "</h1>"}}
	if contract != "" {
		files = append(files, map[string]any{"path": "_application", "content": contract})
	}
	admin.call("manage_site", map[string]any{"command": "publish", "site": s.name, "note": label, "files": files})
	return s.release2617(t, "dev")
}

func TestIssue2679_ALinkedPartMovesOnlyWhenNamed(t *testing.T) {
	admin := connectAs(t, keyAdmin2604)
	s := newSite2604(t, keyAdmin2604, "acc2679-l-")
	first := publish2679(t, admin, s, "one", "")
	s.promote(t, "dev", "prod")
	second := publish2679(t, admin, s, "two", "")

	app := app2539(t, admin, admin, "2679l")
	part := sitePart2613(s.name, "prod")
	added := admin.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "site", "part_id": part})
	if !strings.Contains(fmt.Sprint(added["parts"]), "release_mode:linked") {
		t.Errorf("a new site part is not linked: %v", added["parts"])
	}

	admin.call("manage_application", map[string]any{"command": "release", "application": app, "note": "backend only"})
	if got := s.release2617(t, "prod"); got != first {
		t.Errorf("prod serves %d after a release that did not name it, want %d, where it was", got, first)
	}

	admin.call("manage_application", map[string]any{"command": "release", "application": app, "note": "with the front end", "move_sites": []string{part}})
	if got := s.release2617(t, "prod"); got != second {
		t.Errorf("prod serves %d after a release naming it, want dev's %d", got, second)
	}
	admin.call("manage_application", map[string]any{"command": "rollback", "application": app})
	if got := s.release2617(t, "prod"); got != first {
		t.Errorf("prod serves %d after rolling back the release that moved it, want %d", got, first)
	}

	st := admin.call("manage_application", map[string]any{"command": "get", "application": app})
	if !strings.Contains(fmt.Sprint(st["site_parts"]), "release_mode:linked") {
		t.Errorf("get does not name the part's mode: %v", st["site_parts"])
	}
	coupled := admin.call("manage_application", map[string]any{"command": "set_part", "application": app, "part_id": part, "release_mode": "coupled"})
	if !strings.Contains(fmt.Sprint(coupled["parts"]), "release_mode:coupled") {
		t.Errorf("set_part did not make the part coupled: %v", coupled["parts"])
	}
	admin.call("manage_application", map[string]any{"command": "release", "application": app, "note": "coupled now"})
	if got := s.release2617(t, "prod"); got != second {
		t.Errorf("prod serves %d after a release of a coupled part, want dev's %d", got, second)
	}
}

func TestIssue2679_AContractThatBreaksAFrontEndIsRefused(t *testing.T) {
	admin := connectAs(t, keyAdmin2604)
	s := newSite2604(t, keyAdmin2604, "acc2679-c-")
	app := app2539(t, admin, admin, "2679c")
	publish2679(t, admin, s, "v2", fmt.Sprintf(`{"application": %q, "contract": "=2"}`, app))
	s.promote(t, "dev", "prod")
	part := sitePart2613(s.name, "prod")
	admin.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "site", "part_id": part})
	admin.call("manage_application", map[string]any{"command": "update", "application": app, "contract_version": 2})

	res, text, err := admin.callRaw("manage_application", map[string]any{"command": "release", "application": app, "contract": 4})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text, part) || !strings.Contains(text, "=2") {
		t.Fatalf("a release with contract 4 over a front end needing =2 was not refused naming the part and range: %s", text)
	}
	forced := admin.call("manage_application", map[string]any{"command": "release", "application": app, "contract": 4, "force": true})
	if !strings.Contains(fmt.Sprint(forced["changes"]), part) {
		t.Errorf("the forced release does not record the part it broke: %v", forced["changes"])
	}

	// dev now serves a release needing =2, and the application is at 4: a
	// promote of it into the part is refused naming the contract.
	publish2679(t, admin, s, "still-v2", fmt.Sprintf(`{"application": %q, "contract": "=2"}`, app))
	res, text, err = admin.callRaw("manage_site", map[string]any{"command": "promote", "site": s.name, "to": "prod"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text, "contract 4") {
		t.Errorf("promoting a release that excludes the application's contract was not refused naming it: %s", text)
	}
	publish2679(t, admin, s, "v4", fmt.Sprintf(`{"application": %q, "contract": ">=2 <5"}`, app))
	s.promote(t, "dev", "prod")
}
