//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

// Issue #2293: the resources listing's text search, with no scope, covers
// every library the caller can read, and matches a file by its folder,
// filename and tags as well as its name and description.
//
// Wire forms: q as a query-string word, scope absent and scope as "global",
// within the GET /api/v1/resources route the portal's file manager calls.

// issue2293File is one file seeded for the search: its id suffix, its library,
// and where the word is.
type issue2293File struct {
	id, scope, scopeID, path, filename, name string
	tags                                     []string
}

// issue2293Seed writes the files straight into the dev database, one library
// and one field per file, and removes them when the test ends.
func issue2293Seed(t *testing.T, files []issue2293File) {
	t.Helper()
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, f := range files {
		var scopeID any
		if f.scope != "global" {
			scopeID = f.scopeID
		}
		tags := "{" + strings.Join(f.tags, ",") + "}"
		_, err := db.ExecContext(ctx, `
			INSERT INTO resources (id, scope, scope_id, path, filename, display_name, description, mime_type, size_bytes,
			                       s3_key, uri, tags, uploader_sub)
			VALUES ($1, $2, $3, $4, $5, $6, 'Acceptance #2293.', 'application/octet-stream', 10, $7, $8, $9::text[], 'acceptance')`,
			f.id, f.scope, scopeID, f.path, f.filename, f.name, "acc2293/"+f.id, "mcp://acc2293/"+f.id, tags)
		if err != nil {
			t.Fatalf("seeding %s: %v", f.id, err)
		}
		id := f.id
		t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DELETE FROM resources WHERE id = $1`, id) })
	}
}

// issue2293Listed is the ids a listing returned, of those named.
func issue2293Listed(t *testing.T, c *client, query url.Values, among map[string]bool) []string {
	t.Helper()
	status, out := c.rest(http.MethodGet, "/api/v1/resources?"+query.Encode(), http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/resources?%s: status %d %v", query.Encode(), status, out)
	}
	rows, _ := out["resources"].([]any)
	var ids []string
	for _, r := range rows {
		res, _ := r.(map[string]any)
		if id, _ := res["id"].(string); among[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// issue2293Me is the caller's own library key, as the portal reports it.
func issue2293Me(t *testing.T, c *client) string {
	t.Helper()
	status, me := c.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/portal/me: %d %v", status, me)
	}
	id, _ := me["user_id"].(string)
	if id == "" {
		t.Fatalf("the caller has no user_id: %v", me)
	}
	return id
}

// With no scope, a word lists the matching files of every library the caller
// reads, its own, Global and its persona's, and nothing of a persona library
// it cannot read.
func TestIssue2293_ASearchWithNoScopeCoversEveryLibraryTheCallerReads(t *testing.T) {
	analyst := connectAs(t, "acme-analyst-key")
	word := fmt.Sprintf("ledger%d", time.Now().UnixNano()%1_000_000_000)
	own := issue2293Me(t, analyst)
	files := []issue2293File{
		{id: "acc2293-own-" + word, scope: "user", scopeID: own, path: "notes", filename: "a.bin", name: word + " notes"},
		{id: "acc2293-global-" + word, scope: "global", path: "reports", filename: "b.bin", name: word + " report"},
		{id: "acc2293-persona-" + word, scope: "persona", scopeID: "inventory-analyst", path: "shared", filename: "c.bin", name: word + " shared"},
		{id: "acc2293-other-" + word, scope: "persona", scopeID: "finance-executive", path: "board", filename: "d.bin", name: word + " board"},
	}
	issue2293Seed(t, files)
	among := map[string]bool{}
	for _, f := range files {
		among[f.id] = true
	}

	got := issue2293Listed(t, analyst, url.Values{"q": {word}}, among)
	want := []string{files[1].id, files[0].id, files[2].id}
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("q=%s with no scope listed %v, want %v: the caller's own, Global and its persona's, and not a persona it cannot read", word, got, want)
	}

	got = issue2293Listed(t, analyst, url.Values{"q": {word}, "scope": {"global"}}, among)
	if fmt.Sprint(got) != fmt.Sprint([]string{files[1].id}) {
		t.Fatalf("q=%s with scope global listed %v, want only the Global file", word, got)
	}
}

// A file whose word is only in its folder, only in its filename or only in a
// tag is found by that word.
func TestIssue2293_AFileIsFoundByItsFolderFilenameOrTag(t *testing.T) {
	c := connect(t)
	word := fmt.Sprintf("kcet%d", time.Now().UnixNano()%1_000_000_000)
	files := []issue2293File{
		{id: "acc2293-path-" + word, scope: "global", path: "pipelines/" + word + "/2022", filename: "2022-03-21_OO.csv", name: "2022-03-21_OO"},
		{id: "acc2293-file-" + word, scope: "global", path: "deliveries", filename: word + "-extract.csv", name: "March extract"},
		{id: "acc2293-tag-" + word, scope: "global", path: "deliveries", filename: "e.csv", name: "Weekly ratings", tags: []string{"nltv", word}},
		{id: "acc2293-none-" + word, scope: "global", path: "deliveries", filename: "f.csv", name: "Unrelated"},
	}
	issue2293Seed(t, files)
	among := map[string]bool{}
	for _, f := range files {
		among[f.id] = true
	}
	got := issue2293Listed(t, c, url.Values{"q": {word}, "scope": {"global"}}, among)
	want := []string{files[0].id, files[1].id, files[2].id}
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("q=%s listed %v, want the files with the word only in their folder, filename and tag: %v", word, got, want)
	}
}
