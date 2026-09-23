//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1844: a script's parameters took only scalars, so a set of values
// was a comma-separated string nothing validated, and a form could only render
// a text box.
//
// What these hold, against the running platform: a list<string> parameter
// takes ["a","b"] through run_script, run_draft, a schedule's bound args and
// POST /api/v1/portal/scripts/{id}/runs, and refuses a scalar or a wrongly
// typed element naming it; the script sees a list and platform.query binds it
// as IN (...); a date_range refuses from after to; and
// GET /api/v1/portal/scripts/{id} returns the full contract with items, label,
// ui and the constraints.
//
// Wire forms: manage_script's `command`, `name`, `description`, `source`,
// `cron` and `timezone` are typed string, `params` an array of objects and
// `args` an object; run_script's `args` is an object and `wait_seconds` an
// integer; the portal run route's body is an object with `params` an object.
// A list parameter's VALUE is sent in every form a caller could reasonably
// send: a JSON array (accepted) and a comma-separated string and a scalar
// number (refused); a date_range value as an object (accepted) and a string
// (refused).

// params1844 is the contract under test.
var params1844 = []any{
	map[string]any{
		"name": "ids", "type": "list", "items": "string", "required": true,
		"label": "Store ids", "group": "Filters", "order": 1, "min_items": 1,
		"pattern": "[a-z]+", "ui": map[string]any{"widget": "store-picker"},
	},
	map[string]any{
		"name": "period", "type": "date_range", "default": map[string]any{"from": "2026-01-01", "to": "2026-01-31"},
	},
}

// source1844 reads the list, binds it into a query as IN (...), and prints
// what it saw.
var source1844 = fmt.Sprintf(`ids = run.params["ids"]
print("type", type(ids), "len", len(ids))
rows = platform.query(connection=%q,
    sql="SELECT x FROM UNNEST(ARRAY['a', 'b', 'c']) AS t(x) WHERE x IN :ids ORDER BY x",
    params={"ids": ids})["rows"]
print("matched", ",".join([r["x"] for r in rows]))
print("from", run.params["period"]["from"])
`, scratchResourceConnection)

func save1844(t *testing.T, c *client) (name, id string) {
	t.Helper()
	name = fmt.Sprintf("acc-1844-%d", time.Now().UnixNano())
	c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source1844, "params": params1844,
		"description": "Acceptance #1844: list and date_range parameters.",
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	id, _ = got["id"].(string)
	return name, id
}

func TestIssue1844_AListReachesTheScriptAndBindsAsIN(t *testing.T) {
	c := connect(t)
	name, _ := save1844(t, c)
	out := c.call("run_script", map[string]any{"name": name, "args": map[string]any{"ids": []any{"a", "c"}}, "wait_seconds": 120})
	if out["status"] != "succeeded" {
		t.Fatalf("the run did not succeed: %v", out)
	}
	log, _ := out["log"].(string)
	for _, want := range []string{"type list len 2", "matched a,c", "from 2026-01-01"} {
		if !strings.Contains(log, want) {
			t.Errorf("log = %q; want %q", log, want)
		}
	}

	draft := c.call("manage_script", map[string]any{"command": "run_draft", "name": name, "args": map[string]any{"ids": []any{"b"}}})
	if log, _ := draft["log"].(string); !strings.Contains(log, "matched b") {
		t.Errorf("run_draft log = %q; want the list bound", log)
	}
}

func TestIssue1844_TheRunRouteAndAScheduleBindAList(t *testing.T) {
	c := connect(t)
	name, id := save1844(t, c)
	status, run := c.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/runs?wait=60",
		jsonBody(t, map[string]any{"params": map[string]any{"ids": []any{"a", "b"}}}))
	if status != http.StatusOK || run["status"] != "succeeded" {
		t.Fatalf("the portal run answered %d %v", status, run)
	}
	if log, _ := run["log"].(string); !strings.Contains(log, "matched a,b") {
		t.Errorf("portal run log = %q", log)
	}
	params, _ := run["params"].(map[string]any)
	if ids, _ := params["ids"].([]any); len(ids) != 2 {
		t.Errorf("the run stores params.ids = %v; want the JSON array", params["ids"])
	}

	sched := c.call("manage_script", map[string]any{
		"command": "schedule_set", "name": name, "cron": "0 7 * * 1-5", "timezone": "UTC",
		"args": map[string]any{"ids": []any{"c"}},
	})
	if msg, _ := sched["error"].(string); msg != "" {
		t.Fatalf("a schedule binding a list was refused: %v", sched)
	}
	_, _, _ = c.callRaw("manage_script", map[string]any{"command": "schedule_disable", "name": name})
}

func TestIssue1844_AWrongListValueIsRefusedNamingIt(t *testing.T) {
	c := connect(t)
	name, id := save1844(t, c)
	for _, tc := range []struct {
		ids  any
		want string
	}{
		{"a,b", "expected a list of values"},
		{7, "expected a list of values"},
		{[]any{"a", 7}, "element 1"},
		{[]any{"a", "B"}, "does not match the pattern"},
		{[]any{}, "between 1 and"},
	} {
		_, text, err := c.callRaw("run_script", map[string]any{"name": name, "args": map[string]any{"ids": tc.ids}, "wait_seconds": -1})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, tc.want) {
			t.Errorf("ids=%v: answer %q does not say %q", tc.ids, text, tc.want)
		}
	}
	status, body := c.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/runs",
		jsonBody(t, map[string]any{"params": map[string]any{"ids": []any{"a"}, "period": map[string]any{"from": "2026-02-01", "to": "2026-01-01"}}}))
	if status != http.StatusBadRequest {
		t.Errorf("a reversed range answered %d %v; want 400", status, body)
	}
	status, _ = c.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/runs",
		jsonBody(t, map[string]any{"params": map[string]any{"ids": []any{"a"}, "period": "2026-01-01"}}))
	if status != http.StatusBadRequest {
		t.Errorf("a range sent as a string answered %d; want 400", status)
	}
}

func TestIssue1844_TheContractCarriesTheFormMetadata(t *testing.T) {
	c := connect(t)
	_, id := save1844(t, c)
	status, body := c.rest(http.MethodGet, "/api/v1/portal/scripts/"+id, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET script answered %d", status)
	}
	var ids map[string]any
	for _, container := range []map[string]any{body} {
		contract, _ := container["contract"].(map[string]any)
		list, _ := contract["params"].([]any)
		for _, item := range list {
			p, _ := item.(map[string]any)
			if p["name"] == "ids" {
				ids = p
			}
		}
	}
	if ids == nil {
		t.Fatalf("the contract carries no ids parameter: %v", body)
	}
	for key, want := range map[string]any{
		"type": "list", "items": "string", "label": "Store ids", "group": "Filters",
		"order": float64(1), "min_items": float64(1), "pattern": "[a-z]+",
	} {
		if ids[key] != want {
			t.Errorf("contract ids.%s = %v; want %v", key, ids[key], want)
		}
	}
	if ui, _ := ids["ui"].(map[string]any); ui["widget"] != "store-picker" {
		t.Errorf("contract ids.ui = %v; want the stored hint", ids["ui"])
	}
}
