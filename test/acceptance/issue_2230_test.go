//go:build integration

package acceptance

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

// Acceptance for #2230: a file searched for by the words of its own name was
// missing from the answer, behind files whose names only looked alike.
// PostgreSQL's parser read "acceptance-1627-1792487495133911000" as the word
// "acceptance" and the signed numbers -1627 and -1792487495133911000, so a
// search for "acceptance 1627 1792487495133911000" never matched it, and a
// file not yet embedded had nothing else to be found by. A name is now
// matched by its words on every full-text surface, and an operation not yet
// embedded is blended as the SQL rankers blend a row the vector arm cannot
// see.
//
// Wire forms: search's intent is a string and sources a list of strings;
// manage_resource's content_base64, filename, display_name and path are
// strings; save_asset's name, content and content_type are strings. Each is
// sent as literal params.

const (
	issue2230Alike   = 30
	issue2230Purpose = "Acceptance #2230: a file is found by the words of its name."
)

// issue2230Upload uploads a small file under acceptance-2230 named name and
// returns its reference.
func issue2230Upload(t *testing.T, c *client, name string) string {
	t.Helper()
	out := c.call("manage_resource", map[string]any{
		"action": "create", "filename": name + ".bin", "display_name": name, "path": "acceptance-2230",
		"description":    "Acceptance #2230.",
		"content_base64": base64.StdEncoding.EncodeToString([]byte("acceptance 2230 " + name)),
		"content_type":   "application/octet-stream",
	})
	id, _ := out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	return "mcp:resource:" + id
}

// issue2230First returns the reference of the first hit search answers for
// intent in one source.
func issue2230First(t *testing.T, c *client, intent, source string) string {
	t.Helper()
	out := c.call("search", map[string]any{
		"intent": intent, "sources": []any{source}, "limit": 25, "purpose": issue2230Purpose,
	})
	groups, _ := out["groups"].([]any)
	for _, group := range groups {
		g, _ := group.(map[string]any)
		hits, _ := g["hits"].([]any)
		if len(hits) > 0 {
			first, _ := hits[0].(map[string]any)
			ref, _ := first["reference"].(string)
			return ref
		}
	}
	return ""
}

// TestIssue2230_AFileIsFoundByTheWordsOfItsName: among thirty embedded files
// named alike, a file uploaded a moment ago and named
// acceptance-2230-<n> is the first resource search answers for
// "acceptance 2230 <n>", before its own embedding lands.
func TestIssue2230_AFileIsFoundByTheWordsOfItsName(t *testing.T) {
	c := connectFor(t, 5*time.Minute)
	db := issue1904DB(t)
	stamp := time.Now().UnixNano()
	var alike []string
	for i := range issue2230Alike {
		ref := issue2230Upload(t, c, fmt.Sprintf("acceptance-2230-thing-%d-%d", stamp, i))
		alike = append(alike, strings.TrimPrefix(ref, "mcp:resource:"))
	}
	issue1904Await(t, "the alike files are embedded", func() bool {
		return issue1904Count(t, db, `SELECT COUNT(*) FROM resources WHERE id = ANY($1) AND embedding IS NOT NULL`, pq.Array(alike)) == issue2230Alike
	})

	name := fmt.Sprintf("acceptance-2230-%d", stamp+1)
	target := issue2230Upload(t, c, name)
	if got := issue2230First(t, c, strings.ReplaceAll(name, "-", " "), "resources"); got != target {
		t.Errorf("the first resource for %q is %s; want the file named so, %s", strings.ReplaceAll(name, "-", " "), got, target)
	}
	if got := issue2230First(t, c, name, "resources"); got != target {
		t.Errorf("the first resource for its own name %q is %s; want %s", name, got, target)
	}
}

// TestIssue2230_AnAssetIsFoundByTheWordsOfItsName: a saved asset named
// q3-2024-<n>-report is the first asset search answers for
// "q3 2024 <n> report".
func TestIssue2230_AnAssetIsFoundByTheWordsOfItsName(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("q3-2024-%d-report", time.Now().UnixNano())
	saved := c.call("save_asset", map[string]any{
		"name": name, "content": "<h1>Q3</h1>", "content_type": "text/html",
		"description": "Acceptance #2230: an asset found by the words of its name.",
	})
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
	intent := strings.ReplaceAll(name, "-", " ")
	if got := issue2230First(t, c, intent, "assets"); got != "mcp:asset:"+id {
		t.Errorf("the first asset for %q is %s; want mcp:asset:%s", intent, got, id)
	}
}
