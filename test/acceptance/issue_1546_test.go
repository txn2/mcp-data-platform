//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// scratchConnection is the dev stack's Trino connection that carries a scratch
// target (dev/platform.yaml, acme-scratch) over the dev Trino's Hive catalog.
const scratchConnection = "acme-scratch"

// Issue #1546: a write that runs DROP TABLE (a follow here) asks afterwards
// whether every other registration on the connection still resolves, records
// the ones that do not, and says so in its result, so a registration whose
// table is gone is never reported as registered.
//
// The table is removed by hand through the connection, the way an object
// store with a prefix-listing fault removes a name-prefix sibling during a
// DROP; what is under test is that the platform notices and reports it.
func TestIssue1546_AFollowReportsARegistrationWhoseTableIsGone(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())

	created := c.call("manage_resource", map[string]any{
		"action":       "create",
		"display_name": "Acceptance 1546 " + stamp,
		"description":  "Acceptance: a CSV two tables are registered over, one of which is dropped by hand before a replace.",
		"filename":     "acc-1546-" + stamp + ".csv",
		"path":         "acceptance/issue-1546",
		"content_type": "text/csv",
		"content":      "store_id,units\n1,10\n2,20\n",
	})
	reference, _ := created["reference"].(string)
	if reference == "" {
		t.Fatalf("manage_resource create returned no reference: %v", created)
	}

	following := c.call("manage_table", map[string]any{
		"action": "register", "reference": reference, "connection": scratchConnection,
		"table_name": "acc_" + stamp,
	})
	sibling := c.call("manage_table", map[string]any{
		"action": "register", "reference": reference, "connection": scratchConnection,
		"table_name": "acc_" + stamp + "_pinned", "follow": false,
	})
	siblingTable, _ := sibling["query_table"].(string)
	t.Cleanup(func() {
		for _, reg := range []map[string]any{following, sibling} {
			if id, _ := reg["registration_id"].(string); id != "" {
				_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": id})
			}
		}
	})

	// The sibling's table disappears behind the platform's back.
	c.call("trino_execute", map[string]any{
		"connection": scratchConnection,
		"sql":        "DROP TABLE " + siblingTable,
		"purpose":    "Acceptance: remove a registered table by hand, as a store fault would.",
	})

	replaced := c.call("manage_resource", map[string]any{
		"action":         "replace_content",
		"reference":      reference,
		"content":        "store_id,units\n1,11\n2,22\n3,33\n",
		"change_summary": "Acceptance: a replace whose follow must notice the missing sibling.",
	})
	tables, _ := replaced["tables"].([]any)
	var reported bool
	for _, line := range tables {
		text, _ := line.(string)
		if strings.Contains(text, siblingTable) && strings.Contains(text, "no longer exists") {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("the replace did not report the missing table %s: tables = %v", siblingTable, tables)
	}

	listing := c.call("manage_table", map[string]any{"action": "list", "reference": reference})
	regs, _ := listing["registrations"].([]any)
	var flagged bool
	for _, entry := range regs {
		reg, _ := entry.(map[string]any)
		if reg["query_table"] == siblingTable {
			followErr, _ := reg["follow_error"].(string)
			flagged = strings.Contains(followErr, "no longer exists")
		}
	}
	if !flagged {
		t.Fatalf("the listing still reports %s as registered with no follow_error: %v", siblingTable, listing)
	}
}
