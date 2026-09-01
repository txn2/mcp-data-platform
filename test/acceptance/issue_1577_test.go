//go:build integration

package acceptance

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// Issue #1577: a registration made with repair corrected its file once, on the
// day it was made, and then met the same defect on every version after that. A
// source producing one correctable defect on a schedule stranded its table
// permanently, and a producer reading its own rows back through the stalled
// table regressed its own file while reporting success.
//
// What these hold: the choice is carried on the registration and re-applied by
// the follow -- the corrected version is written under the registrant, above
// the version that carried the defect -- and everything that was refused
// before is refused still.
//
// Wire forms: `repair` is a boolean, so a call admits it as true, as false and
// as absent. Every one of the three is sent below, through the tool and
// through the REST route the portal panel posts.

// torn1577 is the file a weekly spreadsheet export produces: a multi-line
// address in one cell. A query engine splits records on newlines before it
// looks at the quotes, so each of those rows would come back torn.
func torn1577(units ...string) string {
	body := "store_id,address,units\n" +
		"101,\"12 Mill Rd\nSuite 4\"," + units[0] + "\n" +
		"102,\"9 Bay St\nSeattle WA\"," + units[1] + "\n"
	if len(units) > 2 {
		body += "103,880 Pine St," + units[2] + "\n"
	}
	return body
}

// ragged1577 carries a defect the platform will not correct: a torn cell, and
// a record that does not have the header's fields.
const ragged1577 = "store_id,address,units\n101,\"12 Mill Rd\nSuite 4\",10\n9\n"

// utf16LE1577 is a spreadsheet's Unicode Text export: a wide encoding, refused
// outright because reading it as a code page would write mojibake back as the
// person's file.
func utf16LE1577() string {
	var out bytes.Buffer
	out.Write([]byte{0xff, 0xfe})
	for _, r := range utf16.Encode([]rune("store_id,address,units\n101,12 Mill Rd,10\n")) {
		out.WriteByte(byte(r))
		out.WriteByte(byte(r >> 8))
	}
	return base64.StdEncoding.EncodeToString(out.Bytes())
}

// csvResource1577 files a CSV as a managed resource and returns its reference
// and id.
func csvResource1577(t *testing.T, c *client, stamp, content string) (reference, id string) {
	t.Helper()
	created := c.call("manage_resource", map[string]any{
		"action":       "create",
		"display_name": "Acceptance 1577 " + stamp,
		"description":  "Acceptance: a CSV whose next version carries the same correctable defect.",
		"filename":     "acc-1577-" + stamp + ".csv",
		"path":         "acceptance/issue-1577",
		"content_type": "text/csv",
		"content":      content,
	})
	reference, _ = created["reference"].(string)
	id, _ = created["resource_id"].(string)
	if reference == "" || id == "" {
		t.Fatalf("manage_resource create returned no reference: %v", created)
	}
	return reference, id
}

// register1577 registers a table over a file and drops it when the test ends.
// repair is passed as the caller gives it: a *bool so the absent form is
// reachable alongside true and false.
func register1577(t *testing.T, c *client, reference, table string, repair *bool) map[string]any {
	t.Helper()
	args := map[string]any{
		"action": "register", "reference": reference, "connection": scratchConnection,
		"table_name": table, "follow": true,
	}
	if repair != nil {
		args["repair"] = *repair
	}
	registered := c.call("manage_table", args)
	id, _ := registered["registration_id"].(string)
	if id == "" {
		t.Fatalf("manage_table register returned no registration: %v", registered)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": id})
	})
	return registered
}

// versions1577 reads the file's version history as the version panel does.
func versions1577(t *testing.T, c *client, id string) []map[string]any {
	t.Helper()
	status, body := c.rest("GET", "/api/v1/resources/"+id+"/versions", nil)
	if status != 200 {
		t.Fatalf("reading the version history: status %d, %v", status, body)
	}
	raw, _ := body["versions"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		if v, ok := entry.(map[string]any); ok {
			out = append(out, v)
		}
	}
	return out
}

// versionNumbered finds one version of a file by its number.
func versionNumbered(versions []map[string]any, want int) map[string]any {
	for _, v := range versions {
		if n, ok := v["version"].(float64); ok && int(n) == want {
			return v
		}
	}
	return nil
}

// replace1577 writes a new version of the file and returns the sentences the
// write reports about the tables over it.
func replace1577(t *testing.T, c *client, reference string, args map[string]any) (map[string]any, string) {
	t.Helper()
	call := map[string]any{
		"action": "replace_content", "reference": reference,
		"change_summary": "Acceptance #1577: the next version from the same source.",
	}
	for k, v := range args {
		call[k] = v
	}
	replaced := c.call("manage_resource", call)
	lines, _ := replaced["tables"].([]any)
	var said []string
	for _, line := range lines {
		text, _ := line.(string)
		said = append(said, text)
	}
	return replaced, strings.Join(said, " ")
}

// count1577 is how many rows the registered table serves.
func count1577(t *testing.T, c *client, queryTable, why string) string {
	t.Helper()
	queried := c.call("trino_query", map[string]any{
		"connection": scratchConnection,
		"sql":        "SELECT count(*) AS n FROM " + queryTable,
		"purpose":    why,
	})
	return fmt.Sprintf("%v", queried)
}

// TestIssue1577_AFollowReAppliesTheCorrection covers criteria 1, 2 and 3: the
// later version carrying the same defect leaves the table reading that
// version's rows, through a corrected version saved above it under the
// registrant, and the write that triggered it says both halves and succeeds.
func TestIssue1577_AFollowReAppliesTheCorrection(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	admin := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())

	reference, id := csvResource1577(t, owner, stamp, torn1577("10", "20"))
	repair := true
	registered := register1577(t, owner, reference, "acc1577a_"+stamp, &repair)
	queryTable, _ := registered["query_table"].(string)
	if got, _ := registered["repair"].(bool); !got {
		t.Fatalf("the registration does not carry the repair choice: %v", registered)
	}
	// Registering corrected the file it was handed, so the head is version 2.
	if v := versionNumbered(versions1577(t, owner, id), 2); v == nil {
		t.Fatalf("registering with repair wrote no corrected version: %v", versions1577(t, owner, id))
	}

	// The next version arrives from the same source with the same defect, and
	// is written by the administrator rather than by the registrant.
	replaced, said := replace1577(t, admin, reference, map[string]any{"content": torn1577("11", "22", "33")})
	if replaced["resource_id"] != id {
		t.Fatalf("the replace did not answer about this file: %v", replaced)
	}
	if !strings.Contains(said, queryTable+" on "+scratchConnection+" now reads version 4.") {
		t.Fatalf("the write did not report the table as followed: %s", said)
	}
	if !strings.Contains(said, "Saved version 4 of this file, which put 2 rows back onto one line.") {
		t.Fatalf("the write did not report the correction it saved: %s", said)
	}

	// The table reads the later version's rows, corrected.
	rows := count1577(t, owner, queryTable, "Acceptance #1577: the table reads the corrected later version.")
	if !strings.Contains(rows, "3") {
		t.Fatalf("the table does not serve the three rows the new version holds: %s", rows)
	}

	// The corrected version is the registrant's, says what it changed, and
	// sits above the version that carried the defect.
	history := versions1577(t, owner, id)
	corrected, defective := versionNumbered(history, 4), versionNumbered(history, 3)
	if corrected == nil || defective == nil {
		t.Fatalf("the history does not hold the defective version and the correction above it: %v", history)
	}
	if corrected["uploader_email"] != devOwnerEmail {
		t.Fatalf("the corrected version is not attributed to the registrant: %v", corrected)
	}
	if summary, _ := corrected["change_summary"].(string); summary != "put 2 rows back onto one line" {
		t.Fatalf("the corrected version does not say what it changed: %v", corrected)
	}

	// The version carrying the defect is still restorable, which is what makes
	// the correction undoable. Restoring it writes those bytes back as a new
	// version, and the follow meets that version and corrects it again --
	// which is what the registration is for, and what the documentation says
	// happens.
	status, body := owner.rest("POST", "/api/v1/resources/"+id+"/versions/3/restore", nil)
	if status != 200 && status != 201 {
		t.Fatalf("the version below the correction is not restorable: status %d, %v", status, body)
	}
	restored := versions1577(t, owner, id)
	if versionNumbered(restored, 5) == nil || versionNumbered(restored, 6) == nil {
		t.Fatalf("the restore did not write the defective bytes back and get corrected again: %v", restored)
	}
	if summary, _ := versionNumbered(restored, 6)["change_summary"].(string); summary != "put 2 rows back onto one line" {
		t.Fatalf("the version above the restore is not the correction: %v", versionNumbered(restored, 6))
	}
	rows = count1577(t, owner, queryTable,
		"Acceptance #1577: the table is current again after the restored version was corrected.")
	if !strings.Contains(rows, "3") {
		t.Fatalf("the table does not serve the corrected restore: %s", rows)
	}
}

// TestIssue1577_ARegistrationWithoutTheChoiceIsLeftBehind is criterion 4:
// nobody asked for this file to be rewritten, so it is not.
func TestIssue1577_ARegistrationWithoutTheChoiceIsLeftBehind(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())

	reference, id := csvResource1577(t, c, stamp, "store_id,address,units\n101,12 Mill Rd,10\n")
	registered := register1577(t, c, reference, "acc1577b_"+stamp, nil)
	registrationID, _ := registered["registration_id"].(string)
	queryTable, _ := registered["query_table"].(string)
	if got, _ := registered["repair"].(bool); got {
		t.Fatalf("a registration that asked for nothing carries the choice: %v", registered)
	}

	_, said := replace1577(t, c, reference, map[string]any{"content": torn1577("11", "22", "33")})
	if !strings.Contains(said, "Register it again asking for the file to be corrected") {
		t.Fatalf("the write did not report the table as behind with the reason: %s", said)
	}

	if history := versions1577(t, c, id); len(history) != 2 {
		t.Fatalf("the platform wrote a version of a file nobody asked it to correct: %v", history)
	}
	if reason := followError1577(t, c, reference, registrationID); reason == "" {
		t.Fatalf("the registration does not record why it was not moved")
	}
	rows := count1577(t, c, queryTable, "Acceptance #1577: a table nobody asked to correct stays where it was.")
	if !strings.Contains(rows, "1") {
		t.Fatalf("the table did not stay on the version it was registered over: %s", rows)
	}
}

// followError1577 is what the listing says about why one registration was not
// moved.
func followError1577(t *testing.T, c *client, reference, registrationID string) string {
	t.Helper()
	listing := c.call("manage_table", map[string]any{"action": "list", "reference": reference})
	rows, _ := listing["registrations"].([]any)
	for _, entry := range rows {
		reg, _ := entry.(map[string]any)
		if reg["registration_id"] == registrationID {
			reason, _ := reg["follow_error"].(string)
			return reason
		}
	}
	t.Fatalf("the registration is not in the listing: %v", listing)
	return ""
}

// TestIssue1577_AnUncorrectableVersionIsStillRefused is criterion 5: what the
// platform cannot honestly correct it does not touch, whatever the
// registration asked for.
func TestIssue1577_AnUncorrectableVersionIsStillRefused(t *testing.T) {
	cases := []struct {
		name    string
		content map[string]any
		reason  string
	}{
		{
			name:    "a wide encoding",
			content: map[string]any{"content_base64": utf16LE1577(), "content_type": "text/csv"},
			reason:  "Re-export it as UTF-8 CSV",
		},
		{
			name:    "records that do not match the header",
			content: map[string]any{"content": ragged1577},
			reason:  "its records do not all have the header's 3 fields",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := connect(t)
			stamp := fmt.Sprintf("%d", time.Now().UnixNano())

			reference, id := csvResource1577(t, c, stamp, "store_id,address,units\n101,12 Mill Rd,10\n")
			repair := true
			registered := register1577(t, c, reference, "acc1577c_"+stamp, &repair)
			registrationID, _ := registered["registration_id"].(string)

			_, said := replace1577(t, c, reference, tc.content)
			if !strings.Contains(said, tc.reason) {
				t.Fatalf("the write did not report the reason the table stayed behind: %s", said)
			}
			if strings.Contains(said, "Saved version") {
				t.Fatalf("a version was saved for a file the platform will not correct: %s", said)
			}
			if history := versions1577(t, c, id); len(history) != 2 {
				t.Fatalf("a corrected version was written for an uncorrectable file: %v", history)
			}
			if reason := followError1577(t, c, reference, registrationID); !strings.Contains(reason, tc.reason) {
				t.Fatalf("the registration does not record the reason: %q", reason)
			}
		})
	}
}

// TestIssue1577_OneCorrectionServesEveryTableOverTheFile is criterion 6: the
// correction is a version of the file, so a defective version produces one of
// them however many tables are over it, and both tables read it.
func TestIssue1577_OneCorrectionServesEveryTableOverTheFile(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())

	reference, id := csvResource1577(t, c, stamp, "store_id,address,units\n101,12 Mill Rd,10\n")
	repair := true
	first := register1577(t, c, reference, "acc1577d1_"+stamp, &repair)
	second := register1577(t, c, reference, "acc1577d2_"+stamp, &repair)

	_, said := replace1577(t, c, reference, map[string]any{"content": torn1577("11", "22", "33")})
	if n := strings.Count(said, "Saved version 3 of this file"); n != 1 {
		t.Fatalf("the correction was reported %d times rather than once: %s", n, said)
	}
	if history := versions1577(t, c, id); len(history) != 3 {
		t.Fatalf("the file was corrected more than once for one version: %v", history)
	}

	for _, reg := range []map[string]any{first, second} {
		queryTable, _ := reg["query_table"].(string)
		if !strings.Contains(said, queryTable+" on "+scratchConnection+" now reads version 3.") {
			t.Fatalf("%s did not follow onto the corrected version: %s", queryTable, said)
		}
		rows := count1577(t, c, queryTable, "Acceptance #1577: both tables read the one corrected version.")
		if !strings.Contains(rows, "3") {
			t.Fatalf("%s does not serve the corrected version's rows: %s", queryTable, rows)
		}
	}
}

// TestIssue1577_TheChoiceIsReportedAndIsTheOneCurrentlyRegistered covers
// criteria 7 and 8: every listing says which tables correct their file, and
// the choice is the one made at the registration that is current.
func TestIssue1577_TheChoiceIsReportedAndIsTheOneCurrentlyRegistered(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())

	reference, id := csvResource1577(t, c, stamp, "store_id,address,units\n101,12 Mill Rd,10\n")
	table := "acc1577e_" + stamp
	repair, noRepair := true, false
	corrects := register1577(t, c, reference, table, &repair)
	plain := register1577(t, c, reference, "acc1577f_"+stamp, &noRepair)

	// The tool's listing, which is what an agent reads.
	listed := map[string]bool{}
	listing := c.call("manage_table", map[string]any{"action": "list", "reference": reference})
	rows, _ := listing["registrations"].([]any)
	for _, entry := range rows {
		reg, _ := entry.(map[string]any)
		name, _ := reg["query_table"].(string)
		listed[name], _ = reg["repair"].(bool)
	}
	correctsTable, _ := corrects["query_table"].(string)
	plainTable, _ := plain["query_table"].(string)
	if !listed[correctsTable] || listed[plainTable] {
		t.Fatalf("manage_table action=list does not say which tables correct their file: %v", listed)
	}

	// The route the portal's panel reads, which is what a person sees.
	status, body := c.rest("GET", "/api/v1/resources/"+id+"/tables", nil)
	if status != 200 {
		t.Fatalf("reading the file's tables: status %d, %v", status, body)
	}
	panel, _ := body["registrations"].([]any)
	var sawCorrecting bool
	for _, entry := range panel {
		reg, _ := entry.(map[string]any)
		if reg["query_table"] == correctsTable {
			sawCorrecting, _ = reg["repair"].(bool)
		}
	}
	if !sawCorrecting {
		t.Fatalf("the portal's tables route does not report the choice: %v", panel)
	}

	// Registering the same name again, asking for nothing, leaves a table that
	// corrects nothing. The register body says so without the field at all,
	// which is the third form the boolean admits.
	again := register1577(t, c, reference, table, nil)
	if got, _ := again["repair"].(bool); got {
		t.Fatalf("the choice survived a registration that did not make it: %v", again)
	}
	_, said := replace1577(t, c, reference, map[string]any{"content": torn1577("11", "22", "33")})
	if strings.Contains(said, "Saved version") {
		t.Fatalf("a file was corrected for a registration that no longer asks for it: %s", said)
	}
}

// TestIssue1577_AProducerReadingItsOwnRowsBackDoesNotRegress is criterion 9,
// and is the consequence the ticket was filed for. An incremental sync reads
// the current rows through the registered table, applies the window's changes,
// and writes the whole file back. A stalled follow makes that read the version
// before the one it is extending, so the write silently drops everything the
// stalled version added and reports success.
func TestIssue1577_AProducerReadingItsOwnRowsBackDoesNotRegress(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())

	reference, _ := csvResource1577(t, c, stamp, "store_id,address,units\n101,12 Mill Rd,10\n")
	repair := true
	registered := register1577(t, c, reference, "acc1577g_"+stamp, &repair)
	queryTable, _ := registered["query_table"].(string)

	// The source's next run writes three rows, two of them torn.
	if _, said := replace1577(t, c, reference, map[string]any{"content": torn1577("11", "22", "33")}); !strings.Contains(said, "Saved version 3 of this file") {
		t.Fatalf("the defective version was not corrected as the table follows it: %s", said)
	}

	// The producer reads what the table holds now and writes the whole file
	// back with one more row.
	before := count1577(t, c, queryTable, "Acceptance #1577: an incremental sync reads its own prior rows back.")
	if !strings.Contains(before, "3") {
		t.Fatalf("the producer read the stale version back: %s", before)
	}
	_, said := replace1577(t, c, reference, map[string]any{
		"content": "store_id,address,units\n" +
			"101,12 Mill Rd Suite 4,11\n102,9 Bay St Seattle WA,22\n103,880 Pine St,33\n104,4 Elm Ave,44\n",
	})
	if !strings.Contains(said, "now reads version") {
		t.Fatalf("the table did not follow the producer's write: %s", said)
	}

	after := count1577(t, c, queryTable, "Acceptance #1577: the rewrite kept every row the table already served.")
	if !strings.Contains(after, "4") {
		t.Fatalf("the producer's rewrite regressed the file: %s", after)
	}
}
