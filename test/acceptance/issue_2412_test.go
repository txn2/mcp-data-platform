//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq" // the audit rows are read from the platform's database
)

// Acceptance for #2412: an application exported from the dev stack and
// imported into a second deployment (a platform process of its own, with its
// own platform database and its own application database) serves its
// endpoint against the second's database; the bundle, the audit rows of the
// export and the import, and an import's refusal carry none of the source
// deployment's secrets, as written, base64, hex, URL- and JSON-escaped; and
// an import with an unmapped connection is refused naming it.
//
// Wire forms: manage_application export takes command and application as
// JSON strings and release as a JSON number; the portal import route takes
// one JSON object holding the bundle as an object and map as an object of
// strings (the MCP import takes bundle as a string and map as an object). No
// parameter admits a second form.

// second2412 is a second deployment: the platform binary on a database of its
// own, on its own port, with an application database connection of its own.
type second2412 struct {
	*issue2121Replica
	admin *client
	conn  string
	db    string
}

// startSecond2412 creates a fresh platform database and a fresh application
// database, and starts a platform on them from a copy of dev/platform.yaml.
func startSecond2412(t *testing.T) *second2412 {
	t.Helper()
	root, _ := filepath.Abs("../..")
	stamp := unique1579()
	platformDB, appDB := "mcp_platform_2412_"+stamp, "orders_2412_"+stamp
	devPSQL2412(t, "CREATE DATABASE "+platformDB+" OWNER platform")
	t.Cleanup(func() { devPSQL2412(t, "DROP DATABASE IF EXISTS "+platformDB+" WITH (FORCE)") })
	targetPSQL(t, "CREATE DATABASE "+appDB)
	t.Cleanup(func() { targetPSQL(t, "DROP DATABASE IF EXISTS "+appDB+" WITH (FORCE)") })

	raw, err := os.ReadFile(filepath.Join(root, "dev", "platform.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := strings.Replace(string(raw), "/mcp_platform?", "/"+platformDB+"?", 1)
	if cfg == string(raw) {
		t.Fatal("dev/platform.yaml names no mcp_platform database to replace")
	}
	dir := filepath.Join(root, "build", "2412")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "platform-"+stamp+".yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	bin, err := issue2121Bin()
	if err != nil {
		t.Fatal(err)
	}
	api, metrics := issue2121FreePort(t), issue2121FreePort(t)
	env := append(os.Environ(), "DEV_API_PORT="+api, "OTEL_METRICS_ADDR=127.0.0.1:"+metrics, "TRINO_ENABLED=false",
		"AWS_CA_BUNDLE="+filepath.Join(root, "dev", ".tls", "seaweedfs", "public.crt"))
	env = append(env, issue2121DotEnv(filepath.Join(root, ".env"))...)
	r := &issue2121Replica{env: env, base: "http://127.0.0.1:" + api}
	r.startWith(t, bin, cfgPath)
	t.Cleanup(r.stop)

	s := &second2412{issue2121Replica: r, db: appDB}
	s.admin = connectAtFor(t, r.base, devAPIKey(), 5*time.Minute)
	s.conn = "orders-2412-" + stamp
	path := "/api/v1/admin/connection-instances/appdb/" + s.conn
	if status, body := s.admin.rest(http.MethodPut, path, jsonBody(t, map[string]any{"config": map[string]any{
		"host": "localhost", "port": targetPort(), "database": appDB, "user": "orders_app", "password": "orders_secret",
		"sslmode": "disable", "max_conns": 4,
	}, "description": "Acceptance #2412: the second deployment's database"})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the second deployment's connection answered %d: %v", status, body)
	}
	return s
}

// startWith is start with a configuration file of the caller's.
func (r *issue2121Replica) startWith(t *testing.T, bin, cfg string) {
	t.Helper()
	root, _ := filepath.Abs("../..")
	r.log = filepath.Join(root, "build", "2412", "second-"+filepath.Base(cfg)+".log")
	logFile, err := os.Create(r.log)
	if err != nil {
		t.Fatal(err)
	}
	r.cmd = execCommand2412(bin, "--config", cfg)
	r.cmd.Dir, r.cmd.Env = root, r.env
	r.cmd.Stdout, r.cmd.Stderr = logFile, logFile
	if err := r.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = r.cmd.Wait(); _ = logFile.Close() }()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if res, err := http.Get(r.base + "/healthz"); err == nil { // #nosec G107 -- the deployment this test started
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("the second deployment did not answer within three minutes; its log is %s:\n%s", r.log, issue2121Tail(r.log))
}

// devPSQL2412 runs one statement on the dev stack's platform database server.
func devPSQL2412(t *testing.T, statement string) {
	t.Helper()
	out, err := execCommand2412("docker", "exec", "acme-dev-postgres", "psql", "-v", "ON_ERROR_STOP=1", "-At", "-U", "platform",
		"-d", "mcp_platform", "-c", statement).CombinedOutput()
	if err != nil {
		t.Fatalf("psql %q: %v: %s", statement, err, out)
	}
}

// export2412Source is the endpoint script: it answers the database it reads,
// names a stored secret by placeholder, and keeps a state value from a run.
func export2412Source(conn, schema, secret string) string {
	return fmt.Sprintf(`KEY = "{{secret:%s}}"

def handle(req):
    """Answers the database the application reads."""
    rows = platform.query("SELECT current_database() AS db, count(*) AS products FROM %s.products", connection = %q)
    return {"json": rows["rows"][0]}
`, secret, schema, conn)
}

// source2412 is an application released on the dev stack, with every secret
// the deployment holds for it planted with a canary value.
type source2412 struct {
	admin    *client
	app      string
	conn     string
	endpoint string
	schema   string
	secret   string
	canaries map[string]string
}

func newSource2412(t *testing.T) *source2412 {
	t.Helper()
	admin := connectFor(t, 5*time.Minute)
	s := &source2412{admin: admin, app: "acc2412-" + unique1579(), canaries: map[string]string{}}
	stamp := unique1579()
	// The connection password, a persona role's password and a stored
	// secret's value: the credentials an application's parts name.
	s.canaries["connection password"] = "orders_secret"
	roleCanary := "cny2412-role-" + stamp
	s.canaries["persona role password"] = roleCanary
	s.conn = "acc-2412-" + stamp
	path := "/api/v1/admin/connection-instances/appdb/" + s.conn
	if status, body := admin.rest(http.MethodPut, path, jsonBody(t, map[string]any{"config": map[string]any{
		"host": "localhost", "port": targetPort(), "database": "orders", "user": "orders_app", "password": "orders_secret",
		"sslmode": "disable", "max_conns": 4,
		"persona_roles": map[string]any{"report_viewer": "acc2412_viewer"}, "persona_role_passwords": map[string]any{"report_viewer": roleCanary},
	}, "description": "Acceptance #2412: the source"})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the source connection answered %d: %v", status, body)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, path, http.NoBody) })
	var secretValue string
	s.secret, secretValue = secret2051(t, admin, []string{"admin"}, s.conn)
	s.canaries["stored secret value"] = secretValue

	s.schema = "shop2412_" + stamp
	// The application first, holding its schema (#2539); its scripts read the
	// products table as its run role, which the migration grants it.
	admin.call("manage_application", map[string]any{"command": "create", "name": s.app, "connection": s.conn, "schema": s.schema})
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": s.app})
	})
	dropRolesLater(t, s.schema+"_write")
	admin.call("manage_schema", map[string]any{
		"command": "add", "connection": s.conn, "schema": s.schema, "version": 1,
		"name": "products", "up": "CREATE TABLE products (sku text PRIMARY KEY);\n" + writeRole(s.schema, "SELECT", "products"),
		"down": "DROP TABLE products; DROP OWNED BY " + s.schema + "_write; DROP ROLE " + s.schema + "_write",
	})
	t.Cleanup(func() { targetPSQL(t, "DROP SCHEMA IF EXISTS "+s.schema+" CASCADE") })

	s.endpoint = endpointName("acc-2412")
	api := saveEndpointScript(t, admin, s.endpoint, export2412Source(s.conn, s.schema, s.secret), `{"params": {}, "caller": None}`)
	bindEndpoint(t, admin, s.endpoint, "", map[string]any{
		"endpoint": s.endpoint, "methods": []string{"GET"}, "path": "/",
		"auth_mode": "authenticated", "callers": []string{"*"},
	})
	stateCanary := "cny2412-state-" + stamp
	s.canaries["script state"] = stateCanary
	keeper := endpointName("acc-2412-state")
	// The value reaches the state through a run's argument, never the source:
	// a script's source is what a bundle carries.
	admin.saveScript(map[string]any{
		"command": "create", "name": keeper,
		"source": "def main():\n    \"\"\"Keeps the token it is given in the script's state.\"\"\"\n" +
			"    before = run.state.get(\"token\", \"\")\n    platform.save_state({\"token\": run.params[\"token\"]})\n" +
			"    platform.result({\"kept\": True, \"had\": before != \"\"})\n",
		"params": []any{map[string]any{"name": "token", "type": "string", "required": true, "description": "The value kept."}},
	}, map[string]any{"token": "draft-token"})
	ran := admin.call("run_script", map[string]any{"name": keeper, "args": map[string]any{"token": stateCanary}, "wait_seconds": 60})
	if !strings.Contains(asJSON(ran), "succeeded") {
		t.Fatalf("the state-keeping run did not succeed: %v", ran)
	}
	keeperID, _ := admin.call("manage_script", map[string]any{"command": "get", "name": keeper})["id"].(string)

	for _, id := range []string{api, keeperID} {
		admin.call("manage_application", map[string]any{"command": "add_part", "application": s.app, "kind": "script", "part_id": id})
	}
	admin.call("manage_application", map[string]any{"command": "release", "application": s.app})
	return s
}

// export writes the release as a bundle and reads the bundle back.
func (s *source2412) export(t *testing.T) (map[string]any, []byte) {
	t.Helper()
	out := s.admin.call("manage_application", map[string]any{"command": "export", "application": s.app, "release": 1})
	landing, _ := out["bundle"].(map[string]any)
	id, _ := landing["resource_id"].(string)
	if id == "" {
		t.Fatalf("export landed no resource: %v", out)
	}
	got := endpointRequest(t, s.admin.base, http.MethodGet, "/api/v1/resources/"+id+"/content", bearer(s.admin.apiKey), "")
	if got.status != http.StatusOK {
		t.Fatalf("reading the bundle answered %d: %s", got.status, got.body)
	}
	return out, []byte(got.body)
}

// noCanary fails the test when sink holds any canary in any form.
func noCanary(t *testing.T, where string, sink []byte, canaries map[string]string) {
	t.Helper()
	text := string(sink)
	for field, value := range canaries {
		quoted, _ := json.Marshal(value)
		forms := append(forms2363(value), strings.Trim(string(quoted), `"`))
		for _, f := range forms {
			if f != "" && strings.Contains(text, f) {
				t.Errorf("%s holds the %s (as %q)", where, field, f)
			}
		}
	}
}

// importOn posts the bundle to the second deployment's portal import route.
func importOn(t *testing.T, s *second2412, bundle []byte, mapping map[string]string) (int, map[string]any) {
	t.Helper()
	var b map[string]any
	if err := json.Unmarshal(bundle, &b); err != nil {
		t.Fatalf("the bundle is not JSON: %v", err)
	}
	return s.admin.rest(http.MethodPost, "/api/v1/portal/applications/import", jsonBody(t, map[string]any{"bundle": b, "map": mapping}))
}

func TestIssue2412_ExportedAndImportedTheEndpointAnswersFromTheSecondDeploymentsDatabase(t *testing.T) {
	src := newSource2412(t)
	_, bundle := src.export(t)
	second := startSecond2412(t)
	status, out := importOn(t, second, bundle, map[string]string{src.conn: second.conn, src.secret: src.secret})
	if status != http.StatusOK {
		t.Fatalf("the import answered %d: %v", status, out)
	}
	rel, _ := out["release"].(map[string]any)
	if rel["action"] != "import" || !strings.Contains(asJSON(rel["changes"]), `"applied":[1]`) {
		t.Fatalf("the import's release did not apply the migration: %v", out)
	}
	targetPSQL(t, "SELECT 1") // the target answers
	got := endpointRequest(t, second.base, http.MethodGet, "/endpoints/"+src.endpoint+"/", bearer(second.admin.apiKey), "")
	if got.status != http.StatusOK || !strings.Contains(got.body, `"db":"`+second.db+`"`) {
		_, failures, _ := second.admin.callRaw("manage_script", map[string]any{"command": "handler_list", "name": src.endpoint, "kind": "endpoint"})
		t.Fatalf("the imported endpoint did not answer from the second deployment's database %s: %d %s\nthe binding's failures: %s",
			second.db, got.status, got.body, failures)
	}
	own := endpointRequest(t, src.admin.base, http.MethodGet, "/endpoints/"+src.endpoint+"/", bearer(src.admin.apiKey), "")
	if !strings.Contains(own.body, `"db":"orders"`) {
		t.Errorf("the source's endpoint no longer answers from its own database: %d %s", own.status, own.body)
	}
}

func TestIssue2412_TheBundleAndTheAuditCarryNoSecretOfTheSource(t *testing.T) {
	src := newSource2412(t)
	_, bundle := src.export(t)
	noCanary(t, "the bundle", bundle, src.canaries)
	if !strings.Contains(string(bundle), src.secret) || !strings.Contains(string(bundle), src.conn) {
		t.Errorf("the bundle does not name the connection and the secret it uses")
	}

	second := startSecond2412(t)
	status, refused := importOn(t, second, bundle, map[string]string{src.secret: src.secret})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("an import with an unmapped connection answered %d: %v", status, refused)
	}
	noCanary(t, "the import's refusal", []byte(asJSON(refused)), src.canaries)
	if status, out := importOn(t, second, bundle, map[string]string{src.conn: second.conn, src.secret: src.secret}); status != http.StatusOK {
		t.Fatalf("the import answered %d: %v", status, out)
	}

	for _, db := range []string{"mcp_platform", ""} {
		rows := auditRows2412(t, db, second)
		if len(rows) == 0 {
			t.Errorf("no manage_application audit rows were read from %q", db)
		}
		noCanary(t, "the audit rows of "+map[bool]string{true: "the source", false: "the second deployment"}[db != ""], []byte(strings.Join(rows, "\n")), src.canaries)
	}
}

func TestIssue2412_AnImportWithAnUnmappedConnectionIsRefusedNamingIt(t *testing.T) {
	src := newSource2412(t)
	_, bundle := src.export(t)
	second := startSecond2412(t)
	status, out := importOn(t, second, bundle, map[string]string{src.secret: src.secret})
	if status != http.StatusUnprocessableEntity || !strings.Contains(asJSON(out), `unmapped connection \"`+src.conn+`\"`) {
		t.Fatalf("an import with an unmapped connection answered %d: %v", status, out)
	}
	if status, list := second.admin.rest(http.MethodGet, "/api/v1/portal/applications", nil); status != http.StatusOK || fmt.Sprint(list["total"]) != "0" {
		t.Errorf("a refused import wrote an application: %d %v", status, list)
	}
}

// auditRows2412 reads the manage_application audit rows of the dev stack's
// platform database (db "mcp_platform") or of the second deployment's.
func auditRows2412(t *testing.T, db string, second *second2412) []string {
	t.Helper()
	ports := os.Getenv("DEV_PG_PORT")
	if ports == "" {
		ports = "5432"
	}
	name := db
	if name == "" {
		name = second.platformDB()
	}
	conn, err := sql.Open("postgres", fmt.Sprintf("postgres://platform:platform_secret@localhost:%s/%s?sslmode=disable", ports, name))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck // test
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var out []string
	deadline := time.Now().Add(20 * time.Second)
	for len(out) == 0 && time.Now().Before(deadline) {
		rows, err := conn.QueryContext(ctx, `SELECT coalesce(parameters::text, '') || ' ' || coalesce(error_message, '')
			FROM audit_logs WHERE tool_name = 'manage_application' AND timestamp > now() - interval '30 minutes'`)
		if err != nil {
			t.Fatalf("reading the audit rows of %s: %v", name, err)
		}
		for rows.Next() {
			var s string
			_ = rows.Scan(&s)
			out = append(out, s)
		}
		_ = rows.Close()
		if len(out) == 0 {
			time.Sleep(time.Second)
		}
	}
	return out
}

// platformDB is the second deployment's platform database, read from its
// configuration file's name.
func (s *second2412) platformDB() string {
	stamp := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(s.log), "second-platform-"), ".yaml.log")
	return "mcp_platform_2412_" + stamp
}

// execCommand2412 is exec.Command, named so the criteria's commands read as
// the processes this test runs.
var execCommand2412 = exec.Command
