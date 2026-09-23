//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Issue #1854: a run's outputs listed only what platform.export wrote, so a
// script producing its report with platform.call("trino_export", ...) -- the
// right tool once a result is past the row cap -- showed no output at all.
//
// What this holds, against the running platform: a run whose only output is a
// trino_export call lists that asset under outputs, marked with the tool, in
// run_script, get_run and GET /api/v1/portal/scripts/{id}/runs/{runID}; and
// the next run of the same script writes version 2 of that asset rather than
// a new one.
//
// Wire forms: manage_script's `command`, `name`, `description`, `source` and
// `run_id` are typed string and `params` an array of objects; run_script's
// `args` is an object and `wait_seconds` an integer; manage_asset's `action`
// and `asset_id` are strings. Each is sent in that one form.

// trinoExportOutput1854 finds the trino_export output among a run's outputs.
func trinoExportOutput1854(outputs any) map[string]any {
	list, _ := outputs.([]any)
	for _, item := range list {
		o, _ := item.(map[string]any)
		if o["tool"] == "trino_export" {
			return o
		}
	}
	return nil
}

func TestIssue1854_ATrinoExportInsideARunIsListedAsItsOutput(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "acc-1854-" + stamp
	c.call("manage_script", map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1854: a trino_export inside a run is its output.",
		"source": fmt.Sprintf(`platform.call("trino_export", {"connection": %q, "sql": "SELECT 1 AS n, 'a' AS label", "name": "acc-1854-%s", "format": "csv"})`+"\n",
			scratchResourceConnection, stamp),
		"params": []any{map[string]any{"name": "day", "type": "string"}},
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	out := c.call("run_script", map[string]any{"name": name, "args": map[string]any{}, "wait_seconds": 120})
	if out["status"] != "succeeded" {
		t.Fatalf("the run did not succeed: %v", out)
	}
	exported := trinoExportOutput1854(out["outputs"])
	if exported == nil {
		t.Fatalf("run_script outputs = %v; want the trino_export asset", out["outputs"])
	}
	assetID, _ := exported["asset_id"].(string)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID}) })
	if assetID == "" || exported["name"] != "acc-1854-"+stamp || exported["row_count"] != float64(1) {
		t.Errorf("the output does not name the asset the tool wrote: %v", exported)
	}

	runID, _ := out["run_id"].(string)
	got := c.call("manage_script", map[string]any{"command": "get_run", "run_id": runID})
	if o := trinoExportOutput1854(got["outputs"]); o == nil || o["asset_id"] != assetID {
		t.Errorf("get_run outputs = %v; want the same asset", got["outputs"])
	}
	if exported["asset_version"] != float64(1) {
		t.Errorf("the first run's asset_version = %v; want 1", exported["asset_version"])
	}

	// The next run writes the next version of the same asset: a named export
	// inside a run keeps one identity, as platform.export does.
	again := c.call("run_script", map[string]any{"name": name, "args": map[string]any{}, "wait_seconds": 120})
	next := trinoExportOutput1854(again["outputs"])
	if next == nil || next["asset_id"] != assetID || next["asset_version"] != float64(2) {
		t.Errorf("the second run's output = %v; want version 2 of asset %s", next, assetID)
	}

	script := c.call("manage_script", map[string]any{"command": "get", "name": name})
	scriptID, _ := script["id"].(string)
	status, run := c.rest(http.MethodGet, "/api/v1/portal/scripts/"+scriptID+"/runs/"+runID, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET run status = %d: %v", status, run)
	}
	if o := trinoExportOutput1854(run["outputs"]); o == nil || o["asset_id"] != assetID {
		t.Errorf("the portal run outputs = %v; want the same asset", run["outputs"])
	}
}
