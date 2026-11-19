//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2709: each application environment its own schema, sites
// and data. A site environment's /endpoints run in the application
// environment of its name, or answer 404 naming it; every environment runs in
// a schema of its own, built from the last environment's structure with no
// rows, logged in as a role the database refuses on any other schema; a
// release applies migrations to the first environment's schema and a promote
// to the target's, a failure there moving nothing; coupled site parts move
// with their own environment; an agent's promote or rollback into an
// environment that requires approval is refused however it calls; an
// application whose files name its schema is refused a new environment until
// prepare saves their change; the new page teaches it; and the new
// environment's run-role password reaches no sink.
//
// Wire forms: manage_application takes command, application, environment,
// after, schema, kind, part_id, release_mode, variable and value as JSON
// strings, save as a JSON boolean; manage_schema takes command, connection,
// schema, name, up and down as strings and version as a number; manage_script
// and run_script as each documents, run_draft's application as a JSON
// string; the portal routes take JSON objects of
// strings and booleans; an endpoint request is a GET with its query string;
// api_invoke_endpoint sends the portal promote's body as an object. No
// parameter this change adds admits a second form.

// ident2709 answers the version, environment and variables it runs with.
func ident2709(version int) string {
	return fmt.Sprintf(`def handle(req):
    """Says where it runs."""
    return {"json": {"v": %d, "env": platform.environment(), "greeting": platform.env("GREETING", default="none"), "weather": platform.env("WEATHER_CONNECTION", default="none")}}
`, version)
}

// envApp2709 is an application with dev and prod, an endpoint saying where
// it runs, and a site whose dev and prod are coupled parts of it.
type envApp2709 struct {
	admin    *client
	name, ep string
	site     *site2604
}

func newEnvApp2709(t *testing.T) *envApp2709 {
	t.Helper()
	admin := connectFor(t, 6*time.Minute)
	a := &envApp2709{admin: admin, name: "acc2709-" + unique1579(), ep: endpointName("acc-2709")}
	if status := admin.restJSON(http.MethodPost, "/api/v1/portal/applications", map[string]any{
		"name": a.name, "description": "Acceptance #2709", "environments": []string{"dev", "prod"},
	}); status != http.StatusOK {
		t.Fatalf("creating the application answered %d", status)
	}
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": a.name})
	})
	id := saveEndpointScript(t, admin, a.ep, ident2709(1), sample2696)
	bindEndpoint(t, admin, a.ep, "", map[string]any{
		"endpoint": a.ep, "methods": []string{"GET"}, "path": "/", "auth_mode": "authenticated", "callers": []string{"*"},
	})
	admin.application(map[string]any{"command": "add_part", "application": a.name, "kind": "script", "part_id": id})
	for env, greeting := range map[string]string{"dev": "hello from dev", "prod": "hello from prod"} {
		admin.application(map[string]any{"command": "set_variable", "application": a.name, "variable": "GREETING", "value": greeting, "environment": env})
	}
	admin.application(map[string]any{"command": "set_variable", "application": a.name, "variable": "WEATHER_CONNECTION", "value": "weather-public"})

	a.site = newSite2604(t, keyAdmin2604, "acc2709-web-")
	a.site.share(t, a.site.key, map[string]any{"persona": personaAnalyst2604, "level": "view", "environments": []string{"dev", "prod"}})
	a.site.publish(t, a.site.key, "one")
	a.site.promote(t, "dev", "prod")
	for _, env := range []string{"dev", "prod"} {
		admin.application(map[string]any{"command": "add_part", "application": a.name, "kind": "site", "part_id": a.site.name + "/" + env, "release_mode": "coupled"})
	}
	return a
}

// onSite is what env's site address answers for the endpoint, as a signed-in
// viewer on that address.
func (a *envApp2709) onSite(t *testing.T, env, cookie string) pageResponse {
	t.Helper()
	h := withCookie2559(cookie)
	h["Sec-Fetch-Site"] = "same-origin"
	return onHost(t, a.site.host(env), http.MethodGet, "/endpoints/"+a.ep+"/", h, "")
}

// siteRelease is the release a site environment serves.
func (a *envApp2709) siteRelease(t *testing.T, env string) int {
	t.Helper()
	return a.site.release2617(t, env)
}

// Criteria 1, 2, 10 and 11: a site environment calls the application
// environment of its name, with that environment's release and variables; a
// promote changes what the prod site answers and leaves dev's; a variable with
// only a default is read in both; a release moves the dev site part only, a
// promote the prod part, a rollback in prod only prod's.
func TestIssue2709_ASiteEnvironmentCallsTheApplicationEnvironmentOfItsName(t *testing.T) {
	a := newEnvApp2709(t)
	a.admin.application(map[string]any{"command": "release", "application": a.name, "note": "one"})
	a.admin.application(map[string]any{"command": "promote", "application": a.name, "environment": "dev"})
	devSite, prodSite := a.siteRelease(t, "dev"), a.siteRelease(t, "prod")

	a.admin.saveEdit(map[string]any{"command": "update", "name": a.ep, "source": ident2709(2) + draftMain(sample2696)}, nil)
	a.site.publish(t, a.site.key, "two")
	a.admin.application(map[string]any{"command": "release", "application": a.name, "note": "two"})
	if got := a.siteRelease(t, "prod"); got != prodSite {
		t.Errorf("a release moved the prod site part from %d to %d", prodSite, got)
	}
	if got := a.siteRelease(t, "dev"); got == devSite {
		t.Errorf("a release left the dev site part at %d", got)
	}

	devCookie := signIn2604(t, a.site.host("dev"), emailKCAnalyst, passKCAnalyst)
	prodCookie := signIn2604(t, a.site.host("prod"), emailKCAnalyst, passKCAnalyst)
	dev := pollUntil2518(t, func() (pageResponse, bool) {
		r := a.onSite(t, "dev", devCookie)
		return r, r.status == http.StatusOK && strings.Contains(r.body, `"v":2`)
	})
	for _, want := range []string{`"v":2`, `"env":"dev"`, `"greeting":"hello from dev"`, `"weather":"weather-public"`} {
		if !strings.Contains(dev.body, want) {
			t.Errorf("the dev site's endpoint answered %s; want %s", dev.body, want)
		}
	}
	prod := a.onSite(t, "prod", prodCookie)
	for _, want := range []string{`"v":1`, `"env":"prod"`, `"greeting":"hello from prod"`, `"weather":"weather-public"`} {
		if prod.status != http.StatusOK || !strings.Contains(prod.body, want) {
			t.Errorf("the prod site's endpoint answered %d %s; want %s", prod.status, prod.body, want)
		}
	}

	moved := a.siteRelease(t, "dev")
	a.admin.application(map[string]any{"command": "promote", "application": a.name, "environment": "dev"})
	prod = pollUntil2518(t, func() (pageResponse, bool) {
		r := a.onSite(t, "prod", prodCookie)
		return r, strings.Contains(r.body, `"v":2`)
	})
	if !strings.Contains(prod.body, `"env":"prod"`) {
		t.Errorf("after the promote the prod site answered %s", prod.body)
	}
	if got := a.siteRelease(t, "prod"); got != moved {
		t.Errorf("the promote moved the prod site part to %d; want %d, what dev ran with the release", got, moved)
	}
	if r := a.onSite(t, "dev", devCookie); !strings.Contains(r.body, `"v":2`) || !strings.Contains(r.body, `"env":"dev"`) {
		t.Errorf("the promote changed what the dev site answers: %s", r.body)
	}

	a.admin.application(map[string]any{"command": "rollback", "application": a.name, "environment": "prod"})
	if got := a.siteRelease(t, "prod"); got != prodSite {
		t.Errorf("the rollback in prod returned the prod site part to %d; want %d", got, prodSite)
	}
	if got := a.siteRelease(t, "dev"); got != moved {
		t.Errorf("the rollback in prod moved the dev site part to %d", got)
	}
}

// Criteria 3 and 4: a site environment with no application environment of
// its name is refused as a part, through the tool and the portal, naming the
// environment to add; one renamed out of a match answers 404 naming the
// missing environment; an environment a site part calls cannot be removed.
func TestIssue2709_ASiteEnvironmentWithNoMatchIsRefusedAndAnswersNothing(t *testing.T) {
	a := newEnvApp2709(t)
	a.site.setEnv(t, a.site.key, "staging", map[string]any{})
	refusal := a.admin.applicationRefused(map[string]any{"command": "add_part", "application": a.name, "kind": "site", "part_id": a.site.name + "/staging"})
	if !strings.Contains(refusal, "add environment staging first") {
		t.Errorf("the tool's add of a part with no match: %s", refusal)
	}
	status, out := a.admin.rest(http.MethodPost, "/api/v1/portal/applications/"+a.name+"/parts",
		jsonBody(t, map[string]any{"kind": "site", "id": a.site.name + "/staging"}))
	if status != http.StatusUnprocessableEntity || !strings.Contains(asJSON(out), "add environment staging first") {
		t.Errorf("the portal's add of a part with no match: %d %v", status, out)
	}

	refusal = a.admin.applicationRefused(map[string]any{"command": "remove_environment", "application": a.name, "environment": "dev"})
	if !strings.Contains(refusal, "dev can't be removed: site part "+a.site.name+"/dev calls it") {
		t.Errorf("removing an environment a site part calls: %s", refusal)
	}

	// A middle environment that calls one of its name, then renamed out of
	// the match: the rename carries the part, and its endpoints answer 404.
	a.admin.application(map[string]any{"command": "add_environment", "application": a.name, "environment": "staging", "after": "dev"})
	a.admin.application(map[string]any{"command": "add_part", "application": a.name, "kind": "site", "part_id": a.site.name + "/staging"})
	a.admin.application(map[string]any{"command": "release", "application": a.name, "note": "one"})
	a.site.setEnv(t, a.site.key, "staging", map[string]any{"rename_to": "review"})
	cookie := signIn2604(t, a.site.host("review"), emailKCAdmin2709, passKCAdmin2709)
	r := pollUntil2518(t, func() (pageResponse, bool) {
		r := a.onSite(t, "review", cookie)
		return r, r.status == http.StatusNotFound
	})
	if !strings.Contains(r.body, a.name+" has no environment named review") {
		t.Errorf("a site environment renamed out of a match answered %d %s", r.status, r.body)
	}
	if strings.Contains(r.body, `"env":`) {
		t.Errorf("another environment ran in its place: %s", r.body)
	}
}

// The administrator's Keycloak account, which every site environment admits.
const (
	emailKCAdmin2709 = emailAdminKC2604
	passKCAdmin2709  = passAdminKC2604
)

// schemaApp2709 is an application holding a schema with a table and a row in
// prod, and endpoints that run SQL.
type schemaApp2709 struct {
	admin                *client
	name, schema, id     string
	insert, leak, report string
}

// sqlEndpoint2709 runs the statements it is built with against the
// application's connection, one call each, and answers what each returned.
func sqlEndpoint2709(statements ...string) string {
	var calls strings.Builder
	for _, st := range statements {
		b, _ := json.Marshal(st)
		fn := "platform.execute"
		if strings.HasPrefix(st, "SELECT") {
			fn = "platform.query"
		}
		fmt.Fprintf(&calls, "    out.append(%s(%s, connection = \"orders\", on_error = \"return\"))\n", fn, b)
	}
	return "def handle(req):\n    \"\"\"Runs its statements in the environment it is called in.\"\"\"\n    out = []\n" +
		calls.String() + "    return {\"json\": {\"env\": platform.environment(), \"out\": out}}\n"
}

// stub2709 is what an application's SQL script is created as: a new script
// belongs to no application, so its draft runs as no environment's role and
// an unqualified table name reaches nothing. Once it is a part, an edit gives
// it its SQL, and the edit's draft runs in the application's first
// environment (#2709).
const stub2709 = `def handle(req):
    """Answers nothing yet."""
    return {"json": {}}
`

func newSchemaApp2709(t *testing.T) *schemaApp2709 {
	t.Helper()
	admin := connectFor(t, 6*time.Minute)
	s := &schemaApp2709{admin: admin, name: app2539(t, admin, admin, "envs"), schema: schema2539(t, "envs")}
	t.Cleanup(func() { targetPSQL(t, "DROP SCHEMA IF EXISTS "+s.schema+"_dev CASCADE") })
	forgetSchemaLater(t, conn2539, s.schema+"_dev")
	addSchema2539(admin, s.name, conn2539, s.schema)
	migration2539(admin, conn2539, s.schema, 1, "CREATE TABLE invoices (id int, note text)", "DROP TABLE invoices")
	admin.application(map[string]any{"command": "release", "application": s.name, "note": "the table"})
	targetPSQL(t, "INSERT INTO "+s.schema+".invoices VALUES (1, 'prod')")
	s.id = fmt.Sprint(asMap(get2539(admin, s.name)["application"])["id"])
	return s
}

// endpoint saves an endpoint part running statements and answers its name:
// created as stub2709, made a part, then given its SQL.
func (s *schemaApp2709) endpoint(t *testing.T, label string, statements ...string) string {
	t.Helper()
	name := endpointName("acc-2709-" + label)
	id := saveEndpointScript(t, s.admin, name, stub2709, sample2696)
	bindEndpoint(t, s.admin, name, "", map[string]any{"endpoint": name, "methods": []string{"GET"}, "path": "/", "auth_mode": "authenticated", "callers": []string{"*"}})
	s.admin.application(map[string]any{"command": "add_part", "application": s.name, "kind": "script", "part_id": id})
	s.admin.saveEdit(map[string]any{"command": "update", "name": name, "source": sqlEndpoint2709(statements...) + draftMain(sample2696)}, nil)
	return name
}

// endpointAsIs saves an endpoint part whose SQL names prod's schema, as an
// application written before #2709 does: created whole, since an edit adding
// a schema's name is refused, and drafted as the connection's user, which
// reaches every schema.
func (s *schemaApp2709) endpointAsIs(t *testing.T, label string, statements ...string) string {
	t.Helper()
	name := endpointName("acc-2709-" + label)
	id := saveEndpointScript(t, s.admin, name, sqlEndpoint2709(statements...), sample2696)
	bindEndpoint(t, s.admin, name, "", map[string]any{"endpoint": name, "methods": []string{"GET"}, "path": "/", "auth_mode": "authenticated", "callers": []string{"*"}})
	s.admin.application(map[string]any{"command": "add_part", "application": s.name, "kind": "script", "part_id": id})
	return name
}

func (s *schemaApp2709) call(t *testing.T, env, endpoint string) string {
	t.Helper()
	got := endpointRequest(t, s.admin.base, http.MethodGet, "/environments/"+env+"/endpoints/"+endpoint+"/", bearer(s.admin.apiKey), "")
	if got.status != http.StatusOK {
		t.Fatalf("%s in %s answered %d %s", endpoint, env, got.status, got.body)
	}
	return got.body
}

func rows2709(t *testing.T, schema string) string {
	t.Helper()
	return strings.TrimSpace(targetPSQL(t, "SELECT count(*) FROM "+schema+".invoices"))
}

// Criteria 5, 6 and 7: add_environment builds <schema>_dev from prod's
// structure with no rows at prod's migration version, shown on the portal; a
// dev run writes dev's schema with an unqualified name and leaves prod's
// rows; a dev run naming prod's schema, literally or built at run time, is
// refused by the database and touches nothing.
func TestIssue2709_EachEnvironmentRunsInItsOwnSchema(t *testing.T) {
	s := newSchemaApp2709(t)
	added := s.admin.application(map[string]any{"command": "add_environment", "application": s.name, "environment": "dev"})
	if !strings.Contains(asJSON(added), `"schema":"`+s.schema+`_dev"`) {
		t.Fatalf("the new environment does not run in %s_dev: %s", s.schema, asJSON(added))
	}
	if got := rows2709(t, s.schema+"_dev"); got != "0" {
		t.Errorf("%s_dev holds %s rows; a new schema copies none", s.schema, got)
	}
	status, page := s.admin.rest(http.MethodGet, "/api/v1/portal/applications/"+s.name, http.NoBody)
	envs := asList(asMap(page["schema"])["environments"])
	if status != http.StatusOK || len(envs) != 2 || envs[0]["schema"] != s.schema+"_dev" || envs[0]["migration_version"] != envs[1]["migration_version"] {
		t.Errorf("the portal's page does not show dev's schema at prod's migration version: %d %v", status, envs)
	}

	insert := s.endpoint(t, "insert", "INSERT INTO invoices VALUES (7, 'dev')", "SELECT count(*) AS n FROM invoices")
	literal := s.endpointAsIs(t, "literal", "SELECT * FROM "+s.schema+".invoices", "UPDATE "+s.schema+".invoices SET note = note WHERE false")
	built := endpointName("acc-2709-built")
	// Its statement arrives at run time, in the request: nothing in its text
	// names a schema.
	id := saveEndpointScript(t, s.admin, built, `def handle(req):
    """Runs the statement the request carries."""
    sql = req["query"].get("sql", "SELECT 1")
    return {"json": {"out": platform.query(sql, connection = "orders", on_error = "return")}}
`, sample2696)
	bindEndpoint(t, s.admin, built, "", map[string]any{"endpoint": built, "methods": []string{"GET"}, "path": "/", "auth_mode": "authenticated", "callers": []string{"*"}})
	s.admin.application(map[string]any{"command": "add_part", "application": s.name, "kind": "script", "part_id": id})
	s.admin.application(map[string]any{"command": "release", "application": s.name, "note": "the endpoints"})

	// A draft of a part runs in the first environment: it writes dev's
	// schema, never prod's.
	draftBefore := rows2709(t, s.schema+"_dev")
	s.admin.call("manage_script", map[string]any{
		"command": "run_draft", "name": insert, "allow_writes": true,
		"source": sqlEndpoint2709("INSERT INTO invoices VALUES (8, 'draft')") + draftMain(sample2696),
	})
	if rows2709(t, s.schema+"_dev") == draftBefore || rows2709(t, s.schema) != "1" {
		t.Errorf("a draft of a part wrote dev %s (was %s) and prod %s rows; want dev's only", rows2709(t, s.schema+"_dev"), draftBefore, rows2709(t, s.schema))
	}
	before := rows2709(t, s.schema+"_dev")
	body := s.call(t, "dev", insert)
	after := rows2709(t, s.schema+"_dev")
	if !strings.Contains(body, `"env":"dev"`) || after == before || rows2709(t, s.schema) != "1" {
		t.Errorf("an unqualified insert in dev: %s; dev held %s rows and holds %s, prod %s", body, before, after, rows2709(t, s.schema))
	}
	for ep, query := range map[string]string{literal: "", built: "?sql=" + url.QueryEscape("SELECT * FROM "+s.schema+".invoices")} {
		got := endpointRequest(t, s.admin.base, http.MethodGet, "/environments/dev/endpoints/"+ep+"/"+query, bearer(s.admin.apiKey), "")
		if !strings.Contains(got.body, "permission denied for schema "+s.schema) {
			t.Errorf("%s in dev was not refused on prod's schema: %d %s", ep, got.status, got.body)
		}
	}
	if got := rows2709(t, s.schema); got != "1" {
		t.Errorf("prod's schema holds %s rows after dev reached for it", got)
	}
}

// Criterion 8: a release applies a new migration to dev's schema only; a
// promote applies it to prod's; a migration that fails in prod refuses the
// promote and leaves prod's release, ledger and schema as they were.
func TestIssue2709_MigrationsApplyToEachEnvironmentsSchema(t *testing.T) {
	s := newSchemaApp2709(t)
	s.admin.application(map[string]any{"command": "add_environment", "application": s.name, "environment": "dev"})
	column := func(schema string) string {
		return strings.TrimSpace(targetPSQL(t, "SELECT count(*) FROM information_schema.columns WHERE table_schema = '"+schema+"' AND column_name = 'amount'"))
	}
	migration2539(s.admin, conn2539, s.schema, 2, "ALTER TABLE invoices ADD COLUMN amount numeric", "ALTER TABLE invoices DROP COLUMN amount")
	s.admin.application(map[string]any{"command": "release", "application": s.name, "note": "amount"})
	if column(s.schema+"_dev") != "1" || column(s.schema) != "0" {
		t.Fatalf("a release applied migration 2 to dev %s, prod %s; want dev only", column(s.schema+"_dev"), column(s.schema))
	}
	promoted := s.admin.application(map[string]any{"command": "promote", "application": s.name, "environment": "dev"})
	if column(s.schema) != "1" || !strings.Contains(asJSON(promoted), `"applied":[2]`) {
		t.Fatalf("the promote did not apply migration 2 to prod: %s", asJSON(promoted))
	}

	targetPSQL(t, "CREATE TABLE "+s.schema+".extra (id int)")
	migration2539(s.admin, conn2539, s.schema, 3, "CREATE TABLE extra (id int)", "DROP TABLE extra")
	s.admin.application(map[string]any{"command": "release", "application": s.name, "note": "extra"})
	before := asJSON(get2539(s.admin, s.name)["application"])
	refusal := s.admin.applicationRefused(map[string]any{"command": "promote", "application": s.name, "environment": "dev"})
	if !strings.Contains(refusal, "nothing was promoted") {
		t.Errorf("a promote whose migration fails in prod: %s", refusal)
	}
	after := asJSON(get2539(s.admin, s.name)["application"])
	if !strings.Contains(after, `"name":"prod","release":2`) || !strings.Contains(before, `"name":"prod","release":2`) {
		t.Errorf("prod's release moved: before %s after %s", before, after)
	}
	if got := strings.TrimSpace(targetPSQL(t, "SELECT max(version) FROM mcp_schema.migrations WHERE schema_name = '"+s.schema+"'")); got != "2" {
		t.Errorf("prod's ledger records version %s; want 2", got)
	}
}

// Criterion 9: a schema- or role-qualified name in new migration or script
// text is refused at save; a script that already holds one still runs and
// saves an edit that adds none.
func TestIssue2709_ANewQualifiedNameIsRefusedAtSave(t *testing.T) {
	s := newSchemaApp2709(t)
	refusal := refused2539(t, s.admin, "manage_schema", map[string]any{
		"command": "add", "connection": conn2539, "schema": s.schema, "version": 2, "name": "q",
		"up": "CREATE TABLE " + s.schema + ".refunds (id int)", "down": "DROP TABLE refunds",
	})
	if !strings.Contains(refusal, "names refunds in schema "+s.schema) {
		t.Errorf("a schema-qualified migration: %s", refusal)
	}
	refusal = refused2539(t, s.admin, "manage_schema", map[string]any{
		"command": "add", "connection": conn2539, "schema": s.schema, "version": 2, "name": "r",
		"up": "CREATE TABLE refunds (id int);\nGRANT SELECT ON refunds TO " + s.schema + "_read", "down": "DROP TABLE refunds",
	})
	if !strings.Contains(refusal, "names role "+s.schema+"_read") {
		t.Errorf("a migration naming a role: %s", refusal)
	}

	legacy := endpointName("acc-2709-legacy")
	src := sqlEndpoint2709("SELECT count(*) AS n FROM " + s.schema + ".invoices")
	id := saveEndpointScript(t, s.admin, legacy, src, sample2696)
	bindEndpoint(t, s.admin, legacy, "", map[string]any{"endpoint": legacy, "methods": []string{"GET"}, "path": "/", "auth_mode": "authenticated", "callers": []string{"*"}})
	s.admin.application(map[string]any{"command": "add_part", "application": s.name, "kind": "script", "part_id": id})
	s.admin.application(map[string]any{"command": "release", "application": s.name, "note": "legacy"})
	if body := s.call(t, "prod", legacy); !strings.Contains(body, `"n":1`) && !strings.Contains(body, `"n": 1`) {
		t.Errorf("a script holding its schema's name no longer runs in prod: %s", body)
	}
	// saveEdit fails the test when the save is refused.
	s.admin.saveEdit(map[string]any{"command": "update", "name": legacy, "source": src + "\n# a note that adds no schema\n" + draftMain(sample2696)}, nil)
	ok, text := s.admin.callRaw2539("manage_script", map[string]any{
		"command": "update", "name": legacy,
		"source": sqlEndpoint2709("SELECT count(*) AS n FROM "+s.schema+".invoices", "SELECT 1 FROM "+s.schema+".refunds") + draftMain(sample2696),
	})
	_ = ok // a refused save answers as a result, not an error
	if !strings.Contains(text, "names the schema "+s.schema+". Inside an application write refunds") || strings.Contains(text, `"status":"updated"`) {
		t.Errorf("an edit adding a schema-qualified name: %s", firstRunes(text, 400))
	}
}

// Criterion 12: with prod requiring approval, the portal promote and rollback
// over platform-admin and from a managed script, and manage_application,
// answer 409 APPROVAL_REQUIRED and prod does not move; a person in the
// portal carries the promote out.
func TestIssue2709_AnAgentWaitsOnApprovalOnEverySurface(t *testing.T) {
	admin := connectFor(t, 5*time.Minute)
	a := newApp2696(t)
	a.admin.application(map[string]any{"command": "set_environment", "application": a.name, "environment": "prod", "requires_approval": true})
	a.admin.application(map[string]any{"command": "release", "application": a.name, "note": "one"})
	base := "/api/v1/portal/applications/" + a.name + "/environments/"
	for _, path := range []string{base + "dev/promote", base + "prod/rollback"} {
		text, waits := invoke2656(t, admin, http.MethodPost, path, map[string]any{})
		if !waits || !strings.Contains(text, "APPROVAL_REQUIRED: environment prod of application "+a.name) {
			t.Errorf("%s over platform-admin: %s", path, firstRunes(text, 400))
		}
	}
	source := fmt.Sprintf(`def main():
    """Promotes an application's dev to prod through platform-admin."""
    res = platform.call("api_invoke_endpoint", {
        "connection": "platform-admin", "method": "POST", "path": %q, "body": {},
        "headers": {"Content-Type": "application/json"},
        "purpose": "Acceptance #2709: a saved script promotes an application.",
    }, on_error = "return")
    platform.result({"status": res.get("status", 0), "text": str(res)[:600]})
`, base+"dev/promote")
	name := fmt.Sprintf("acc-2709-promote-%d", time.Now().UnixNano())
	admin.saveScript(map[string]any{"command": "create", "name": name, "description": "Acceptance #2709: a script promotes over platform-admin.", "source": source}, nil)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	res := result2086(t, admin, name)
	if status, _ := res["status"].(float64); status != http.StatusConflict || !strings.Contains(fmt.Sprint(res["text"]), "APPROVAL_REQUIRED") {
		t.Errorf("a script's promote over platform-admin: %v", res)
	}
	if refusal := a.admin.applicationRefused(map[string]any{"command": "promote", "application": a.name, "environment": "dev"}); !strings.Contains(refusal, "APPROVAL_REQUIRED") {
		t.Errorf("manage_application promote: %s", refusal)
	}
	if got := asJSON(a.admin.application(map[string]any{"command": "get", "application": a.name})); !strings.Contains(got, `"name":"prod","release":0`) {
		t.Fatalf("prod moved on an agent's word: %s", got)
	}
	if status := admin.restJSON(http.MethodPost, base+"dev/promote", map[string]any{}); status != http.StatusOK {
		t.Errorf("a person's promote in the portal answered %d", status)
	}
}

// Criteria 13 and 14: an application whose scripts and pending migrations
// name its schema keeps running as one prod environment and is refused
// add_environment naming the counts; prepare lists the files with their
// change and replay, saving makes new versions, and after releasing them to
// prod add_environment succeeds with dev built from prod's structure.
func TestIssue2709_PrepareBringsAnExistingApplicationToTwoEnvironments(t *testing.T) {
	s := newSchemaApp2709(t)
	legacy := endpointName("acc-2709-old")
	src := sqlEndpoint2709("SELECT count(*) AS n FROM " + s.schema + ".invoices")
	id := saveEndpointScript(t, s.admin, legacy, src, sample2696)
	bindEndpoint(t, s.admin, legacy, "", map[string]any{"endpoint": legacy, "methods": []string{"GET"}, "path": "/", "auth_mode": "authenticated", "callers": []string{"*"}})
	s.admin.application(map[string]any{"command": "add_part", "application": s.name, "kind": "script", "part_id": id})
	s.admin.application(map[string]any{"command": "release", "application": s.name, "note": "the old script"})
	if body := s.call(t, "prod", legacy); !strings.Contains(body, `"env":"prod"`) {
		t.Fatalf("the application does not run as one prod environment: %s", body)
	}

	refusal := s.admin.applicationRefused(map[string]any{"command": "add_environment", "application": s.name, "environment": "dev"})
	if !strings.Contains(refusal, "1 scripts, 0 libraries and 0 pending migrations name schema "+s.schema) {
		t.Fatalf("add_environment on an unprepared application: %s", refusal)
	}
	plan := s.admin.application(map[string]any{"command": "prepare", "application": s.name})
	files := asList(plan["files"])
	if len(files) != 1 || files[0]["name"] != legacy || !strings.Contains(fmt.Sprint(files[0]["after"]), "FROM invoices") {
		t.Fatalf("prepare lists %v", asJSON(plan))
	}
	saved := s.admin.application(map[string]any{"command": "prepare", "application": s.name, "save": true})
	if saved["saved"] != true || fmt.Sprint(asList(saved["files"])[0]["saved"]) == "0" {
		t.Fatalf("prepare did not save a new version: %s", asJSON(saved))
	}
	s.admin.application(map[string]any{"command": "release", "application": s.name, "note": "prepared"})
	if body := s.call(t, "prod", legacy); !strings.Contains(body, `"n":1`) && !strings.Contains(body, `"n": 1`) {
		t.Errorf("the prepared script answers differently in prod: %s", body)
	}
	added := s.admin.application(map[string]any{"command": "add_environment", "application": s.name, "environment": "dev"})
	if !strings.Contains(asJSON(added), `"schema":"`+s.schema+`_dev"`) {
		t.Errorf("add_environment after prepare: %s", asJSON(added))
	}
	if got := strings.TrimSpace(targetPSQL(t, "SELECT count(*) FROM information_schema.tables WHERE table_schema = '"+s.schema+"_dev' AND table_name = 'invoices'")); got != "1" {
		t.Errorf("dev's schema lacks prod's table: %s", got)
	}
}

// Criterion 18: prepare rewrites a name only where SQL names an object with
// it. A qualified name inside a quoted SQL value is data, left as written and
// listed under values; a file whose name starts with the schema's is not SQL
// and is left alone; neither keeps the application from an environment.
func TestIssue2709_PrepareLeavesAQuotedValueAndAFileAlone(t *testing.T) {
	s := newSchemaApp2709(t)
	name := endpointName("acc-2709-values")
	src := sqlEndpoint2709("SELECT count(*) AS n FROM "+s.schema+".invoices WHERE note != '"+s.schema+".invoices'") +
		"\nSTYLE = \"/_static/" + s.schema + ".css\"\n"
	id := saveEndpointScript(t, s.admin, name, src, sample2696)
	bindEndpoint(t, s.admin, name, "", map[string]any{"endpoint": name, "methods": []string{"GET"}, "path": "/", "auth_mode": "authenticated", "callers": []string{"*"}})
	s.admin.application(map[string]any{"command": "add_part", "application": s.name, "kind": "script", "part_id": id})

	plan := s.admin.application(map[string]any{"command": "prepare", "application": s.name})
	files := asList(plan["files"])
	if len(files) != 1 {
		t.Fatalf("prepare lists %s", asJSON(plan))
	}
	after := fmt.Sprint(files[0]["after"])
	if !strings.Contains(after, "FROM invoices WHERE note != '"+s.schema+".invoices'") {
		t.Errorf("the table is not unqualified, or the quoted value was rewritten: %s", after)
	}
	if !strings.Contains(after, `"/_static/`+s.schema+`.css"`) {
		t.Errorf("the file was rewritten: %s", after)
	}
	values := asList(plan["values"])
	if len(values) != 1 || values[0]["text"] != s.schema+".invoices" || values[0]["name"] != name {
		t.Errorf("the quoted value is not listed once: %s", asJSON(plan["values"]))
	}
	saved := s.admin.application(map[string]any{"command": "prepare", "application": s.name, "save": true})
	if saved["saved"] != true {
		t.Fatalf("prepare did not save: %s", asJSON(saved))
	}
	again := s.admin.application(map[string]any{"command": "prepare", "application": s.name})
	if again["ready"] != true || len(asList(again["files"])) != 0 {
		t.Errorf("after the save the application is not ready, or still lists files: %s", asJSON(again))
	}
}

// Criterion 19: a script not yet a part of an application drafts in the
// application it names, as its first environment's role with that schema as
// its search path, so its unqualified SQL reads that environment's table;
// without the name the draft belongs to no application and the table is not
// found.
func TestIssue2709_ADraftNamesTheApplicationItRunsIn(t *testing.T) {
	s := newSchemaApp2709(t)
	src := "def main():\n    \"\"\"Counts the invoices.\"\"\"\n" +
		"    print(json.encode(platform.query(\"SELECT count(*) AS n FROM invoices\", connection = \"orders\")[\"rows\"]))\n"
	name := "acc-2709-draft-" + unique1579()
	in := asJSON(s.admin.call("manage_script", map[string]any{"command": "run_draft", "name": name, "source": src, "application": s.name}))
	if !strings.Contains(in, `\"n\":1`) && !strings.Contains(in, `"n":1`) {
		t.Errorf("the draft in the application did not read its first environment's table: %s", in)
	}
	_, alone, _ := s.admin.callRaw("manage_script", map[string]any{"command": "run_draft", "name": name, "source": src})
	if !strings.Contains(alone, "42P01 (undefined_table)") {
		t.Errorf("a draft in no application found the table: %s", alone)
	}
	_, refused, _ := s.admin.callRaw("manage_script", map[string]any{"command": "run_draft", "name": name, "source": src, "application": "acc-2709-no-such-app"})
	if !strings.Contains(refused, `no application`) {
		t.Errorf("a draft naming no application that exists: %s", refused)
	}
}

// Criteria 15 and 16: the new page answers with its sections and no
// schema-qualified example, and manage_application carries the ENVIRONMENTS
// paragraph pointing to it.
func TestIssue2709_AgentsAreToldThePage(t *testing.T) {
	c := connect(t)
	page := asJSON(c.call("fetch", map[string]any{"reference": "mcp:knowledge_page:platform-application-environments", "purpose": "Reading how application environments work."}))
	for _, section := range []string{
		"What an environment is", "Same code, different systems", "A script that runs in every environment", "Migrations",
		"Sites in front of an application", "Promote and roll back", "Bringing an existing application to two environments", "Before promoting to prod", "Refusals",
	} {
		if !strings.Contains(page, section) {
			t.Errorf("the page lacks the section %q", section)
		}
	}
	tools := asJSON(c.tools())
	if !strings.Contains(tools, "ALWAYS its own schema") || !strings.Contains(tools, "platform-application-environments") {
		t.Error("manage_application's description lacks the ENVIRONMENTS paragraph naming the page")
	}
}

// Criterion 17: the dev environment's run-role password reaches no response,
// audit row, call record, run record, log or refusal of this change.
func TestIssue2709_TheEnvironmentsRunRolePasswordReachesNoSink(t *testing.T) {
	start := time.Now().UTC()
	s := newSchemaApp2709(t)
	added := s.admin.application(map[string]any{"command": "add_environment", "application": s.name, "environment": "dev"})
	devSchema := ""
	for _, e := range asList(asMap(get2539(s.admin, s.name)["schema"])["environments"]) {
		if e["environment"] == "dev" {
			devSchema = fmt.Sprint(e["schema"])
		}
	}
	role := strings.TrimSpace(platformText2539(t, "SELECT role_name FROM application_run_roles WHERE application_id = $1 AND schema_name = $2", s.id, devSchema))
	dropRolesLater(t, role)
	password := runRolePasswordIn2709(t, s.id, devSchema, role)
	canaries := map[string]string{"dev run role password": password}

	insert := s.endpoint(t, "sink", "INSERT INTO invoices VALUES (9, 'sink')", "SELECT current_user AS who")
	s.admin.application(map[string]any{"command": "release", "application": s.name, "note": "sink"})
	ran := s.call(t, "dev", insert)
	refusals := []string{
		s.admin.applicationRefused(map[string]any{"command": "add_environment", "application": s.name, "environment": "dev"}),
		s.admin.applicationRefused(map[string]any{"command": "remove_environment", "application": s.name, "environment": "prod"}),
	}
	_, portal := s.admin.rest(http.MethodGet, "/api/v1/portal/applications/"+s.name, http.NoBody)
	sinks := map[string]string{
		"add_environment answer": asJSON(added), "manage_application get": asJSON(get2539(s.admin, s.name)),
		"portal application page": asJSON(portal), "the dev run": ran, "refusals": strings.Join(refusals, "\n"),
		"audit rows": platformText2539(t, `SELECT coalesce(string_agg(coalesce(parameters::text, '') || ' ' || coalesce(error_message, ''), E'\n'), '')
			FROM audit_logs WHERE timestamp >= $1`, start),
		"call records":  platformText2539(t, `SELECT coalesce(string_agg(row_to_json(c)::text, E'\n'), '') FROM call_records c WHERE c.created_at >= $1`, start),
		"run records":   platformText2539(t, `SELECT coalesce(string_agg(row_to_json(r)::text, E'\n'), '') FROM script_runs r WHERE r.created_at >= $1`, start),
		"platform logs": platformLogs2709(t),
	}
	for where, sink := range sinks {
		noCanary(t, where, []byte(sink), canaries)
	}
}

// runRolePasswordIn2709 opens the stored password of the run role of schema.
func runRolePasswordIn2709(t *testing.T, appID, schema, role string) string {
	t.Helper()
	if role == "" || schema == "" {
		t.Fatalf("no run role recorded for schema %q", schema)
	}
	return runRolePassword2539(t, appID, schema, role)
}

// platformLogs2709 is the dev stack's platform log.
func platformLogs2709(t *testing.T) string {
	t.Helper()
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
	return logs
}
