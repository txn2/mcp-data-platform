//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// Issue #1819: a table registered over a CSV read it with the Hive reader's
// default escape character, a backslash, which no writer of these files uses.
// "back\slash" read as "backslash", a trailing backslash vanished, and a quoted
// cell holding a backslash beside a quote read as an empty string, with the row
// and column counts intact.
//
// What these hold, against the running platform: a CSV uploaded as a managed
// resource and a CSV a managed script exports both serve every backslash from
// the table registered over them, wherever it sits in the cell.
//
// Wire forms: manage_resource's `action`, `display_name`, `filename`, `path`,
// `content_type`, `content` and `description` are typed string, as are every
// argument of the script, manage_table and trino_query calls #1818's helpers
// send, so each is sent in its one form.

const issue1819Purpose = "Acceptance for #1819: a registered table serves every backslash in its file."

// issue1819Values are the backslash placements the default escape lost, keyed
// by the column each is written under.
var issue1819Values = map[string]string{
	"mid":      `back\slash`,
	"trailing": `trailing \`,
	"quoted":   `q"\`,
	"leading":  `\"`,
	"path":     `C:\data\n.txt`,
	"doubled":  `\\double`,
	"regex":    `^\d+\.\d+$`,
}

// issue1819CSV is those values as RFC 4180 writes them: a quote doubled inside
// a quoted cell, and nothing else escaped. It is the bytes Go's encoding/csv
// produces for issue1819Values, written out so the file is the ticket's shape
// whatever the platform's own writer does.
const issue1819CSV = "mid,trailing,quoted,leading,path,doubled,regex\n" +
	`back\slash,trailing \,"q""\","\""",C:\data\n.txt,\\double,^\d+\.\d+$` + "\n"

// TestIssue1819_AnUploadedCSVKeepsItsBackslashes registers a table over an
// uploaded file and reads every value back.
func TestIssue1819_AnUploadedCSVKeepsItsBackslashes(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	created := c.call("manage_resource", map[string]any{
		"action":       "create",
		"display_name": "Acceptance 1819 " + stamp,
		"filename":     "acc-1819-" + stamp + ".csv",
		"path":         "acceptance/issue-1819",
		"content_type": "text/csv",
		"content":      issue1819CSV,
		"description":  "Acceptance fixture for #1819: one row holding backslashes in every position.",
	})
	reference, _ := created["reference"].(string)
	if reference == "" {
		t.Fatalf("manage_resource create returned no reference: %v", created)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_resource", map[string]any{"action": "delete", "reference": reference})
	})

	got := registeredRow(t, c, reference, "acc_1819_upload_"+stamp, issue1819Purpose)
	assertValues(t, issue1819Values, got)
}

// TestIssue1819_AScriptExportKeepsItsBackslashes is the ticket's pipeline: the
// platform's own writer produces the file, and the platform's own reader must
// serve what it wrote.
func TestIssue1819_AScriptExportKeepsItsBackslashes(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	row, err := json.Marshal(issue1819Values)
	if err != nil {
		t.Fatal(err)
	}
	reference := runExportScript(t, c, "acc-1819-"+stamp,
		fmt.Sprintf(issue1818ScriptSource, string(row), "acc-1819-script-"+stamp+".csv"))

	got := registeredRow(t, c, reference, "acc_1819_script_"+stamp, issue1819Purpose)
	assertValues(t, issue1819Values, got)
}
