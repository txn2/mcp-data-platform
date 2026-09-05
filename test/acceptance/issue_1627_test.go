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

// Issue #1627: `fetch` on a stored file that carries several table
// registrations reported ONE table, whichever was registered last, whatever
// state that registration was in. A file registered twice was therefore
// described by half of itself, and when the newest registration was one a
// follow had reported gone, the document named a table that did not exist and
// offered sample SQL over it while saying nothing about the table that would
// have answered.
//
// A fetched file now carries `tables`: one entry per registration, newest
// first, each with the registration_id, follow, repair and follow_error
// `manage_table action=list` reports. A search hit still carries one `table`,
// and it is the newest registration whose follow_error is empty.
//
// Wire forms: every parameter these criteria touch is typed on its tool's
// input struct, so each admits exactly one JSON form and each is sent below as
// a literal tools/call param: `manage_table` takes action, reference,
// connection, table_name and registration_id as strings and follow/repair as
// booleans (pkg/toolkits/portal/table_actions.go:113); `fetch` takes reference
// as a string; `search` takes intent as a string, limit as a number and
// purpose as a string. The fields under test -- tables, registration_id,
// follow, repair, follow_error -- are result fields, read here off the real
// tool results rather than asserted against a shape built in the test. The
// file itself is stored through the multipart upload route, whose `file` part
// is bytes and whose metadata fields are form-encoded strings.

// TestIssue1627_AFileWithOneRegistrationReportsIt is criterion 3: nothing
// changes for the ordinary case except the shape it arrives in. One
// registration is a `tables` list of length one, and the hit points at it.
func TestIssue1627_AFileWithOneRegistrationReportsIt(t *testing.T) {
	c := connect(t)
	reference, name := uploadCSV1627(t, c)
	table := registerTable1627(t, c, reference, "one")

	tables := fetchTables1627(t, c, reference)
	if len(tables) != 1 {
		t.Fatalf("a file with one registration carries %d tables: %v", len(tables), tables)
	}
	assertTableEntry1627(t, tables[0], table)

	if hit := hitTable1627(t, c, reference, name); hit == nil {
		t.Fatalf("the search hit for a registered file carries no table")
	} else if got, _ := hit["query_table"].(string); got != table.qualified {
		t.Errorf("the hit names %q; want the one registration %q", got, table.qualified)
	}
}

// TestIssue1627_AFileWithTwoRegistrationsReportsBoth is criterion 1, and the
// failure the ticket was filed for: the second registration used to be the
// whole answer.
func TestIssue1627_AFileWithTwoRegistrationsReportsBoth(t *testing.T) {
	c := connect(t)
	reference, name := uploadCSV1627(t, c)
	first := registerTable1627(t, c, reference, "first")
	second := registerTable1627(t, c, reference, "second")

	tables := fetchTables1627(t, c, reference)
	if len(tables) != 2 {
		t.Fatalf("a file registered twice carries %d tables: %v", len(tables), tables)
	}
	// Newest first, which is the order manage_table action=list reports.
	assertTableEntry1627(t, tables[0], second)
	assertTableEntry1627(t, tables[1], first)

	// The document says the same thing about each registration the managing
	// tool says: the two surfaces cannot disagree about the same file.
	listed := listTables1627(t, c, reference)
	if len(listed) != len(tables) {
		t.Fatalf("manage_table lists %d registrations and fetch carries %d", len(listed), len(tables))
	}
	for i := range listed {
		if got, want := idOf1627(tables[i]), idOf1627(listed[i]); got != want {
			t.Errorf("entry %d: fetch names registration %q, the listing names %q", i, got, want)
		}
	}

	// The hit carries one table, and it is the newest healthy registration.
	hit := hitTable1627(t, c, reference, name)
	if hit == nil {
		t.Fatalf("the search hit for a registered file carries no table")
	}
	if got, _ := hit["query_table"].(string); got != second.qualified {
		t.Errorf("the hit names %q; want the newest healthy registration %q", got, second.qualified)
	}
	if got, _ := hit["follow_error"].(string); got != "" {
		t.Errorf("the hit names a registration a follow disowned: %q", got)
	}
}

// TestIssue1627_AnUnregisteredFileCarriesNoTables is criterion 4: the key is
// absent rather than present and empty, so "nothing is registered over this
// file" is not something a reader has to infer from a zero-length list.
func TestIssue1627_AnUnregisteredFileCarriesNoTables(t *testing.T) {
	c := connect(t)
	reference, name := uploadCSV1627(t, c)

	doc := fetchDoc1627(t, c, reference)
	if raw, present := doc["tables"]; present {
		t.Errorf("an unregistered file carries a tables key: %v", raw)
	}
	if hit := hitTable1627(t, c, reference, name); hit != nil {
		t.Errorf("an unregistered file's hit carries a table: %v", hit)
	}
}

// --- helpers ---

// registration1627 is what one register call produced, as the assertions below
// need to name it.
type registration1627 struct {
	id        string
	qualified string
	follow    bool
	repair    bool
}

// registerTable1627 registers one table over the file and arranges for it to
// be dropped when the test ends. follow and repair are passed explicitly so
// the criteria can assert the values back off the fetched document rather than
// off a default.
func registerTable1627(t *testing.T, c *client, reference, suffix string) registration1627 {
	t.Helper()
	name := fmt.Sprintf("acc_1627_%s_%d", suffix, time.Now().UnixNano())
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
	return registration1627{id: id, qualified: qualified, follow: true, repair: false}
}

// assertTableEntry1627 checks one entry of a fetched document's tables against
// the registration that produced it.
func assertTableEntry1627(t *testing.T, entry map[string]any, want registration1627) {
	t.Helper()
	if got := idOf1627(entry); got != want.id {
		t.Errorf("registration_id = %q; want %q", got, want.id)
	}
	if got, _ := entry["query_table"].(string); got != want.qualified {
		t.Errorf("query_table = %q; want %q", got, want.qualified)
	}
	if got, _ := entry["sample_sql"].(string); !strings.Contains(got, want.qualified) {
		t.Errorf("sample_sql = %q; want a statement over %q", got, want.qualified)
	}
	if got, _ := entry["follow"].(bool); got != want.follow {
		t.Errorf("follow = %v; want %v", got, want.follow)
	}
	if got, _ := entry["repair"].(bool); got != want.repair {
		t.Errorf("repair = %v; want %v", got, want.repair)
	}
	if got, _ := entry["follow_error"].(string); got != "" {
		t.Errorf("follow_error = %q on a registration nothing has disowned", got)
	}
}

func idOf1627(entry map[string]any) string {
	id, _ := entry["registration_id"].(string)
	return id
}

// fetchDoc1627 reads the file in full through the tool a caller uses.
func fetchDoc1627(t *testing.T, c *client, reference string) map[string]any {
	t.Helper()
	out := c.call("fetch", map[string]any{
		"reference": reference,
		"purpose":   "Acceptance for #1627: reading a stored file in full to see the tables registered over it.",
	})
	doc, ok := out["document"].(map[string]any)
	if !ok {
		t.Fatalf("fetch %s returned no document: %v", reference, out)
	}
	return doc
}

// fetchTables1627 reads the tables a fetched document carries.
func fetchTables1627(t *testing.T, c *client, reference string) []map[string]any {
	t.Helper()
	raw, ok := fetchDoc1627(t, c, reference)["tables"].([]any)
	if !ok {
		t.Fatalf("the fetched document carries no tables list for %s", reference)
	}
	return objects1627(t, raw)
}

// listTables1627 is the managing tool's view of the same file.
func listTables1627(t *testing.T, c *client, reference string) []map[string]any {
	t.Helper()
	out := c.call("manage_table", map[string]any{"action": "list", "reference": reference})
	raw, _ := out["registrations"].([]any)
	return objects1627(t, raw)
}

func objects1627(t *testing.T, raw []any) []map[string]any {
	t.Helper()
	out := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		obj, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("entry is not an object: %v", entry)
		}
		out = append(out, obj)
	}
	return out
}

// hitTable1627 finds this file among the search results and returns the table
// its hit carries, or nil when the hit carries none. It searches by the file's
// own display name, over the resources source, and retries: a file stored a
// moment ago has to become findable before what its hit says can be asserted.
func hitTable1627(t *testing.T, c *client, reference, name string) map[string]any {
	t.Helper()
	intent := strings.ReplaceAll(name, "-", " ")
	for attempt := range 10 {
		out := c.call("search", map[string]any{
			"intent": intent, "sources": []any{"resources"}, "limit": 25,
			"purpose": "Acceptance for #1627: what a search hit says about a registered file.",
		})
		if hit := hitFor1627(out, reference); hit != nil {
			table, _ := hit["table"].(map[string]any)
			return table
		}
		if attempt < 9 {
			time.Sleep(2 * time.Second)
		}
	}
	t.Fatalf("the file %s is not among the search results for %q", reference, intent)
	return nil
}

// hitFor1627 returns the hit naming this reference, or nil.
func hitFor1627(out map[string]any, reference string) map[string]any {
	groups, _ := out["groups"].([]any)
	for _, group := range groups {
		g, ok := group.(map[string]any)
		if !ok {
			continue
		}
		hits, _ := g["hits"].([]any)
		for _, entry := range hits {
			hit, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if ref, _ := hit["reference"].(string); ref == reference {
				return hit
			}
		}
	}
	return nil
}

// uploadCSV1627 stores a small, readable CSV as a managed resource and returns
// the reference a registration is made against, plus the display name a search
// finds it by.
func uploadCSV1627(t *testing.T, c *client) (reference, name string) {
	t.Helper()
	name = fmt.Sprintf("acceptance-1627-%d", time.Now().UnixNano())
	status, body := send1631(t, c, http.MethodPost, "/api/v1/resources", func(w *multipart.Writer) error {
		for field, value := range map[string]string{
			"scope":        "global",
			"path":         "acceptance-1627",
			"display_name": name,
			"description":  "Acceptance #1627: a file with more than one table registered over it.",
		} {
			if err := w.WriteField(field, value); err != nil {
				return err
			}
		}
		part, err := filePart1631(w, name+".csv", "text/csv")
		if err != nil {
			return err
		}
		if _, err := part.Write([]byte(issue1627CSV)); err != nil {
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
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	return "mcp:resource:" + id, name
}

// issue1627CSV is a readable CSV: a registration takes its column names from
// the header row and asks whether a line-based reader can read the rest.
const issue1627CSV = "store_id,units,region\n1,10,north\n2,20,south\n3,30,east\n"
