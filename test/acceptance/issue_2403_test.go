//go:build integration

package acceptance

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2403: a persona's statements log in as its own database
// role, and every transaction carries the caller's claims. Each criterion
// makes login roles and a schema of its own on the dev stack's
// postgres-target and an appdb connection mapping the admin and collaborator
// personas to them; the admin key's runs present the admin persona and the
// owner key's the collaborator persona.
//
// Wire forms: the admin connection route takes one JSON object whose
// persona_roles and persona_role_passwords are JSON objects of strings (the
// schema admits no other form); manage_script takes command, name and source
// as strings and endpoint_set's methods and callers as JSON arrays of
// strings; an endpoint request carries its API key in Authorization: Bearer.

// roles2403 creates two login roles and a schema whose table only the first
// may read, and drops them when the test ends.
func roles2403(t *testing.T) (schema, granted, refused, grantedPW, refusedPW string) {
	t.Helper()
	u := unique1579()
	schema = "acc2403_" + u
	granted, refused = "acc2403_granted_"+u, "acc2403_refused_"+u
	grantedPW, refusedPW = "pw-granted-"+u, "pw-refused-"+u
	targetPSQL(t, "CREATE ROLE "+granted+" LOGIN PASSWORD '"+grantedPW+"' NOSUPERUSER NOBYPASSRLS")
	targetPSQL(t, "CREATE ROLE "+refused+" LOGIN PASSWORD '"+refusedPW+"' NOSUPERUSER NOBYPASSRLS")
	targetPSQL(t, "CREATE SCHEMA "+schema)
	targetPSQL(t, "CREATE TABLE "+schema+".ledger (id int PRIMARY KEY, owner_id text NOT NULL, note text NOT NULL)")
	targetPSQL(t, "GRANT USAGE ON SCHEMA "+schema+" TO "+granted+", "+refused)
	targetPSQL(t, "GRANT SELECT ON "+schema+".ledger TO "+granted)
	t.Cleanup(func() {
		targetPSQL(t, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		for _, r := range []string{granted, refused} {
			targetPSQL(t, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = '"+r+"'")
			targetPSQL(t, "DROP ROLE IF EXISTS "+r)
		}
	})
	return schema, granted, refused, grantedPW, refusedPW
}

// targetPort is the host port the dev stack publishes postgres-target on.
func targetPort() string {
	if p := os.Getenv("DEV_PG_TARGET_PORT"); p != "" {
		return p
	}
	return "5442"
}

// roleConn2403 saves an appdb connection on the target's orders database
// mapping the admin persona to adminRole and the collaborator persona to
// collabRole, and returns its name.
func roleConn2403(t *testing.T, admin *client, adminRole, adminPW, collabRole, collabPW string) string {
	t.Helper()
	conn := "acc-2403-" + unique1579()
	path := "/api/v1/admin/connection-instances/appdb/" + conn
	cfg := map[string]any{
		"host": "localhost", "port": targetPort(), "database": "orders", "user": "orders_app", "password": "orders_secret",
		"sslmode": "disable", "max_conns": 2,
		"persona_roles":          map[string]any{"admin": adminRole, "collaborator": collabRole},
		"persona_role_passwords": map[string]any{"admin": adminPW, "collaborator": collabPW},
	}
	if status, body := admin.rest(http.MethodPut, path, jsonBody(t, map[string]any{"config": cfg, "description": "Acceptance #2403"})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the connection answered %d: %v", status, body)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, path, http.NoBody) })
	return conn
}

// readDraft2403 runs a draft as c that reads sql on conn and prints the
// result or the refusal as JSON.
func readDraft2403(t *testing.T, c *client, conn, sql string) map[string]any {
	t.Helper()
	out := draft2428(c, "acceptance-2403-read-"+unique1579(), `def main():
    """Reads one statement and prints what came back."""
    print(json.encode(platform.query("`+sql+`", connection="`+conn+`", on_error="return")))
`, false)
	if out["status"] != "succeeded" {
		t.Fatalf("the draft did not run: %v", out)
	}
	return out
}

func TestIssue2403_TwoPersonasMappedToTwoRolesAreGrantedAndRefusedByTheDatabase(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	schema, granted, refused, gpw, rpw := roles2403(t)
	targetPSQL(t, "INSERT INTO "+schema+".ledger VALUES (1, 'x', 'first'), (2, 'y', 'second')")
	conn := roleConn2403(t, admin, granted, gpw, refused, rpw)

	read := "SELECT count(*) AS n, current_user AS who FROM " + schema + ".ledger"
	got := readDraft2403(t, admin, conn, read)
	if log, _ := got["log"].(string); !strings.Contains(log, `"n":2`) || !strings.Contains(log, granted) {
		t.Fatalf("the admin persona's read, as %s: want 2 rows, got %s", granted, log)
	}
	got = readDraft2403(t, owner, conn, read)
	if log, _ := got["log"].(string); !strings.Contains(log, "42501") {
		t.Fatalf("the collaborator persona's read, as %s, must be refused with 42501: %s", refused, log)
	}
}

func TestIssue2403_ResetRoleUnderTheRestrictedPersonaReadsNothingMore(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	schema, granted, refused, gpw, rpw := roles2403(t)
	conn := roleConn2403(t, admin, granted, gpw, refused, rpw)

	out := draft2428(owner, "acceptance-2403-reset-"+unique1579(), `def main():
    """Resets the role, then reads who it is and the table."""
    def look(tx):
        """Runs RESET ROLE and reads in the same transaction."""
        tx.query("RESET ROLE")
        who = tx.query("SELECT current_user AS who, session_user AS s")["rows"][0]
        print(json.encode(who))
    platform.transaction("`+conn+`", look, read_only=True)
    print(json.encode(platform.query("SELECT count(*) AS n FROM `+schema+`.ledger", connection="`+conn+`", on_error="return")))
    def reset_then_read(tx):
        """Reads the table after RESET ROLE."""
        tx.query("RESET ROLE")
        return tx.query("SELECT count(*) AS n FROM `+schema+`.ledger")
    print(json.encode(platform.transaction("`+conn+`", reset_then_read, read_only=True, on_error="return")))
`, false)
	log, _ := out["log"].(string)
	if out["status"] != "succeeded" {
		t.Fatalf("the draft did not run: %v", out)
	}
	if !strings.Contains(log, `"who":"`+refused+`"`) || !strings.Contains(log, `"s":"`+refused+`"`) {
		t.Errorf("after RESET ROLE the session must still be %s, which it logged in as: %s", refused, log)
	}
	if strings.Count(log, "42501") < 2 {
		t.Errorf("the read before and after RESET ROLE must both be refused with 42501: %s", log)
	}
}

func TestIssue2403_AnRLSPolicyOnTheCallerShowsEachEndpointCallerOnlyTheirRows(t *testing.T) {
	admin := connect(t)
	schema, granted, refused, gpw, rpw := roles2403(t)
	targetPSQL(t, "ALTER TABLE "+schema+".ledger ENABLE ROW LEVEL SECURITY")
	targetPSQL(t, "CREATE POLICY own ON "+schema+".ledger TO "+granted+" USING (owner_id = nullif(current_setting('mcp.user_id', true), ''))")
	conn := roleConn2403(t, admin, granted, gpw, refused, rpw)

	ep := endpointName("acc-2403-rls")
	handler := `def handle(req):
    """Answers with the caller's claims and the rows the policy shows them."""
    claims = platform.query("SELECT current_setting('mcp.user_id', true) AS uid, current_setting('mcp.email', true) AS email, current_setting('mcp.persona', true) AS persona, current_setting('mcp.source', true) AS source, current_user AS who", connection="` + conn + `")["rows"][0]
    rows = platform.query("SELECT note FROM ` + schema + `.ledger ORDER BY id", connection="` + conn + `")["rows"]
    return {"json": {"claims": claims, "notes": [r["note"] for r in rows]}}
`
	saveEndpointScript(t, admin, ep, handler, `{"params": {}, "caller": None}`)
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_script", map[string]any{"command": "delete", "name": ep}) })
	bindEndpoint(t, admin, ep, "", map[string]any{
		"endpoint": ep, "methods": []string{"GET"}, "path": "/", "auth_mode": "authenticated", "callers": []string{"*"},
	})

	type answer struct {
		Claims map[string]string `json:"claims"`
		Notes  []string          `json:"notes"`
	}
	ask := func(key string) answer {
		got := endpointRequest(t, admin.base, http.MethodGet, "/endpoints/"+ep+"/", bearer(key), "")
		if got.status != http.StatusOK {
			t.Fatalf("GET /endpoints/%s/ with %s: %d %s", ep, key, got.status, got.body)
		}
		var a answer
		if err := json.Unmarshal([]byte(got.body), &a); err != nil {
			t.Fatalf("decoding %s: %v", got.body, err)
		}
		return a
	}
	analyst, ownerCaller := ask(analystKey1892), ask(devOwnerAPIKey)
	if analyst.Claims["uid"] == "" || analyst.Claims["uid"] == ownerCaller.Claims["uid"] {
		t.Fatalf("each signed-in caller must be their own mcp.user_id: %v vs %v", analyst.Claims, ownerCaller.Claims)
	}
	for _, a := range []answer{analyst, ownerCaller} {
		if a.Claims["source"] != "endpoint" || a.Claims["persona"] != "admin" || a.Claims["who"] != granted {
			t.Errorf("an endpoint request runs as the author's persona and role with source endpoint: %v", a.Claims)
		}
	}
	if analyst.Claims["email"] != "rachel.thompson@example.com" || ownerCaller.Claims["email"] != "asset.owner@example.com" {
		t.Errorf("mcp.email must be the caller's address: %v, %v", analyst.Claims, ownerCaller.Claims)
	}
	if len(analyst.Notes)+len(ownerCaller.Notes) != 0 {
		t.Fatalf("no row is anyone's yet: %v %v", analyst.Notes, ownerCaller.Notes)
	}

	targetPSQL(t, "INSERT INTO "+schema+".ledger VALUES (1, '"+analyst.Claims["uid"]+"', 'analyst row'), (2, '"+
		ownerCaller.Claims["uid"]+"', 'owner row'), (3, 'someone-else', 'other row'), (4, '', 'nobody row')")
	if got := ask(analystKey1892).Notes; strings.Join(got, ",") != "analyst row" {
		t.Errorf("the analyst sees %v, want only their own row", got)
	}
	if got := ask(devOwnerAPIKey).Notes; strings.Join(got, ",") != "owner row" {
		t.Errorf("the owner sees %v, want only their own row", got)
	}
}

func TestIssue2403_RoleCredentialsReachNoSink(t *testing.T) {
	admin := connectFor(t, 10*time.Minute)
	k := newCanary2363(t)
	u := unique1579()
	role := "acc2403_canary_" + u
	good := k.plant("appdb-persona-role-password")
	wrong := k.plant("appdb-persona-role-wrong-password")
	targetPSQL(t, "CREATE ROLE "+role+" LOGIN PASSWORD '"+good+"'")
	t.Cleanup(func() {
		targetPSQL(t, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = '"+role+"'")
		targetPSQL(t, "DROP ROLE IF EXISTS "+role)
	})

	for _, c := range []struct{ suffix, password string }{{"good", good}, {"wrong", wrong}} {
		conn := "acc-2403-canary-" + c.suffix + "-" + u
		path := "/api/v1/admin/connection-instances/appdb/" + conn
		cfg := map[string]any{
			"host": "localhost", "port": targetPort(), "database": "orders", "user": "orders_app", "password": "orders_secret",
			"sslmode": "disable", "persona_roles": map[string]any{"admin": role},
			"persona_role_passwords": map[string]any{"admin": c.password},
		}
		status, body := k.rest(admin, http.MethodPut, path, map[string]any{"config": cfg, "description": "Acceptance #2403 " + k.control})
		if status != http.StatusOK && status != http.StatusCreated {
			t.Fatalf("saving %s answered %d: %s", conn, status, body)
		}
		t.Cleanup(func() { admin.rest(http.MethodDelete, path, http.NoBody) })
		k.rest(admin, http.MethodGet, path, nil)
		k.rest(admin, http.MethodPost, path+"/test", map[string]any{})
		_, text := k.call(admin, "manage_script", map[string]any{"command": "run_draft", "name": "acc-2403-canary", "source": "def main():\n" +
			"    \"\"\"Reads as the admin persona's role.\"\"\"\n" +
			"    print(json.encode(platform.query(\"SELECT current_user AS who\", connection=\"" + conn + "\", on_error=\"return\")))\n"})
		if c.suffix == "good" && !strings.Contains(text, role) {
			t.Errorf("the statement did not log in as the persona's role %s: %s", role, text)
		}
		if c.suffix == "wrong" && !strings.Contains(text, "28P01") {
			t.Errorf("a wrong role password must fail the login with 28P01: %s", text)
		}
	}
	k.rest(admin, http.MethodGet, "/api/v1/admin/connection-instances", nil)
	k.rest(admin, http.MethodGet, "/api/v1/admin/connection-instances/effective", nil)
	k.rest(admin, http.MethodGet, "/api/v1/admin/config/export", nil)

	k.awaitWriters(admin)
	k.searchDatabase()
	k.searchFiles()
	k.searchResponses()
	if !k.found["table"] || !k.found["response"] {
		t.Errorf("the control value was not found in a table and a response; the search would miss a leak there: %v", k.found)
	}
}
