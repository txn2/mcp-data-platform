//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1851: a table registered with follow=false over a script's portal
// output did not stay pinned. The script's next platform.export wrote the new
// version into the directory the table read, so the table returned the new
// rows beside the old ones and the run reported it as moved to the new
// version.
//
// What these hold, against the running platform: after a second run writes
// version 2 of the output, a table pinned over version 1 still returns version
// 1's rows and nothing else, the run's table_changes says it is pinned and
// behind rather than that it now reads version 2, and manage_table's listing
// reports it stale; and a table registered over the output once it has two
// versions is accepted and reads the head alone.
//
// Wire forms: manage_script's `command`, `name`, `description` and `source`
// are typed string and `params` an array of objects; run_script's `name` is a
// string, `args` an object and `wait_seconds` an integer; manage_table's
// `action`, `reference`, `connection`, `table_name` and `registration_id` are
// strings and `follow` a boolean; trino_query's `connection`, `sql` and
// `purpose` are strings; manage_asset's `action` and `asset_id` are strings.
// Each admits one form and is sent in it.

const issue1851Purpose = "Acceptance for #1851: a pinned table over a script output stays on its version."

// issue1851Source writes one CSV output whose rows carry the run's label, so a
// query can tell one version's rows from the other's.
const issue1851Source = `
label = run.params["label"]
platform.export(name=%q, rows=[{"label": label, "n": 1}, {"label": label, "n": 2}], format="csv")
`

// issue1851Script saves the script and returns its name and output name.
func issue1851Script(t *testing.T, c *client) (name, output string) {
	t.Helper()
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	name, output = "acc-1851-"+stamp, "acc-1851-out-"+stamp
	c.call("manage_script", map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1851: a script output a pinned table is registered over.",
		"source":      fmt.Sprintf(issue1851Source, output),
		"params": []any{map[string]any{
			"name": "label", "type": "string", "required": true, "description": "What each row is labelled.",
		}},
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	return name, output
}

// issue1851Run runs the script with a label and returns its one portal output.
func issue1851Run(t *testing.T, c *client, name, label string) map[string]any {
	t.Helper()
	out := c.call("run_script", map[string]any{
		"name": name, "args": map[string]any{"label": label}, "wait_seconds": 120,
	})
	if out["status"] != "succeeded" {
		t.Fatalf("the run labelled %s did not succeed: %v", label, out)
	}
	outputs, _ := out["outputs"].([]any)
	if len(outputs) != 1 {
		t.Fatalf("the run wrote %d outputs; want 1: %v", len(outputs), out)
	}
	written, _ := outputs[0].(map[string]any)
	return written
}

// issue1851Labels returns the label of every row the table serves, sorted.
func issue1851Labels(t *testing.T, c *client, query string) []string {
	t.Helper()
	got := c.call("trino_query", map[string]any{
		"connection": scratchConnection, "purpose": issue1851Purpose,
		"sql": "SELECT label FROM " + query + " ORDER BY label, n",
	})
	rows, _ := got["rows"].([]any)
	labels := make([]string, 0, len(rows))
	for _, r := range rows {
		row, _ := r.(map[string]any)
		label, _ := row["label"].(string)
		labels = append(labels, label)
	}
	return labels
}

// issue1851Register registers the output's asset on the scratch connection
// and unregisters it when the test ends.
func issue1851Register(t *testing.T, c *client, assetID, table string, follow bool) map[string]any {
	t.Helper()
	reg := c.call("manage_table", map[string]any{
		"action": "register", "reference": "mcp:asset:" + assetID, "connection": scratchConnection,
		"table_name": table, "follow": follow,
	})
	if q, _ := reg["query_table"].(string); q == "" {
		t.Fatalf("manage_table did not register the output: %v", reg)
	}
	t.Cleanup(func() {
		if id, _ := reg["registration_id"].(string); id != "" {
			_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": id})
		}
	})
	return reg
}

func TestIssue1851_APinnedTableOverAScriptOutputStaysOnItsVersion(t *testing.T) {
	c := connect(t)
	name, _ := issue1851Script(t, c)

	first := issue1851Run(t, c, name, "first")
	assetID, _ := first["asset_id"].(string)
	if assetID == "" || first["asset_version"] != float64(1) {
		t.Fatalf("the first run did not write version 1 of an asset: %v", first)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID}) })

	table := strings.ReplaceAll(name, "-", "_") + "_pinned"
	reg := issue1851Register(t, c, assetID, table, false)
	query, _ := reg["query_table"].(string)
	if got := issue1851Labels(t, c, query); strings.Join(got, ",") != "first,first" {
		t.Fatalf("the pinned table serves %v before the second run; want version 1's two rows", got)
	}

	second := issue1851Run(t, c, name, "second")
	if second["asset_id"] != assetID || second["asset_version"] != float64(2) {
		t.Fatalf("the second run did not write version 2 of the same asset: %v", second)
	}
	changes := fmt.Sprint(second["table_changes"])
	if !strings.Contains(changes, query+" on "+scratchConnection+" is pinned to the version it was registered over") {
		t.Errorf("the run does not report the table pinned and behind: table_changes = %s", changes)
	}
	if strings.Contains(changes, "now reads version 2") {
		t.Errorf("the run reports the pinned table as moved: table_changes = %s", changes)
	}

	if got := issue1851Labels(t, c, query); strings.Join(got, ",") != "first,first" {
		t.Errorf("the pinned table serves %v after the second run; want version 1's two rows and nothing else", got)
	}

	listing := c.call("manage_table", map[string]any{"action": "list", "reference": "mcp:asset:" + assetID})
	regs, _ := listing["table_registrations"].([]any)
	if len(regs) != 1 {
		t.Fatalf("want one registration over the output: %v", listing)
	}
	listed, _ := regs[0].(map[string]any)
	if listed["stale"] != true || listed["follow"] != false {
		t.Errorf("the listing does not report the pinned table behind the file: %v", listed)
	}
}

func TestIssue1851_AScriptOutputWithTwoVersionsRegisters(t *testing.T) {
	c := connect(t)
	name, _ := issue1851Script(t, c)

	first := issue1851Run(t, c, name, "first")
	assetID, _ := first["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("the first run wrote no asset: %v", first)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID}) })
	if second := issue1851Run(t, c, name, "second"); second["asset_version"] != float64(2) {
		t.Fatalf("the second run did not write version 2: %v", second)
	}

	reg := issue1851Register(t, c, assetID, strings.ReplaceAll(name, "-", "_")+"_head", true)
	query, _ := reg["query_table"].(string)
	if got := issue1851Labels(t, c, query); strings.Join(got, ",") != "second,second" {
		t.Errorf("a table over the output's head serves %v; want version 2's two rows alone", got)
	}
}
