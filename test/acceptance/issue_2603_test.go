//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Issue #2603: a thread on the General (standalone) feedback channel notified
// nobody: it has no item, so no owner and no grantees, and the first message's
// only participant is its author. Now Admin > Settings > General feedback names
// who hears about a new General post (personas, addresses, channels; seeded
// from portal.feedback.standalone_recipients, else the administrator persona):
// they are told of the post and every reply, the email links to the thread on
// the Feedback page, the author can be told it was received, and a list that
// reaches nobody says so.
//
// Against the dev stack: admin@example.com signs in through Keycloak, which
// makes them a person the directory holds with the admin persona's roles; the
// analyst API key (rachel.thompson@example.com) posts. The seed itself (the
// administrator persona with nothing configured) is held by
// generalfeedbackwire's TestService_SeedsFromTheConfiguration: a stack that
// has saved a list no longer serves the seed, so a criterion here saves the
// administrator persona explicitly.
//
// Wire forms: the settings PUT body is one JSON object (personas, recipients
// and channels JSON arrays of strings, acknowledge_author a JSON boolean); the
// thread POST body is one JSON object with kind, target_type and body strings;
// the reply POST body likewise; GETs carry no body.

const (
	issue2603Settings = "/api/v1/admin/settings/general-feedback"
	issue2603Reach    = "/api/v1/portal/feedback/general"
	issue2603Author   = "rachel.thompson@example.com"
	issue2603Admin    = "admin@example.com"
)

type issue2603Row struct {
	Recipient string `json:"recipient"`
	Subject   string `json:"subject"`
	ItemTitle string `json:"item_title"`
	Link      string `json:"link"`
}

// issue2603Save stores a list and restores the one in force when the test ends.
func issue2603Save(t *testing.T, admin *client, list map[string]any) map[string]any {
	t.Helper()
	status, before := admin.rest(http.MethodGet, issue2603Settings, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the list: %d %v", status, before)
	}
	t.Cleanup(func() {
		admin.rest(http.MethodPut, issue2603Settings, jsonBody(t, map[string]any{
			"personas": before["personas"], "recipients": before["recipients"], "channels": before["channels"],
			"acknowledge_author": before["acknowledge_author"],
		}))
	})
	status, saved := admin.rest(http.MethodPut, issue2603Settings, jsonBody(t, list))
	if status != http.StatusOK {
		t.Fatalf("saving the list %v: %d %v", list, status, saved)
	}
	return saved
}

// issue2603Post opens a General thread as the poster and returns its id and
// title.
func issue2603Post(t *testing.T, poster *client) (id, title string) {
	t.Helper()
	title = fmt.Sprintf("Acceptance 2603 data request %d", time.Now().UnixNano())
	status, thread := poster.rest(http.MethodPost, "/api/v1/portal/threads", jsonBody(t, map[string]any{
		"kind": "suggestion", "target_type": "standalone", "body": title,
	}))
	if status != http.StatusCreated {
		t.Fatalf("opening a General thread: %d %v", status, thread)
	}
	id, _ = thread["id"].(string)
	got, _ := thread["title"].(string)
	if got != "" {
		title = got
	}
	return id, title
}

// issue2603Rows are the notifications queued for recipient about title.
func issue2603Rows(t *testing.T, admin *client, recipient, title string) []issue2603Row {
	t.Helper()
	status, body := admin.rest(http.MethodGet, "/api/v1/admin/notifications?recipient="+url.QueryEscape(recipient)+"&per_page=200", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the notification history: %d %v", status, body)
	}
	raw, _ := json.Marshal(body["data"])
	var rows []issue2603Row
	_ = json.Unmarshal(raw, &rows)
	var out []issue2603Row
	for _, r := range rows {
		if r.ItemTitle == title {
			out = append(out, r)
		}
	}
	return out
}

// issue2603Await polls for a notification to recipient about title whose
// subject contains want.
func issue2603Await(t *testing.T, admin *client, recipient, title, want string) issue2603Row {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		for _, r := range issue2603Rows(t, admin, recipient, title) {
			if strings.Contains(r.Subject, want) {
				return r
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no notification to %s about %q with %q within 20s: %+v", recipient, title, want, issue2603Rows(t, admin, recipient, title))
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestIssue2603_ANewGeneralPostReachesTheList: with the administrator persona
// and a team address on the list and acknowledgement on, a General post with
// no mention notifies the signed-in administrator and the address (no
// account), each email linking to the thread, and tells its author it was
// received.
func TestIssue2603_ANewGeneralPostReachesTheList(t *testing.T) {
	browserSignIn(t, baseURL(), issue2603Admin, "admin-password")
	admin := connect(t)
	team := fmt.Sprintf("requests-%d@example.com", time.Now().UnixNano())
	saved := issue2603Save(t, admin, map[string]any{
		"personas": []string{"admin"}, "recipients": []string{team}, "channels": []string{}, "acknowledge_author": true,
	})
	people, _ := saved["people"].([]any)
	if !strings.Contains(fmt.Sprint(people), issue2603Admin) || !strings.Contains(fmt.Sprint(people), team) {
		t.Fatalf("the list reaches %v; want the signed-in administrator and %s", people, team)
	}

	id, title := issue2603Post(t, connectAs(t, "acme-analyst-key"))
	link := "/portal/feedback#thread-" + id
	for _, to := range []string{issue2603Admin, team} {
		row := issue2603Await(t, admin, to, title, "left feedback on")
		if !strings.HasSuffix(row.Link, link) {
			t.Errorf("the email to %s links to %q; want one ending %s", to, row.Link, link)
		}
	}
	ack := issue2603Await(t, admin, issue2603Author, title, "Your feedback was received")
	if !strings.HasSuffix(ack.Link, link) {
		t.Errorf("the acknowledgement links to %q", ack.Link)
	}
	if strings.Contains(ack.Subject, issue2603Admin) || strings.Contains(ack.Subject, team) {
		t.Errorf("the acknowledgement names a recipient: %q", ack.Subject)
	}
}

// TestIssue2603_AReplyReachesTheListAndTheAuthor: a reply by someone on the
// list notifies the author and the rest of the list, never the replier.
func TestIssue2603_AReplyReachesTheListAndTheAuthor(t *testing.T) {
	admin := connect(t)
	team := fmt.Sprintf("requests-%d@example.com", time.Now().UnixNano())
	issue2603Save(t, admin, map[string]any{
		"personas": []string{}, "recipients": []string{team, issue2603Admin}, "channels": []string{}, "acknowledge_author": false,
	})
	id, title := issue2603Post(t, connectAs(t, "acme-analyst-key"))
	status, reply := admin.rest(http.MethodPost, "/api/v1/portal/threads/"+id+"/events", jsonBody(t, map[string]any{
		"event_type": "comment", "body": "We are on it.",
	}))
	if status != http.StatusCreated {
		t.Fatalf("replying: %d %v", status, reply)
	}
	issue2603Await(t, admin, issue2603Author, title, "on")
	deadline := time.Now().Add(20 * time.Second)
	for len(issue2603Rows(t, admin, team, title)) < 2 && time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
	}
	if n := len(issue2603Rows(t, admin, team, title)); n < 2 {
		t.Errorf("the team address was told %d times; want the post and the reply", n)
	}
	for _, r := range issue2603Rows(t, admin, issue2603Admin, title) {
		if strings.Contains(r.Subject, "We are on it") || strings.HasPrefix(r.Subject, issue2603Admin) {
			t.Errorf("the replier was told of their own reply: %+v", r)
		}
	}
	if got := issue2603Rows(t, admin, issue2603Author, title); len(got) > 0 {
		for _, r := range got {
			if strings.Contains(r.Subject, "Your feedback was received") {
				t.Errorf("acknowledgement off, yet the author was told: %+v", r)
			}
		}
	}
}

// TestIssue2603_AListThatReachesNobodySaysSo: an empty list carries the
// reaches_nobody warning, the composer's read says nobody is told, and a post
// acknowledges nothing; a list naming an unknown persona or a non-address is
// refused.
func TestIssue2603_AListThatReachesNobodySaysSo(t *testing.T) {
	admin := connect(t)
	saved := issue2603Save(t, admin, map[string]any{
		"personas": []string{}, "recipients": []string{}, "channels": []string{}, "acknowledge_author": true,
	})
	if warnings, _ := saved["warnings"].([]any); !strings.Contains(fmt.Sprint(warnings), "reaches_nobody") {
		t.Errorf("an empty list's warnings = %v", saved["warnings"])
	}
	poster := connectAs(t, "acme-analyst-key")
	if status, reach := poster.rest(http.MethodGet, issue2603Reach, http.NoBody); status != http.StatusOK || reach["reaches_someone"] != false {
		t.Errorf("the composer's read: %d %v; want reaches_someone false", status, reach)
	}
	_, title := issue2603Post(t, poster)
	time.Sleep(2 * time.Second)
	if rows := issue2603Rows(t, admin, issue2603Author, title); len(rows) != 0 {
		t.Errorf("a post that reached nobody was acknowledged: %+v", rows)
	}

	for _, bad := range []map[string]any{
		{"personas": []string{"no-such-persona"}},
		{"recipients": []string{"not an address"}},
	} {
		if status, body := admin.rest(http.MethodPut, issue2603Settings, jsonBody(t, bad)); status != http.StatusBadRequest {
			t.Errorf("saving %v answered %d %v; want 400", bad, status, body)
		}
	}
}
