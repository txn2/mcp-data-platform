//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #2131: a registration whose CREATE TABLE the platform stopped waiting
// for, and which the coordinator completed anyway, left a table with no
// registration, and every later registration of the same file failed "already
// exists". The orphan is made here by hand through the scratch connection,
// with the definition an earlier registration of the file would have given
// it; what is under test is that the next registration through manage_table
// adopts it, and that a table over anything else is still refused.
//
// Wire forms: every manage_table, manage_resource and trino_execute parameter
// these calls set is a JSON string, the one form each schema admits.

// file2131 stores a CSV as a managed resource and returns its reference.
func file2131(t *testing.T, c *client, stamp, label string) string {
	t.Helper()
	created := c.call("manage_resource", map[string]any{
		"action":       "create",
		"display_name": "Acceptance 2131 " + label + " " + stamp,
		"description":  "Acceptance: a CSV a table is left over by an abandoned registration.",
		"filename":     "acc-2131-" + label + "-" + stamp + ".csv",
		"path":         "acceptance/issue-2131",
		"content_type": "text/csv",
		"content":      "store_id,units\n1,10\n2,20\n",
	})
	reference, _ := created["reference"].(string)
	if reference == "" {
		t.Fatalf("manage_resource create returned no reference: %v", created)
	}
	return reference
}

// register2131 registers a file under a name and unregisters it at cleanup.
func register2131(t *testing.T, c *client, reference, table string) map[string]any {
	t.Helper()
	reg := c.call("manage_table", map[string]any{
		"action": "register", "reference": reference, "connection": scratchResourceConnection,
		"table_name": table,
	})
	t.Cleanup(func() {
		if id, _ := reg["registration_id"].(string); id != "" {
			_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": id})
		}
	})
	return reg
}

// location2131 reads the external_location a table was created with.
func location2131(t *testing.T, c *client, table string) string {
	t.Helper()
	result := c.call("trino_execute", map[string]any{
		"connection": scratchResourceConnection,
		"sql":        "SHOW CREATE TABLE " + table,
		"purpose":    "Acceptance #2131: read where a registered table reads.",
	})
	text := fmt.Sprintf("%v", result["rows"])
	_, rest, found := strings.Cut(text, "external_location = '")
	if !found {
		t.Fatalf("SHOW CREATE TABLE %s carries no external_location: %v", table, result)
	}
	location, _, _ := strings.Cut(rest, "'")
	return location
}

// orphan2131 creates, behind the platform's back, the table an abandoned
// registration leaves: the registration's own definition, with no row.
func orphan2131(t *testing.T, c *client, table, location string) {
	t.Helper()
	c.call("trino_execute", map[string]any{
		"connection": scratchResourceConnection,
		"sql": "CREATE TABLE " + table + ` ("store_id" VARCHAR, "units" VARCHAR) WITH (external_location = '` +
			location + `', format = 'CSV', skip_header_line_count = 1)`,
		"purpose": "Acceptance #2131: leave the table an abandoned registration leaves.",
	})
	t.Cleanup(func() {
		_, _, _ = c.callRaw("trino_execute", map[string]any{
			"connection": scratchResourceConnection,
			"sql":        "DROP TABLE IF EXISTS " + table,
			"purpose":    "Acceptance #2131: remove the table the test left.",
		})
	})
}

// queryTable2131 is the qualified name a registration answers with.
func queryTable2131(t *testing.T, reg map[string]any) string {
	t.Helper()
	table, _ := reg["query_table"].(string)
	if table == "" {
		t.Fatalf("the registration carries no query_table: %v", reg)
	}
	return table
}

// A table left over the file's own directory is adopted: the registration
// succeeds, is listed, and the table serves the file's rows.
func TestIssue2131_ARegistrationAdoptsTheTableAnEarlierOneOfTheFileLeft(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	reference := file2131(t, c, stamp, "adopt")

	// A first registration says where a table over this file reads and what
	// the scratch schema is called; the orphan takes a second name beside it.
	first := register2131(t, c, reference, "acc2131_"+stamp)
	firstTable := queryTable2131(t, first)
	location := location2131(t, c, firstTable)
	orphanTable := firstTable + "_left"
	orphan2131(t, c, orphanTable, location)

	adopted := register2131(t, c, reference, "acc2131_"+stamp+"_left")
	if got := queryTable2131(t, adopted); got != orphanTable {
		t.Fatalf("the registration landed on %s, not the table left behind (%s)", got, orphanTable)
	}

	listing := c.call("manage_table", map[string]any{"action": "list", "reference": reference})
	var listed bool
	regs, _ := listing["table_registrations"].([]any)
	for _, entry := range regs {
		reg, _ := entry.(map[string]any)
		listed = listed || reg["query_table"] == orphanTable
	}
	if !listed {
		t.Fatalf("the adopted table is not listed against its file: %v", listing)
	}

	result := c.call("trino_query", map[string]any{
		"connection": scratchResourceConnection,
		"sql":        "SELECT store_id, units FROM " + orphanTable + " ORDER BY store_id",
		"purpose":    "Acceptance #2131: the adopted table serves the file's rows.",
	})
	if rows, _ := result["rows"].([]any); len(rows) != 2 {
		t.Fatalf("the adopted table serves %d rows, not the file's 2: %v", len(rows), result)
	}
}

// A table under the name that reads another file is a real collision: it is
// refused, naming where it reads, and it is left as it was.
func TestIssue2131_ATableOverAnotherFileIsStillRefused(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	mine := file2131(t, c, stamp, "mine")
	other := file2131(t, c, stamp, "other")

	otherReg := register2131(t, c, other, "acc2131other_"+stamp)
	otherLocation := location2131(t, c, queryTable2131(t, otherReg))
	// The name the next registration asks for, as the platform qualifies and
	// persona-prefixes it: the other registration's name with its slug swapped.
	held := strings.Replace(queryTable2131(t, otherReg), "acc2131other_", "acc2131held_", 1)
	orphan2131(t, c, held, otherLocation)

	res, text, err := c.callRaw("manage_table", map[string]any{
		"action": "register", "reference": mine, "connection": scratchResourceConnection,
		"table_name": "acc2131held_" + stamp,
	})
	if err != nil {
		t.Fatalf("manage_table register: transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("a registration took a table over another file: %s", text)
	}
	if !strings.Contains(text, "no registration holds it") || !strings.Contains(text, otherLocation) {
		t.Fatalf("the refusal does not say what holds the name and where it reads: %s", text)
	}
	if got := location2131(t, c, held); got != otherLocation {
		t.Fatalf("the table over the other file was changed: it reads %s, not %s", got, otherLocation)
	}
}
