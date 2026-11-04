//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2413: an MCP client following the platform-applications
// knowledge page builds the e-commerce application with platform tools alone
// (the migrations are the page's own SQL blocks, the checkout is the built-in
// example, the API and the confirmation are the page's scripts): the release
// applies, the API lists the products, a checkout through the form creates an
// order with its lines and lowers the inventory, a checkout that would
// oversell leaves no order, the order sends its confirmation, and two
// customers see only their own orders.
//
// Wire forms: fetch takes reference and purpose as strings; manage_schema,
// manage_script, manage_application and save_asset take their arguments as
// JSON strings (version as a JSON number, template and capture as a string and
// a list); the form takes its fields as a JSON object of strings; the
// webhook source and channel routes take JSON objects. No parameter admits a
// second form.

const shopSchema2413 = "shop"

// page2413 fetches the knowledge page and returns its markdown.
func page2413(t *testing.T, c *client) string {
	t.Helper()
	page := c.call("fetch", map[string]any{
		"reference": "mcp:knowledge_page:platform-applications",
		"purpose":   "Acceptance #2413: the page an agent builds an application from.",
	})
	var walk func(v any) string
	walk = func(v any) string {
		switch x := v.(type) {
		case string:
			if strings.Contains(x, "```sql") {
				return x
			}
		case map[string]any:
			for _, e := range x {
				if s := walk(e); s != "" {
					return s
				}
			}
		case []any:
			for _, e := range x {
				if s := walk(e); s != "" {
					return s
				}
			}
		}
		return ""
	}
	body := walk(page)
	if body == "" {
		t.Fatalf("the page carries no SQL blocks: %v", page)
	}
	return body
}

// blocks2413 returns the page's fenced blocks of one language, in order.
func blocks2413(body, lang string) []string {
	re := regexp.MustCompile("(?s)```" + lang + "\n(.*?)```")
	var out []string
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

// customer2413 is a signed-in customer of the shop.
type customer2413 struct {
	key, email string
}

var (
	ann2413 = customer2413{key: devOwnerAPIKey, email: "asset.owner@example.com"}
	bob2413 = customer2413{key: "acme-peer-key", email: "asset.peer@example.com"}
)

// shop2413 is the built application.
type shop2413 struct {
	admin              *client
	conn, app          string
	checkout, api      string
	confirm, maintain  string
	storefront, source string
}

// buildShop2413 builds the shop as the page says, with platform tools.
func buildShop2413(t *testing.T) *shop2413 {
	t.Helper()
	admin := connectFor(t, 10*time.Minute)
	stamp := unique1579()
	s := &shop2413{admin: admin, app: "acc2413-shop-" + stamp}
	clean := func() {
		targetPSQL(t, "DROP SCHEMA IF EXISTS "+shopSchema2413+" CASCADE")
		targetPSQL(t, "DO $$BEGIN IF to_regclass('mcp_schema.migrations') IS NOT NULL THEN DELETE FROM mcp_schema.migrations WHERE schema_name = '"+shopSchema2413+"'; END IF; END$$")
	}
	clean()
	t.Cleanup(clean)
	// The database administrator's part: the ordinary role the scripts'
	// statements log in as, created outside any migration.
	targetPSQL(t, "DO $$BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'shop_app') THEN "+
		"CREATE ROLE shop_app LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD 'shop-app-2413'; END IF; END$$")
	s.conn = "acc-2413-" + stamp
	path := "/api/v1/admin/connection-instances/appdb/" + s.conn
	if status, out := admin.rest(http.MethodPut, path, jsonBody(t, map[string]any{"config": map[string]any{
		"host": "localhost", "port": targetPort(), "database": "orders", "user": "orders_app", "password": "orders_secret",
		"sslmode": "disable", "max_conns": 4,
		"persona_roles": map[string]any{"admin": "shop_app"}, "persona_role_passwords": map[string]any{"admin": "shop-app-2413"},
	}, "description": "Acceptance #2413: the shop's database"})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the shop's connection answered %d: %v", status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, path, http.NoBody) })
	body := page2413(t, admin)
	sqls, grants, pys := blocks2413(body, "sql"), blocks2413(body, "sql no-run"), blocks2413(body, "python")
	if len(sqls) != 3 || len(grants) != 1 || len(pys) != 3 {
		t.Fatalf("the page has %d SQL, %d grant and %d Python blocks, not 3, 1 and 3", len(sqls), len(grants), len(pys))
	}
	inConn := func(src string) string {
		return strings.Replace(src, `CONNECTION = "orders"`, fmt.Sprintf("CONNECTION = %q", s.conn), 1)
	}

	// 1. The schema: the page's migrations, the outbox the template writes,
	// and the shop's products as a last migration. Added, not applied: the
	// release applies them.
	schema := func(args map[string]any) map[string]any {
		args["connection"], args["schema"] = s.conn, shopSchema2413
		return admin.call("manage_schema", args)
	}
	downs := []string{"DROP TABLE inventory; DROP TABLE products", "DROP TABLE order_lines; DROP TABLE orders", "SELECT 1"}
	for i, up := range sqls {
		schema(map[string]any{"command": "add", "version": i + 1, "name": fmt.Sprintf("page block %d", i+1), "up": up, "down": downs[i]})
	}
	schema(map[string]any{"command": "add", "version": 4, "template": "outbox", "capture": []string{"orders"}})
	schema(map[string]any{"command": "add", "version": 5, "name": "the application's role", "up": grants[0], "down": "REVOKE ALL ON ALL TABLES IN SCHEMA shop FROM shop_app"})
	schema(map[string]any{"command": "add", "version": 6, "name": "products", "up": `INSERT INTO products (sku, name, price) VALUES ('mug', 'Mug', 12.50), ('tee', 'Tee', 20);
INSERT INTO inventory (sku, on_hand) VALUES ('mug', 5), ('tee', 1);`, "down": "DELETE FROM inventory; DELETE FROM products"})

	// 2. The checkout: the built-in example, saved with its own tests.
	example := admin.call("manage_script", map[string]any{"command": "get", "name": "example-shop-checkout"})
	src, _ := example["source"].(string)
	s.checkout = "acc2413-checkout-" + stamp
	s.saveWithItsTests(t, s.checkout, inConn(src))
	s.api = "acc2413-api-" + stamp
	s.saveWithItsTests(t, s.api, inConn(pys[1]))
	s.confirm = "acc2413-confirm-" + stamp
	s.saveWithItsTests(t, s.confirm, pys[2])
	maintain := admin.call("manage_script", map[string]any{"command": "get", "name": "example-nightly-maintenance"})
	msrc, _ := maintain["source"].(string)
	s.maintain = "acc2413-maintain-" + stamp
	// The example maintains public.orders; this shop's orders are shop.orders.
	s.saveWithItsTests(t, s.maintain, strings.ReplaceAll(strings.Replace(msrc, `CONNECTION = "orders"`, fmt.Sprintf("CONNECTION = %q", s.conn), 1),
		"public", shopSchema2413))
	admin.call("manage_script", map[string]any{"command": "schedule_set", "name": s.maintain, "cron": "0 3 * * *", "timezone": "UTC"})

	// 3. The front: the checkout as a form and the API as an endpoint, both
	// for signed-in customers; the storefront as an asset.
	bindEndpoint(t, admin, s.checkout, "", map[string]any{
		"endpoint": s.checkout, "methods": []string{"GET", "POST"},
		"auth_mode": "authenticated", "callers": []string{"*"}, "connection": formsConn,
	})
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/webhooks/sources/form-"+s.checkout, http.NoBody) })
	bindEndpoint(t, admin, s.api, "", map[string]any{
		"endpoint": s.api, "methods": []string{"GET"}, "path": "/{resource}",
		"auth_mode": "authenticated", "callers": []string{"*"},
	})
	saved := admin.call("save_asset", map[string]any{
		"name": "Shop " + stamp, "content_type": "text/html",
		"content":     `<h1>Shop</h1><p><a href="/forms/` + s.checkout + `">Check out</a></p>`,
		"description": "Acceptance #2413: the storefront.",
	})
	s.storefront, _ = saved["asset_id"].(string)
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": s.storefront})
	})

	// 7. The release: the application names its parts and applies the schema.
	admin.call("manage_application", map[string]any{
		"command": "create", "name": s.app, "connection": s.conn, "schema": shopSchema2413,
		"description": "Acceptance #2413: the shop",
	})
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_application", map[string]any{"command": "delete", "application": s.app})
	})
	for _, name := range []string{s.checkout, s.api, s.confirm, s.maintain} {
		id, _ := admin.call("manage_script", map[string]any{"command": "get", "name": name})["id"].(string)
		admin.call("manage_application", map[string]any{"command": "add_part", "application": s.app, "kind": "script", "part_id": id})
	}
	admin.call("manage_application", map[string]any{"command": "add_part", "application": s.app, "kind": "asset", "part_id": s.storefront})
	rel := admin.call("manage_application", map[string]any{"command": "release", "application": s.app, "note": "the shop"})
	if !strings.Contains(asJSON(rel["changes"]), `"applied":[1,2,3,4,5,6]`) {
		t.Fatalf("the release did not apply the six migrations: %v", rel)
	}

	// 4. The confirmation: the outbox as a webhook source the confirmation
	// script runs for, posting to an email channel.
	s.source = "acc2413-orders-" + stamp
	if status, out := admin.rest(http.MethodPost, "/api/v1/admin/webhooks/sources", jsonBody(t, map[string]any{
		"name": s.source, "connection": formsConn,
		"config": map[string]any{"input": "postgres", "postgres": map[string]any{"connection": s.conn, "mode": "outbox", "table": "shop.outbox", "poll_seconds": 1}},
	})); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("creating the outbox source answered %d: %v", status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/webhooks/sources/"+s.source, http.NoBody) })
	putChannel(t, admin, "order-confirmations", map[string]any{"kind": "email", "recipients": []string{"orders-2413@example.com"}})
	t.Cleanup(func() { deleteChannel(t, admin, "order-confirmations") })
	admin.call("manage_script", map[string]any{"command": "update", "name": s.confirm, "event_source": s.source})
	admin.call("manage_application", map[string]any{"command": "add_part", "application": s.app, "kind": "source", "part_id": s.source})
	admin.call("manage_application", map[string]any{"command": "release", "application": s.app, "note": "with the confirmation"})
	return s
}

// saveWithItsTests creates a script from source, whose own tests the save
// runs.
func (s *shop2413) saveWithItsTests(t *testing.T, name, source string) {
	t.Helper()
	out := s.admin.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source,
		"description": "Acceptance #2413: part of the shop.",
	})
	if out["status"] != "created" {
		t.Fatalf("saving %s with its tests: %v", name, out)
	}
}

// api reads the shop's API as c.
func (s *shop2413) api2413(t *testing.T, c customer2413, resource string) map[string]any {
	t.Helper()
	got := endpointRequest(t, s.admin.base, http.MethodGet, "/endpoints/"+s.api+"/"+resource, bearer(c.key), "")
	if got.status != http.StatusOK {
		t.Fatalf("GET %s as %s: %d %s", resource, c.email, got.status, got.body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("the API's %s answer is not JSON: %s", resource, got.body)
	}
	return out
}

// checkout submits the form as c.
func (s *shop2413) checkout2413(t *testing.T, c customer2413, values map[string]string) (int, map[string]any) {
	t.Helper()
	shown := showForm(t, s.admin.base, s.checkout, "", bearer(c.key))
	shown.waitToFill()
	fields := map[string]string{"form_token": shown.token}
	for k, v := range values {
		fields[k] = v
	}
	return submitForm(t, s.admin.base, s.checkout, fields, true, bearer(c.key))
}

func (s *shop2413) stock(t *testing.T, sku string) string {
	t.Helper()
	return strings.TrimSpace(targetPSQL(t, "SELECT on_hand FROM shop.inventory WHERE sku = '"+sku+"'"))
}

func orders2413(out map[string]any) []any {
	list, _ := out["orders"].([]any)
	return list
}

func TestIssue2413_AnMCPClientFollowingThePageBuildsTheShop(t *testing.T) {
	s := buildShop2413(t)

	products := s.api2413(t, ann2413, "products")
	if list, _ := products["products"].([]any); len(list) != 2 || !strings.Contains(asJSON(list), `"sku":"mug"`) {
		t.Fatalf("the API does not list the two products: %v", products)
	}

	status, out := s.checkout2413(t, ann2413, map[string]string{"sku_1": "mug", "qty_1": "2", "sku_2": "tee", "qty_2": "1"})
	if status != http.StatusOK || !strings.Contains(asJSON(out), "is placed") {
		t.Fatalf("ann's checkout answered %d: %v", status, out)
	}
	if got := s.stock(t, "mug") + "/" + s.stock(t, "tee"); got != "3/0" {
		t.Errorf("the checkout did not lower the inventory to 3 mugs and 0 tees: %s", got)
	}
	anns := orders2413(s.api2413(t, ann2413, "orders"))
	if len(anns) != 1 || !strings.Contains(asJSON(anns), `"total":"45.00"`) || !strings.Contains(asJSON(anns), `"sku":"tee"`) {
		t.Fatalf("ann's order and its lines are not what she checked out: %v", anns)
	}

	status, out = s.checkout2413(t, bob2413, map[string]string{"sku_1": "tee", "qty_1": "1"})
	if status != http.StatusUnprocessableEntity || !strings.Contains(asJSON(out), "not enough stock") {
		t.Fatalf("a checkout that would oversell answered %d: %v", status, out)
	}
	if bobs := orders2413(s.api2413(t, bob2413, "orders")); len(bobs) != 0 {
		t.Errorf("the oversold checkout left an order: %v", bobs)
	}
	if got := s.stock(t, "tee"); got != "0" {
		t.Errorf("the oversold checkout moved the stock: %s", got)
	}

	if status, out := s.checkout2413(t, bob2413, map[string]string{"sku_1": "mug", "qty_1": "1"}); status != http.StatusOK {
		t.Fatalf("bob's checkout answered %d: %v", status, out)
	}
	bobs := orders2413(s.api2413(t, bob2413, "orders"))
	anns = orders2413(s.api2413(t, ann2413, "orders"))
	if len(bobs) != 1 || len(anns) != 1 || asJSON(bobs) == asJSON(anns) {
		t.Errorf("each customer does not see only their own order: ann %v, bob %v", anns, bobs)
	}

	annID := fmt.Sprint(anns[0].(map[string]any)["id"])
	waitFor(t, "ann's order confirmation", func() (bool, string) {
		_, ok := mailpitMessageWith(t, "Order "+annID+" is placed")
		return ok, "no mail yet"
	})

	st := s.admin.call("manage_application", map[string]any{"command": "get", "application": s.app})
	app, _ := st["application"].(map[string]any)
	if fmt.Sprint(app["current_release"]) != "2" || len(st["problems"].([]any)) != 0 {
		t.Errorf("the application does not stand at release 2 with no problems: %v", st)
	}
}
