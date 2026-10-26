//go:build integration

package acceptance

import (
	"fmt"
	"testing"
)

// Acceptance for #2315: the flow diagram draws an endpoint script's
// handle(req). A script defining only handle(req) that queries a table is
// drawn with the query step and the handler's early return; a script
// defining both main() and handle(req) is drawn from main().
//
// Wire forms: the graph route takes two path parameters, each a string in the
// URL, sent once in that form; manage_script's command, name and source are
// strings, sent as the literal tools/call params.

const (
	issue2315Handler = `def handle(req):
    """Looks one order up."""
    rows = platform.query("SELECT id, total FROM hive.sales.orders WHERE id = ?", [req["params"]["id"]], connection="trino")
    if not rows["rows"]:
        return {"status": 404, "json": {"error": "no such order"}}
    return {"json": rows["rows"][0]}
`
	issue2315Both = `def handle(req):
    """Looks one return up."""
    rows = platform.query("SELECT id FROM hive.sales.returns", connection="trino")
    return {"json": rows["rows"]}

def main():
    """Counts the orders."""
    rows = platform.query("SELECT count(*) FROM hive.sales.orders", connection="trino")
    platform.result(rows["rows"])
`
)

// structureKinds is the kinds of the graph's structure nodes.
func structureKinds(g graph1906) map[string]int {
	kinds := map[string]int{}
	structure, _ := g.raw["structure"].(map[string]any)
	nodes, _ := structure["nodes"].([]any)
	for _, n := range nodes {
		node, _ := n.(map[string]any)
		kinds[fmt.Sprint(node["kind"])]++
	}
	return kinds
}

// TestIssue2315_AnEndpointScriptsHandlerIsDrawn.
func TestIssue2315_AnEndpointScriptsHandlerIsDrawn(t *testing.T) {
	c := connect(t)
	id, name := issue2315Seed(t, c, "handler", issue2315Handler)
	g := portalGraph1906(t, c, id, 1)
	query := g.titled(t, "Query trino")
	if fmt.Sprint(query["detail"]) != "[hive.sales.orders]" {
		t.Fatalf("%s's query step reads %v, want hive.sales.orders", name, query["detail"])
	}
	kinds := structureKinds(g)
	if kinds["step"] != 1 || kinds["if"] != 1 || kinds["return"] != 1 {
		t.Fatalf("%s's structure: %v, want one step, one if and the early return", name, kinds)
	}
}

// TestIssue2315_AScriptWithMainAndHandleIsDrawnFromMain.
func TestIssue2315_AScriptWithMainAndHandleIsDrawnFromMain(t *testing.T) {
	c := connect(t)
	id, _ := issue2315Seed(t, c, "both", issue2315Both)
	g := portalGraph1906(t, c, id, 1)
	query := g.titled(t, "Query trino")
	if fmt.Sprint(query["detail"]) != "[hive.sales.orders]" {
		t.Fatalf("the drawn step reads %v, want main()'s hive.sales.orders", query["detail"])
	}
	for _, n := range g.nodes {
		if fmt.Sprint(n["detail"]) == "[hive.sales.returns]" {
			t.Fatalf("handle(req)'s step is drawn beside main()'s: %v", n)
		}
	}
}

// issue2315Seed stores source as a script without drafting it: it reads
// tables the stack does not serve, and is drawn, never run (#1939).
func issue2315Seed(t *testing.T, c *client, label, source string) (id, name string) {
	t.Helper()
	name = endpointName("acc-2315-" + label)
	created := c.seedPreGate(t, map[string]any{
		"command": "create", "name": name, "source": source,
		"description": "Acceptance #2315: an endpoint script the Flow tab draws.",
	})
	id, _ = created["id"].(string)
	if id == "" {
		t.Fatalf("manage_script create returned no id: %v", created)
	}
	return id, name
}
