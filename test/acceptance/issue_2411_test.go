//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2411: an application with a schema, two scripts, an
// endpoint and an asset is released, and editing a script or the asset
// afterward changes nothing it serves until the next release; a release
// whose migration fails changes nothing; a rollback puts the previous script
// version back behind the endpoint; a part belongs to one application; the
// owner edits and an administrator releases; the appdb kind still registers
// manage_schema alone; and a migration holding a literal password is refused
// at add.
//
// Wire forms: manage_application takes command, application, name,
// description, connection, schema, kind, part_id and note as JSON strings
// and to_release as a JSON number; manage_schema takes its strings and
// version as a JSON number; manage_script and manage_asset take their
// arguments as strings; the portal routes take JSON objects. No parameter
// admits a second form.

// app2411Handler answers the version written into it.
func app2411Handler(version int) string {
	return fmt.Sprintf("def handle(req):\n    \"\"\"Answers the version it was saved at.\"\"\"\n    return {\"json\": {\"v\": %d}}\n", version)
}

// application calls manage_application and fails the test on a refusal.
func (c *client) application(args map[string]any) map[string]any {
	c.t.Helper()
	return c.call("manage_application", args)
}

// applicationRefused calls manage_application and returns its refusal, failing
// the test when it is not refused.
func (c *client) applicationRefused(args map[string]any) string {
	c.t.Helper()
	res, text, err := c.callRaw("manage_application", args)
	if err != nil {
		c.t.Fatalf("manage_application %v: %v", args["command"], err)
	}
	if !res.IsError {
		c.t.Fatalf("manage_application %v was not refused: %s", args["command"], text)
	}
	return text
}

// shop2411 is a released application: a schema on its own appdb connection,
// an endpoint script answering {"v": 1}, a second script, and an asset.
type shop2411 struct {
	admin            *client
	name, conn       string
	schema, endpoint string
	api, other       string
	asset            string
}

func newShop2411(t *testing.T) *shop2411 {
	t.Helper()
	admin := connectFor(t, 5*time.Minute)
	s := &shop2411{admin: admin, name: "acc2411-" + unique1579(), schema: "acc2411_" + unique1579()}
	s.conn = appdbConn(t, admin, "acc-2411", "orders_app", "orders_secret")
	// The application first, holding its schema: a schema's migrations are
	// added only inside the application that holds it (#2539).
	admin.application(map[string]any{
		"command": "create", "name": s.name, "description": "Acceptance #2411",
		"connection": s.conn, "schema": s.schema,
	})
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": s.name})
	})
	admin.call("manage_schema", map[string]any{
		"command": "add", "connection": s.conn, "schema": s.schema, "version": 1,
		"name": "products", "up": "CREATE TABLE products (sku text PRIMARY KEY)", "down": "DROP TABLE products",
	})
	t.Cleanup(func() { targetPSQL(t, "DROP SCHEMA IF EXISTS "+s.schema+" CASCADE") })

	s.endpoint = endpointName("acc-2411")
	s.api = saveEndpointScript(t, admin, s.endpoint, app2411Handler(1), `{"params": {}, "caller": None}`)
	bindEndpoint(t, admin, s.endpoint, "", map[string]any{
		"endpoint": s.endpoint, "methods": []string{"GET"}, "path": "/",
		"auth_mode": "authenticated", "callers": []string{"*"},
	})
	otherName := endpointName("acc-2411-other")
	s.other = saveEndpointScript(t, admin, otherName, app2411Handler(9), `{"params": {}, "caller": None}`)
	saved := admin.call("save_asset", map[string]any{
		"name": s.name + " storefront", "content": "<h1>v1</h1>",
		"content_type": "text/html", "description": "Acceptance #2411: the storefront.",
	})
	s.asset, _ = saved["asset_id"].(string)
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": s.asset})
	})

	for _, p := range [][2]string{{"script", s.api}, {"script", s.other}, {"asset", s.asset}} {
		admin.application(map[string]any{"command": "add_part", "application": s.name, "kind": p[0], "part_id": p[1]})
	}
	rel := admin.application(map[string]any{"command": "release", "application": s.name, "note": "first"})
	if number(t, rel, "number") != 1 || !strings.Contains(asJSON(rel["changes"]), `"applied":[1]`) {
		t.Fatalf("the first release did not apply the migration and pin the parts: %v", rel)
	}
	return s
}

func (s *shop2411) answer(t *testing.T) string {
	t.Helper()
	got := endpointRequest(t, s.admin.base, http.MethodGet, "/endpoints/"+s.endpoint+"/", bearer(s.admin.apiKey), "")
	if got.status != http.StatusOK {
		t.Fatalf("GET /endpoints/%s/: %d %s", s.endpoint, got.status, got.body)
	}
	return strings.TrimSpace(got.body)
}

func (s *shop2411) assetContent(t *testing.T) string {
	t.Helper()
	got := endpointRequest(t, s.admin.base, http.MethodGet, "/api/v1/portal/assets/"+s.asset+"/content", bearer(s.admin.apiKey), "")
	if got.status != http.StatusOK {
		t.Fatalf("GET asset content: %d %s", got.status, got.body)
	}
	return got.body
}

// editAPI saves version 2 of the endpoint script, answering {"v": 2}.
func (s *shop2411) editAPI(t *testing.T) {
	t.Helper()
	s.admin.saveEdit(map[string]any{
		"command": "update", "name": s.endpoint,
		"source": app2411Handler(2) + draftMain(`{"params": {}, "caller": None}`),
	}, nil)
}

func TestIssue2411_EditingAScriptChangesNothingTheEndpointAnswersUntilTheNextRelease(t *testing.T) {
	s := newShop2411(t)
	if got := s.answer(t); got != `{"v":1}` {
		t.Fatalf("the released endpoint answered %s", got)
	}
	s.editAPI(t)
	if got := s.answer(t); got != `{"v":1}` {
		t.Fatalf("after the script was edited the endpoint answered %s before any release", got)
	}
	status := s.admin.application(map[string]any{"command": "get", "application": s.name})
	if pending := asJSON(status["pending"]); !strings.Contains(pending, `"from":1`) || !strings.Contains(pending, `"to":2`) {
		t.Errorf("get does not say the next release moves the script from 1 to 2: %v", status["pending"])
	}
	rel := s.admin.application(map[string]any{"command": "release", "application": s.name, "note": "second"})
	if number(t, rel, "number") != 2 {
		t.Fatalf("the second release: %v", rel)
	}
	if got := s.answer(t); got != `{"v":2}` {
		t.Fatalf("after the second release the endpoint answered %s", got)
	}
}

func TestIssue2411_TheAssetServesItsReleasedVersionUntilTheNextRelease(t *testing.T) {
	s := newShop2411(t)
	s.admin.call("manage_asset", map[string]any{"action": "update", "asset_id": s.asset, "content": "<h1>v2</h1>"})
	if got := s.assetContent(t); !strings.Contains(got, "v1") {
		t.Fatalf("an asset edited after the release served %q before the next release", got)
	}
	s.admin.application(map[string]any{"command": "release", "application": s.name})
	if got := s.assetContent(t); !strings.Contains(got, "v2") {
		t.Fatalf("after the release the asset served %q", got)
	}
}

func TestIssue2411_AReleaseWithAFailingMigrationChangesNothing(t *testing.T) {
	s := newShop2411(t)
	s.editAPI(t)
	s.admin.call("manage_schema", map[string]any{
		"command": "add", "connection": s.conn, "schema": s.schema, "version": 2,
		"name": "carts", "up": "CREATE TABLE carts (id bigint PRIMARY KEY)", "down": "DROP TABLE carts",
	})
	// The constraint holds on the empty database a migration is built on at
	// add, and fails on the live table, which holds a row it refuses: the
	// failure a release meets.
	targetPSQL(t, "INSERT INTO "+s.schema+".products VALUES ('a-long-sku')")
	s.admin.call("manage_schema", map[string]any{
		"command": "add", "connection": s.conn, "schema": s.schema, "version": 3,
		"name": "short skus", "up": "ALTER TABLE products ADD CONSTRAINT sku_short CHECK (length(sku) < 4)",
		"down": "ALTER TABLE products DROP CONSTRAINT sku_short",
	})

	refusal := s.admin.applicationRefused(map[string]any{"command": "release", "application": s.name})
	if !strings.Contains(refusal, "nothing was released") {
		t.Errorf("the refusal does not say nothing was released: %s", refusal)
	}
	if got := s.answer(t); got != `{"v":1}` {
		t.Errorf("a refused release moved the endpoint: %s", got)
	}
	got := s.admin.application(map[string]any{"command": "get", "application": s.name})
	app, _ := got["application"].(map[string]any)
	if fmt.Sprint(app["current_release"]) != "1" {
		t.Errorf("a refused release moved the current release: %v", app["current_release"])
	}
	if exists := strings.TrimSpace(targetPSQL(t, "SELECT to_regclass('"+s.schema+".carts') IS NOT NULL")); exists != "f" {
		t.Errorf("the passing migration of a refused release was applied: carts exists = %s", exists)
	}
	status := s.admin.call("manage_schema", map[string]any{"command": "status", "connection": s.conn, "schema": s.schema})
	if fmt.Sprint(status["current_version"]) != "1" {
		t.Errorf("the ledger moved: %v", status["current_version"])
	}
}

func TestIssue2411_RollingBackRestoresThePreviousScriptVersionBehindTheEndpoint(t *testing.T) {
	s := newShop2411(t)
	s.editAPI(t)
	s.admin.application(map[string]any{"command": "release", "application": s.name})
	if got := s.answer(t); got != `{"v":2}` {
		t.Fatalf("after the second release the endpoint answered %s", got)
	}
	rel := s.admin.application(map[string]any{"command": "rollback", "application": s.name, "to_release": 1})
	if rel["action"] != "rollback" || number(t, rel, "restored") != 1 {
		t.Fatalf("the rollback: %v", rel)
	}
	if got := s.answer(t); got != `{"v":1}` {
		t.Fatalf("after the rollback the endpoint answered %s", got)
	}
	list := s.admin.application(map[string]any{"command": "releases", "application": s.name})
	if rels, _ := list["releases"].([]any); len(rels) != 3 {
		t.Errorf("releases lists %d releases, not 3: %v", len(rels), list)
	}
}

func TestIssue2411_APartBelongsToOneApplication(t *testing.T) {
	s := newShop2411(t)
	other := "acc2411-other-" + unique1579()
	s.admin.application(map[string]any{"command": "create", "name": other})
	t.Cleanup(func() {
		_, _, _ = s.admin.callRaw("manage_application", map[string]any{"command": "delete", "application": other})
	})
	refusal := s.admin.applicationRefused(map[string]any{"command": "add_part", "application": other, "kind": "script", "part_id": s.api})
	if !strings.Contains(refusal, s.name) {
		t.Errorf("adding a part another application holds is not refused naming it: %s", refusal)
	}
}

func TestIssue2411_TheOwnerEditsAndOnlyAnAdministratorReleases(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	name := "acc2411-owned-" + unique1579()
	owner.application(map[string]any{"command": "create", "name": name, "description": "Acceptance #2411: owned"})
	admin := connect(t)
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": name})
	})
	script := endpointName("acc-2411-owned")
	id := saveEndpointScript(t, owner, script, app2411Handler(1), `{"params": {}, "caller": None}`)
	owner.application(map[string]any{"command": "add_part", "application": name, "kind": "script", "part_id": id})
	if refusal := owner.applicationRefused(map[string]any{"command": "release", "application": name}); !strings.Contains(refusal, "administrators") {
		t.Errorf("an owner's release is not refused for administrators: %s", refusal)
	}
	if refusal := admin.applicationRefused(map[string]any{"command": "add_part", "application": name, "kind": "script", "part_id": "11111111-1111-1111-1111-111111111111"}); refusal == "" {
		t.Error("adding a script that does not exist was not refused")
	}
	rel := admin.application(map[string]any{"command": "release", "application": name})
	if number(t, rel, "number") != 1 {
		t.Errorf("an administrator's release of the owner's application: %v", rel)
	}
	if refusal := owner.applicationRefused(map[string]any{"command": "remove_part", "application": name, "kind": "script", "part_id": id}); !strings.Contains(refusal, "administrators") {
		t.Errorf("the owner took a pinned part out from behind the release: %s", refusal)
	}
	if refusal := owner.applicationRefused(map[string]any{"command": "delete", "application": name}); !strings.Contains(refusal, "administrators") {
		t.Errorf("the owner deleted a released application: %s", refusal)
	}
	status, page := owner.rest(http.MethodGet, "/api/v1/portal/applications/"+name, nil)
	if status != http.StatusOK || page["releases"] == nil {
		t.Errorf("the portal page of the application answered %d: %v", status, page)
	}
	if status, _ := owner.rest(http.MethodPost, "/api/v1/portal/applications/"+name+"/release", jsonBody(t, map[string]any{})); status != http.StatusForbidden {
		t.Errorf("an owner's release through the portal answered %d, not 403", status)
	}
}

func TestIssue2411_TheAppdbKindRegistersManageSchemaAloneAndManageApplicationIsListed(t *testing.T) {
	c := connect(t)
	var names []string
	for _, tool := range c.tools() {
		names = append(names, tool.Name)
	}
	if !slices.Contains(names, "manage_application") || !slices.Contains(names, "manage_schema") {
		t.Fatalf("tools/list lacks manage_application or manage_schema: %v", names)
	}
	for _, n := range names {
		if strings.HasPrefix(n, "postgres_") || strings.HasPrefix(n, "appdb_") {
			t.Errorf("the appdb kind registers %s beside manage_schema", n)
		}
	}
	c.application(map[string]any{"command": "list"})
}

func TestIssue2411_AMigrationWithALiteralPasswordIsRefusedAtAdd(t *testing.T) {
	admin := connect(t)
	conn := appdbConn(t, admin, "acc-2411-pw", "orders_app", "orders_secret")
	const secret = "canary-2411-role-password"
	schema := "acc2411pw_" + unique1579()
	holdSchema(t, admin, conn, schema)
	res, text, err := admin.callRaw("manage_schema", map[string]any{
		"command": "add", "connection": conn, "schema": schema,
		"version": 1, "name": "role", "up": "CREATE ROLE acc2411 LOGIN PASSWORD '" + secret + "'",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text, "is refused") || !strings.Contains(text, "writes a credential") {
		t.Fatalf("a migration with a literal password was not refused for writing a credential: %s", text)
	}
	if strings.Contains(text, secret) {
		t.Error("the refusal repeats the password")
	}
}
