//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1906: a script version's flow graph, derived from its source and
// served at GET /api/v1/portal/scripts/{id}/versions/{version}/graph (and the
// admin counterpart), which the script page's Flow tab draws.
//
// What these hold, against the running platform: the ticket's three-step
// script draws its three steps, the registered table and the edges between
// them; a helper called twice is two steps in one box; a one-call wrapper is a
// chip; a module constant is shown by value and manage_script validate reports
// it; a computed destination is marked and never guessed; an implied edge is
// not drawn; a parameter reaches exactly its steps; the state edge goes back to
// the next run; a new version adding an export gains exactly that card; a
// stored version that no longer parses answers its findings; a script with no
// platform calls answers empty lists, never null; and a reader who does not
// own the script reads the graph as they read the source.
//
// Wire forms: the graph route takes two path parameters, each a string in the
// URL, so each is sent once in that form. manage_script's `command`, `name`,
// `source` and `description` are typed string in its schema and are sent once
// as that literal.

// script1906 creates a script owned by c's identity and returns its id. The
// script is deleted when the test ends.
func script1906(t *testing.T, c *client, label, source string) (id, name string) {
	t.Helper()
	name = fmt.Sprintf("acc-1906-%s-%d", label, time.Now().UnixNano())
	created := c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source,
		"description": "Acceptance #1906: a script the Flow tab draws.",
	})
	id, _ = created["id"].(string)
	if id == "" {
		t.Fatalf("manage_script create returned no id: %v", created)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})
	return id, name
}

// graph1906 is one version's graph as the portal serves it.
type graph1906 struct {
	raw    map[string]any
	nodes  []map[string]any
	edges  []map[string]any
	groups []map[string]any
	params []map[string]any
}

func readGraph1906(t *testing.T, c *client, path string) graph1906 {
	t.Helper()
	status, out := c.rest(http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d: %v", path, status, out)
	}
	list := func(key string) []map[string]any {
		raw, ok := out[key].([]any)
		if !ok {
			t.Fatalf("%s is not a list (null is not an empty list): %v", key, out[key])
		}
		items := make([]map[string]any, 0, len(raw))
		for _, r := range raw {
			m, _ := r.(map[string]any)
			items = append(items, m)
		}
		return items
	}
	return graph1906{raw: out, nodes: list("nodes"), edges: list("edges"), groups: list("groups"), params: list("params")}
}

func portalGraph1906(t *testing.T, c *client, id string, version int) graph1906 {
	t.Helper()
	g := readGraph1906(t, c, fmt.Sprintf("/api/v1/portal/scripts/%s/versions/%d/graph", id, version))
	if ok, _ := g.raw["ok"].(bool); !ok {
		t.Fatalf("the graph is not ok: %v", g.raw["findings"])
	}
	return g
}

// titled finds the one node whose title starts with prefix.
func (g graph1906) titled(t *testing.T, prefix string) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, n := range g.nodes {
		if title, _ := n["title"].(string); strings.HasPrefix(title, prefix) {
			found = append(found, n)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d nodes titled %q in %v", len(found), prefix, g.titles())
	}
	return found[0]
}

func (g graph1906) titles() []string {
	out := make([]string, 0, len(g.nodes))
	for _, n := range g.nodes {
		title, _ := n["title"].(string)
		out = append(out, title)
	}
	return out
}

func (g graph1906) hasEdge(from, to any) bool {
	for _, e := range g.edges {
		if e["from"] == from && e["to"] == to {
			return true
		}
	}
	return false
}

const threeSteps1906 = `
orders = platform.query("SELECT id, total FROM acme.sales.orders", connection="acme")
ids = [r["id"] for r in orders["rows"]]
people = platform.call("api_invoke_endpoint", {
    "connection": "api-test-fixture",
    "method": "GET",
    "path": "/people",
    "purpose": "Acceptance #1906: look up the people behind the orders.",
    "query": {"ids": ids},
})
platform.export("orders-people", people["rows"], format="csv", destination="resources",
                key="acc-1906/orders.csv", register={"connection": "acme", "table_name": "acc_1906_orders"})
`

func TestIssue1906_TheThreeStepsTheTableAndTheEdgesBetweenThem(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, _ := script1906(t, c, "three", threeSteps1906)
	g := portalGraph1906(t, c, id, 1)

	q := g.titled(t, "Query acme")
	if detail, _ := q["detail"].([]any); len(detail) != 1 || detail[0] != "acme.sales.orders" {
		t.Errorf("the query reaches %v, want [acme.sales.orders]", q["detail"])
	}
	api := g.titled(t, "API api-test-fixture")
	if api["subtitle"] != "GET /people" || api["purpose"] != "Acceptance #1906: look up the people behind the orders." {
		t.Errorf("the API card says %v / %v", api["subtitle"], api["purpose"])
	}
	exp := g.titled(t, "Export CSV to resources")
	tbl := g.titled(t, "Table on acme")
	if tbl["subtitle"] != "acc_1906_orders" {
		t.Errorf("the registered table is %v", tbl["subtitle"])
	}
	for _, e := range [][2]map[string]any{{q, api}, {api, exp}, {exp, tbl}} {
		if !g.hasEdge(e[0]["id"], e[1]["id"]) {
			t.Errorf("no edge %v -> %v in %v", e[0]["title"], e[1]["title"], g.edges)
		}
	}
	// An edge from A to C is not drawn when A to B and B to C are.
	if g.hasEdge(q["id"], exp["id"]) {
		t.Errorf("the query -> export edge is implied by query -> API -> export and is drawn anyway")
	}
	if len(g.edges) != 3 {
		t.Errorf("%d edges, want 3: %v", len(g.edges), g.edges)
	}
}

const helper1906 = `
def fetch(path, why):
    return platform.call("api_invoke_endpoint", {"connection": "api-test-fixture", "method": "GET", "path": path, "purpose": why})

# Pull both lists and write them out.
def pull():
    a = fetch("/people", "Acceptance #1906: read the people.")
    b = fetch("/orders", "Acceptance #1906: read the orders.")
    return a["rows"] + b["rows"]

def report(rows):
    platform.export("both", rows, format="csv")

report(pull())
`

func TestIssue1906_AHelperCalledTwiceIsTwoStepsInOneBoxAndAWrapperIsAChip(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, _ := script1906(t, c, "helper", helper1906)
	g := portalGraph1906(t, c, id, 1)

	var apis []map[string]any
	for _, n := range g.nodes {
		if n["kind"] == "api" {
			apis = append(apis, n)
		}
	}
	if len(apis) != 2 {
		t.Fatalf("%d API steps, want 2: %v", len(apis), g.titles())
	}
	want := []struct{ path, purpose string }{
		{"GET /people", "Acceptance #1906: read the people."},
		{"GET /orders", "Acceptance #1906: read the orders."},
	}
	for i, n := range apis {
		if n["subtitle"] != want[i].path || n["purpose"] != want[i].purpose {
			t.Errorf("step %d says %v / %v, want %v", i, n["subtitle"], n["purpose"], want[i])
		}
		if n["group"] != "/pull" {
			t.Errorf("step %d is in box %v, want /pull", i, n["group"])
		}
		if n["wrapper"] != "fetch" {
			t.Errorf("step %d carries wrapper %v, want the fetch chip", i, n["wrapper"])
		}
	}
	for _, gr := range g.groups {
		if gr["id"] == "/fetch" {
			t.Errorf("the one-call wrapper fetch is drawn as a box: %v", gr)
		}
	}
	var pull map[string]any
	for _, gr := range g.groups {
		if gr["id"] == "/pull" {
			pull = gr
		}
	}
	if pull == nil || pull["caption"] != "Pull both lists and write them out." || pull["label"] != "pull()" {
		t.Errorf("the pull box is %v", pull)
	}
}

const constant1906 = `
WAREHOUSE = "acme"
rows = platform.query("SELECT 1 AS n", connection=WAREHOUSE)
platform.export("one", rows["rows"], format="csv")
`

func TestIssue1906_AModuleConstantIsShownByValueAndValidateReportsIt(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, _ := script1906(t, c, "const", constant1906)
	g := portalGraph1906(t, c, id, 1)
	q := g.titled(t, "Query acme")
	if computed, _ := q["computed"].(bool); computed {
		t.Errorf("a query naming its connection through a module constant is marked computed")
	}

	report := c.call("manage_script", map[string]any{"command": "validate", "source": constant1906})
	conns, _ := report["connections"].([]any)
	if len(conns) != 1 || conns[0] != "acme" {
		t.Errorf("validate reports connections %v, want [acme]", report["connections"])
	}
	if report["dynamic_connections"] != false {
		t.Errorf("validate reports dynamic_connections %v, want false", report["dynamic_connections"])
	}
}

func TestIssue1906_AComputedDestinationIsMarkedNeverGuessed(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, _ := script1906(t, c, "dest", `
where = run.params.get("where", "portal")
platform.export("out", [], format="csv", destination=where, key="acc-1906/out.csv")
`)
	g := portalGraph1906(t, c, id, 1)
	exp := g.titled(t, "Export CSV to")
	if computed, _ := exp["computed"].(bool); !computed {
		t.Errorf("an export computing its destination is not marked computed: %v", exp)
	}
	if title, _ := exp["title"].(string); !strings.HasPrefix(title, "Export CSV to {run.params.get(") {
		t.Errorf("the computed destination is shown as %q, want {its source} and no name", title)
	}
}

func TestIssue1906_AParameterReachesExactlyItsSteps(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, _ := script1906(t, c, "param", `
day = run.params.get("day", "2026-01-01")
mode = run.params.get("mode", "full")
rows = platform.query("SELECT 1 AS n WHERE '" + day + "' <> ''", connection="acme")
if mode == "full":
    platform.export("all", rows["rows"], format="csv")
platform.query("SELECT 2 AS n", connection="acme")
`)
	g := portalGraph1906(t, c, id, 1)
	byName := map[string]map[string]any{}
	for _, p := range g.params {
		name, _ := p["name"].(string)
		byName[name] = p
	}
	first := g.nodes[0]["id"]
	if reaches, _ := byName["day"]["reaches"].([]any); len(reaches) != 1 || reaches[0] != first {
		t.Errorf("day reaches %v, want exactly [%v]", byName["day"]["reaches"], first)
	}
	if reaches, _ := byName["mode"]["reaches"].([]any); len(reaches) != 0 {
		t.Errorf("mode reaches %v, want no step's arguments", reaches)
	}
	if byName["mode"]["decides"] != true {
		t.Errorf("mode decides which steps run and is not marked so: %v", byName["mode"])
	}
}

func TestIssue1906_StateIsReadAndSavedForTheNextRun(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, _ := script1906(t, c, "state", `
since = run.state.get("since", 0)
rows = platform.query("SELECT 1 AS n WHERE 1 > " + str(since), connection="acme")
platform.save_state({"since": len(rows["rows"])})
`)
	g := portalGraph1906(t, c, id, 1)
	if g.nodes[0]["id"] != "state" || g.nodes[0]["role"] != "input" {
		t.Fatalf("the first node is %v, want the run.state input", g.nodes[0])
	}
	save := g.titled(t, "Save state")
	var back map[string]any
	for _, e := range g.edges {
		if e["kind"] == "state" {
			back = e
		}
	}
	if back == nil || back["from"] != save["id"] || back["to"] != "state" {
		t.Errorf("the state edge is %v, want save_state -> state", back)
	}
	if !g.hasEdge("state", g.titled(t, "Query acme")["id"]) {
		t.Errorf("run.state does not feed the query: %v", g.edges)
	}
}

func TestIssue1906_SavingAVersionThatAddsAnExportAddsItsCard(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	v1 := "rows = platform.query(\"SELECT 1 AS n\", connection=\"acme\")\nplatform.export(\"a\", rows[\"rows\"], format=\"csv\")\n"
	id, name := script1906(t, c, "version", v1)
	v2 := v1 + "platform.export(\"b\", rows[\"rows\"], format=\"jsonl\", destination=\"resources\", key=\"acc-1906/b.jsonl\")\n"
	out := c.call("manage_script", map[string]any{"command": "update", "name": name, "source": v2})
	if msg, _ := out["error"].(string); msg != "" {
		t.Fatalf("update refused: %v", out)
	}
	g1, g2 := portalGraph1906(t, c, id, 1), portalGraph1906(t, c, id, 2)
	if len(g2.nodes) != len(g1.nodes)+1 {
		t.Fatalf("v2 has %v, v1 has %v: want exactly one more card", g2.titles(), g1.titles())
	}
	for i, title := range g1.titles() {
		if g2.titles()[i] != title {
			t.Errorf("card %d changed from %q to %q", i, title, g2.titles()[i])
		}
	}
	if last := g2.titles()[len(g2.nodes)-1]; last != "Export JSONL to resources" {
		t.Errorf("the new card is %q", last)
	}
}

// A version saved before the dialect refused a name (#1823 made `load` a
// reserved word) no longer parses. Saving refuses such a source now, so the
// stored version is rewritten in the database to stand for one saved then.
func TestIssue1906_AVersionThatNoLongerParsesAnswersItsFindings(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, _ := script1906(t, c, "legacy", "platform.query(\"SELECT 1\", connection=\"acme\")\n")
	db := issue1904DB(t)
	issue1904Exec(t, db, `UPDATE script_versions SET source_code = $1 WHERE script_id = $2 AND version = 1`,
		"def load():\n    platform.query(\"SELECT 1\")\nload()\n", id)

	g := readGraph1906(t, c, fmt.Sprintf("/api/v1/portal/scripts/%s/versions/1/graph", id))
	if g.raw["ok"] != false {
		t.Fatalf("ok is %v for a source that does not parse", g.raw["ok"])
	}
	findings, _ := g.raw["findings"].([]any)
	if len(findings) == 0 || !strings.Contains(fmt.Sprint(findings), "reserved word") {
		t.Errorf("findings are %v, want the reserved-word refusal", findings)
	}
	if len(g.nodes) != 0 || len(g.edges) != 0 {
		t.Errorf("a source that does not parse drew %v", g.titles())
	}
}

func TestIssue1906_AScriptWithNoPlatformCallsIsEmptyListsNotNull(t *testing.T) {
	c := connectAs(t, devOwnerAPIKey)
	id, _ := script1906(t, c, "empty", "x = 1 + 2\nprint(x)\n")
	status, body := c.restText(fmt.Sprintf("/api/v1/portal/scripts/%s/versions/1/graph", id))
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	for _, key := range []string{`"nodes":[]`, `"edges":[]`, `"groups":[]`, `"params":[]`} {
		if !strings.Contains(body, key) {
			t.Errorf("the body does not carry %s: %s", key, body)
		}
	}
	if strings.Contains(body, "null") {
		t.Errorf("the body carries null: %s", body)
	}
}

// The graph is read under the rule the source is: everyone signed in (#1866),
// and the admin surface over every script.
func TestIssue1906_AReaderAndAnAdministratorReadTheGraph(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	id, _ := script1906(t, owner, "reader", threeSteps1906)
	peer := connectAs(t, devPeerAPIKey)
	g := portalGraph1906(t, peer, id, 1)
	if len(g.nodes) != 4 {
		t.Errorf("a reader sees %v", g.titles())
	}
	admin := connect(t)
	a := readGraph1906(t, admin, fmt.Sprintf("/api/v1/admin/scripts/%s/versions/1/graph", id))
	if len(a.nodes) != 4 {
		t.Errorf("the admin route answers %v", a.titles())
	}
	status, _ := peer.rest(http.MethodGet, fmt.Sprintf("/api/v1/portal/scripts/%s/versions/99/graph", id), nil)
	if status != http.StatusNotFound {
		t.Errorf("a version that does not exist answers %d, want 404", status)
	}
}
