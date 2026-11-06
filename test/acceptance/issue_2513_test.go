//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Issue #2513: a feedback thread takes no title. It is its messages, and
// wherever it is listed it is shown by its author, the first line of its first
// message and its target. manage_feedback create no longer takes a title.
//
// Wire forms: action, asset_id and body as JSON strings (body with embedded
// newlines); requires_resolution as a JSON boolean; title as a JSON string,
// which the tool now refuses. Each is sent as literal tools/call params.

const (
	issue2513Body      = "acc-2513 the March total is off by one\nIt counts the 31st twice.\nSee the totals row."
	issue2513FirstLine = "acc-2513 the March total is off by one"
)

// issue2513Row finds the row for thread id in a list's data array.
func issue2513Row(t *testing.T, where string, data any, id string) map[string]any {
	t.Helper()
	rows, _ := data.([]any)
	for _, r := range rows {
		if row, _ := r.(map[string]any); row["id"] == id {
			return row
		}
	}
	t.Fatalf("%s does not list thread %s: %v", where, id, data)
	return nil
}

// issue2513Listed checks a listed row is shown by its first message, author
// and (when the list names it) target, and carries no title.
func issue2513Listed(t *testing.T, where string, row map[string]any, author, targetLabel string) {
	t.Helper()
	if row["first_message"] != issue2513FirstLine {
		t.Errorf("%s: first_message %q, want %q", where, row["first_message"], issue2513FirstLine)
	}
	if title, ok := row["title"]; ok && title != "" {
		t.Errorf("%s: the thread carries a title %q", where, title)
	}
	if row["author_email"] != author {
		t.Errorf("%s: author_email %v, want %s", where, row["author_email"], author)
	}
	if targetLabel != "" && row["target_label"] != targetLabel {
		t.Errorf("%s: target_label %v, want %s", where, row["target_label"], targetLabel)
	}
}

// A thread opened with manage_feedback create and no title is listed by the
// first line of its first message, its author and its target: in the asset's
// own list, the activity feed, the practitioner worklist and manage_feedback
// list.
func TestIssue2513_ACreateWithoutATitleListsByItsFirstMessage(t *testing.T) {
	owner := connectAs(t, "acme-owner-key")
	assetID := issue2290Asset(t, owner, "2513 listing")
	status, asset := owner.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID, http.NoBody)
	name, _ := asset["name"].(string)
	if status != http.StatusOK || name == "" {
		t.Fatalf("reading the asset back: %d %v", status, asset)
	}

	res, text, err := owner.callRaw("manage_feedback", map[string]any{
		"action": "create", "asset_id": assetID, "body": issue2513Body, "requires_resolution": true,
	})
	if err != nil || res.IsError {
		t.Fatalf("manage_feedback create without a title: %v %s", err, text)
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(text), &created); err != nil {
		t.Fatalf("the created thread is not JSON: %v\n%s", err, text)
	}
	id, _ := created["id"].(string)
	author, _ := created["author_email"].(string)
	if id == "" || author == "" {
		t.Fatalf("the created thread has no id or author: %s", text)
	}

	_, panel := owner.rest(http.MethodGet, "/api/v1/portal/threads?asset_id="+assetID, http.NoBody)
	issue2513Listed(t, "the asset's feedback panel", issue2513Row(t, "the asset's feedback panel", panel["data"], id), author, "")

	_, activity := owner.rest(http.MethodGet, "/api/v1/portal/feedback/activity?limit=200", http.NoBody)
	issue2513Listed(t, "the activity feed", issue2513Row(t, "the activity feed", activity["data"], id), author, name)

	_, worklist := owner.rest(http.MethodGet, "/api/v1/portal/worklist/practitioner?limit=200", http.NoBody)
	issue2513Listed(t, "the practitioner worklist", issue2513Row(t, "the practitioner worklist", worklist["data"], id), author, name)

	listed := owner.call("manage_feedback", map[string]any{"action": "list", "asset_id": assetID})
	issue2513Listed(t, "manage_feedback list", issue2513Row(t, "manage_feedback list", listed["threads"], id), author, "")

	if strings.Contains(fmt.Sprint(panel, activity, worklist, listed), "untitled") {
		t.Errorf("a list shows the thread as untitled")
	}
}

// manage_feedback create no longer takes a title: a call still sending one is
// refused, and no thread is opened.
func TestIssue2513_CreateRefusesATitle(t *testing.T) {
	owner := connectAs(t, "acme-owner-key")
	assetID := issue2290Asset(t, owner, "2513 title refused")

	res, text, err := owner.callRaw("manage_feedback", map[string]any{
		"action": "create", "asset_id": assetID, "body": "acc-2513 with a title", "title": "acc-2513 subject",
	})
	if err == nil && !res.IsError {
		t.Fatalf("manage_feedback create accepted a title: %s", text)
	}
	_, list := owner.rest(http.MethodGet, "/api/v1/portal/threads?asset_id="+assetID, http.NoBody)
	if list["total"] != float64(0) {
		t.Errorf("a create carrying a title opened a thread: %v", list)
	}
}
