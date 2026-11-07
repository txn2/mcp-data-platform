//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2522: appending an event to a portal thread answered 201 with
// created_at as the zero time; the stored event had the right time. The
// response now carries the time the event was stored, the same one a later
// read of the thread's events returns.
//
// Wire forms: the thread body's kind, target_type, asset_id and body as
// strings; the event body's event_type and body as strings.

func TestIssue2522_AnAppendedEventCarriesItsStoredTime(t *testing.T) {
	admin := connect(t)
	stamp := time.Now().UnixNano()
	saved := admin.call("save_asset", map[string]any{
		"name": fmt.Sprintf("acc-2522 %d", stamp), "content_type": "text/html",
		"content": "<p>acc-2522</p>", "description": "Acceptance #2522.",
	})
	assetID, _ := saved["asset_id"].(string)
	t.Cleanup(func() {
		_, _, _ = admin.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID})
	})

	status, thread := admin.rest(http.MethodPost, "/api/v1/portal/threads", jsonBody(t, map[string]any{
		"kind": "comment", "target_type": "asset", "asset_id": assetID, "body": "acc-2522 opening note",
	}))
	threadID, _ := thread["id"].(string)
	if status != http.StatusCreated || threadID == "" {
		t.Fatalf("opening a thread: %d %v", status, thread)
	}

	before := time.Now().Add(-time.Minute)
	status, event := admin.rest(http.MethodPost, "/api/v1/portal/threads/"+threadID+"/events", jsonBody(t, map[string]any{
		"event_type": "comment", "body": "acc-2522 reply",
	}))
	if status != http.StatusCreated {
		t.Fatalf("appending an event: %d %v", status, event)
	}
	eventID, _ := event["id"].(string)
	raw, _ := event["created_at"].(string)
	answered, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatalf("the appended event's created_at %q is not a time: %v", raw, err)
	}
	if answered.Year() == 1 || answered.Before(before) {
		t.Errorf("the appended event answered created_at %s; want the time it was stored", raw)
	}

	// A later read of the thread returns the same time for the same event.
	listStatus, body := admin.restText("/api/v1/portal/threads/" + threadID + "/events")
	if listStatus != http.StatusOK || !strings.Contains(body, eventID) {
		t.Fatalf("reading the thread's events: %d %.300s", listStatus, body)
	}
	stored := issue2522StoredAt(t, body, eventID)
	if !stored.Equal(answered) {
		t.Errorf("the append answered %s and the stored event reads %s", answered, stored)
	}
}

// issue2522StoredAt finds one event's created_at in a thread's events body,
// whether the body is a list or an object holding one.
func issue2522StoredAt(t *testing.T, body, eventID string) time.Time {
	t.Helper()
	var list []map[string]any
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		var wrapped map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &wrapped); err != nil {
			t.Fatalf("the events body is neither a list nor an object: %.300s", body)
		}
		for _, v := range wrapped {
			if json.Unmarshal(v, &list) == nil && len(list) > 0 {
				break
			}
		}
	}
	for _, e := range list {
		if e["id"] == eventID {
			raw, _ := e["created_at"].(string)
			at, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				t.Fatalf("the stored event's created_at %q: %v", raw, err)
			}
			return at
		}
	}
	t.Fatalf("event %s is not in the thread's events: %.300s", eventID, body)
	return time.Time{}
}
