//go:build integration

package acceptance

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1820: the one sanctioned path from script rows to a queryable table --
// write the rows with platform.export, register the file, INSERT ... SELECT
// from it -- was not lossless. A line break inside a value could not survive a
// CSV table at all, and an agent had to invent an escape scheme to get exact
// text through.
//
// What these hold, against the running platform: a managed script's
// platform.export with format="jsonl" and register= writes the file and makes
// the table in one call, and the table serves every string of the ticket's
// hostile corpus byte for byte, and a null as NULL; trino_export writes the same
// format and manage_table registers it as exactly; a JSON-lines file the reader
// cannot read is refused naming its line; and manage_table's description says
// which characters each format alters, refuses or repairs.
//
// Wire forms: manage_script's `command`, `name`, `description` and `source`,
// run_script's `name`, manage_table's `action`, `reference`, `connection`,
// `table_name` and `registration_id`, trino_query's `connection`, `sql` and
// `purpose`, trino_export's `sql`, `format`, `name` and `purpose`, and
// manage_resource's `action`, `filename`, `display_name`, `path`,
// `description`, `content_type`, `content` and `content_base64` are typed
// string, and run_script's `wait_seconds` an integer, so each is sent in its
// one form. manage_resource carries the file in `content` or in
// `content_base64`, and the refusal criterion sends it both ways.
// trino_export's `resource` admits only the object form.

const issue1820Purpose = "Acceptance for #1820: script rows reach a table exactly."

// issue1820Corpus is the hostile corpus the ticket names: quotes, doubled
// quotes, backslashes, CR, LF, CRLF, a leading -, =, + or @, tabs, emoji and
// private-use code points, and a few the probe of Trino's JSON reader added.
var issue1820Corpus = []string{
	`plain`, `say "hi"`, `""`, `back\slash`, `trailing\`, "cr\rhere", "lf\nhere", "crlf\r\nhere",
	"-1+1", "=SUM(A1)", "+44 20 7946 0000", "@handle", "tab\there", "emoji \U0001F600",
	"pua \ue000\U000F0000", "ls\u2028ps\u2029", "<a>&amp;", "", " lead and trail ", "nul\x00byte",
	`{"json":"inside"}`, "a,b,c", "multi\nline\r\nparagraph\n\nwith a blank line",
}

// issue1820ScriptSource exports the corpus as JSON lines to the library and
// registers it in the same call, then records where the table is. The first
// verb is the rows as a JSON string literal, the second the key, the third the
// connection and the fourth the table name.
const issue1820ScriptSource = `
out = platform.export(
    name="Acceptance 1820 staging",
    rows=json.decode(%q),
    format="jsonl",
    destination="resources",
    key="acceptance/issue-1820/%s",
    register={"connection": %q, "table_name": %q},
)
platform.save_state({
    "reference": out["reference"],
    "query_table": out["table"]["query_table"],
    "registration_id": out["table"]["registration_id"],
    "format": out["table"]["format"],
})
`

// issue1820Rows renders the corpus as script rows: one per string, keyed by
// position, and one more whose text is null.
func issue1820Rows(t *testing.T) string {
	t.Helper()
	rows := make([]map[string]any, 0, len(issue1820Corpus))
	for i, s := range issue1820Corpus {
		rows = append(rows, map[string]any{"id": i, "text": s})
	}
	rows = append(rows, map[string]any{"id": len(issue1820Corpus), "text": nil})
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestIssue1820_AScriptRegistersJSONLinesAndEveryStringComesBack is the
// ticket's ask: script rows reach a queryable table in one call, losslessly.
func TestIssue1820_AScriptRegistersJSONLinesAndEveryStringComesBack(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "acc-1820-" + stamp
	table := "acc_1820_script_" + stamp
	source := fmt.Sprintf(issue1820ScriptSource, issue1820Rows(t),
		"acc-1820-"+stamp+".jsonl", scratchResourceConnection, table)

	_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source,
		"description": "Acceptance #1820: rows exported as JSON lines and registered in one call.",
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})

	state := issue1663RunScript(t, c, name)
	query, _ := state["query_table"].(string)
	if query == "" {
		t.Fatalf("the export made no table: %v", state)
	}
	if id, _ := state["registration_id"].(string); id != "" {
		t.Cleanup(func() {
			_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": id})
		})
	}
	if format, _ := state["format"].(string); format != "jsonl" {
		t.Errorf("the table is read as %q; want jsonl", format)
	}
	issue1820AssertCorpus(t, c, query)
}

// TestIssue1820_ATrinoExportRegistersExactly holds the same for trino_export,
// which writes through the same formatter: values a query produces, line
// breaks and a null among them, come back from the table over the export.
func TestIssue1820_ATrinoExportRegistersExactly(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	values := "'a' || chr(10) || 'b' AS lf, 'c' || chr(13) || chr(10) || 'd' AS crlf, " +
		"'back\\slash' AS bs, '=1+1' AS eq, CAST(NULL AS varchar) AS nothing"
	landed := issue1663Landing(t, c.call("trino_export", map[string]any{
		"sql":     "SELECT " + values,
		"format":  "jsonl",
		"name":    "Acceptance 1820 query " + stamp,
		"purpose": issue1820Purpose,
		"resource": map[string]any{
			"path": "acceptance/issue-1820", "filename": "acc-1820-query-" + stamp + ".jsonl",
		},
	}))
	reference, _ := landed["reference"].(string)
	if reference == "" {
		t.Fatalf("trino_export returned no reference: %v", landed)
	}
	reg := c.call("manage_table", map[string]any{
		"action": "register", "reference": reference,
		"connection": scratchResourceConnection, "table_name": "acc_1820_query_" + stamp,
	})
	query, _ := reg["query_table"].(string)
	if query == "" {
		t.Fatalf("manage_table did not register the file: %v", reg)
	}
	t.Cleanup(func() {
		if id, _ := reg["registration_id"].(string); id != "" {
			_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": id})
		}
	})
	if format, _ := reg["format"].(string); format != "jsonl" {
		t.Errorf("manage_table reports format %q; want jsonl", format)
	}

	got := c.call("trino_query", map[string]any{
		"connection": scratchResourceConnection,
		"purpose":    issue1820Purpose,
		"sql": "SELECT to_hex(to_utf8(lf)) AS lf, to_hex(to_utf8(crlf)) AS crlf, to_hex(to_utf8(bs)) AS bs, " +
			"eq, nothing IS NULL AS nothing_null FROM " + query,
	})
	rows, _ := got["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("the table serves %d rows; want 1: %v", len(rows), got)
	}
	row, _ := rows[0].(map[string]any)
	for col, want := range map[string]string{"lf": "a\nb", "crlf": "c\r\nd", "bs": `back\slash`} {
		if gotHex, _ := row[col].(string); !strings.EqualFold(gotHex, hex.EncodeToString([]byte(want))) {
			t.Errorf("column %s = %s; want the bytes of %q", col, gotHex, want)
		}
	}
	if row["eq"] != "=1+1" {
		t.Errorf("column eq = %v; want =1+1", row["eq"])
	}
	if row["nothing_null"] != true {
		t.Errorf("a null came back as %v; want NULL", row["nothing_null"])
	}
}

// TestIssue1820_ARegistrationRefusesAJSONLinesLineByNumber holds the refusal:
// a file the JSON reader would fail on is refused before any table exists, and
// the refusal names the line and what is wrong with it. The file is sent in
// both forms manage_resource takes it in.
func TestIssue1820_ARegistrationRefusesAJSONLinesLineByNumber(t *testing.T) {
	c := connect(t)
	body := "{\"id\":\"1\",\"note\":\"fine\"}\n{\"id\":\"2\",\"note\":{\"nested\":true}}\n"
	for form, content := range map[string]map[string]any{
		"content":        {"content": body},
		"content_base64": {"content_base64": base64.StdEncoding.EncodeToString([]byte(body))},
	} {
		t.Run(form, func(t *testing.T) {
			stamp := fmt.Sprintf("%d", time.Now().UnixNano())
			args := map[string]any{
				"action":       "create",
				"filename":     "acc-1820-refused-" + stamp + ".jsonl",
				"display_name": "Acceptance 1820 refused " + stamp,
				"path":         "acceptance/issue-1820",
				"description":  "Acceptance #1820: a JSON-lines file with a nested value.",
				"content_type": "application/x-ndjson",
			}
			for k, v := range content {
				args[k] = v
			}
			out := c.call("manage_resource", args)
			id, _ := out["resource_id"].(string)
			reference, _ := out["reference"].(string)
			if id == "" || reference == "" {
				t.Fatalf("manage_resource create returned no resource: %v", out)
			}
			t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })

			res, text, err := c.callRaw("manage_table", map[string]any{
				"action": "register", "reference": reference,
				"connection": scratchResourceConnection, "table_name": "acc_1820_refused_" + stamp,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !res.IsError {
				t.Fatalf("a file with a nested value was registered: %s", text)
			}
			// Since #1833 a nested value is declared rather than refused, and
			// what the reader cannot read is a key holding a string on one line
			// and an object on the next.
			for _, want := range []string{"line 2", `the value of "note" is an object here and was a scalar`} {
				if !strings.Contains(text, want) {
					t.Errorf("the refusal does not say %q: %s", want, text)
				}
			}
		})
	}
}

// TestIssue1820_TheToolsSayWhatEachFormatAlters is the ticket's minimum: the
// help an agent reads names what a CSV alters, refuses or repairs, and offers
// JSON lines and register= as the lossless path.
func TestIssue1820_TheToolsSayWhatEachFormatAlters(t *testing.T) {
	c := connect(t)
	var table string
	for _, tool := range c.tools() {
		if tool.Name == "manage_table" {
			table = tool.Description
		}
	}
	for _, want := range []string{"JSON-lines", "every string exactly", "a null as an empty", "repair rewrites"} {
		if !strings.Contains(table, want) {
			t.Errorf("manage_table's description does not say %q", want)
		}
	}
	help := c.call("manage_script", map[string]any{"command": "help"})
	text, _ := json.Marshal(help)
	for _, want := range []string{"jsonl", "register={", "INSERT ... SELECT"} {
		if !strings.Contains(string(text), want) {
			t.Errorf("manage_script help does not say %q", want)
		}
	}
}

// issue1820AssertCorpus reads the registered table and fails for every row
// whose text is not the corpus string, compared as bytes, and for a null that
// did not stay one.
func issue1820AssertCorpus(t *testing.T, c *client, query string) {
	t.Helper()
	got := c.call("trino_query", map[string]any{
		"connection": scratchResourceConnection,
		"purpose":    issue1820Purpose,
		"sql": "SELECT CAST(id AS integer) AS id, to_hex(to_utf8(text)) AS hex, text IS NULL AS is_null FROM " +
			query + " ORDER BY CAST(id AS integer)",
	})
	rows, _ := got["rows"].([]any)
	if len(rows) != len(issue1820Corpus)+1 {
		t.Fatalf("the table serves %d rows; want %d: %v", len(rows), len(issue1820Corpus)+1, got)
	}
	for i, r := range rows {
		row, _ := r.(map[string]any)
		if i == len(issue1820Corpus) {
			if row["is_null"] != true {
				t.Errorf("row %d: a null came back as %v; want NULL", i, row["hex"])
			}
			continue
		}
		want := hex.EncodeToString([]byte(issue1820Corpus[i]))
		if gotHex, _ := row["hex"].(string); !strings.EqualFold(gotHex, want) {
			t.Errorf("row %d: %q came back as bytes %s; want %s", i, issue1820Corpus[i], gotHex, want)
		}
	}
}

// TestIssue1820_AOneRecordJSONLinesFileRegisters: a JSON-lines file holding one
// record is one JSON object on one line, which a writer, or detection, can
// reasonably call application/json. Under its .jsonl name it registers as the
// JSON-lines file it is, so the day a scheduled export has one row is not the
// day its table stops registering.
func TestIssue1820_AOneRecordJSONLinesFileRegisters(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	out := c.call("manage_resource", map[string]any{
		"action":       "create",
		"filename":     "acc-1820-one-" + stamp + ".jsonl",
		"display_name": "Acceptance 1820 one record " + stamp,
		"path":         "acceptance/issue-1820",
		"description":  "Acceptance #1820: a JSON-lines file with one record.",
		"content_type": "application/json",
		"content":      "{\"id\":\"1\",\"note\":\"only\\nrow\"}\n",
	})
	id, _ := out["resource_id"].(string)
	reference, _ := out["reference"].(string)
	if id == "" || reference == "" {
		t.Fatalf("manage_resource create returned no resource: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })

	reg := c.call("manage_table", map[string]any{
		"action": "register", "reference": reference,
		"connection": scratchResourceConnection, "table_name": "acc_1820_one_" + stamp,
	})
	query, _ := reg["query_table"].(string)
	if query == "" {
		t.Fatalf("a one-record JSON-lines file did not register: %v", reg)
	}
	t.Cleanup(func() {
		if rid, _ := reg["registration_id"].(string); rid != "" {
			_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": rid})
		}
	})
	got := c.call("trino_query", map[string]any{
		"connection": scratchResourceConnection, "purpose": issue1820Purpose,
		"sql": "SELECT to_hex(to_utf8(note)) AS hex FROM " + query,
	})
	rows, _ := got["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("the table serves %d rows; want 1: %v", len(rows), got)
	}
	row, _ := rows[0].(map[string]any)
	if h, _ := row["hex"].(string); !strings.EqualFold(h, hex.EncodeToString([]byte("only\nrow"))) {
		t.Errorf("the note came back as bytes %s; want the bytes of %q", h, "only\nrow")
	}
}
