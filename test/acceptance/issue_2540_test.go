//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2540: GET /api/v1/portal/applications/{app}/diagram draws
// how an application's parts fit together, at the current release, the
// latest versions or a release by number, and compares two. For an
// application of a form embedded in an asset, the form's source, a script
// running on that source and writing a schema table, a scheduled script
// reading that table and refreshing an asset's data region, and an asset
// calling an endpoint script, every link is in the response with its kind; a
// computed export name is a node marked computed and no guessed link; drawing
// creates no run. The diagram's three findings (a library loaded at an
// unpinned version, a schema table no part writes, a schema the application
// does not hold) appear in the response and in manage_application get
// problems; a save introducing one reports it; a release with a part loading
// an application library at an unpinned version is refused naming the
// script and both versions, and succeeds once the load matches.
//
// Wire forms: the diagram route takes the application in its path and
// version and compare as query strings; manage_application, manage_script,
// manage_schema, run_script and save_asset take their arguments as JSON
// strings (args as an object of strings, references as a list); the schedule
// and endpoint routes take one JSON object. No parameter admits a second form.

// diagram2540 reads an application's diagram.
func diagram2540(t *testing.T, c *client, app, query string) map[string]any {
	t.Helper()
	status, out := c.rest(http.MethodGet, "/api/v1/portal/applications/"+app+"/diagram"+query, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("the diagram of %s%s answered %d: %v", app, query, status, out)
	}
	return out
}

// links2540 is each link as "from kind to".
func links2540(d map[string]any) []string {
	out := []string{}
	for _, l := range asList(d["links"]) {
		out = append(out, fmt.Sprint(l["from"], " ", l["kind"], " ", l["to"]))
	}
	return out
}

func scriptID2540(c *client, name string) string {
	id, _ := c.call("manage_script", map[string]any{"command": "get", "name": name})["id"].(string)
	return id
}

func TestIssue2540_TheDiagramDrawsEveryLinkOfTheApplication(t *testing.T) {
	admin := connectFor(t, 10*time.Minute)
	stamp := unique1579()
	app := app2539(t, admin, admin, "diagram")
	schema := schema2539(t, "diagram")
	addSchema2539(admin, app, conn2539, schema)
	migration2539(admin, conn2539, schema, 1, "CREATE TABLE signups (email text);\n"+writeRole(schema, "SELECT, INSERT", "signups"),
		"DROP TABLE signups; DROP OWNED BY "+schema+"_write; DROP ROLE "+schema+"_write")
	admin.call("manage_schema", map[string]any{"command": "apply", "connection": conn2539, "schema": schema})

	form, _ := bindForm(t, admin, "acc-2540-form", formFields, map[string]any{"auth_mode": "public"})
	source := "form-" + strings.ReplaceAll(form, "_", "-")

	intake := "acc2540-intake-" + stamp
	admin.saveScript(map[string]any{
		"command": "create", "name": intake, "description": "Acceptance #2540: files a signup.",
		"source": "def main():\n    \"\"\"Files a signup.\"\"\"\n    platform.execute(\"INSERT INTO " + schema +
			".signups (email) VALUES (:e)\", connection = \"orders\", params = {\"e\": \"a@example.com\"})\n",
	}, nil)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": intake}) })
	admin.call("manage_script", map[string]any{"command": "update", "name": intake, "event_source": source})

	refresh := "acc2540-refresh-" + stamp
	// It writes the dashboard on its first run and refreshes its data region
	// on the next: one output name is written once per run.
	refreshSrc := "def main():\n    \"\"\"Writes the dashboard, then refreshes its data region.\"\"\"\n" +
		"    rows = platform.query(\"SELECT count(*) AS n FROM " + schema + ".signups\", connection = \"orders\")[\"rows\"]\n" +
		"    if run.state.get(\"exported\"):\n" +
		"        platform.publish_data(\"dash\", {\"rows\": rows})\n" +
		"    else:\n" +
		"        platform.export(\"dash\", \"<html><body><script type=\\\"application/json\\\" id=\\\"data\\\">{}</script></body></html>\", format = \"html\")\n" +
		"        platform.save_state({\"exported\": True})\n"
	admin.saveScript(map[string]any{"command": "create", "name": refresh, "description": "Acceptance #2540: refreshes.", "source": refreshSrc}, nil)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": refresh}) })
	refreshID := scriptID2540(admin, refresh)
	ran := admin.call("run_script", map[string]any{"name": refresh, "wait_seconds": 120})
	dash := firstAssetID2540(t, ran)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": dash}) })
	if status, out := admin.rest(http.MethodPut, "/api/v1/portal/scripts/"+refreshID+"/schedule",
		jsonBody(t, map[string]any{"cron": "0 6 * * *", "timezone": "UTC", "enabled": false})); status != http.StatusOK {
		t.Fatalf("scheduling the refresh: %d %v", status, out)
	}

	// A script whose export name the source computes.
	daily := "acc2540-daily-" + stamp
	admin.saveScript(map[string]any{
		"command": "create", "name": daily, "description": "Acceptance #2540: exports a day.",
		"source": "def main():\n    \"\"\"Exports the day it is given.\"\"\"\n    platform.export(\"daily-\" + run.params[\"d\"], [{\"n\": 1}], \"csv\")\n",
		"params": []any{map[string]any{"name": "d", "type": "string", "required": true, "description": "The day."}},
	},
		map[string]any{"d": "x"})
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": daily}) })

	api := endpointName("acc-2540-api")
	saveEndpointScript(t, admin, api, "def handle(req):\n    \"\"\"Answers.\"\"\"\n    return {\"json\": {\"ok\": True}}\n", `{"params": {}, "caller": None}`)
	bindEndpoint(t, admin, api, "", map[string]any{"endpoint": api, "methods": []string{"GET"}, "path": "/", "auth_mode": "authenticated", "callers": []string{"*"}})

	saved := admin.call("save_asset", map[string]any{
		"name": "acc-2540 storefront " + stamp, "content_type": "text/html", "references": []string{"mcp:form:" + form},
		"content":     "<div>mcp:form:" + form + "</div><script>fetch(\"/endpoints/" + api + "/\")</script>",
		"description": "Acceptance #2540: the storefront.",
	})
	store, _ := saved["asset_id"].(string)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": store}) })

	ids := map[string]string{
		"intake": scriptID2540(admin, intake), "refresh": refreshID, "api": scriptID2540(admin, api),
		"form": scriptID2540(admin, form), "daily": scriptID2540(admin, daily),
	}
	for _, id := range ids {
		admin.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "script", "part_id": id})
	}
	for _, id := range []string{store, dash} {
		admin.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "asset", "part_id": id})
	}
	admin.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "source", "part_id": source})
	admin.call("manage_application", map[string]any{"command": "release", "application": app})

	runsBefore := platformCount2539(t, "SELECT count(*) FROM script_runs")
	d := diagram2540(t, admin, app, "?version=current")
	signups := "table:" + conn2539 + "/" + schema + ".signups"
	got := links2540(d)
	for _, want := range []string{
		"asset:" + store + " embeds script:" + ids["form"],
		"script:" + ids["form"] + " submits source:" + source,
		"source:" + source + " event script:" + ids["intake"],
		"script:" + ids["intake"] + " writes " + signups,
		"script:" + refreshID + " reads " + signups,
		"script:" + refreshID + " refreshes asset:" + dash,
		"asset:" + store + " calls script:" + ids["api"],
	} {
		if !contains2540(got, want) {
			t.Errorf("the diagram lacks the link %q: %v", want, got)
		}
	}
	computed := false
	for _, n := range asList(d["nodes"]) {
		if n["computed"] == true && n["kind"] == "output" {
			computed = true
		}
	}
	if !computed {
		t.Errorf("the computed export name is not a node marked computed: %v", d["nodes"])
	}
	for _, l := range got {
		if strings.HasPrefix(l, "script:"+ids["daily"]+" exports asset:") {
			t.Errorf("a computed export name was matched to an asset: %s", l)
		}
	}
	if !contains2540(got, "script:"+refreshID+" exports asset:"+dash) {
		t.Errorf("the literal export name is not matched to the asset it writes: %v", got)
	}
	latest := diagram2540(t, admin, app, "?version=latest&compare=1")
	if latest["version"] != "latest" || latest["compared_with"] != "1" || !strings.Contains(asJSON(latest["nodes"]), `"change":"unchanged"`) {
		t.Errorf("the latest diagram compared with release 1 is not marked: %v", latest["compared_with"])
	}
	diagram2540(t, admin, app, "?version=1")
	if after := platformCount2539(t, "SELECT count(*) FROM script_runs"); after != runsBefore {
		t.Errorf("drawing the diagram created %d runs", after-runsBefore)
	}
	if status, _ := admin.rest(http.MethodGet, "/api/v1/portal/applications/"+app+"/diagram?version=nope", http.NoBody); status != http.StatusUnprocessableEntity {
		t.Errorf("a version naming none answered %d", status)
	}
}

// firstAssetID2540 is the asset a run's export created.
func firstAssetID2540(t *testing.T, ran map[string]any) string {
	t.Helper()
	text := asJSON(ran)
	i := strings.Index(text, `"asset_id":"`)
	if ran["status"] != "succeeded" || i < 0 {
		t.Fatalf("the run created no asset: %s", firstRunes(text, 600))
	}
	rest := text[i+len(`"asset_id":"`):]
	return rest[:strings.Index(rest, `"`)]
}

func contains2540(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func TestIssue2540_FindingsAreReportedAndALibraryAtAnUnpinnedVersionRefusesTheRelease(t *testing.T) {
	admin := connectFor(t, 10*time.Minute)
	stamp := unique1579()
	app := app2539(t, admin, admin, "findings")
	schema := schema2539(t, "findings")
	addSchema2539(admin, app, conn2539, schema)
	migration2539(admin, conn2539, schema, 1, "CREATE TABLE lonely (id int);\n"+writeRole(schema, "SELECT", "lonely"),
		"DROP TABLE lonely; DROP OWNED BY "+schema+"_write; DROP ROLE "+schema+"_write")
	admin.call("manage_schema", map[string]any{"command": "apply", "connection": conn2539, "schema": schema})

	lib := "acc2540-lib-" + stamp
	issue1941CreateLibrary(t, admin, lib, issue1941Library(2))
	caller := "acc2540-caller-" + stamp
	admin.saveScript(map[string]any{"command": "create", "name": caller, "source": issue1941Caller(lib, 1), "description": "Acceptance #2540: loads a library."}, nil)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": caller}) })
	reader := "acc2540-reader-" + stamp
	admin.saveScript(map[string]any{
		"command": "create", "name": reader, "description": "Acceptance #2540: reads.",
		"source": "def main():\n    \"\"\"Reads a table nobody writes and one of no application.\"\"\"\n" +
			"    a = platform.query(\"SELECT count(*) AS n FROM " + schema + ".lonely\", connection = \"orders\")[\"rows\"]\n" +
			"    b = platform.query(\"SELECT count(*) AS n FROM public.customers\", connection = \"orders\")[\"rows\"]\n" +
			"    print(\"lonely %d customers %d\" % (a[0][\"n\"], b[0][\"n\"]))\n",
	}, nil)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": reader}) })
	for _, name := range []string{lib, caller, reader} {
		admin.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "script", "part_id": scriptID2540(admin, name)})
	}

	// The library's next version: the release would pin 2 while the caller
	// loads 1.
	admin.call("manage_script", map[string]any{
		"command": "update", "name": lib, "source": issue1941Library(3),
		"change_summary": "Scales by 3 instead of 2.", "user_agreed": true,
	})

	d := diagram2540(t, admin, app, "?version=latest")
	kinds := asJSON(d["findings"])
	for _, k := range []string{"library_version", "unwritten_table", "foreign_schema"} {
		if !strings.Contains(kinds, `"kind":"`+k+`"`) {
			t.Errorf("the diagram does not find %s: %s", k, kinds)
		}
	}
	problems := asJSON(get2539(admin, app)["problems"])
	for _, want := range []string{"loads library " + lib + " at version 1", schema + ".lonely", "public.customers"} {
		if !strings.Contains(problems, want) {
			t.Errorf("manage_application get problems do not report %q: %s", want, problems)
		}
	}

	text := refused2539(t, admin, "manage_application", map[string]any{"command": "release", "application": app})
	if !strings.Contains(text, caller) || !strings.Contains(text, "at version 1") || !strings.Contains(text, `lib:`+lib+`@2`) {
		t.Errorf("the release is not refused naming the script, the version it loads and the one to load: %s", text)
	}
	admin.saveEdit(map[string]any{"command": "update", "name": caller, "source": issue1941Caller(lib, 2)}, nil)
	rel := admin.call("manage_application", map[string]any{"command": "release", "application": app})
	if fmt.Sprint(rel["number"]) != "1" {
		t.Fatalf("the release did not succeed once the load matched: %v", rel)
	}
	if !strings.Contains(asJSON(rel["changes"]), "lonely") {
		t.Errorf("the release does not record the findings it did not refuse: %v", rel["changes"])
	}

	// A save that loads the library at the old version again reports it.
	out := admin.saveEdit(map[string]any{"command": "update", "name": caller, "source": issue1941Caller(lib, 1)}, nil)
	if !strings.Contains(asJSON(out["application_findings"]), "loads library "+lib+" at version 1") {
		t.Errorf("the save does not report the finding it introduced: %v", out)
	}
}
