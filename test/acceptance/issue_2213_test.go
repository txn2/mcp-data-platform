//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2213: platform.export(register=...) checks the registration
// target before it writes anything. A connection with no scratch target fails
// the export with nothing written, naming the connections a table can be
// registered on; manage_table check answers the same question directly; and
// validate flags the connection before a save.
//
// Wire forms: manage_table's action, connection and table_name are typed
// strings; run_draft's source a typed string and allow_writes a typed
// boolean; each sent as a literal tools/call parameter.

// TestIssue2213_ManageTableCheckAnswersWithNothingWritten.
func TestIssue2213_ManageTableCheckAnswersWithNothingWritten(t *testing.T) {
	c := connect(t)
	ok := c.call("manage_table", map[string]any{"action": "check", "connection": "acme-scratch", "table_name": "acc_2213"})
	if !strings.HasSuffix(fmt.Sprint(ok["query_table"]), "acc_2213") || ok["catalog"] == nil {
		t.Fatalf("check on a scratch connection: %v", ok)
	}
	res, text, err := c.callRaw("manage_table", map[string]any{"action": "check", "connection": "acme"})
	if err != nil || !res.IsError || !strings.Contains(text, "acme-scratch") {
		t.Fatalf("check on a connection with no scratch target names the ones with one: %v %s", err, text)
	}
}

// TestIssue2213_AnExportRegisteringOnAConnectionWithNoScratchTargetWritesNothing.
func TestIssue2213_AnExportRegisteringOnAConnectionWithNoScratchTargetWritesNothing(t *testing.T) {
	c := connect(t)
	stamp := time.Now().UnixNano()
	key := fmt.Sprintf("acceptance/issue-2213/out-%d.jsonl", stamp)
	source := fmt.Sprintf(`def main():
    """Exports one row and registers it on a connection with no scratch target."""
    platform.export(name = "out", rows = [{"a": 1}], format = "jsonl", destination = "resources",
        key = %q, register = {"connection": "acme"})
`, key)
	res, text, err := c.callRaw("manage_script", map[string]any{
		"command": "run_draft", "name": fmt.Sprintf("acc-2213-%d", stamp), "source": source, "allow_writes": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `register cannot use connection \"acme\", so nothing was written`) &&
		!strings.Contains(text, `register cannot use connection "acme", so nothing was written`) {
		t.Fatalf("the draft's failure does not say nothing was written: isError=%v %s", res.IsError, text)
	}
	if !strings.Contains(text, "acme-scratch") {
		t.Fatalf("the failure does not name the connection a table can go on: %s", text)
	}
	got := c.call("manage_resource", map[string]any{"action": "get", "scope": "user", "path": "acceptance/issue-2213", "filename": fmt.Sprintf("out-%d.jsonl", stamp)})
	if got["found"] == true {
		t.Fatalf("the export wrote its file before the registration was refused: %v", got)
	}

	report := c.call("manage_script", map[string]any{"command": "validate", "source": source})
	if !strings.Contains(fmt.Sprint(report["findings"]), `register names connection "acme"`) {
		t.Fatalf("validate does not flag the connection: %v", report["findings"])
	}
}
