//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
)

// Acceptance for #2532: picking Acknowledged and then Undo within 10 minutes
// leaves the thread at its previous status with no status line in its
// timeline; Undo is refused to a person who did not make the change, after
// another person adds an event, and past 10 minutes; an undone change sends
// nobody a notification; and the audit log holds both the change and the
// undo.
//
// Wire forms: the thread PATCH takes one JSON object with status as a JSON
// string; the undo route takes no body, the thread and event in its path. No
// parameter admits a second form.

const threads2532 = "/api/v1/portal/threads/"

// platformQuery2532 runs one query on the platform database and returns its
// rows, one per line.
func platformQuery2532(t *testing.T, query string) string {
	t.Helper()
	out, err := exec.Command("docker", "exec", "acme-dev-postgres", // #nosec G204 -- the dev stack's own container
		"psql", "-v", "ON_ERROR_STOP=1", "-At", "-U", "platform", "-d", "mcp_platform", "-c", query).CombinedOutput()
	if err != nil {
		t.Fatalf("psql %q: %v: %s", query, err, out)
	}
	return strings.TrimSpace(string(out))
}

// statusEvent2532 is the id of the thread's status change line, "" for none.
func statusEvent2532(t *testing.T, c *client, threadID string) string {
	t.Helper()
	status, body := c.rest(http.MethodGet, threads2532+threadID+"/events", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the thread's events: %d %v", status, body)
	}
	for _, e := range asList(body["data"]) {
		if e["event_type"] == "status_change" || e["event_type"] == "resolution" {
			id, _ := e["id"].(string)
			return id
		}
	}
	return ""
}

func threadStatus2532(t *testing.T, c *client, threadID string) string {
	t.Helper()
	status, body := c.rest(http.MethodGet, threads2532+threadID, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the thread: %d %v", status, body)
	}
	s, _ := body["status"].(string)
	return s
}

// acknowledge2532 marks the thread Acknowledged as the owner and returns the
// line it wrote.
func acknowledge2532(t *testing.T, w issue2512World, threadID string) string {
	t.Helper()
	if status, body := w.owner.rest(http.MethodPatch, threads2532+threadID, jsonBody(t, map[string]any{"status": "acknowledged"})); status != http.StatusOK {
		t.Fatalf("marking the thread Acknowledged: %d %v", status, body)
	}
	id := statusEvent2532(t, w.owner, threadID)
	if id == "" {
		t.Fatal("the status change wrote no line")
	}
	return id
}

func TestIssue2532_UndoRestoresTheStatusAndRemovesTheLine(t *testing.T) {
	w := newIssue2512World(t)
	id := w.openThread(t, w.b, "acc-2532 does the chart count returning visitors?")
	before := len(w.notices(t, w.b, false))
	line := acknowledge2532(t, w, id)

	status, body := w.owner.rest(http.MethodPost, threads2532+id+"/events/"+line+"/undo", http.NoBody)
	if status != http.StatusOK || body["status"] != "open" {
		t.Fatalf("the owner's undo answered %d %v", status, body)
	}
	if got := threadStatus2532(t, w.owner, id); got != "open" {
		t.Errorf("the thread reads %q after the undo, not open", got)
	}
	if line := statusEvent2532(t, w.owner, id); line != "" {
		t.Errorf("the timeline still holds a status line: %s", line)
	}
	if after := len(w.notices(t, w.b, false)); after != before {
		t.Errorf("the thread's author was notified of the change or its undo: %d notifications, %d before", after, before)
	}
	waitFor(t, "the change and the undo in the audit log", func() (bool, string) {
		rows := platformQuery2532(t, fmt.Sprintf(`SELECT parameters->>'action' FROM audit_logs
			WHERE tool_name = 'manage_feedback' AND parameters->>'thread_id' = '%s' ORDER BY "timestamp"`, id))
		return rows == "status\nundo_status", rows
	})
}

func TestIssue2532_UndoIsRefusedToOthersAfterAReplyAndPastTenMinutes(t *testing.T) {
	w := newIssue2512World(t)

	id := w.openThread(t, w.b, "acc-2532 who may take this back?")
	line := acknowledge2532(t, w, id)
	if status, body := w.b.do(t, http.MethodPost, threads2532+id+"/events/"+line+"/undo", nil); status != http.StatusForbidden {
		t.Errorf("a person who did not make the change was answered %d %v", status, body)
	}

	replied := w.openThread(t, w.b, "acc-2532 and after a reply?")
	line = acknowledge2532(t, w, replied)
	if status, body := w.b.do(t, http.MethodPost, threads2532+replied+"/events", map[string]any{"body": "acc-2532 thanks"}); status != http.StatusCreated {
		t.Fatalf("the author's reply: %d %v", status, body)
	}
	if status, body := w.owner.rest(http.MethodPost, threads2532+replied+"/events/"+line+"/undo", http.NoBody); status != http.StatusConflict {
		t.Errorf("an undo after another person replied was answered %d %v", status, body)
	}

	late := w.openThread(t, w.b, "acc-2532 and later?")
	line = acknowledge2532(t, w, late)
	// The change is made eleven minutes old where it is stored: the criterion
	// does not wait out the window.
	platformQuery2532(t, fmt.Sprintf(`UPDATE portal_thread_events SET created_at = NOW() - interval '11 minutes' WHERE id = '%s'`, line))
	if status, body := w.owner.rest(http.MethodPost, threads2532+late+"/events/"+line+"/undo", http.NoBody); status != http.StatusConflict ||
		!strings.Contains(fmt.Sprint(body["detail"]), "10 minutes") {
		t.Errorf("an undo past the window was answered %d %v", status, body)
	}
	for _, thread := range []string{id, replied, late} {
		if got := threadStatus2532(t, w.owner, thread); got != "acknowledged" {
			t.Errorf("a refused undo moved thread %s to %q", thread, got)
		}
	}
}
