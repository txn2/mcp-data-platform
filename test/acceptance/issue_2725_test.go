//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2725: an administrator's request they can decide
// themselves asks no other administrator and never the requester; a request
// from someone who cannot decide it still notifies every approver; the email
// links to that request; a decided request reads who decided it; and the
// recent decisions are listed under the queue's read rule.
//
// The requests are form_public requests for a form's version, filed with
// manage_approval as #2494's are: the administrator is the default key, the
// ordinary person acme-owner-key, another ordinary person acme-peer-key.
// Notices are read from the administrators' notification history, where a
// notice is written in the same call that files the request.
//
// Wire forms: manage_approval submit and decide take their arguments as the
// literal tools/call params (version as a JSON number); the portal's
// GET /api/v1/portal/approvals/{id} and
// GET /api/v1/portal/approvals?decided_since=<RFC 3339> take no body, and
// decided_since is sent once as a valid time and once as a value that is not.

// issue2725Submit files a request for version 1 of a fresh form owned by c and
// returns its id and the form's name.
func issue2725Submit(t *testing.T, c *client, prefix string) (id, name string) {
	t.Helper()
	name = issue2494Save(t, c, prefix)
	t.Cleanup(func() {
		_, _, _ = connect(t).callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})
	out := c.call("manage_approval", map[string]any{"command": "submit", "script": name, "version": 1, "reason": "Acceptance 2725."})
	request, _ := out["request"].(map[string]any)
	id, _ = request["id"].(string)
	if id == "" || request["status"] != "pending" {
		t.Fatalf("submit: %v", out)
	}
	return id, name
}

// issue2725Notices are the approval_request notices for version 1 of the
// form name, by recipient, and the links they carry.
func issue2725Notices(t *testing.T, admin *client, name string) (map[string]int, []string) {
	t.Helper()
	status, body := admin.rest(http.MethodGet, "/api/v1/admin/notifications?per_page=200", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the notification history: %d %v", status, body)
	}
	rows, _ := body["data"].([]any)
	want := fmt.Sprintf("asked for approval of version 1 of the form %s ", name)
	got := map[string]int{}
	var links []string
	for _, r := range rows {
		row, _ := r.(map[string]any)
		if !strings.Contains(fmt.Sprint(row["subject"]), want) {
			continue
		}
		got[fmt.Sprint(row["recipient"])]++
		links = append(links, fmt.Sprint(row["link"]))
	}
	return got, links
}

// TestIssue2725_AnAdministratorsOwnRequestAsksNobody: an administrator's
// request that they approve themselves sends no approval_request notice to any
// administrator, the requester included.
func TestIssue2725_AnAdministratorsOwnRequestAsksNobody(t *testing.T) {
	admin := connect(t)
	id, name := issue2725Submit(t, admin, "acc-2725-own")
	if notices, _ := issue2725Notices(t, admin, name); len(notices) != 0 {
		t.Fatalf("an administrator's own request notified %v", notices)
	}
	decided := admin.call("manage_approval", map[string]any{"command": "decide", "id": id, "decision": "approve"})
	if decided["status"] != "approved" || decided["self_approved"] != true {
		t.Fatalf("self-approval: %v", decided)
	}
	if notices, _ := issue2725Notices(t, admin, name); len(notices) != 0 {
		t.Fatalf("the self-approved request notified %v after its decision", notices)
	}
}

// TestIssue2725_ARequestFromSomeoneWhoCannotDecideItNotifiesEveryApprover: a
// request filed by a person who cannot decide it notifies the approvers, each
// once and never the requester, and each notice links to that request.
func TestIssue2725_ARequestFromSomeoneWhoCannotDecideItNotifiesEveryApprover(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	admin := connect(t)
	id, name := issue2725Submit(t, owner, "acc-2725-asked")
	notices, links := issue2725Notices(t, admin, name)
	if len(notices) == 0 {
		t.Fatal("no approver was notified of a request its requester cannot decide")
	}
	for recipient, n := range notices {
		if n != 1 {
			t.Errorf("%s was notified %d times of one request", recipient, n)
		}
		if strings.EqualFold(recipient, devOwnerEmail) {
			t.Errorf("the requester was notified of their own request")
		}
	}
	want := "/portal/approvals?request=" + url.QueryEscape(id)
	for _, l := range links {
		if !strings.HasSuffix(l, want) {
			t.Errorf("a notice links to %q; want it to open the request (%s)", l, want)
		}
	}
	if len(links) == 0 {
		t.Error("the notices carry no link")
	}
}

// TestIssue2725_ADecidedRequestReadsWhoDecidedIt: the request an email opens,
// read after another administrator approved it, names who approved it and
// when, and is not shown to a reader who may not read it.
func TestIssue2725_ADecidedRequestReadsWhoDecidedIt(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	admin := connect(t)
	id, _ := issue2725Submit(t, owner, "acc-2725-decided")
	admin.call("manage_approval", map[string]any{"command": "decide", "id": id, "decision": "approve", "comment": "Looks right."})

	for who, c := range map[string]*client{"an administrator": admin, "the requester": owner} {
		status, body := c.rest(http.MethodGet, "/api/v1/portal/approvals/"+url.PathEscape(id), http.NoBody)
		if status != http.StatusOK || body["status"] != "approved" || body["decided_by"] == "" || body["decided_at"] == nil {
			t.Fatalf("%s reading the decided request: %d %v", who, status, body)
		}
	}
	if status, body := peer.rest(http.MethodGet, "/api/v1/portal/approvals/"+url.PathEscape(id), http.NoBody); status != http.StatusNotFound {
		t.Fatalf("a reader who may not read the request read it: %d %v", status, body)
	}
}

// TestIssue2725_TheRecentDecisionsAreListed: with decided_since, the requests
// decided since then are listed most recently decided first with who decided
// each, a reader who may not read one does not see it, and a value that is not
// a time is refused.
func TestIssue2725_TheRecentDecisionsAreListed(t *testing.T) {
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	admin := connect(t)
	since := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	first, _ := issue2725Submit(t, owner, "acc-2725-recent-a")
	second, _ := issue2725Submit(t, owner, "acc-2725-recent-b")
	admin.call("manage_approval", map[string]any{"command": "decide", "id": first, "decision": "deny", "comment": "Not yet."})
	admin.call("manage_approval", map[string]any{"command": "decide", "id": second, "decision": "approve"})

	recent := func(c *client) []map[string]any {
		t.Helper()
		status, body := c.rest(http.MethodGet, "/api/v1/portal/approvals?decided_since="+url.QueryEscape(since), http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("listing the recent decisions: %d %v", status, body)
		}
		rows, _ := body["requests"].([]any)
		out := make([]map[string]any, 0, len(rows))
		for _, r := range rows {
			row, _ := r.(map[string]any)
			out = append(out, row)
		}
		return out
	}
	rows := recent(admin)
	at := map[string]int{}
	for i, r := range rows {
		at[fmt.Sprint(r["id"])] = i
		if r["decided_by"] == "" || r["status"] == "pending" {
			t.Errorf("a listed request has no decision: %v", r)
		}
	}
	a, okA := at[first]
	b, okB := at[second]
	if !okA || !okB {
		t.Fatalf("the two decided requests are not both listed: %v", rows)
	}
	if b > a {
		t.Errorf("the request decided last is listed after the one decided first")
	}
	for _, r := range recent(peer) {
		if r["id"] == first || r["id"] == second {
			t.Errorf("a reader who may not read %v saw it", r["id"])
		}
	}
	if status, _ := admin.rest(http.MethodGet, "/api/v1/portal/approvals?decided_since=last-week", http.NoBody); status != http.StatusBadRequest {
		t.Errorf("decided_since=last-week answered %d; want 400", status)
	}
}
