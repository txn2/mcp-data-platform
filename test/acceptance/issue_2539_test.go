//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
)

// Acceptance for #2539: an application database schema exists only inside an
// application. A schema is added to an existing application as a part, on an
// existing appdb connection the caller reaches; a second schema, or one
// another application holds, is refused naming the holder; manage_schema and
// the portal's plan refuse a schema no application holds; a schema whose
// migrations were applied before it was added is adopted; removing it from a
// released application is an administrator's; the unheld schemas are listed
// with the call that adds them; the knowledge page covers the growing case.
// Adding the schema creates the run role the application's scripts log in
// as: a draft of a part runs as it, it is granted the write role a migration
// defines, a script of another application is refused on the schema by the
// database (naming the holder), DDL from a part is refused while the same
// change as a migration succeeds, a connection whose user cannot create
// roles is refused naming CREATEROLE, removal drops the role and its stored
// password, and the password appears in no response, log, audit row, call
// record, run record, bundle or error.
//
// Wire forms: manage_application, manage_schema, manage_script and run_script
// take their arguments as JSON strings (version as a JSON number, args as an
// object of strings); the portal part route takes one JSON object of strings;
// the plan route takes the schema in its query string. No parameter admits a
// second form.

const conn2539 = "orders"

// app2539 creates an application owned by c with no schema.
func app2539(t *testing.T, c, admin *client, label string) string {
	t.Helper()
	name := "acc2539-" + label + "-" + unique1579()
	c.call("manage_application", map[string]any{"command": "create", "name": name, "description": "Acceptance #2539: " + label})
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": name})
	})
	return name
}

// get2539 is manage_application get.
func get2539(c *client, app string) map[string]any {
	return c.call("manage_application", map[string]any{"command": "get", "application": app})
}

// refused2539 calls a tool expected to refuse and returns its text.
func refused2539(t *testing.T, c *client, tool string, args map[string]any) string {
	t.Helper()
	res, text, err := c.callRaw(tool, args)
	if err != nil {
		t.Fatalf("%s %v: %v", tool, args["command"], err)
	}
	if !res.IsError {
		t.Fatalf("%s %v succeeded and was expected to be refused: %s", tool, args["command"], firstRunes(text, 400))
	}
	return text
}

// schema2539 is a fresh schema name, dropped with its roles when the test
// ends.
func schema2539(t *testing.T, label string) string {
	t.Helper()
	schema := "acc2539_" + label + "_" + unique1579()
	t.Cleanup(func() { targetPSQL(t, "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	dropRolesLater(t, schema+"_write")
	forgetSchemaLater(t, conn2539, schema)
	return schema
}

func addSchema2539(c *client, app, conn, schema string) map[string]any {
	return c.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "schema", "part_id": conn + "/" + schema})
}

func migration2539(c *client, conn, schema string, version int, up, down string) {
	c.call("manage_schema", map[string]any{"command": "add", "connection": conn, "schema": schema, "version": version, "name": fmt.Sprintf("m%d", version), "up": up, "down": down})
}

func runRole2539(t *testing.T, c *client, app string) string {
	t.Helper()
	view, _ := get2539(c, app)["schema"].(map[string]any)
	role, _ := view["run_role"].(string)
	if role == "" {
		t.Fatalf("application %s has no run role: %v", app, view)
	}
	return role
}

func TestIssue2539_ASchemaIsAddedToAnExistingApplicationAsAPart(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	app := app2539(t, admin, admin, "grows")
	schema := schema2539(t, "grows")

	added := addSchema2539(admin, app, conn2539, schema)
	if added["schema"] != schema || added["connection"] != conn2539 {
		t.Fatalf("add_part did not give the application the schema: %v", added)
	}
	st := get2539(admin, app)
	found := false
	for _, p := range asList(asMap(st["application"])["parts"]) {
		found = found || (p["kind"] == "schema" && p["id"] == conn2539+"/"+schema)
	}
	if !found {
		t.Errorf("get does not list the schema among the parts: %v", st["application"])
	}
	view := asMap(st["schema"])
	if _, ok := view["migration_version"]; !ok || view["run_role"] == "" {
		t.Errorf("get carries no schema part view with its migration version and run role: %v", view)
	}

	// The portal's part route adds one too.
	other := app2539(t, admin, admin, "portal")
	otherSchema := schema2539(t, "portal")
	status, out := admin.rest(http.MethodPost, "/api/v1/portal/applications/"+other+"/parts",
		jsonBody(t, map[string]any{"kind": "schema", "id": conn2539 + "/" + otherSchema}))
	if status != http.StatusOK || out["schema"] != otherSchema {
		t.Errorf("the portal's add part answered %d: %v", status, out)
	}
	status, detail := admin.rest(http.MethodGet, "/api/v1/portal/applications/"+other, http.NoBody)
	if status != http.StatusOK || !strings.Contains(asJSON(detail["parts"]), `"kind":"schema"`) {
		t.Errorf("the portal's application page lists no schema part: %d %v", status, detail["parts"])
	}
}

func TestIssue2539_TheConnectionMustBeAnAppDBConnectionTheCallerReaches(t *testing.T) {
	admin := connect(t)
	app := app2539(t, admin, admin, "conn")
	text := refused2539(t, admin, "manage_application", map[string]any{
		"command": "add_part", "application": app, "kind": "schema", "part_id": "no-such-connection/x",
	})
	if !strings.Contains(text, `"no-such-connection" is not an application database connection`) || !strings.Contains(text, conn2539) {
		t.Errorf("an unknown connection is not refused naming the appdb connections the caller may choose: %s", text)
	}
}

func TestIssue2539_ASecondSchemaOrOneAnotherApplicationHoldsIsRefused(t *testing.T) {
	admin := connect(t)
	app := app2539(t, admin, admin, "one")
	schema := schema2539(t, "one")
	addSchema2539(admin, app, conn2539, schema)
	second := schema2539(t, "two")
	if text := refused2539(t, admin, "manage_application", map[string]any{
		"command": "add_part", "application": app, "kind": "schema", "part_id": conn2539 + "/" + second,
	}); !strings.Contains(text, "holds schema") {
		t.Errorf("a second schema is not refused: %s", text)
	}
	rival := app2539(t, admin, admin, "rival")
	if text := refused2539(t, admin, "manage_application", map[string]any{
		"command": "add_part", "application": rival, "kind": "schema", "part_id": conn2539 + "/" + schema,
	}); !strings.Contains(text, app) {
		t.Errorf("a schema another application holds is not refused naming it: %s", text)
	}
}

func TestIssue2539_MigrationsOnASchemaNoApplicationHoldsAreRefused(t *testing.T) {
	admin := connect(t)
	schema := schema2539(t, "loose")
	for _, args := range []map[string]any{
		{"command": "add", "version": 1, "name": "t", "up": "CREATE TABLE t (id int)"},
		{"command": "plan"},
		{"command": "apply"},
	} {
		args["connection"], args["schema"] = conn2539, schema
		text := refused2539(t, admin, "manage_schema", args)
		if !strings.Contains(text, "belongs to no application") || !strings.Contains(text, "manage_application add_part") {
			t.Errorf("manage_schema %v on a schema no application holds is not refused naming add_part: %s", args["command"], text)
		}
	}
	status, out := admin.rest(http.MethodPost, appdbPath(conn2539, "/migrations/plan?schema="+schema), http.NoBody)
	if status != http.StatusBadRequest || !strings.Contains(asJSON(out), "manage_application add_part") {
		t.Errorf("the portal's plan of a schema no application holds answered %d: %v", status, out)
	}
}

func TestIssue2539_ASchemaMigratedBeforeItWasAddedIsAdoptedAndListedMeanwhile(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	schema := schema2539(t, "adopt")
	// The schema migrated inside one application, then taken out of it: what a
	// schema migrated before #2539 looks like.
	first := app2539(t, admin, admin, "first")
	addSchema2539(admin, first, conn2539, schema)
	migration2539(admin, conn2539, schema, 1, "CREATE TABLE t (id int)", "DROP TABLE t")
	migration2539(admin, conn2539, schema, 2, "CREATE TABLE u (id int)", "DROP TABLE u")
	admin.call("manage_schema", map[string]any{"command": "apply", "connection": conn2539, "schema": schema})
	admin.call("manage_application", map[string]any{"command": "remove_part", "application": first, "kind": "schema", "part_id": conn2539 + "/" + schema})

	status := admin.call("manage_schema", map[string]any{"command": "status", "connection": conn2539, "schema": schema})
	unheld := asJSON(status["unheld_schemas"])
	if !strings.Contains(unheld, `"schema":"`+schema+`"`) || !strings.Contains(unheld, "part_id="+conn2539+"/"+schema) {
		t.Errorf("manage_schema status does not list the schema no application holds with the call that adds it: %s", unheld)
	}
	code, report := admin.rest(http.MethodGet, appdbPath(conn2539, "/migrations?schema="+schema), http.NoBody)
	if code != http.StatusOK || !strings.Contains(asJSON(report["unheld_schemas"]), schema) {
		t.Errorf("the admin migrations report does not list it: %d %v", code, report["unheld_schemas"])
	}

	app := app2539(t, admin, admin, "adopter")
	admin.call("manage_application", map[string]any{"command": "release", "application": app, "note": "before the schema"})
	addSchema2539(admin, app, conn2539, schema)
	migration2539(admin, conn2539, schema, 3, "CREATE TABLE v (id int)", "DROP TABLE v")
	rel := admin.call("manage_application", map[string]any{"command": "release", "application": app, "note": "with the schema"})
	if !strings.Contains(asJSON(rel["changes"]), `"applied":[3]`) || fmt.Sprint(rel["migration_version"]) != "3" {
		t.Errorf("the release did not apply only what was not applied and record version 3: %v", rel)
	}
	back := admin.call("manage_application", map[string]any{"command": "rollback", "application": app, "to_release": 1})
	if !strings.Contains(asJSON(back["changes"]), `"reverted":[3]`) {
		t.Errorf("the rollback reverted more than the migration a release applied: %v", back["changes"])
	}
	if got := targetPSQL(t, "SELECT count(*) FROM pg_tables WHERE schemaname = '"+schema+"' AND tablename IN ('t', 'u')"); strings.TrimSpace(got) != "2" {
		t.Errorf("the adopted migrations were reverted: %s tables of t and u remain", got)
	}
}

func TestIssue2539_RemovingTheSchemaOfAReleasedApplicationIsAnAdministrators(t *testing.T) {
	admin, owner := connect(t), connectAs(t, devOwnerAPIKey)
	app := app2539(t, owner, admin, "released")
	schema := schema2539(t, "released")
	addSchema2539(admin, app, conn2539, schema)
	role := runRole2539(t, admin, app)
	admin.call("manage_application", map[string]any{"command": "release", "application": app})
	if text := refused2539(t, owner, "manage_application", map[string]any{
		"command": "remove_part", "application": app, "kind": "schema", "part_id": conn2539 + "/" + schema,
	}); !strings.Contains(text, "administrators") {
		t.Errorf("the owner's removal of a released application's schema is not refused for administrators: %s", text)
	}
	appID := fmt.Sprint(asMap(get2539(admin, app)["application"])["id"])
	admin.call("manage_application", map[string]any{"command": "remove_part", "application": app, "kind": "schema", "part_id": conn2539 + "/" + schema})
	if got := targetPSQL(t, "SELECT count(*) FROM pg_roles WHERE rolname = '"+role+"'"); strings.TrimSpace(got) != "0" {
		t.Errorf("the run role %s outlived the schema's removal", role)
	}
	if n := platformCount2539(t, "SELECT count(*) FROM application_run_roles WHERE application_id = $1", appID); n != 0 {
		t.Errorf("the stored password outlived the schema's removal: %d rows", n)
	}
}

func TestIssue2539_TheKnowledgePagesCoverTheGrowingCaseAndTheRunRole(t *testing.T) {
	admin := connect(t)
	for slug, wants := range map[string][]string{
		"platform-applications":          {"scratch tables or files", `kind: "schema"`, "run role", "shop_write"},
		"platform-application-databases": {"CREATEROLE", "run role", "<schema>_write", "endpoints and exports"},
	} {
		page := admin.call("fetch", map[string]any{"reference": "mcp:knowledge_page:" + slug, "purpose": "Acceptance #2539: the guidance."})
		body := asJSON(page)
		for _, w := range wants {
			if !strings.Contains(body, strings.Trim(asJSON(w), `"`)) {
				t.Errorf("%s does not say %q", slug, w)
			}
		}
	}
}

// shop2539 is an application holding a schema with a table its run role is
// granted through the write role its migration defines.
type shop2539 struct {
	app, schema, role string
}

func newShop2539(t *testing.T, admin *client, label string) shop2539 {
	t.Helper()
	s := shop2539{app: app2539(t, admin, admin, label), schema: schema2539(t, label)}
	addSchema2539(admin, s.app, conn2539, s.schema)
	s.role = runRole2539(t, admin, s.app)
	migration2539(admin, conn2539, s.schema, 1, "CREATE TABLE t (id int);\nINSERT INTO t VALUES (1), (2);\n"+writeRole(s.schema, "SELECT, INSERT", "t"),
		"DROP TABLE t; DROP OWNED BY "+s.schema+"_write; DROP ROLE "+s.schema+"_write")
	admin.call("manage_application", map[string]any{"command": "release", "application": s.app})
	return s
}

// part2539 saves a script and makes it a part of app.
func part2539(t *testing.T, admin *client, app, label, source string, draft map[string]any, params []any) string {
	t.Helper()
	name := "acc2539-" + label + "-" + unique1579()
	create := map[string]any{"command": "create", "name": name, "description": "Acceptance #2539: " + label, "source": source}
	if params != nil {
		create["params"] = params
	}
	admin.saveScript(create, draft)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	id, _ := admin.call("manage_script", map[string]any{"command": "get", "name": name})["id"].(string)
	admin.call("manage_application", map[string]any{"command": "add_part", "application": app, "kind": "script", "part_id": id})
	return name
}

const query2539 = `def main():
    """Runs the statement it is given and returns what it answered."""
    out = platform.query(run.params["sql"], connection = "orders", on_error = "return")
    platform.result(out)
`

const execute2539 = `def main():
    """Runs the statement it is given and returns what it answered."""
    out = platform.execute(run.params["sql"], connection = "orders", on_error = "return")
    platform.result(out)
`

var sqlParam2539 = []any{map[string]any{"name": "sql", "type": "string", "required": true, "description": "The statement."}}

// run2539 runs a saved script with sql and returns the run as JSON.
func run2539(admin *client, name, statement string) string {
	return asJSON(admin.call("run_script", map[string]any{"name": name, "args": map[string]any{"sql": statement}, "wait_seconds": 60}))
}

func TestIssue2539_APartRunsAsTheRunRoleGrantedTheWriteRoleItsMigrationDefines(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	s := newShop2539(t, admin, "role")
	if got := targetPSQL(t, "SELECT pg_has_role('"+s.role+"', '"+s.schema+"_write', 'MEMBER')"); strings.TrimSpace(got) != "t" {
		t.Errorf("the run role is not a member of the write role the migration defined: %s", got)
	}
	src := "def main():\n    \"\"\"Answers who it runs as.\"\"\"\n    platform.result(platform.query(\"SELECT current_user AS who\", connection = \"orders\"))\n"
	name := part2539(t, admin, s.app, "who", src, nil, nil)
	_, text := admin.callRaw2539("manage_script", map[string]any{"command": "run_draft", "name": name, "source": src})
	if !strings.Contains(text, s.role) {
		t.Errorf("a draft of a part did not run as the run role %s: %s", s.role, firstRunes(text, 400))
	}
}

func TestIssue2539_AnotherApplicationsScriptIsRefusedOnTheSchemaByTheDatabase(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	b := newShop2539(t, admin, "holder")
	a := newShop2539(t, admin, "outsider")
	statement := "SELECT count(*) AS n FROM " + b.schema + ".t"
	draft := map[string]any{"sql": "SELECT 1 AS n"}
	inA := part2539(t, admin, a.app, "outside", query2539, draft, sqlParam2539)
	inB := part2539(t, admin, b.app, "inside", query2539, draft, sqlParam2539)

	refusedRun := run2539(admin, inA, statement)
	if !strings.Contains(refusedRun, b.app) || !strings.Contains(refusedRun, "endpoints") || !strings.Contains(refusedRun, "exports") {
		t.Errorf("a script of another application is not refused naming %s and its endpoints and exports: %s", b.app, firstRunes(refusedRun, 600))
	}
	if ok := run2539(admin, inB, statement); !strings.Contains(ok, `"n":2`) {
		t.Errorf("the same statement from a part of %s did not succeed: %s", b.app, firstRunes(ok, 600))
	}

	// Adding a script that names the other application's schema is refused.
	literal := "def main():\n    \"\"\"Reads another application's table.\"\"\"\n    platform.result(platform.query(\"" + statement + "\", connection = \"orders\"))\n"
	name := "acc2539-literal-" + unique1579()
	admin.saveScript(map[string]any{"command": "create", "name": name, "description": "Acceptance #2539: literal", "source": literal}, nil)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	id, _ := admin.call("manage_script", map[string]any{"command": "get", "name": name})["id"].(string)
	if text := refused2539(t, admin, "manage_application", map[string]any{"command": "add_part", "application": a.app, "kind": "script", "part_id": id}); !strings.Contains(text, b.app) {
		t.Errorf("adding a script that uses another application's schema is not refused naming it: %s", text)
	}
}

func TestIssue2539_DDLFromAPartIsRefusedAndTheSameChangeAsAMigrationApplies(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	s := newShop2539(t, admin, "ddl")
	name := part2539(t, admin, s.app, "ddl", execute2539, map[string]any{"sql": "SELECT 1"}, sqlParam2539)
	for _, stmt := range []string{
		"DROP TABLE " + s.schema + ".t",
		"ALTER TABLE " + s.schema + ".t ADD COLUMN note text",
		"CREATE TABLE " + s.schema + ".extra (id int)",
	} {
		if out := run2539(admin, name, stmt); !strings.Contains(out, "42501") {
			t.Errorf("%q from a part was not refused by the database: %s", stmt, firstRunes(out, 500))
		}
	}
	migration2539(admin, conn2539, s.schema, 2, "ALTER TABLE t ADD COLUMN note text", "ALTER TABLE t DROP COLUMN note")
	rel := admin.call("manage_application", map[string]any{"command": "release", "application": s.app})
	if !strings.Contains(asJSON(rel["changes"]), `"applied":[2]`) {
		t.Errorf("the same change as a migration did not apply by release: %v", rel["changes"])
	}
}

func TestIssue2539_AConnectionWhoseUserCannotCreateRolesIsRefusedNamingIt(t *testing.T) {
	admin := connect(t)
	d := newDB2528(t, admin)
	app := app2539(t, admin, admin, "nocreaterole")
	text := refused2539(t, admin, "manage_application", map[string]any{
		"command": "add_part", "application": app, "kind": "schema", "part_id": d.conn + "/shop",
	})
	if !strings.Contains(text, "CREATEROLE") || !strings.Contains(text, `ALTER ROLE "`+d.role+`" CREATEROLE`) {
		t.Errorf("adding a schema on a connection whose user cannot create roles is not refused naming the privilege and statement: %s", text)
	}
}

func TestIssue2539_TheRunRolePasswordReachesNoSink(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	start := time.Now().UTC()
	s := newShop2539(t, admin, "sinks")
	appID := fmt.Sprint(asMap(get2539(admin, s.app)["application"])["id"])
	password := runRolePassword2539(t, appID, s.schema, s.role)
	canaries := map[string]string{"run role password": password}

	name := part2539(t, admin, s.app, "sinks", query2539, map[string]any{"sql": "SELECT 1 AS n"}, sqlParam2539)
	ran := run2539(admin, name, "SELECT count(*) AS n FROM "+s.schema+".t")
	refusedRun := run2539(admin, name, "SELECT * FROM pg_authid")

	sinks := map[string]string{
		"manage_application get":      asJSON(get2539(admin, s.app)),
		"manage_application list":     asJSON(admin.call("manage_application", map[string]any{"command": "list"})),
		"manage_application releases": asJSON(admin.call("manage_application", map[string]any{"command": "releases", "application": s.app})),
		"manage_schema status":        asJSON(admin.call("manage_schema", map[string]any{"command": "status", "connection": conn2539, "schema": s.schema})),
		"run_script":                  ran,
		"a refused run":               refusedRun,
	}
	_, portal := admin.rest(http.MethodGet, "/api/v1/portal/applications/"+s.app, http.NoBody)
	sinks["portal application page"] = asJSON(portal)
	_, report := admin.rest(http.MethodGet, appdbPath(conn2539, "/migrations?schema="+s.schema), http.NoBody)
	sinks["admin migrations report"] = asJSON(report)
	exported := admin.call("manage_application", map[string]any{"command": "export", "application": s.app})
	sinks["export result"] = asJSON(exported)
	if id, _ := asMap(exported["bundle"])["resource_id"].(string); id != "" {
		got := endpointRequest(t, admin.base, http.MethodGet, "/api/v1/resources/"+id+"/content", bearer(admin.apiKey), "")
		sinks["export bundle"] = got.body
		if !strings.Contains(got.body, s.schema+"_write") {
			t.Errorf("the export carries no migration of the schema")
		}
	} else {
		t.Errorf("export landed no bundle: %v", exported)
	}
	sinks["audit rows"] = platformText2539(t, `SELECT coalesce(string_agg(coalesce(parameters::text, '') || ' ' || coalesce(error_message, ''), E'\n'), '')
		FROM audit_logs WHERE timestamp >= $1`, start)
	sinks["call records"] = platformText2539(t, `SELECT coalesce(string_agg(row_to_json(c)::text, E'\n'), '') FROM call_records c WHERE c.created_at >= $1`, start)
	sinks["run records"] = platformText2539(t, `SELECT coalesce(string_agg(row_to_json(r)::text, E'\n'), '') FROM script_runs r WHERE r.created_at >= $1`, start)
	logs := ""
	for _, path := range []string{os.Getenv("DEV_AIR_LOG"), "/tmp/mcp-dev-air.log", "/tmp/mcp-dev-air-b.log"} {
		if path == "" {
			continue
		}
		if b, err := os.ReadFile(path); err == nil { // #nosec G304 -- the dev stack's own log
			logs += string(b)
		}
	}
	if logs == "" {
		t.Fatal("no platform log was read; `make dev` writes it to /tmp/mcp-dev-air.log")
	}
	sinks["platform logs"] = logs
	for where, sink := range sinks {
		noCanary(t, where, []byte(sink), canaries)
	}
}

// runRolePassword2539 opens the run role's stored password with the stack's
// key: the value every sink is searched for.
func runRolePassword2539(t *testing.T, appID, schema, role string) string {
	t.Helper()
	key := os.Getenv("ENCRYPTION_KEY")
	if key == "" {
		t.Fatal("ENCRYPTION_KEY is not set; `make acceptance` loads it from dev/.dev-ports.env, which `make dev` writes")
	}
	enc, err := fieldcrypt.NewFieldEncryptor(fieldcrypt.DecodeKey(key))
	if err != nil {
		t.Fatal(err)
	}
	sealed := platformText2539(t, "SELECT password_sealed FROM application_run_roles WHERE application_id = $1", appID)
	if strings.Contains(sealed, role) || !strings.HasPrefix(sealed, "encb:") {
		t.Fatalf("the stored password is not sealed: %q", firstRunes(sealed, 20))
	}
	password, err := fieldcrypt.NewRestFieldEncryptor(enc).OpenBound(appsvc.RunRoleBinding(appID, conn2539, schema, role), sealed)
	if err != nil || len(password) < 32 {
		t.Fatalf("opening the run role's password: %v", err)
	}
	return password
}

// platformDB2539 opens the dev stack's platform database.
func platformDB2539(t *testing.T) *sql.DB {
	t.Helper()
	port := os.Getenv("DEV_PG_PORT")
	if port == "" {
		port = "5432"
	}
	db, err := sql.Open("postgres", "postgres://platform:platform_secret@localhost:"+port+"/mcp_platform?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func platformText2539(t *testing.T, query string, args ...any) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var out sql.NullString
	if err := platformDB2539(t).QueryRowContext(ctx, query, args...).Scan(&out); err != nil {
		t.Fatalf("reading the platform database (%s): %v", firstRunes(query, 60), err)
	}
	return out.String
}

func platformCount2539(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := platformDB2539(t).QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("counting in the platform database: %v", err)
	}
	return n
}

// callRaw2539 calls a tool and returns its result text whether it succeeded
// or not.
func (c *client) callRaw2539(tool string, args map[string]any) (bool, string) {
	res, text, err := c.callRaw(tool, args)
	if err != nil {
		c.t.Fatalf("%s: %v", tool, err)
	}
	return !res.IsError, text
}

// asMap is v as a JSON object, empty when it is not one.
func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
