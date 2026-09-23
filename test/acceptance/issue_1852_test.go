//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1852: rows platform.query handed a script came back as dicts with
// their keys alphabetized, so a script exporting rows straight from a query
// wrote its CSV columns in alphabetical order instead of the SELECT's.
//
// What these hold, against the running platform: a saved script's run sees
// each row's keys in SELECT order, the CSV it exports from those rows has its
// header in that order, and a script reaching trino_query through
// platform.call sees the same order.
//
// Wire forms: manage_script's `command`, `name`, `description` and `source`
// are typed string and `params` an array of objects; run_script's `name` is a
// string, `args` an object and `wait_seconds` an integer; manage_asset's
// `action` and `asset_id` are strings. Each is sent in that one form.

// issue1852Select names its columns out of alphabetical order on purpose.
const issue1852Select = "SELECT 'x' AS zeta, 1 AS alpha, true AS mid"

// runScript1852 saves source as a script, runs it and returns the run.
func runScript1852(t *testing.T, c *client, name, source string) map[string]any {
	t.Helper()
	c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source,
		"description": "Acceptance #1852: query rows keep the SELECT's column order.",
		"params":      []any{map[string]any{"name": "day", "type": "string"}},
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	out := c.call("run_script", map[string]any{"name": name, "args": map[string]any{}, "wait_seconds": 120})
	if status, _ := out["status"].(string); status != "succeeded" {
		t.Fatalf("the run did not succeed: %v", out)
	}
	return out
}

func TestIssue1852_QueryRowsAndTheirExportKeepTheSelectOrder(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	out := runScript1852(t, c, "acc-1852-query-"+stamp, fmt.Sprintf(`
rows = platform.query(connection=%q, sql=%q)["rows"]
print(list(rows[0].keys()))
platform.export(name="acc-1852-%s", rows=rows, format="csv")
`, scratchResourceConnection, issue1852Select, stamp))

	if log, _ := out["log"].(string); !strings.Contains(log, `["zeta", "alpha", "mid"]`) {
		t.Errorf("the row keys are not in SELECT order: log = %q", log)
	}
	outputs, _ := out["outputs"].([]any)
	if len(outputs) != 1 {
		t.Fatalf("outputs = %v; want the one export", out["outputs"])
	}
	output, _ := outputs[0].(map[string]any)
	assetID, _ := output["asset_id"].(string)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID}) })
	content := c.call("manage_asset", map[string]any{"action": "get_content", "asset_id": assetID})
	body, _ := content["content"].(string)
	if header, _, _ := strings.Cut(body, "\n"); strings.TrimSpace(header) != "zeta,alpha,mid" {
		t.Errorf("the CSV header is %q; want the SELECT's order zeta,alpha,mid", header)
	}
}

func TestIssue1852_PlatformCallOfTheQueryToolKeepsTheSelectOrder(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	out := runScript1852(t, c, "acc-1852-call-"+stamp, fmt.Sprintf(`
res = platform.call("trino_query", {"connection": %q, "sql": %q})
print(list(res["rows"][0].keys()))
`, scratchResourceConnection, issue1852Select))
	if log, _ := out["log"].(string); !strings.Contains(log, `["zeta", "alpha", "mid"]`) {
		t.Errorf("the row keys are not in SELECT order through platform.call: log = %q", log)
	}
}
