//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2512: mentions and replies notify the people involved. A mention
// reaches the named person whenever they can open the target, by any route the
// asset read accepts (a persona share among them); a thread's participants hear
// of every later message; a person who cannot open the target hears nothing;
// nobody is told of their own message; and every notification is in the
// person's own unread list in the portal, whether or not an email reaches them.
//
// Wire forms: every call here is a portal REST request with a JSON object body
// (kind, target_type, asset_id, body, event_type as JSON strings; ids as a JSON
// array of numbers); no tool parameter changed.

// issue2512World is an asset the dev owner saved and shared with one persona
// only, two people of that persona, and one person of another.
type issue2512World struct {
	admin, owner *client
	asset, name  string
	// b and c reach the asset only through the persona share; d reaches
	// nothing.
	b, c, d externalSession
}

func newIssue2512World(t *testing.T) issue2512World {
	t.Helper()
	w := issue2512World{admin: connect(t), owner: connectAs(t, devOwnerAPIKey)}
	shared := newPersona2290(t, w.admin, "2512", nil)
	other := newPersona2290(t, w.admin, "2512-other", nil)
	w.b = newPerson2290(t, w.admin, shared, "2512b")
	w.c = newPerson2290(t, w.admin, shared, "2512c")
	w.d = newPerson2290(t, w.admin, other, "2512d")

	w.name = fmt.Sprintf("acc-2512 report %d", time.Now().UnixNano())
	saved := w.owner.call("save_asset", map[string]any{
		"name": w.name, "content_type": "text/html", "content": "<p>acc-2512</p>", "description": "Acceptance #2512.",
	})
	w.asset, _ = saved["asset_id"].(string)
	if w.asset == "" {
		t.Fatalf("saving the asset: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = w.owner.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": w.asset})
	})
	if status, out := w.owner.rest(http.MethodPost, "/api/v1/portal/assets/"+w.asset+"/shares",
		jsonBody(t, map[string]any{"shared_with_persona": shared.name, "permission": "viewer"})); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("sharing the asset with %s: %d %v", shared.name, status, out)
	}
	return w
}

// mentionOf is the token a body names a person with: @local(domain).
func mentionOf(email string) string {
	at := strings.LastIndex(email, "@")
	return "@" + email[:at] + "(" + email[at+1:] + ")"
}

// openThread opens a thread on the asset as a person and returns its id.
func (w issue2512World) openThread(t *testing.T, s externalSession, body string) string {
	t.Helper()
	status, thread := s.do(t, http.MethodPost, "/api/v1/portal/threads", map[string]any{
		"kind": "correction", "target_type": "asset", "asset_id": w.asset, "body": body,
	})
	id, _ := thread["id"].(string)
	if status != http.StatusCreated || id == "" {
		t.Fatalf("%s opening a thread: %d %v", s.email, status, thread)
	}
	return id
}

// ownerReply posts a reply as the owner and returns the event.
func (w issue2512World) ownerReply(t *testing.T, threadID, body string) map[string]any {
	t.Helper()
	status, event := w.owner.rest(http.MethodPost, "/api/v1/portal/threads/"+threadID+"/events", jsonBody(t, map[string]any{"body": body}))
	if status != http.StatusCreated {
		t.Fatalf("the owner's reply: %d %v", status, event)
	}
	return event
}

// notices is a person's own notifications about the world's asset, read from
// the portal's self-scoped list.
func (w issue2512World) notices(t *testing.T, s externalSession, unreadOnly bool) []map[string]any {
	t.Helper()
	path := "/api/v1/portal/notifications?per_page=200"
	if unreadOnly {
		path += "&unread=true"
	}
	status, body := s.do(t, http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("%s reading their notifications: %d %v", s.email, status, body)
	}
	var out []map[string]any
	for _, r := range asList(body["data"]) {
		if r["item_title"] == w.name {
			out = append(out, r)
		}
	}
	return out
}

func asList(v any) []map[string]any {
	raw, _ := v.([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func countCategory(rows []map[string]any, category string) int {
	n := 0
	for _, r := range rows {
		if r["category"] == category {
			n++
		}
	}
	return n
}

// eventMentions is what the platform recorded as delivered on an event: the
// addresses that render as a chip.
func eventMentions(event map[string]any) string {
	meta, _ := event["metadata"].(map[string]any)
	return fmt.Sprint(meta["mentions"])
}

// B opens A only through a persona share. The owner mentions B: one mention
// notification is queued for B and B's mention renders as a chip.
func TestIssue2512_AMentionReachesAPersonaShareRecipient(t *testing.T) {
	w := newIssue2512World(t)
	id := w.openThread(t, w.c, "acc-2512 the total looks off")
	event := w.ownerReply(t, id, mentionOf(w.b.email)+" can you confirm the total?")
	if !strings.Contains(eventMentions(event), w.b.email) {
		t.Fatalf("the reply did not record B as mentioned (no chip): %v", event)
	}
	if got := countCategory(w.notices(t, w.b, false), "mention"); got != 1 {
		t.Fatalf("mention notifications for B = %d; want 1", got)
	}
}

// B posts feedback on A; the owner replies: B is notified of the reply.
func TestIssue2512_TheOwnersReplyReachesTheAuthor(t *testing.T) {
	w := newIssue2512World(t)
	id := w.openThread(t, w.b, "acc-2512 March is missing")
	w.ownerReply(t, id, "acc-2512 added March, thanks")
	rows := w.notices(t, w.b, false)
	if countCategory(rows, "comment") != 1 {
		t.Fatalf("B's notifications about the asset after the owner's reply: %v; want one comment", rows)
	}
	if rows[0]["actor"] != devOwnerEmail {
		t.Errorf("the notification names %v; want the owner", rows[0]["actor"])
	}
}

// C replied in the thread earlier and holds no email share; a later reply
// notifies C.
func TestIssue2512_AnEarlierReplierHearsOfLaterReplies(t *testing.T) {
	w := newIssue2512World(t)
	id := w.openThread(t, w.b, "acc-2512 is Q3 final?")
	if status, out := w.c.do(t, http.MethodPost, "/api/v1/portal/threads/"+id+"/events", map[string]any{"body": "acc-2512 I think so"}); status != http.StatusCreated {
		t.Fatalf("C's reply: %d %v", status, out)
	}
	if got := countCategory(w.notices(t, w.c, false), "comment"); got != 0 {
		t.Fatalf("C was told of their own reply: %d notifications", got)
	}
	w.ownerReply(t, id, "acc-2512 yes, final")
	if got := countCategory(w.notices(t, w.c, false), "comment"); got != 1 {
		t.Fatalf("notifications for C after the owner's reply = %d; want 1", got)
	}
}

// D, mentioned but unable to open A, gets nothing and the mention stays plain
// text. The author of a message is never notified of it.
func TestIssue2512_NobodyOutsideAndNobodyOfTheirOwn(t *testing.T) {
	w := newIssue2512World(t)
	id := w.openThread(t, w.b, "acc-2512 the chart axis is wrong")
	event := w.ownerReply(t, id, mentionOf(w.d.email)+" FYI")
	if strings.Contains(eventMentions(event), w.d.email) {
		t.Errorf("D cannot open the asset but was recorded as mentioned: %v", event)
	}
	if rows := w.notices(t, w.d, false); len(rows) != 0 {
		t.Errorf("D was told about an asset they cannot open: %v", rows)
	}
	status, thread := w.d.do(t, http.MethodGet, "/api/v1/portal/threads/"+id, nil)
	if status == http.StatusOK {
		t.Errorf("D reads the thread: %v", thread)
	}
	status, body := w.owner.rest(http.MethodGet, "/api/v1/portal/notifications?per_page=200", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("the owner's notifications: %d %v", status, body)
	}
	for _, r := range asList(body["data"]) {
		if r["item_title"] == w.name && r["actor"] == devOwnerEmail {
			t.Errorf("the owner was told of their own message: %v", r)
		}
	}
	for _, r := range w.notices(t, w.b, false) {
		if r["actor"] == w.b.email {
			t.Errorf("B was told of their own message: %v", r)
		}
	}
}

// B sees the reply and the mention in the portal's unread list, and reading
// them clears the count. The list reads the notification rows themselves, so
// it answers the same whether the mail server delivered them, failed, or was
// never configured.
func TestIssue2512_TheUnreadListHoldsTheReplyAndTheMention(t *testing.T) {
	w := newIssue2512World(t)
	id := w.openThread(t, w.b, "acc-2512 a typo in the title")
	w.ownerReply(t, id, "acc-2512 fixed")
	w.ownerReply(t, id, mentionOf(w.b.email)+" does it read right now?")

	unread := w.notices(t, w.b, true)
	if countCategory(unread, "comment") != 1 || countCategory(unread, "mention") != 1 {
		t.Fatalf("B's unread notifications about the asset: %v; want the reply and the mention", unread)
	}
	_, count := w.b.do(t, http.MethodGet, "/api/v1/portal/notifications/unread-count", nil)
	before, _ := count["unread"].(float64)
	if before < 2 {
		t.Fatalf("B's unread count = %v; want at least the two", count)
	}
	ids := make([]any, 0, len(unread))
	for _, r := range unread {
		t.Logf("unread for B: category=%v actor=%v", r["category"], r["actor"])
		if r["unread"] != true {
			t.Errorf("a row in the unread list is not marked unread: %v", r)
		}
		ids = append(ids, r["id"])
	}
	if status, out := w.b.do(t, http.MethodPost, "/api/v1/portal/notifications/read", map[string]any{"ids": ids}); status != http.StatusOK || out["marked"] != float64(len(ids)) {
		t.Fatalf("marking them read: %d %v; the unread rows were %v", status, out, unread)
	}
	if rows := w.notices(t, w.b, true); len(rows) != 0 {
		t.Errorf("read notifications are still listed as unread: %v", rows)
	}
	_, count = w.b.do(t, http.MethodGet, "/api/v1/portal/notifications/unread-count", nil)
	if after, _ := count["unread"].(float64); after != before-float64(len(ids)) {
		t.Errorf("the unread count went from %v to %v; want %d fewer", before, after, len(ids))
	}
	// Another person's ids change nothing of theirs.
	if status, out := w.c.do(t, http.MethodPost, "/api/v1/portal/notifications/read", map[string]any{"ids": ids}); status != http.StatusOK || out["marked"] != float64(0) {
		t.Errorf("C marking B's rows: %d %v; want nothing marked", status, out)
	}
}
