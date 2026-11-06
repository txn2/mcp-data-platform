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

// Issue #2514: edit and delete your own feedback messages. Only a comment (the
// opening message of a comment, question, correction or suggestion thread, and
// every reply) can be changed; only its author edits it; its author, the
// target's owner or editor, and an administrator delete it; a deleted
// message's text is returned by no route; and an edit that names someone new
// tells that person once.
//
// Wire forms: manage_feedback action, thread_id, event_id and body as JSON
// strings, each sent as literal tools/call params (the four parameters edit
// and delete take admit no other form); the portal routes take a JSON object
// body with body as a JSON string, and DELETE takes none.

// issue2514Reply posts a reply through manage_feedback as the owner and
// returns its id.
func issue2514Reply(t *testing.T, w issue2512World, threadID, body string) string {
	t.Helper()
	res, text, err := w.owner.callRaw("manage_feedback", map[string]any{"action": "reply", "thread_id": threadID, "body": body})
	if err != nil || res.IsError {
		t.Fatalf("manage_feedback reply: %v %s", err, text)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(text), &event); err != nil {
		t.Fatalf("the reply is not JSON: %v %s", err, text)
	}
	id, _ := event["id"].(string)
	if id == "" {
		t.Fatalf("the reply has no id: %s", text)
	}
	return id
}

// issue2514Feedback calls manage_feedback with literal params and returns the
// result text and whether it is an error.
func issue2514Feedback(t *testing.T, c *client, args map[string]any) (string, bool) {
	t.Helper()
	res, text, err := c.callRaw("manage_feedback", args)
	if err != nil {
		t.Fatalf("manage_feedback %v: %v", args, err)
	}
	return text, res.IsError
}

// issue2514Event reads one event of a thread as a person.
func issue2514Event(t *testing.T, s externalSession, threadID, eventID string) map[string]any {
	t.Helper()
	status, body := s.do(t, http.MethodGet, "/api/v1/portal/threads/"+threadID+"/events", nil)
	if status != http.StatusOK {
		t.Fatalf("%s reading the thread's events: %d %v", s.email, status, body)
	}
	for _, e := range asList(body["data"]) {
		if e["id"] == eventID {
			return e
		}
	}
	t.Fatalf("event %s is not in the thread: %v", eventID, body)
	return nil
}

// The author edits a reply; others see the new text marked edited.
func TestIssue2514_TheAuthorEditsAReply(t *testing.T) {
	w := newIssue2512World(t)
	id := w.openThread(t, w.b, "acc-2514 is the Q4 total right?")
	reply := issue2514Reply(t, w, id, "acc-2514 the total is 41")
	text, isErr := issue2514Feedback(t, w.owner, map[string]any{
		"action": "edit", "thread_id": id, "event_id": reply, "body": "acc-2514 the total is 42",
	})
	if isErr {
		t.Fatalf("the author's edit was refused: %s", text)
	}
	seen := issue2514Event(t, w.b, id, reply)
	if seen["body"] != "acc-2514 the total is 42" || seen["edited_at"] == nil {
		t.Fatalf("B sees %v; want the new text marked edited", seen)
	}
	// The portal route takes the same edit from a person in the portal.
	opening := issue2514OpeningEvent(t, w, id)
	if status, out := w.b.do(t, http.MethodPatch, "/api/v1/portal/threads/"+id+"/events/"+opening,
		map[string]any{"body": "acc-2514 is the Q4 total of 42 right?"}); status != http.StatusOK || out["edited_at"] == nil {
		t.Fatalf("B editing their opening message: %d %v", status, out)
	}
}

// issue2514OpeningEvent is the id of a thread's opening message.
func issue2514OpeningEvent(t *testing.T, w issue2512World, threadID string) string {
	t.Helper()
	status, body := w.owner.rest(http.MethodGet, "/api/v1/portal/threads/"+threadID+"/events", http.NoBody)
	events := asList(body["data"])
	if status != http.StatusOK || len(events) == 0 {
		t.Fatalf("reading the thread's events: %d %v", status, body)
	}
	id, _ := events[0]["id"].(string)
	return id
}

// The author deletes a reply; others see "message deleted" and the text is in
// no response: the thread's events, the thread list, the activity feed, the
// agent's read of the thread, search and the notification lists.
func TestIssue2514_ADeletedReplyIsInNoResponse(t *testing.T) {
	w := newIssue2512World(t)
	id := w.openThread(t, w.b, "acc-2514 which quarter is this?")
	secret := fmt.Sprintf("acc2514secret%d", time.Now().UnixNano())
	reply := issue2514Reply(t, w, id, secret+" is the wrong figure")
	text, isErr := issue2514Feedback(t, w.owner, map[string]any{"action": "delete", "thread_id": id, "event_id": reply})
	var deleted struct {
		Deleted bool `json:"deleted"`
	}
	if isErr || json.Unmarshal([]byte(text), &deleted) != nil || !deleted.Deleted {
		t.Fatalf("the author's delete: %s", text)
	}
	seen := issue2514Event(t, w.b, id, reply)
	if seen["deleted_at"] == nil || seen["body"] != nil {
		t.Fatalf("B sees %v; want the message marked deleted with no text", seen)
	}

	responses := map[string]string{}
	for name, path := range map[string]string{
		"events":        "/api/v1/portal/threads/" + id + "/events",
		"thread":        "/api/v1/portal/threads/" + id,
		"thread list":   "/api/v1/portal/threads?asset_id=" + w.asset,
		"chain":         "/api/v1/portal/threads/" + id + "/chain",
		"activity":      "/api/v1/portal/feedback/activity",
		"notifications": "/api/v1/portal/notifications?per_page=200",
	} {
		_, body := w.b.do(t, http.MethodGet, path, nil)
		raw, _ := json.Marshal(body)
		responses["B "+name] = string(raw)
	}
	got, _ := issue2514Feedback(t, w.owner, map[string]any{"action": "get", "thread_id": id})
	responses["manage_feedback get"] = got
	found, _ := issue2514Feedback(t, w.owner, map[string]any{"action": "list", "asset_id": w.asset})
	responses["manage_feedback list"] = found
	searched := w.owner.call("search", map[string]any{
		"intent": secret, "limit": 50, "purpose": "Acceptance #2514 checks a deleted message's text is not found.",
	})
	raw, _ := json.Marshal(searched["groups"]) // the hits; the response does not echo the intent
	responses["search"] = string(raw)
	_, ownerNotices := w.owner.rest(http.MethodGet, "/api/v1/portal/notifications?per_page=200", http.NoBody)
	raw, _ = json.Marshal(ownerNotices)
	responses["owner notifications"] = string(raw)
	for name, body := range responses {
		if strings.Contains(body, secret) {
			t.Errorf("%s still carries the deleted text: %s", name, body)
		}
	}
}

// Another participant's edit and delete are refused; the target owner's delete
// succeeds.
func TestIssue2514_WhoMayChangeAMessage(t *testing.T) {
	w := newIssue2512World(t)
	id := w.openThread(t, w.b, "acc-2514 the legend is cut off")
	reply := issue2514Reply(t, w, id, "acc-2514 the owner's reply")
	path := "/api/v1/portal/threads/" + id + "/events/" + reply
	if status, out := w.c.do(t, http.MethodPatch, path, map[string]any{"body": "acc-2514 rewritten"}); status != http.StatusForbidden {
		t.Errorf("C editing the owner's reply: %d %v; want 403", status, out)
	}
	if status, out := w.c.do(t, http.MethodDelete, path, nil); status != http.StatusForbidden {
		t.Errorf("C deleting the owner's reply: %d %v; want 403", status, out)
	}
	if seen := issue2514Event(t, w.b, id, reply); seen["body"] != "acc-2514 the owner's reply" {
		t.Errorf("a refused change changed the message: %v", seen)
	}

	status, cReply := w.c.do(t, http.MethodPost, "/api/v1/portal/threads/"+id+"/events", map[string]any{"body": "acc-2514 C's reply"})
	cID, _ := cReply["id"].(string)
	if status != http.StatusCreated || cID == "" {
		t.Fatalf("C's reply: %d %v", status, cReply)
	}
	if text, isErr := issue2514Feedback(t, w.owner, map[string]any{"action": "edit", "thread_id": id, "event_id": cID, "body": "x"}); !isErr {
		t.Errorf("the target owner edited C's message: %s", text)
	}
	if text, isErr := issue2514Feedback(t, w.owner, map[string]any{"action": "delete", "thread_id": id, "event_id": cID}); isErr {
		t.Fatalf("the target owner's delete was refused: %s", text)
	}
	if seen := issue2514Event(t, w.b, id, cID); seen["deleted_at"] == nil {
		t.Errorf("the owner's delete did not delete C's message: %v", seen)
	}
}

// Editing an approval is refused, and the sign-off count is unchanged.
func TestIssue2514_AnApprovalIsNeverEdited(t *testing.T) {
	w := newIssue2512World(t)
	id := w.openThread(t, w.b, "acc-2514 ready to publish")
	status, approval := w.b.do(t, http.MethodPost, "/api/v1/portal/threads/"+id+"/events", map[string]any{"event_type": "approval", "body": "acc-2514 approved"})
	approvalID, _ := approval["id"].(string)
	if status != http.StatusCreated || approvalID == "" {
		t.Fatalf("B's approval: %d %v", status, approval)
	}
	_, before := w.owner.rest(http.MethodGet, "/api/v1/portal/assets/"+w.asset+"/signoff", http.NoBody)
	path := "/api/v1/portal/threads/" + id + "/events/" + approvalID
	if status, out := w.b.do(t, http.MethodPatch, path, map[string]any{"body": "acc-2514 not approved"}); status != http.StatusConflict {
		t.Errorf("B editing their approval: %d %v; want 409", status, out)
	}
	if status, out := w.b.do(t, http.MethodDelete, path, nil); status != http.StatusConflict {
		t.Errorf("B deleting their approval: %d %v; want 409", status, out)
	}
	if text, isErr := issue2514Feedback(t, w.owner, map[string]any{"action": "delete", "thread_id": id, "event_id": approvalID}); !isErr {
		t.Errorf("the owner deleted an approval through manage_feedback: %s", text)
	}
	_, after := w.owner.rest(http.MethodGet, "/api/v1/portal/assets/"+w.asset+"/signoff", http.NoBody)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Errorf("the sign-off count moved from %v to %v", before, after)
	}
}

// Editing a reply to add @B queues one mention notification for B; saving it
// again unchanged queues none.
func TestIssue2514_AnEditThatNamesSomeoneTellsThemOnce(t *testing.T) {
	w := newIssue2512World(t)
	id := w.openThread(t, w.c, "acc-2514 the scale is off")
	reply := issue2514Reply(t, w, id, "acc-2514 looking into it")
	if got := countCategory(w.notices(t, w.b, false), "mention"); got != 0 {
		t.Fatalf("B already has %d mention notifications", got)
	}
	edit := map[string]any{"action": "edit", "thread_id": id, "event_id": reply, "body": "acc-2514 looking into it, " + mentionOf(w.b.email)}
	for range 2 {
		if text, isErr := issue2514Feedback(t, w.owner, edit); isErr {
			t.Fatalf("the edit: %s", text)
		}
	}
	if got := countCategory(w.notices(t, w.b, false), "mention"); got != 1 {
		t.Fatalf("mention notifications for B after two identical edits = %d; want 1", got)
	}
	if !strings.Contains(eventMentions(issue2514Event(t, w.b, id, reply)), w.b.email) {
		t.Error("the edited reply does not record B as mentioned")
	}
}
