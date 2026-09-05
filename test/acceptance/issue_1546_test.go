//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The dev stack's two Trino connections that carry a scratch target
// (dev/platform.yaml), one per object store.
//
// There are two because a Hive catalog reads its metastore and its tables
// through one S3 client, and the dev stack runs two object stores: portal
// assets on SeaweedFS, managed resources on MinIO, which is the backend that
// bounds a single PUT and so the one the upload path is exercised against
// (#1631). A registration over a file has to name the connection whose catalog
// can reach that file's store. A deployment has one store and one of these.
const (
	// scratchConnection reaches portal assets.
	scratchConnection = "acme-scratch"
	// scratchResourceConnection reaches managed resources.
	scratchResourceConnection = "acme-scratch-resources"
)

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
		"action": "register", "reference": reference, "connection": scratchResourceConnection,
		"table_name": "acc_" + stamp,
	})
	sibling := c.call("manage_table", map[string]any{
		"action": "register", "reference": reference, "connection": scratchResourceConnection,
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
		"connection": scratchResourceConnection,
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

// registerPair files a CSV and registers a following table and a pinned
// sibling over it, returning the reference and both registrations.
func registerPair(c *client, stamp string) (reference string, following, sibling map[string]any) {
	c.t.Helper()
	created := c.call("manage_resource", map[string]any{
		"action":       "create",
		"display_name": "Acceptance 1546 " + stamp,
		"description":  "Acceptance: a CSV two tables are registered over, one of which is dropped by hand.",
		"filename":     "acc-1546-" + stamp + ".csv",
		"path":         "acceptance/issue-1546",
		"content_type": "text/csv",
		"content":      "store_id,units\n1,10\n2,20\n",
	})
	reference, _ = created["reference"].(string)
	if reference == "" {
		c.t.Fatalf("manage_resource create returned no reference: %v", created)
	}
	following = c.call("manage_table", map[string]any{
		"action": "register", "reference": reference, "connection": scratchResourceConnection,
		"table_name": "acc_" + stamp,
	})
	sibling = c.call("manage_table", map[string]any{
		"action": "register", "reference": reference, "connection": scratchResourceConnection,
		"table_name": "acc_" + stamp + "_pinned", "follow": false,
	})
	c.t.Cleanup(func() {
		for _, reg := range []map[string]any{following, sibling} {
			if id, _ := reg["registration_id"].(string); id != "" {
				_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": id})
			}
		}
	})
	return reference, following, sibling
}

// dropByHand removes a registered table through the connection, as a store
// fault would, behind the platform's back.
func dropByHand(c *client, table string) {
	c.t.Helper()
	c.call("trino_execute", map[string]any{
		"connection": scratchResourceConnection,
		"sql":        "DROP TABLE " + table,
		"purpose":    "Acceptance: remove a registered table by hand, as a store fault would.",
	})
}

// siblingFollowError reads the follow_error the listing carries for a table.
func siblingFollowError(c *client, reference, table string) string {
	c.t.Helper()
	listing := c.call("manage_table", map[string]any{"action": "list", "reference": reference})
	regs, _ := listing["registrations"].([]any)
	for _, entry := range regs {
		reg, _ := entry.(map[string]any)
		if reg["query_table"] == table {
			followErr, _ := reg["follow_error"].(string)
			return followErr
		}
	}
	c.t.Fatalf("%s is not in the listing: %v", table, listing)
	return ""
}

// A replacing registration (the same name registered again) runs DROP TABLE
// too, and its result names the sibling whose table is gone.
func TestIssue1546_AReplacingRegistrationReportsARegistrationWhoseTableIsGone(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	reference, _, sibling := registerPair(c, stamp)
	siblingTable, _ := sibling["query_table"].(string)
	dropByHand(c, siblingTable)

	replaced := c.call("manage_table", map[string]any{
		"action": "register", "reference": reference, "connection": scratchResourceConnection,
		"table_name": "acc_" + stamp,
	})
	tables, _ := replaced["tables"].([]any)
	var reported bool
	for _, line := range tables {
		text, _ := line.(string)
		reported = reported || (strings.Contains(text, siblingTable) && strings.Contains(text, "no longer exists") && strings.Contains(text, "was replaced"))
	}
	message, _ := replaced["message"].(string)
	if !reported || !strings.Contains(message, siblingTable) {
		t.Fatalf("the replacing registration did not report the missing table %s: tables = %v, message = %q", siblingTable, tables, message)
	}
	if got := siblingFollowError(c, reference, siblingTable); !strings.Contains(got, "no longer exists") {
		t.Fatalf("the listing still reports %s as registered: follow_error = %q", siblingTable, got)
	}
}

// An unregister runs DROP TABLE and answers nothing but its own outcome, so
// the sibling whose table is gone is reported on the row the listing reads.
func TestIssue1546_AnUnregisterRecordsARegistrationWhoseTableIsGone(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	reference, following, sibling := registerPair(c, stamp)
	siblingTable, _ := sibling["query_table"].(string)
	dropByHand(c, siblingTable)

	id, _ := following["registration_id"].(string)
	c.call("manage_table", map[string]any{"action": "unregister", "registration_id": id})

	if got := siblingFollowError(c, reference, siblingTable); !strings.Contains(got, "no longer exists") || !strings.Contains(got, "was dropped") {
		t.Fatalf("the listing still reports %s as registered after the unregister: follow_error = %q", siblingTable, got)
	}
}
