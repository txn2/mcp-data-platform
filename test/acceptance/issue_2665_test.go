//go:build integration

package acceptance

import (
	"strings"
	"testing"
	"time"
)

// Acceptance for #2665: the application diagram reads a table loaded by
// tx.copy_in inside platform.transaction, and one written by a function
// reached only through a wrapper that returns a lambda, as written: neither
// draws an unwritten_table finding, and each draws a writes link from its
// script. Every table the schema's migrations create is drawn in the schema's
// group, one no script names included.
//
// Wire forms: the diagram route takes the application in its path and version
// as a query string; manage_application and manage_schema take their
// arguments as JSON strings and version as a JSON number; manage_script takes
// command, name, description and source as strings.

// TestIssue2665_CopyInAndWrappedHandlersAreWrites: copy_in and a wrapped
// handler's INSERT are writes, and an unused created table is drawn.
func TestIssue2665_CopyInAndWrappedHandlersAreWrites(t *testing.T) {
	admin := connectFor(t, 10*time.Minute)
	stamp := unique1579()
	app := app2539(t, admin, admin, "copyin")
	schema := schema2539(t, "copyin")
	addSchema2539(admin, app, conn2539, schema)
	migration2539(admin, conn2539, schema, 1,
		"CREATE TABLE photos (id int);\nCREATE TABLE guesses (g int);\nCREATE TABLE unused (id int);\n"+
			writeRole(schema, "SELECT, INSERT", "photos, guesses, unused"),
		"DROP TABLE photos, guesses, unused")
	admin.call("manage_schema", map[string]any{"command": "apply", "connection": conn2539, "schema": schema})

	loader := "acc2665-loader-" + stamp
	admin.seedPreGate(t, map[string]any{
		"command": "create", "name": loader, "description": "Acceptance #2665: loads photos with copy_in.",
		"source": "def main():\n    \"\"\"Loads photos.\"\"\"\n" +
			"    def work(tx):\n        tx.copy_in(\"" + schema + ".photos\", [\"id\"], [[1], [2]])\n" +
			"    platform.transaction(\"" + conn2539 + "\", work)\n" +
			"    print(len(platform.query(\"SELECT id FROM " + schema + ".photos\", connection = \"" + conn2539 + "\")[\"rows\"]))\n",
	})
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": loader}) })

	game := "acc2665-game-" + stamp
	admin.seedPreGate(t, map[string]any{
		"command": "create", "name": game, "description": "Acceptance #2665: a guess behind a sign-in wrapper.",
		"source": "def signed_in(fn):\n    \"\"\"Refuses a caller who is not signed in.\"\"\"\n" +
			"    return lambda req: fn(req) if req[\"caller\"] else {\"status\": 401, \"json\": {\"error\": \"sign in\"}}\n\n" +
			"def guess(req):\n    \"\"\"Files a guess.\"\"\"\n" +
			"    platform.execute(\"INSERT INTO " + schema + ".guesses (g) VALUES (1)\", connection = \"" + conn2539 + "\")\n" +
			"    return {\"json\": {\"ok\": True, \"n\": len(platform.query(\"SELECT g FROM " + schema + ".guesses\", connection = \"" + conn2539 + "\")[\"rows\"])}}\n\n" +
			"def routes():\n    \"\"\"The game's routes.\"\"\"\n    return {\"POST guess\": signed_in(guess)}\n\n" +
			"def handle(req):\n    \"\"\"Answers each request by its route.\"\"\"\n" +
			"    fn = routes().get(req[\"method\"] + \" \" + req[\"path\"].strip(\"/\"))\n" +
			"    return fn(req) if fn else {\"status\": 404, \"json\": {\"error\": \"no route\"}}\n",
	})
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": game}) })

	ids := map[string]string{"loader": scriptID2540(admin, loader), "game": scriptID2540(admin, game)}
	for _, id := range ids {
		admin.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "script", "part_id": id})
	}

	d := diagram2540(t, admin, app, "?version=latest")
	table := func(name string) string { return "table:" + conn2539 + "/" + schema + "." + name }
	links := links2540(d)
	for _, want := range []string{
		"script:" + ids["loader"] + " writes " + table("photos"),
		"script:" + ids["game"] + " writes " + table("guesses"),
	} {
		if !contains2540(links, want) {
			t.Errorf("the diagram lacks the link %q: %v", want, links)
		}
	}
	for _, f := range asList(d["findings"]) {
		if f["kind"] == "unwritten_table" && (strings.Contains(asJSON(f), ".photos") || strings.Contains(asJSON(f), ".guesses")) {
			t.Errorf("a written table is reported unwritten: %v", f)
		}
	}
	drawn := map[string]string{}
	for _, n := range asList(d["nodes"]) {
		id, _ := n["id"].(string)
		group, _ := n["group"].(string)
		drawn[id] = group
	}
	for _, name := range []string{"photos", "guesses", "unused"} {
		if g, ok := drawn[table(name)]; !ok || g != "schema" {
			t.Errorf("the created table %s is not drawn in the schema's group (group %q, drawn %v)", name, g, ok)
		}
	}
}
