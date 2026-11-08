//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2447: manage_schema describe returns the server's major and
// version, the database's extensions and every object a migration creates,
// and an application database on a server older than PostgreSQL 17 is
// refused. describe runs against the dev stack's PostgreSQL 17 `orders`
// target, each criterion in a schema of its own; the floor runs against the
// dev stack's platform server, which is PostgreSQL 16. The PostgreSQL 18
// half of the ticket's acceptance is TestDescribeReportsEveryKind_RealDB, run
// in the real-database gate on 17 and 18.
//
// Wire forms: manage_schema takes command, connection and schema as strings
// (describe has no other parameter); the admin connection route takes one
// JSON object whose config is a JSON object with port a JSON number; the
// probe route takes no body. No parameter admits a second form.

// everyKind2447 creates one object of each kind describe reports.
const everyKind2447 = `CREATE TYPE mood AS ENUM ('sad', 'ok');
CREATE DOMAIN email AS text CHECK (VALUE LIKE '%@%');
CREATE TABLE notes (id int PRIMARY KEY, owner text NOT NULL, body text, m mood, e email);
CREATE FUNCTION touch() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp
AS $fn$BEGIN RETURN NEW; END$fn$;
REVOKE ALL ON FUNCTION touch() FROM PUBLIC;
CREATE PROCEDURE tidy(days int) LANGUAGE sql AS $fn$DELETE FROM notes WHERE id < days$fn$;
CREATE TRIGGER notes_touch BEFORE INSERT OR UPDATE ON notes FOR EACH ROW WHEN (NEW.id > 0) EXECUTE FUNCTION touch();
ALTER TABLE notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE notes FORCE ROW LEVEL SECURITY;
CREATE POLICY own ON notes FOR SELECT TO PUBLIC USING (owner = current_user);
CREATE VIEW mine WITH (security_invoker = true) AS SELECT id FROM notes;
CREATE MATERIALIZED VIEW counts AS SELECT count(*) AS n FROM notes;
CREATE TABLE events (at date NOT NULL) PARTITION BY RANGE (at);
CREATE TABLE events_2026 PARTITION OF events FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')`

// byName2447 indexes a describe list of objects by name.
func byName2447(list any) map[string]map[string]any {
	out := map[string]map[string]any{}
	items, _ := list.([]any)
	for _, raw := range items {
		m, _ := raw.(map[string]any)
		name, _ := m["name"].(string)
		out[name] = m
	}
	return out
}

func TestIssue2447_DescribeReportsTheServerExtensionsAndEveryObjectKind(t *testing.T) {
	admin := connect(t)
	schema := "acc2447_" + unique1579()
	holdSchema(t, admin, conn2404, schema)
	schemaCall(admin, schema, map[string]any{"command": "add", "version": 1, "name": "every kind", "up": everyKind2447})
	schemaCall(admin, schema, map[string]any{"command": "apply"})
	t.Cleanup(func() { targetPSQL(t, "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	out := schemaCall(admin, schema, map[string]any{"command": "describe"})
	raw, _ := json.Marshal(out)

	server, _ := out["server"].(map[string]any)
	if server["major"] != float64(17) || !strings.HasPrefix(asString(server["version"]), "17.") {
		t.Errorf("server = %v, want the dev target's PostgreSQL 17", server)
	}
	if byName2447(out["extensions"])["plpgsql"] == nil || len(byName2447(out["available_extensions"])) == 0 {
		t.Errorf("extensions = %v; available = %d", out["extensions"], len(byName2447(out["available_extensions"])))
	}

	tables := byName2447(out["tables"])
	notes := tables["notes"]
	if notes["row_security"] != true || notes["force_row_security"] != true ||
		!strings.Contains(asJSON(notes["policies"]), `"command":"SELECT"`) {
		t.Errorf("notes row security and policies = %v %v %v", notes["row_security"], notes["force_row_security"], notes["policies"])
	}
	trigger := byName2447(notes["triggers"])["notes_touch"]
	if trigger["timing"] != "before" || trigger["level"] != "row" || trigger["when"] != "(new.id > 0)" ||
		asJSON(trigger["events"]) != `["insert","update"]` || trigger["enabled"] != "enabled" {
		t.Errorf("notes trigger = %v", trigger)
	}
	if v, _ := tables["mine"]["view"].(map[string]any); v["security_invoker"] != true || !strings.Contains(asString(v["definition"]), "notes") {
		t.Errorf("mine = %v", tables["mine"])
	}
	if tables["counts"]["kind"] != "materialized_view" {
		t.Errorf("counts = %v", tables["counts"])
	}
	if ev := tables["events"]; ev["partition_key"] != "RANGE (at)" || byName2447(ev["partitions"])["events_2026"] == nil {
		t.Errorf("events = %v", ev)
	}
	functions := byName2447(out["functions"])
	if f := functions["touch"]; f["security_definer"] != true || f["search_path"] != "pg_catalog, pg_temp" || f["language"] != "plpgsql" {
		t.Errorf("touch = %v", f)
	}
	if functions["tidy"]["kind"] != "procedure" {
		t.Errorf("tidy = %v", functions["tidy"])
	}
	if asJSON(byName2447(out["enums"])["mood"]["values"]) != `["sad","ok"]` || byName2447(out["domains"])["email"]["base_type"] != "text" {
		t.Errorf("enums = %v; domains = %v", out["enums"], out["domains"])
	}
	if strings.Contains(string(raw), ":null") {
		t.Errorf("an empty kind is [], never null: %s", raw)
	}

	empty := schemaCall(admin, "acc2447_empty_"+unique1579(), map[string]any{"command": "describe"})
	for _, kind := range []string{"tables", "functions", "enums", "domains"} {
		if list, ok := empty[kind].([]any); !ok || len(list) != 0 {
			t.Errorf("an empty schema's %s = %#v, want []", kind, empty[kind])
		}
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// A database on the dev stack's platform server, PostgreSQL 16, is refused
// at save and at probe naming 16 and the floor 17. The connection is first
// saved while its database does not exist, which a save does not refuse,
// so the probe has a connection to test once the database is created.
func TestIssue2447_ASixteenServerIsRefusedAtSaveAndAtProbe(t *testing.T) {
	admin := connect(t)
	db := "acc2447_old_" + unique1579()
	conn := "acc-2447-old-" + unique1579()
	path := "/api/v1/admin/connection-instances/appdb/" + conn
	cfg := map[string]any{
		"host": "localhost", "port": devPGPort2447(), "database": db, "user": "platform", "password": "platform_secret",
		"sslmode": "disable",
	}
	if status, body := admin.rest(http.MethodPut, path, jsonBody(t, map[string]any{"config": cfg, "description": "Acceptance #2447"})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving a connection to a database that does not exist yet answered %d: %v", status, body)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, path, http.NoBody) })
	devPSQL(t, "postgres", "CREATE DATABASE "+db)
	t.Cleanup(func() { devPSQL(t, "postgres", "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)") })

	status, probe := admin.rest(http.MethodPost, path+"/test", http.NoBody)
	if text := asJSON(probe); probe["ok"] != false || !strings.Contains(text, "PostgreSQL 16.") || !strings.Contains(text, "accepts is 17") {
		t.Errorf("probe answered %d %s, want a failure naming 16 and the floor 17", status, text)
	}
	status, saved := admin.rest(http.MethodPut, path, jsonBody(t, map[string]any{"config": cfg, "description": "Acceptance #2447, saved again"}))
	if text := asJSON(saved); status < 400 || !strings.Contains(text, "PostgreSQL 16.") || !strings.Contains(text, "PostgreSQL 17 or later") {
		t.Errorf("saving it again answered %d %s, want a refusal naming 16 and the floor 17", status, text)
	}
}

// devPGPort2447 is the dev platform server's port as a JSON number.
func devPGPort2447() int {
	port, _ := strconv.Atoi(devPostgresPort())
	return port
}

// Catalog text describe returns -- a function body, a view definition and a
// policy expression -- reaches the caller and nothing else: not a table of
// the platform's database, not either replica's log, not a span or log
// record the collector received. The objects are created on the target with
// psql, so the platform first reads the markers in describe's answer.
func TestIssue2447_CatalogTextReachesOnlyTheCaller(t *testing.T) {
	admin := connectFor(t, 10*time.Minute)
	k := newCanary2363(t)
	fn, view, policy := k.plant("appdb-describe-function-body"), k.plant("appdb-describe-view"), k.plant("appdb-describe-policy")
	for _, v := range []string{fn, view, policy} {
		k.returned[v] = k.values[v]
		delete(k.values, v)
	}
	schema := "acc2447_sink_" + unique1579()
	targetPSQL(t, "CREATE SCHEMA "+schema)
	t.Cleanup(func() { targetPSQL(t, "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	targetPSQL(t, "CREATE TABLE "+schema+".notes (id int, body text)")
	targetPSQL(t, "CREATE FUNCTION "+schema+".f() RETURNS text LANGUAGE sql AS $$SELECT '"+fn+"'::text$$")
	targetPSQL(t, "CREATE VIEW "+schema+".v AS SELECT id FROM "+schema+".notes WHERE body <> '"+view+"'")
	targetPSQL(t, "ALTER TABLE "+schema+".notes ENABLE ROW LEVEL SECURITY")
	targetPSQL(t, "CREATE POLICY p ON "+schema+".notes USING (body <> '"+policy+"')")

	_, text := k.call(admin, "manage_schema", map[string]any{"command": "describe", "connection": conn2404, "schema": schema})
	for _, v := range []string{fn, view, policy} {
		if !strings.Contains(text, v) {
			t.Fatalf("describe did not return %s to the caller: %s", k.returned[v], text)
		}
	}
	control := "/api/v1/admin/connection-instances/appdb/acc-2447-control-" + unique1579()
	k.rest(admin, http.MethodPut, control, map[string]any{"config": map[string]any{
		"host": "localhost", "port": 9, "database": "none", "user": "none", "sslmode": "disable",
	}, "description": "Acceptance #2447 " + k.control})
	t.Cleanup(func() { admin.rest(http.MethodDelete, control, http.NoBody) })
	k.rest(admin, http.MethodGet, control, nil)

	k.awaitWriters(admin)
	k.searchDatabase()
	k.searchFiles()
	k.searchResponses()
	if !k.found["table"] || !k.found["response"] {
		t.Errorf("the control value was not found in a table and a response; the search would miss a leak there: %v", k.found)
	}
}
