//go:build integration

package acceptance

import (
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

// Issue #1774: a CSV whose bytes begin with a UTF-8 byte-order mark followed
// by a QUOTED first header field was refused with "the file has no header row,
// so the table has no column names" -- about a header the file has and the
// portal's own CSV viewer renders.
//
// The mark is the first bytes of the first field, not a declaration, so
// encoding/csv reads <mark>"Post ID" as an unquoted field carrying a bare
// quote and fails the parse on line 1. ReadHeaderColumns turned every error
// there into ErrEmptyHeader, and tablecsv.Inspect -- which runs first -- died
// on the same line, recorded it as Unreadable, and returned nil, so the
// defects the file actually had were never found and the correction was never
// offered.
//
// Every export that quotes its strings writes this shape: Facebook Insights,
// Excel's "CSV UTF-8", Google Ads. It reached a live customer session.
//
// What these hold: such a file registers, a defect BEHIND the mark is found
// and correctable, and a first line that genuinely cannot be parsed says so
// instead of blaming a header that is there.
//
// Wire forms. The bytes reach the platform three ways and every one of them is
// sent below: manage_resource `content` (the file as text in a JSON string),
// manage_resource `content_base64` (the same bytes base64-encoded), and the
// multipart POST /api/v1/resources route the portal's upload dialog posts.
// `repair` on the register call is a boolean, so it admits true, false and
// absent; all three are sent.

// bom1774 is the mark's bytes. It is written out rather than as "<mark>"
// because a Go source file may not carry one in a literal.
var bom1774 = []byte{0xEF, 0xBB, 0xBF}

// insightsExport1774 is the Facebook Insights shape: the mark, then a header
// whose every field is quoted.
func insightsExport1774(rows string) []byte {
	return append(append([]byte{}, bom1774...),
		[]byte("\"Post ID\",\"Page ID\",\"Title\"\n"+rows)...)
}

// insightsWithCommaColumn1774 is the same export with the column that heading
// actually carries: "Reactions, Comments and Shares". The Hive metastore
// stores a table's column list comma-separated, so the connector refuses a
// name holding one and no quoting gets past it.
func insightsWithCommaColumn1774(rows string) []byte {
	return append(append([]byte{}, bom1774...),
		[]byte("\"Post ID\",\"Reactions, Comments and Shares\",\"Title\"\n"+rows)...)
}

// insightsRows1774 is a clean body for that header.
const insightsRows1774 = "p1,pg1,Launch day\np2,pg2,Second post\n"

// insightsTornRows1774 carries the defect the correction exists for: a line
// break inside a quoted cell, which a line-based reader tears the record on.
const insightsTornRows1774 = "p1,pg1,\"Launch day\nand the night after\"\np2,pg2,Second post\n"

// TestIssue1774_AMarkBeforeAQuotedHeaderRegisters is criterion 1, sent in all
// three forms the bytes admit. Each is a separate file and a separate table,
// and all three must answer identically.
func TestIssue1774_AMarkBeforeAQuotedHeaderRegisters(t *testing.T) {
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())

	for _, form := range []string{"content", "content_base64", "multipart"} {
		t.Run(form, func(t *testing.T) {
			c := connect(t)
			reference, _ := file1774(t, c, form, stamp+form, insightsExport1774(insightsRows1774))

			// The whole form is in the name: two forms sharing a prefix would
			// ask for one table, and the second registration would replace the
			// first rather than standing beside it.
			registered := register1774(t, c, reference, "acc1774"+form+"_"+stamp, nil)
			assertHeaderColumns1774(t, registered)

			// And the table serves the file's rows, so the columns the
			// registration declared are the ones Trino reads -- which is also
			// what proves the header line, mark and all, was skipped.
			if got := countRows1774(t, c, queryTable1774(t, registered)); got != 2 {
				t.Fatalf("the table serves %d rows, not the file's 2", got)
			}
		})
	}
}

// TestIssue1774_AMarkDoesNotHideTheDefectBehindIt is criterion 2, and the half
// that cost the most: with the parse failing on line 1 the inspection reached
// no record, so the line breaks inside cells were never seen, the refusal
// blamed the header, and `repair: true` did nothing because there was no
// defect to correct.
func TestIssue1774_AMarkDoesNotHideTheDefectBehindIt(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	reference, id := file1774(t, c, "content", stamp, insightsExport1774(insightsTornRows1774))

	// repair absent, then explicitly false: both are the unasked form, and
	// both must name the line breaks rather than the header.
	for _, repair := range []*bool{nil, boolPtr1774(false)} {
		_, text, err := c.callRaw("manage_table", registerArgs1774(reference, "acc1774torn_"+stamp, repair))
		if err != nil {
			t.Fatalf("manage_table register: transport error: %v", err)
		}
		if !strings.Contains(text, "line break inside a cell") {
			t.Fatalf("the refusal does not name the line breaks: %s", text)
		}
		if !strings.Contains(text, "Title") {
			t.Fatalf("the refusal does not name the column they are in: %s", text)
		}
		if strings.Contains(text, "no header row") {
			t.Fatalf("the refusal still blames the header (#1774): %s", text)
		}
		if !strings.Contains(text, "asking for the file to be corrected") {
			t.Fatalf("the correction is not offered: %s", text)
		}
	}

	// Asked, the correction is saved as the file's next version and the
	// registration is built over it.
	registered := register1774(t, c, reference, "acc1774torn_"+stamp, boolPtr1774(true))
	assertHeaderColumns1774(t, registered)
	if repaired, _ := registered["repaired"].(string); !strings.Contains(repaired, "put 1 row back onto one line") {
		t.Fatalf("the registration does not report the correction: %v", registered)
	}

	// The corrected version is the head, above the bytes that were uploaded.
	status, versions := c.rest(http.MethodGet, "/api/v1/resources/"+id+"/versions", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the version history: status %d, %v", status, versions)
	}
	if list, _ := versions["versions"].([]any); len(list) < 2 {
		t.Fatalf("no corrected version was saved above the upload: %v", versions)
	}

	if got := countRows1774(t, c, queryTable1774(t, registered)); got != 2 {
		t.Fatalf("the corrected table serves %d rows, not the file's 2 -- a torn row is fragments", got)
	}
}

// TestIssue1774_AFirstLineThatCannotBeParsedSaysSo is criterion 3. A file with
// nothing in it has no header row; one whose first line the reader could not
// parse has one, and answering both with the same sentence sent its owner
// looking for a row that is there.
func TestIssue1774_AFirstLineThatCannotBeParsedSaysSo(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	reference, _ := file1774(t, c, "content_base64", stamp,
		[]byte("post_id,he said \"hi\",title\np1,x,Launch\n"))

	for _, repair := range []*bool{nil, boolPtr1774(true)} {
		_, text, err := c.callRaw("manage_table", registerArgs1774(reference, "acc1774bad_"+stamp, repair))
		if err != nil {
			t.Fatalf("manage_table register: transport error: %v", err)
		}
		if !strings.Contains(text, "first line cannot be read as a CSV header") {
			t.Fatalf("the refusal does not name what the reader could not do: %s", text)
		}
		if !strings.Contains(text, "bare \" in non-quoted-field") {
			t.Fatalf("the refusal does not carry the parse error: %s", text)
		}
		if strings.Contains(text, "no header row") {
			t.Fatalf("the refusal still blames the header (#1774): %s", text)
		}
		if strings.Contains(text, "asking for the file to be corrected") {
			t.Fatalf("a correction is offered that would then decline: %s", text)
		}
	}
}

// --- helpers ---

func boolPtr1774(v bool) *bool { return &v }

// file1774 files a CSV as a managed resource through the form named, and
// returns its reference and id. The three forms are the whole set the bytes
// admit: two tool parameters and the multipart route the portal posts.
func file1774(t *testing.T, c *client, form, stamp string, content []byte) (reference, id string) {
	t.Helper()
	name := "Acceptance 1774 " + form + " " + stamp
	filename := "acc-1774-" + form + "-" + stamp + ".csv"

	if form == "multipart" {
		status, body := send1631(t, c, http.MethodPost, "/api/v1/resources", func(w *multipart.Writer) error {
			return writeBytesUpload1774(w, filename, content, map[string]string{
				"scope":        "global",
				"path":         "acceptance/issue-1774",
				"display_name": name,
				"description":  "Acceptance #1774: an export that leads with a byte-order mark.",
			})
		})
		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("uploading the file: status %d, %v", status, body)
		}
		id, _ = body["id"].(string)
		reference, _ = body["reference"].(string)
		if reference == "" && id != "" {
			reference = "mcp:resource:" + id
		}
	} else {
		args := map[string]any{
			"action":       "create",
			"display_name": name,
			"description":  "Acceptance #1774: an export that leads with a byte-order mark.",
			"filename":     filename,
			"path":         "acceptance/issue-1774",
			"content_type": "text/csv",
		}
		if form == "content_base64" {
			args["content_base64"] = base64.StdEncoding.EncodeToString(content)
		} else {
			args["content"] = string(content)
		}
		created := c.call("manage_resource", args)
		reference, _ = created["reference"].(string)
		id, _ = created["resource_id"].(string)
	}

	if id == "" || reference == "" {
		t.Fatalf("the %s form filed no resource (reference %q, id %q)", form, reference, id)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	return reference, id
}

// registerArgs1774 is one register call, with repair sent as the caller gives
// it: a *bool, so the absent form is reachable beside true and false.
func registerArgs1774(reference, table string, repair *bool) map[string]any {
	args := map[string]any{
		"action": "register", "reference": reference, "connection": scratchResourceConnection,
		"table_name": table, "follow": true,
	}
	if repair != nil {
		args["repair"] = *repair
	}
	return args
}

// register1774 registers a table over a file and drops it when the test ends.
func register1774(t *testing.T, c *client, reference, table string, repair *bool) map[string]any {
	t.Helper()
	registered := c.call("manage_table", registerArgs1774(reference, table, repair))
	regID, _ := registered["registration_id"].(string)
	if regID == "" {
		t.Fatalf("manage_table register returned no registration: %v", registered)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": regID})
	})
	return registered
}

// assertHeaderColumns1774 checks the columns the registration declared: the
// header's three names, and no mark on the first of them.
func assertHeaderColumns1774(t *testing.T, registered map[string]any) {
	t.Helper()
	raw, _ := registered["columns"].([]any)
	names := make([]string, 0, len(raw))
	for _, entry := range raw {
		// The tool surface carries the names alone; the REST route carries the
		// name and the type. Both are read, so the criterion holds wherever it
		// is asked.
		switch col := entry.(type) {
		case string:
			names = append(names, col)
		case map[string]any:
			name, _ := col["name"].(string)
			names = append(names, name)
		}
	}
	want := []string{"Post ID", "Page ID", "Title"}
	if len(names) != len(want) {
		t.Fatalf("the table declares %v, not the header's %v", names, want)
	}
	for i, name := range names {
		if name != want[i] {
			t.Fatalf("column %d is %q, not %q (a mark left on the name?)", i+1, name, want[i])
		}
	}
}

// queryTable1774 is the qualified name a query addresses the table by.
func queryTable1774(t *testing.T, registered map[string]any) string {
	t.Helper()
	table, _ := registered["query_table"].(string)
	if table == "" {
		t.Fatalf("the registration names no query table: %v", registered)
	}
	return table
}

// writeBytesUpload1774 composes the multipart body the upload dialog posts:
// the metadata fields, then the file. The file goes last because that is the
// order the route reads, and the part declares text/csv the way a browser
// does.
func writeBytesUpload1774(w *multipart.Writer, filename string, content []byte, fields map[string]string) error {
	for field, value := range fields {
		if err := w.WriteField(field, value); err != nil {
			return err
		}
	}
	head := make(textproto.MIMEHeader)
	head.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	head.Set("Content-Type", "text/csv")
	part, err := w.CreatePart(head)
	if err != nil {
		return err
	}
	if _, err := part.Write(content); err != nil {
		return err
	}
	return w.Close()
}

// countRows1774 is how many rows the registered table serves. The count is
// read as a number rather than matched in the rendered result: a substring
// check for "2" matches the stamp in the table name and the query's own
// timings as readily as the answer.
func countRows1774(t *testing.T, c *client, queryTable string) int {
	t.Helper()
	result := c.call("trino_query", map[string]any{
		"connection": scratchResourceConnection,
		"sql":        "SELECT count(*) AS n FROM " + queryTable,
		"purpose":    "Acceptance #1774: the table over a byte-order-marked export serves its rows.",
	})
	list, _ := result["rows"].([]any)
	if len(list) != 1 {
		t.Fatalf("the count query returned %d rows: %v", len(list), result)
	}
	row, _ := list[0].(map[string]any)
	n, ok := row["n"].(float64)
	if !ok {
		t.Fatalf("the count is not a number: %v", result)
	}
	return int(n)
}

// TestIssue1774_AHeadingHoldingACommaIsStillRegisterable. Getting past the
// byte-order mark is not enough on its own: the header it uncovers is a real
// spreadsheet heading, and a Facebook Insights export names a column
// "Reactions, Comments and Shares". Hive stores a table's column list
// comma-separated, so the connector refuses that name outright -- which made
// the file fail at the DDL, after the correction of its rows had already been
// written.
func TestIssue1774_AHeadingHoldingACommaIsStillRegisterable(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	reference, _ := file1774(t, c, "content", "1774comma"+stamp,
		insightsWithCommaColumn1774("p1,5,Launch day\np2,17,Second post\n"))

	registered := register1774(t, c, reference, "acc1774comma_"+stamp, nil)

	raw, _ := registered["columns"].([]any)
	names := make([]string, 0, len(raw))
	for _, entry := range raw {
		switch col := entry.(type) {
		case string:
			names = append(names, col)
		case map[string]any:
			name, _ := col["name"].(string)
			names = append(names, name)
		}
	}
	want := []string{"Post ID", "Reactions Comments and Shares", "Title"}
	if len(names) != len(want) {
		t.Fatalf("the table declares %v, not %v", names, want)
	}
	for i, name := range names {
		if name != want[i] {
			t.Fatalf("column %d is %q, not %q", i+1, name, want[i])
		}
		if strings.Contains(name, ",") {
			t.Fatalf("column %d still holds a comma, which Hive cannot store: %q", i+1, name)
		}
	}

	// And the table exists and serves the file's rows, which is what proves
	// the DDL ran rather than being refused by the connector.
	if got := countRows1774(t, c, queryTable1774(t, registered)); got != 2 {
		t.Fatalf("the table serves %d rows, not the file's 2", got)
	}
}
