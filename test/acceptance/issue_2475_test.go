//go:build integration

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Issue #2475: a manage_schema add whose SQL gives a role a password is
// refused by the migration review's password_literal rule, and the call's
// audit row stored the SQL as sent, password and all. The password literal
// is now replaced wherever the SQL is kept: the audit row, a script's
// recording; the down SQL is refused by the same rule, so it is never saved.
//
// Wire forms: manage_schema takes command, connection, schema, name, up and
// down as strings and version as an integer, sent as those JSON types; no
// parameter of the tool admits a second form. manage_script takes command,
// name and source as strings and allow_writes as a boolean; command=recording
// takes run_id as a string.

// TestIssue2475_ARefusedMigrationLeavesNoPasswordInAnySink is the first two
// criteria: CREATE ROLE ... PASSWORD in up, ALTER ROLE ... PASSWORD in up,
// and a down that sets a password are each refused, and no form of any of
// the three passwords is found in the platform database (the audit rows and
// the call catalog among its tables), the replicas' logs, the collector's
// traces and logs, or any response the run read, the refusals included.
func TestIssue2475_ARefusedMigrationLeavesNoPasswordInAnySink(t *testing.T) {
	admin := connectFor(t, 10*time.Minute)
	k := newCanary2363(t)
	schema := schema2404(t, "pw")

	cases := []struct {
		label    string
		up, down string
		want     string
	}{
		{"create role in up", "CREATE ROLE acc2475_r LOGIN PASSWORD '" + k.plant("create-role") + "'", "", "statement 1 (CREATE ROLE)"},
		{"alter role in up", "CREATE TABLE acc2475_t (id int);\nALTER ROLE acc2475_r PASSWORD '" + k.plant("alter-role") + "'", "", "statement 2 (ALTER ROLE)"},
		{"password in down", "CREATE TABLE acc2475_t (id int)", "DROP TABLE acc2475_t;\nALTER ROLE acc2475_r WITH PASSWORD '" + k.plant("down") + "'", "down statement 2 (ALTER ROLE)"},
	}
	for _, c := range cases {
		args := map[string]any{
			"command": "add", "connection": conn2404, "schema": schema, "version": 1,
			"name": "acceptance 2475 " + c.label, "up": c.up,
		}
		if c.down != "" {
			args["down"] = c.down
		}
		ok, text := k.call(admin, "manage_schema", args)
		if ok || !strings.Contains(text, c.want) || !strings.Contains(text, "writes a credential") {
			t.Errorf("%s: want a password_literal refusal naming %q, got ok=%v %s", c.label, c.want, ok, text)
		}
	}
	status := schemaCall(admin, schema, map[string]any{"command": "status"})
	if pending, _ := status["pending"].([]any); len(pending) != 0 {
		t.Errorf("a refused migration was saved: %v", status)
	}

	// The same statement sent by a script, the way a run executes SQL on an
	// application database: its audit row and its recording. The password is
	// joined at run time, so the source the draft is audited with does not
	// hold it whole; only the statement the run executes does. The run prints
	// the control, which comes back in the response.
	value := k.plant("script")
	tail := value[len(value)-4:]
	ok, text := k.call(admin, "manage_script", map[string]any{
		"command": "run_draft", "name": "acceptance-2475-" + unique1579(), "allow_writes": true,
		"source": `def main():
    """Sets a role's password the way a migration may not."""
    print("` + k.control + `")
    password = "` + value[:len(value)-len(tail)] + `" + "` + tail + `"
    platform.execute("ALTER ROLE acc2475_nobody PASSWORD '" + password + "'", connection = "` + conn2404 + `")
`,
	})
	var ran map[string]any
	_ = json.Unmarshal([]byte(text), &ran)
	if recording, _ := ran["recording"].(string); recording != "" {
		ok, text = k.call(admin, "manage_script", map[string]any{"command": "recording", "run_id": recording})
		if !ok || !strings.Contains(text, "[REDACTED]") {
			t.Errorf("the run's recording does not read back with the password replaced: %s", firstRunes(text, 600))
		}
	} else {
		t.Errorf("the draft kept no recording (ok=%v): %s", ok, firstRunes(text, 600))
	}

	k.awaitWriters(admin)
	k.searchDatabase()
	k.searchFiles()
	k.searchResponses()
	if !k.found["response"] {
		t.Error("the control value was not found in any response; the search would miss a leak there")
	}
}

// TestIssue2475_AMigrationWithNoCredentialIsAuditedAsSent is the third
// criterion: a migration that sets no password is added, and its audit row
// holds the up and down SQL exactly as they were sent.
func TestIssue2475_AMigrationWithNoCredentialIsAuditedAsSent(t *testing.T) {
	admin := connect(t)
	schema := schema2404(t, "plain")
	up := "CREATE TABLE acc2475_plain (id bigint PRIMARY KEY, note text DEFAULT 'password reset pending');\nCOMMENT ON TABLE acc2475_plain IS 'kept as sent'"
	down := "DROP TABLE acc2475_plain"
	added := schemaCall(admin, schema, map[string]any{"command": "add", "version": 1, "name": "plain", "up": up, "down": down})
	if added["status"] != "pending" {
		t.Fatalf("the migration did not add: %v", added)
	}

	var params map[string]any
	for attempt := 0; attempt < 40 && params == nil; attempt++ {
		if attempt > 0 {
			time.Sleep(250 * time.Millisecond)
		}
		for _, row := range admin.list("/api/v1/admin/audit/events?per_page=200&tool_name=manage_schema&session_id=" + admin.sessionID) {
			m, _ := row.(map[string]any)
			p, _ := m["parameters"].(map[string]any)
			if p["command"] == "add" && p["schema"] == schema {
				params = p
			}
		}
	}
	if params == nil {
		t.Fatal("no audit row for the add")
	}
	if params["up"] != up || params["down"] != down {
		b, _ := json.Marshal(params)
		t.Errorf("the audit row changed the SQL: %s", b)
	}
}
