//go:build integration

package acceptance

import (
	"encoding/base64"
	"net/http"
	"testing"
	"time"
)

// Issue #2286: a create wrote the resource's row twice, once on insert and
// again recording version 1, so a tile drawn between the two writes was owed
// again and the same bytes were drawn a second time.
//
// Wire forms. manage_resource's action, filename, display_name, path,
// description, content_base64 and content_type are typed strings in its schema
// (pkg/toolkits/portal/schemas.go), so each admits exactly one JSON form, sent
// below as a literal tools/call parameter of that form.

// createResource2286 creates a small table through the tool a person calls and
// returns its id.
func createResource2286(t *testing.T, c *client) string {
	t.Helper()
	name := "acceptance-2286-" + unique1802()
	out := c.call("manage_resource", map[string]any{
		"action":         "create",
		"filename":       name + ".csv",
		"display_name":   name,
		"path":           "acceptance-2286",
		"description":    "Acceptance #2286: a resource written once on create.",
		"content_base64": base64.StdEncoding.EncodeToString([]byte(table1802("once", headRows1802, ","))),
		"content_type":   "text/csv",
	})
	id, _ := out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	return id
}

// TestIssue2286_ACreateHasOneUpdatedAt is criterion 1: the row a create
// leaves is the row it answers with, and its version 1 is recorded.
func TestIssue2286_ACreateHasOneUpdatedAt(t *testing.T) {
	c := connectFor(t, time.Minute)
	id := createResource2286(t, c)
	first, _ := resourceRow1802(t, c, id)["updated_at"].(string)
	if first == "" {
		t.Fatalf("resource %s reports no updated_at", id)
	}
	time.Sleep(2 * time.Second)
	if again, _ := resourceRow1802(t, c, id)["updated_at"].(string); again != first {
		t.Fatalf("resource %s was written again after its create: updated_at %s, then %s", id, first, again)
	}
	status, body := c.rest(http.MethodGet, "/api/v1/resources/"+id+"/versions", http.NoBody)
	versions, _ := body["versions"].([]any)
	if current, _ := body["current"].(float64); status != http.StatusOK || len(versions) != 1 || current != 1 {
		t.Fatalf("GET versions of %s: status %d; want 200 with version 1 alone and current: %v", id, status, body)
	}
}

// TestIssue2286_ACreatedFileIsDrawnOnce is criterion 2: the tile is dated by
// the row as created and is not drawn again while the file is unchanged.
func TestIssue2286_ACreatedFileIsDrawnOnce(t *testing.T) {
	c := connectFor(t, 3*tileWait1802)
	id := createResource2286(t, c)
	row := awaitResourceTile1802(t, c, id, "")
	drawn, _ := row["thumbnail_captured_at"].(string)
	updated, _ := row["updated_at"].(string)
	if !sameInstant2286(t, drawn, updated) {
		t.Fatalf("the tile of %s is dated %s and the file %s; the tile was drawn from a row the create wrote over", id, drawn, updated)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if again, _ := resourceRow1802(t, c, id)["thumbnail_captured_at"].(string); again != drawn {
			t.Fatalf("an unchanged file was drawn again: tile dated %s, then %s", drawn, again)
		}
		time.Sleep(3 * time.Second)
	}
}

// sameInstant2286 compares two RFC 3339 times the API may write with different
// fractional precision.
func sameInstant2286(t *testing.T, a, b string) bool {
	t.Helper()
	ta, errA := time.Parse(time.RFC3339Nano, a)
	tb, errB := time.Parse(time.RFC3339Nano, b)
	if errA != nil || errB != nil {
		t.Fatalf("reading times %q and %q: %v %v", a, b, errA, errB)
	}
	return ta.Equal(tb)
}
