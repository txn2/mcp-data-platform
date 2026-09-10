//go:build integration

package acceptance

import (
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1666: the same set of rows -- the query-engine tables registered over
// one stored file -- was reported under two different keys depending on which
// surface answered. `manage_table action=list` said `registrations`, while a
// fetched document, a content write and a script run log all said `tables`,
// and the row shapes differed too. A script checking `manage_table list` for
// an existing registration read `tables`, found an empty list every run, and
// re-registered; the table survived, because registering the same name
// replaces the registration, but the registration id changed on every run.
//
// The vocabulary is now one word per use:
//
//	tables              the query view: a fetch document and a search hit,
//	                    carrying the columns and sample SQL a caller needs to
//	                    write the query without a second call
//	table_registrations the maintenance view: manage_table action=list and a
//	                    refused manage_resource delete, carrying the
//	                    registration_id an unregister takes
//	table_changes       neither: one sentence per table saying what a write
//	                    just did to it
//
// Wire forms: every parameter these criteria touch is typed on its tool's
// input struct, so each admits exactly one JSON form, and each is sent below
// as a literal tools/call param. `manage_table` takes action, reference,
// connection, table_name and registration_id as strings and follow/repair as
// booleans (pkg/toolkits/portal/table_actions.go). `manage_resource` takes
// action, reference, content, content_type, change_summary and path as strings
// and force as a boolean (pkg/toolkits/portal/resource_actions.go); its
// content arrives as text in `content` or as bytes in `content_base64`, and
// the replace below sends `content`, the form a CSV a table is registered over
// is written with. `fetch` takes reference and purpose as strings. The keys
// under test -- table_registrations, tables, table_changes -- are result
// fields, read here off the real tool results rather than asserted against a
// shape built in the test. The file itself is stored through the multipart
// upload route, whose `file` part is bytes and whose metadata fields are
// form-encoded strings.

// TestIssue1666_TheListingAnswersUnderTableRegistrations is criterion 1: the
// maintenance view has one name, the old one is gone, and each row carries
// what a caller changing a registration needs.
func TestIssue1666_TheListingAnswersUnderTableRegistrations(t *testing.T) {
	c := connect(t)
	reference, _ := uploadCSV1666(t, c)
	want := registerTable1666(t, c, reference, "listing")

	listing := c.call("manage_table", map[string]any{
		"action": "list", "reference": reference,
	})
	if _, present := listing["registrations"]; present {
		t.Errorf("the listing still answers under registrations: %v", listing)
	}
	rows, ok := listing["table_registrations"].([]any)
	if !ok {
		t.Fatalf("the listing carries no table_registrations: %v", listing)
	}
	if len(rows) != 1 {
		t.Fatalf("the listing carries %d rows over a file registered once: %v", len(rows), rows)
	}

	row := object1666(t, rows[0])
	if got, _ := row["registration_id"].(string); got != want.id {
		t.Errorf("registration_id = %q; want the id the register call returned, %q", got, want.id)
	}
	if got, _ := row["query_table"].(string); got != want.qualified {
		t.Errorf("query_table = %q; want %q", got, want.qualified)
	}
	if len(columnsOf1666(t, row)) == 0 {
		t.Errorf("the row names no columns: %v", row)
	}
	if got, _ := row["sample_sql"].(string); !strings.Contains(got, want.qualified) {
		t.Errorf("sample_sql = %q; want a statement over %q", got, want.qualified)
	}
	if _, present := row["follow"]; !present {
		t.Errorf("the row does not say whether the table follows its file: %v", row)
	}
}

// TestIssue1666_AFetchedFileCarriesTheQueryView is criterion 2: the same
// registrations reach the caller who only wants to query, under `tables`, and
// carry enough to write the query -- the columns included -- so that reading a
// file in full is not followed by a manage_table call.
func TestIssue1666_AFetchedFileCarriesTheQueryView(t *testing.T) {
	c := connect(t)
	reference, _ := uploadCSV1666(t, c)
	want := registerTable1666(t, c, reference, "query")

	doc := fetchDoc1666(t, c, reference)
	if _, present := doc["table_registrations"]; present {
		t.Errorf("a fetched document carries the maintenance key: %v", doc)
	}
	raw, ok := doc["tables"].([]any)
	if !ok {
		t.Fatalf("the fetched document carries no tables: %v", doc)
	}
	if len(raw) != 1 {
		t.Fatalf("the document carries %d tables over a file registered once: %v", len(raw), raw)
	}

	row := object1666(t, raw[0])
	if got, _ := row["registration_id"].(string); got != want.id {
		t.Errorf("the document names registration %q; the register call returned %q", got, want.id)
	}
	if got, _ := row["query_table"].(string); got != want.qualified {
		t.Errorf("query_table = %q; want %q", got, want.qualified)
	}
	if got, _ := row["connection"].(string); got != scratchResourceConnection {
		t.Errorf("connection = %q; want %q", got, scratchResourceConnection)
	}
	if got, _ := row["sample_sql"].(string); !strings.Contains(got, want.qualified) {
		t.Errorf("sample_sql = %q; want a statement over %q", got, want.qualified)
	}
	// The columns are the half that was missing: without them the caller who
	// fetched the file still had to describe the table before selecting from
	// it.
	if got, want := columnsOf1666(t, row), issue1666Columns; !sameStrings1666(got, want) {
		t.Errorf("columns = %v; want the file's header %v", got, want)
	}

	// The two views are the same registrations, which is the whole claim.
	listing := c.call("manage_table", map[string]any{
		"action": "list", "reference": reference,
	})
	rows, _ := listing["table_registrations"].([]any)
	if len(rows) != len(raw) {
		t.Fatalf("the listing carries %d registrations and the document %d", len(rows), len(raw))
	}
	for i := range rows {
		listed := object1666(t, rows[i])
		fetched := object1666(t, raw[i])
		lid, _ := listed["registration_id"].(string)
		fid, _ := fetched["registration_id"].(string)
		if lid != fid {
			t.Errorf("entry %d: the listing names %q, the document names %q", i, lid, fid)
		}
		lcols := columnsOf1666(t, listed)
		fcols := columnsOf1666(t, fetched)
		if !sameStrings1666(lcols, fcols) {
			t.Errorf("entry %d: the listing names columns %v, the document names %v", i, lcols, fcols)
		}
	}
}

// TestIssue1666_AWriteReportsTableChanges is criterion 3: what a write did to
// the tables over the file it changed is prose about an event, and it no
// longer wears the name the rows wear.
func TestIssue1666_AWriteReportsTableChanges(t *testing.T) {
	c := connect(t)
	reference, _ := uploadCSV1666(t, c)
	want := registerTable1666(t, c, reference, "write")

	replaced := c.call("manage_resource", map[string]any{
		"action": "replace_content", "reference": reference,
		"content":        issue1666CSVSecondVersion,
		"content_type":   "text/csv",
		"change_summary": "Acceptance #1666: a second version, to see what the write says about the table.",
	})
	if _, present := replaced["tables"]; present {
		t.Errorf("the write's result still answers under tables: %v", replaced)
	}
	lines, ok := replaced["table_changes"].([]any)
	if !ok {
		t.Fatalf("the write reports no table_changes over a registered file: %v", replaced)
	}

	var named bool
	for _, line := range lines {
		text, _ := line.(string)
		named = named || strings.Contains(text, want.qualified)
	}
	if !named {
		t.Fatalf("the write says nothing about %s: table_changes = %v", want.qualified, lines)
	}
	// The sentences stay on the message too, so a caller reading the message
	// alone still learns what happened to the table.
	message, _ := replaced["message"].(string)
	if !strings.Contains(message, want.qualified) {
		t.Errorf("the message does not name %s: %q", want.qualified, message)
	}
}

// TestIssue1666_ARefusedDeleteReportsTheRegistrations is criterion 4: a
// refusal about registrations names them as registrations, in the shape the
// listing reports, so the caller deciding whether to force it can act on the
// rows rather than re-derive them.
func TestIssue1666_ARefusedDeleteReportsTheRegistrations(t *testing.T) {
	c := connect(t)
	reference, _ := uploadCSV1666(t, c)
	want := registerTable1666(t, c, reference, "delete")

	refused := c.call("manage_resource", map[string]any{
		"action": "delete", "reference": reference,
	})
	if deleted, _ := refused["deleted"].(bool); deleted {
		t.Fatalf("the file was deleted while a table was registered over it: %v", refused)
	}
	if _, present := refused["tables"]; present {
		t.Errorf("the refusal still answers under tables: %v", refused)
	}
	rows, ok := refused["table_registrations"].([]any)
	if !ok {
		t.Fatalf("the refusal names no table_registrations: %v", refused)
	}
	if len(rows) != 1 {
		t.Fatalf("the refusal names %d registrations over a file registered once: %v", len(rows), rows)
	}
	row := object1666(t, rows[0])
	if got, _ := row["registration_id"].(string); got != want.id {
		t.Errorf("the refusal names registration %q; want %q", got, want.id)
	}
	if got, _ := row["query_table"].(string); got != want.qualified {
		t.Errorf("query_table = %q; want %q", got, want.qualified)
	}
}

// --- helpers ---

// issue1666Columns is the header of the CSV uploaded below, which is what a
// registration over it declares as its columns.
var issue1666Columns = []string{"store_id", "store_name", "units"}

const issue1666CSV = "store_id,store_name,units\n1,North,10\n2,South,20\n"

const issue1666CSVSecondVersion = "store_id,store_name,units\n1,North,11\n2,South,22\n3,East,33\n"

// registration1666 is what one register call produced.
type registration1666 struct {
	id        string
	qualified string
}

// registerTable1666 registers one table over the file and drops it when the
// test ends.
func registerTable1666(t *testing.T, c *client, reference, suffix string) registration1666 {
	t.Helper()
	name := fmt.Sprintf("acc_1666_%s_%d", suffix, time.Now().UnixNano())
	out := c.call("manage_table", map[string]any{
		"action": "register", "reference": reference,
		"connection": scratchResourceConnection, "table_name": name,
		"follow": true, "repair": false,
	})
	id, _ := out["registration_id"].(string)
	qualified, _ := out["query_table"].(string)
	if id == "" || qualified == "" {
		t.Fatalf("registering %s returned no registration: %v", name, out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_table",
			map[string]any{"action": "unregister", "registration_id": id})
	})
	return registration1666{id: id, qualified: qualified}
}

// fetchDoc1666 reads the file in full through the tool a caller uses.
func fetchDoc1666(t *testing.T, c *client, reference string) map[string]any {
	t.Helper()
	out := c.call("fetch", map[string]any{
		"reference": reference,
		"purpose":   "Acceptance for #1666: reading a stored file in full to see the tables registered over it.",
	})
	doc, ok := out["document"].(map[string]any)
	if !ok {
		t.Fatalf("fetch %s returned no document: %v", reference, out)
	}
	return doc
}

// columnsOf1666 reads a row's column names, tolerating either shape a surface
// could report them in: the tool's list of names, or objects carrying a name.
func columnsOf1666(t *testing.T, row map[string]any) []string {
	t.Helper()
	raw, _ := row["columns"].([]any)
	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		switch v := entry.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			name, _ := v["name"].(string)
			out = append(out, name)
		default:
			t.Fatalf("a column is neither a name nor an object: %v", entry)
		}
	}
	return out
}

func sameStrings1666(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func object1666(t *testing.T, entry any) map[string]any {
	t.Helper()
	obj, ok := entry.(map[string]any)
	if !ok {
		t.Fatalf("entry is not an object: %v", entry)
	}
	return obj
}

// uploadCSV1666 stores a small, readable CSV as a managed resource and returns
// the reference a registration is made against, plus its display name.
func uploadCSV1666(t *testing.T, c *client) (reference, name string) {
	t.Helper()
	name = fmt.Sprintf("acceptance-1666-%d", time.Now().UnixNano())
	status, body := send1631(t, c, http.MethodPost, "/api/v1/resources", func(w *multipart.Writer) error {
		for field, value := range map[string]string{
			"scope":        "global",
			"path":         "acceptance-1666",
			"display_name": name,
			"description":  "Acceptance #1666: one vocabulary for the tables registered over a file.",
		} {
			if err := w.WriteField(field, value); err != nil {
				return err
			}
		}
		part, err := filePart1631(w, name+".csv", "text/csv")
		if err != nil {
			return err
		}
		if _, err := part.Write([]byte(issue1666CSV)); err != nil {
			return err
		}
		return w.Close()
	})
	if status != http.StatusCreated {
		t.Fatalf("uploading the CSV: status %d: %v", status, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("the upload returned no id: %v", body)
	}
	t.Cleanup(func() {
		_, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody)
	})
	return "mcp:resource:" + id, name
}
