//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// everyKind is a migration creating one object of each kind describe reports
// (#2447), with the marker literal in a function body, a view definition and
// a policy expression.
const everyKind = `CREATE TYPE mood AS ENUM ('sad', 'ok');
CREATE DOMAIN email AS text CHECK (VALUE LIKE '%@%');
CREATE TABLE notes (id int PRIMARY KEY, owner text NOT NULL, body text, m mood, e email);
CREATE FUNCTION touch() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp
AS $fn$BEGIN NEW.body := coalesce(NEW.body, 'marker-2447-fn'); RETURN NEW; END$fn$;
CREATE PROCEDURE tidy(days int) LANGUAGE sql AS $fn$DELETE FROM notes WHERE id < days$fn$;
CREATE TRIGGER notes_touch BEFORE INSERT OR UPDATE ON notes FOR EACH ROW WHEN (NEW.id > 0) EXECUTE FUNCTION touch();
ALTER TABLE notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE notes FORCE ROW LEVEL SECURITY;
CREATE POLICY own ON notes FOR SELECT TO PUBLIC USING (owner = current_user OR body = 'marker-2447-policy');
CREATE VIEW mine WITH (security_invoker = true) AS SELECT id FROM notes WHERE body <> 'marker-2447-view';
CREATE MATERIALIZED VIEW counts AS SELECT count(*) AS n FROM notes;
CREATE TABLE events (at date NOT NULL) PARTITION BY RANGE (at);
CREATE TABLE events_2026 PARTITION OF events FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')`

func TestDescribeReportsEveryKind_RealDB(t *testing.T) {
	onTargets(t, func(t *testing.T) {
		tk, _ := newSchemaTarget(t, nil)
		ctx := admin()
		schema(t, ctx, tk, schemaInput{Command: schemaAdd, Version: 1, Name: "every kind", Up: everyKind}, false)
		schema(t, ctx, tk, schemaInput{Command: schemaApply}, false)
		out := schema(t, ctx, tk, schemaInput{Command: schemaDescribe}, false)
		raw, _ := json.Marshal(out)
		var d Description
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}

		if want := subtestMajor(t.Name()); fmt.Sprintf("pg%d", d.Server.Major) != want || !strings.HasPrefix(d.Server.Version, want[2:]) {
			t.Errorf("server = %+v, want the %s target's", d.Server, want)
		}
		if !hasExtension(d.Extensions, "plpgsql") || len(d.AvailableExtensions) == 0 {
			t.Errorf("extensions = %v; available = %d", d.Extensions, len(d.AvailableExtensions))
		}
		tables := map[string]Table{}
		for _, tb := range d.Tables {
			tables[tb.Name] = tb
		}
		notes := tables["notes"]
		if !notes.RowSecurity || !notes.ForceRowSecurity || len(notes.Policies) != 1 ||
			notes.Policies[0].Command != "SELECT" || !strings.Contains(notes.Policies[0].Using, "marker-2447-policy") {
			t.Errorf("notes row security = %v %v %+v", notes.RowSecurity, notes.ForceRowSecurity, notes.Policies)
		}
		if len(notes.Constraints) != 1 {
			t.Errorf("notes constraints = %+v; NOT NULL is the column's, on every major", notes.Constraints)
		}
		if len(notes.Triggers) != 1 || notes.Triggers[0].Timing != "before" || notes.Triggers[0].Level != "row" ||
			strings.Join(notes.Triggers[0].Events, ",") != "insert,update" || notes.Triggers[0].When != "(new.id > 0)" ||
			notes.Triggers[0].Enabled != "enabled" || !strings.HasSuffix(notes.Triggers[0].Function, ".touch") {
			t.Errorf("notes triggers = %+v", notes.Triggers)
		}
		if v := tables["mine"].View; v == nil || !v.SecurityInvoker || !strings.Contains(v.Definition, "marker-2447-view") {
			t.Errorf("mine view = %+v", v)
		}
		if tables["counts"].Kind != "materialized_view" || tables["counts"].View == nil {
			t.Errorf("counts = %+v", tables["counts"])
		}
		if ev := tables["events"]; ev.PartitionKey != "RANGE (at)" || len(ev.Partitions) != 1 || ev.Partitions[0].Name != "events_2026" {
			t.Errorf("events = %+v", ev)
		}
		functions := map[string]Function{}
		for _, f := range d.Functions {
			functions[f.Name] = f
		}
		if f := functions["touch"]; !f.SecurityDefiner || f.SearchPath != "pg_catalog, pg_temp" || f.Language != "plpgsql" ||
			f.Kind != "function" || !strings.Contains(f.Body, "marker-2447-fn") {
			t.Errorf("touch = %+v", f)
		}
		if f := functions["tidy"]; f.Kind != "procedure" || f.Arguments != "IN days integer" {
			t.Errorf("tidy = %+v", f)
		}
		if len(d.Enums) != 1 || strings.Join(d.Enums[0].Values, ",") != "sad,ok" {
			t.Errorf("enums = %+v", d.Enums)
		}
		if len(d.Domains) != 1 || d.Domains[0].BaseType != "text" || len(d.Domains[0].Checks) != 1 {
			t.Errorf("domains = %+v", d.Domains)
		}
		if strings.Contains(string(raw), ":null") {
			t.Errorf("an empty kind is [], never null: %s", raw)
		}
	})
}

func hasExtension(es []Extension, name string) bool {
	for _, e := range es {
		if e.Name == name {
			return true
		}
	}
	return false
}

// The platform's real-database server is PostgreSQL 16, below the floor: a
// database on it is refused at save, at probe and at its first statement.
func TestServerBelowTheFloorIsRefused_RealDB(t *testing.T) {
	platform, dsn := testdb.NewWithDSN(t)
	var num int
	if err := platform.QueryRow("SELECT current_setting('server_version_num')::int").Scan(&num); err != nil {
		t.Fatal(err)
	}
	if num >= MinServerMajor*10000 {
		t.Fatalf("this test needs a server below PostgreSQL %d; the platform's real-database server is %d", MinServerMajor, num)
	}
	name := fmt.Sprintf("pgkind_old_%d", targetSeq.Add(1))
	if _, err := platform.Exec(`CREATE DATABASE "` + name + `"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = platform.Exec(`DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`) })
	cfg := cfgFromDSN(t, dsn)
	cfg[cfgKeyDatabase] = name
	major := fmt.Sprintf("PostgreSQL %d.", num/10000)
	floorText := fmt.Sprintf("PostgreSQL %d or later", MinServerMajor)

	tk := newRealToolkit(t, platform, map[string]map[string]any{"old": cfg})
	if err := tk.ValidateConnection("old", cfg); err == nil || !strings.Contains(err.Error(), major) || !strings.Contains(err.Error(), floorText) {
		t.Errorf("save: err = %v, want a refusal naming %q and %q", err, major, floorText)
	}
	res := tk.ProbeConnection(context.Background(), "old")
	if res.OK || !strings.Contains(res.Detail, major) || !strings.Contains(res.Detail, fmt.Sprintf("accepts is %d", MinServerMajor)) {
		t.Errorf("probe = %+v, want a failure naming %q and the floor", res, major)
	}
	_, err := tk.Query(context.Background(), Statement{Connection: "old", SQL: "SELECT 1"})
	if err == nil || !strings.Contains(err.Error(), floorText) {
		t.Errorf("a statement: err = %v, want the floor refusal", err)
	}
}
