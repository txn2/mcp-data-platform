//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1861: one run could hold more memory than its container had, and the
// kernel killed the replica with every session and run on it. Nothing told the
// author how much a run held, and a paged API could only be exported as one
// file by holding every page at once.
//
// What these hold, against the running platform (dev/platform.yaml sets
// scripts.worker.max_run_memory to 128MiB): a run that holds more than its
// budget fails naming the budget and what it held, and is not retried; a
// draft and a run report the peak they reached; and platform.export with
// append=True lands the pages of one output as one file and one table.
//
// Wire forms: manage_script's `command`, `name`, `description` and `source`
// are typed string; run_script's `wait_seconds` integer. Each is sent in that
// one form. append= is a Starlark bool inside the source.

// TestIssue1861_ARunOverItsMemoryBudgetFails builds a list far past the dev
// budget and then makes a host call, where the platform measures what the run
// holds.
func TestIssue1861_ARunOverItsMemoryBudgetFails(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-1861-budget-%d", time.Now().UnixNano())
	c.call("manage_script", map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1861: a run over its memory budget.",
		"source": fmt.Sprintf(`held = ["x" * 1024 + str(i) for i in range(200000)]
platform.query(connection=%q, sql="SELECT 1 AS one")
print(len(held))
`, scratchResourceConnection),
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 90})
	got := c.call("manage_script", map[string]any{"command": "get_run", "run_id": run["run_id"]})
	if got["status"] != "failed" {
		t.Fatalf("a run holding ~200 MiB under a 128 MiB budget did not fail: %v", got)
	}
	errText, _ := got["error"].(string)
	for _, want := range []string{"memory budget", "128 MiB", "platform.query"} {
		if !strings.Contains(errText, want) {
			t.Errorf("error %q does not name %q", errText, want)
		}
	}
	if got["cause"] != "memory" || got["retryable"] != false {
		t.Errorf("cause = %v, retryable = %v; want memory and false", got["cause"], got["retryable"])
	}
	if attempt := number(t, got, "attempt"); attempt != 1 {
		t.Errorf("the run was executed %v times; a budget failure is not retried", attempt)
	}
}

// TestIssue1861_DraftAndRunReportPeakMemory holds item 3: the author sees how
// close a draft came before production does, and the run records the same.
func TestIssue1861_DraftAndRunReportPeakMemory(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-1861-peak-%d", time.Now().UnixNano())
	source := fmt.Sprintf(`rows = [{"n": i, "label": "row " + str(i)} for i in range(20000)]
platform.query(connection=%q, sql="SELECT 1 AS one")
print(len(rows))
`, scratchResourceConnection)
	c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source,
		"description": "Acceptance #1861: peak memory is reported.",
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	draft := c.call("manage_script", map[string]any{"command": "run_draft", "name": name, "source": source})
	if peak, _ := draft["peak_memory_bytes"].(float64); peak < 1<<20 {
		t.Errorf("run_draft reports peak_memory_bytes %v; want at least a MiB for 20,000 dicts: %v", draft["peak_memory_bytes"], draft)
	}
	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 60})
	got := c.call("manage_script", map[string]any{"command": "get_run", "run_id": run["run_id"]})
	metrics, _ := got["metrics"].(map[string]any)
	if peak, _ := metrics["peak_memory_bytes"].(float64); peak < 1<<20 {
		t.Errorf("the run's metrics report peak_memory_bytes %v; want at least a MiB: %v", metrics["peak_memory_bytes"], got)
	}
}

// issue1861AppendSource pages rows out of the warehouse and appends each page
// to one output. Verbs: the connection (twice), the key, the table name.
const issue1861AppendSource = `
for page in range(3):
    res = platform.query(connection=%q, sql="SELECT x AS n, 'page ' || CAST(%%d AS varchar) AS label FROM UNNEST(sequence(1, 500)) AS t(x)" %% page)
    out = platform.export(
        name="Acceptance 1861 pages",
        rows=res["rows"],
        format="jsonl",
        destination="resources",
        key=%q,
        register={"connection": %q, "table_name": %q},
        append=True,
    )
    print("appended page {}: {} rows so far".format(page, out["row_count"]))
`

// TestIssue1861_AppendedPagesLandAsOneFileAndOneTable holds item 4: three
// pages appended to one output are one resource version and one table with
// every row, and the run records one output.
func TestIssue1861_AppendedPagesLandAsOneFileAndOneTable(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "acc-1861-append-" + stamp
	table := "acc_1861_pages_" + stamp
	c.call("manage_script", map[string]any{
		"command": "create", "name": name,
		"description": "Acceptance #1861: pages appended to one output.",
		"source": fmt.Sprintf(issue1861AppendSource, scratchResourceConnection,
			"acceptance/issue-1861/pages-"+stamp+".jsonl", scratchResourceConnection, table),
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	run := c.call("run_script", map[string]any{"name": name, "wait_seconds": 90})
	if run["status"] != "succeeded" {
		t.Fatalf("the appending run did not succeed: %v", run)
	}
	got := c.call("manage_script", map[string]any{"command": "get_run", "run_id": run["run_id"]})
	outputs, _ := got["outputs"].([]any)
	if len(outputs) != 1 {
		t.Fatalf("the run recorded %d outputs; want the three pages as one: %v", len(outputs), outputs)
	}
	out, _ := outputs[0].(map[string]any)
	if number(t, out, "row_count") != 1500 {
		t.Errorf("the output carries %v rows; want 1500", out["row_count"])
	}
	if log, _ := got["log"].(string); !strings.Contains(log, "appended page 2: 1500 rows so far") {
		t.Errorf("the per-call record did not report the running count:\n%s", log)
	}
	resourceID, _ := out["resource_id"].(string)
	tables := c.call("manage_table", map[string]any{"action": "list", "reference": "mcp:resource:" + resourceID})
	query, regID := issue1861Registration(t, tables, table)
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": regID})
	})
	counted := c.call("trino_query", map[string]any{
		"connection": scratchResourceConnection,
		"purpose":    "Acceptance #1861: the appended table holds every page.",
		"sql":        "SELECT count(*) AS n, count(DISTINCT label) AS pages FROM " + query,
	})
	rows, _ := counted["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("count returned %v", counted)
	}
	row, _ := rows[0].(map[string]any)
	if number(t, row, "n") != 1500 || number(t, row, "pages") != 3 {
		t.Errorf("the table holds %v rows over %v pages; want 1500 over 3", row["n"], row["pages"])
	}
}

// issue1861Registration finds the registration over the output whose query
// table is named table, and returns its query table and registration id.
func issue1861Registration(t *testing.T, listing map[string]any, table string) (query, id string) {
	t.Helper()
	regs, _ := listing["table_registrations"].([]any)
	for _, raw := range regs {
		reg, _ := raw.(map[string]any)
		q, _ := reg["query_table"].(string)
		if strings.HasSuffix(q, table) { // the persona prefixes the name
			id, _ = reg["registration_id"].(string)
			return q, id
		}
	}
	t.Fatalf("no table %s is registered over the output: %v", table, listing)
	return "", ""
}

// TestIssue1861_HelpStatesTheMemoryCost holds item 5: the help names the
// budget, what a decoded page costs against its wire size, and the append path.
func TestIssue1861_HelpStatesTheMemoryCost(t *testing.T) {
	c := connect(t)
	_, text, err := c.callRaw("manage_script", map[string]any{"command": "help"})
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	for _, want := range []string{"max_run_memory", "append=True", "14 times"} {
		if !strings.Contains(text, want) {
			t.Errorf("help does not mention %q", want)
		}
	}
}
