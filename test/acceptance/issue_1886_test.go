//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1886: a resource folder name may start with a digit. The webhook
// compactor files every window under webhooks/{source}/{YYYY-MM-DD}/, and the
// path rule refused that folder, so the platform wrote files its own API could
// not address.
//
// Wire forms: manage_resource's action, path, filename, reference, scope,
// scope_id, display_name, description, content_base64 and content_type are
// typed strings, each admitting one JSON form, and are sent in it. The folder
// move is PATCH /api/v1/resources/{id} with a JSON object body, the one form
// the route takes.

// issue1886Root is a top-level folder this run owns.
func issue1886Root() string {
	return fmt.Sprintf("acc1886-%d", time.Now().UnixNano()%1_000_000_000)
}

// issue1886Got is the resource id a manage_resource get found at an address.
func issue1886Got(got map[string]any) string {
	found, _ := got["resource"].(map[string]any)
	id, _ := found["resource_id"].(string)
	return id
}

// issue1886ID is the resource id a reference names.
func issue1886ID(t *testing.T, ref string) string {
	t.Helper()
	id, ok := strings.CutPrefix(ref, "mcp:resource:")
	if !ok || id == "" {
		t.Fatalf("%q is not a resource reference", ref)
	}
	return id
}

// TestIssue1886_ADatedFolderIsCreatedReadMovedAndDeletedByAddress is the rule
// itself, through every action that addresses a file by its folder: create,
// get, move and delete.
func TestIssue1886_ADatedFolderIsCreatedReadMovedAndDeletedByAddress(t *testing.T) {
	c := connect(t)
	root := issue1886Root()
	dated := root + "/2026-09-26"

	out := c.call("manage_resource", map[string]any{
		"action": "create", "path": dated, "filename": "orders.csv",
		"display_name": "Acceptance 1886 orders", "description": "Acceptance #1886: a file in a dated folder.",
		"content_base64": "aWQsbmFtZQoxLGEK", "content_type": "text/csv",
	})
	ref, _ := out["reference"].(string)
	id := issue1886ID(t, ref)
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_resource", map[string]any{"action": "delete", "reference": ref, "force": true})
	})

	got := c.call("manage_resource", map[string]any{"action": "get", "path": dated, "filename": "orders.csv"})
	if issue1886Got(got) != id {
		t.Fatalf("getting %s/orders.csv by its address returned another resource: %v", dated, got)
	}

	// A move into a year-and-quarter folder, the other shape people file
	// delivered data under.
	quarter := root + "/2026/2026-q3"
	if status, body := c.rest(http.MethodPatch, "/api/v1/resources/"+id,
		jsonBody(t, map[string]any{"path": quarter})); status != http.StatusOK {
		t.Fatalf("moving the file to %s answered %d: %v", quarter, status, body)
	}
	moved := c.call("manage_resource", map[string]any{"action": "get", "path": quarter, "filename": "orders.csv"})
	if issue1886Got(moved) != id {
		t.Errorf("getting the moved file by its new address returned another resource: %v", moved)
	}
	if uri, _ := moved["uri"].(string); !strings.HasSuffix(uri, "/"+quarter+"/orders.csv") {
		t.Errorf("after the move the file's uri is %q, want it under %s", uri, quarter)
	}

	c.call("manage_resource", map[string]any{"action": "delete", "path": quarter, "filename": "orders.csv", "force": true})
	gone := c.call("manage_resource", map[string]any{"action": "get", "path": quarter, "filename": "orders.csv"})
	if found, _ := gone["found"].(bool); found {
		t.Errorf("the deleted file is still read by its address: %v", gone)
	}

	// The rest of the rule stands: a folder name still starts with a letter or
	// a digit, so a leading hyphen is refused, naming the folder.
	res, text, err := c.callRaw("manage_resource", map[string]any{
		"action": "create", "path": root + "/-2026", "filename": "x.csv",
		"display_name": "x", "description": "x", "content_base64": "eAo=", "content_type": "text/csv",
	})
	if err != nil || !res.IsError || !strings.Contains(text, `"-2026"`) {
		t.Errorf("a folder starting with a hyphen was not refused by name: %s (err %v)", text, err)
	}
}

// TestIssue1886_AWebhookWindowIsReadByItsAddress is the report: the compactor
// filed a window under a dated folder, and getting that file by its path was
// refused.
func TestIssue1886_AWebhookWindowIsReadByItsAddress(t *testing.T) {
	c := connectFor(t, 5*time.Minute)
	db := issue1870DB(t)

	source := issue1870Name("acc1886-window")
	const secret = "whsec_1886"
	issue1870Create(t, c, issue1870HMAC(source, secret, map[string]any{
		"split": "$", "event_id_path": "$.id", "persona": "admin", "compact_every_minutes": 1,
	}))
	if res, body := issue1870Signed(t, source, secret, "application/json", []byte(`[{"id":"e1"}]`)); res.StatusCode != http.StatusAccepted {
		t.Fatalf("posting to %s answered %d: %s", source, res.StatusCode, body)
	}
	issue1870WaitAllCompacted(t, db, source, 3*time.Minute)
	window := issue1870Windows(t, db, source)[0]

	status, stored := c.rest(http.MethodGet, "/api/v1/resources/"+window.resourceID, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the window's resource %s answered %d: %v", window.resourceID, status, stored)
	}
	path, _ := stored["path"].(string)
	filename, _ := stored["filename"].(string)
	day := window.start.UTC().Format("2006-01-02")
	if path != "webhooks/"+source+"/"+day {
		t.Fatalf("the window is filed under %q, want webhooks/%s/%s", path, source, day)
	}

	got := c.call("manage_resource", map[string]any{
		"action": "get", "scope": "persona", "scope_id": "admin", "path": path, "filename": filename,
	})
	if issue1886Got(got) != window.resourceID {
		t.Fatalf("getting %s/%s by its address returned another resource: %v", path, filename, got)
	}
}
