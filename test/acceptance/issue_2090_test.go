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

// Issue #2090: a signed-in person who follows a link to an item they cannot
// open asks its owner for access from that page. The owner is emailed and
// approves in one step; the link then opens with no further step. A second
// request while one waits notifies nobody, and a declined request reaches the
// requester.
//
// Wire forms: the request body as a JSON object with a message string, as an
// empty JSON object, and absent; the approval body's permission as the
// strings "viewer" and "editor"; the decline with no body.

// issue2090Mail counts the messages to the address whose subject holds
// subject.
func issue2090Mail(t *testing.T, to, subject string) int {
	t.Helper()
	out := getJSON(t, mailpitBase()+"/api/v1/search?query="+url.QueryEscape("to:"+to), "")
	items, _ := out["messages"].([]any)
	n := 0
	for _, it := range items {
		m, _ := it.(map[string]any)
		if s, _ := m["Subject"].(string); strings.Contains(s, subject) {
			n++
		}
	}
	return n
}

// issue2090WaitMail waits for want messages to the address with subject.
func issue2090WaitMail(t *testing.T, to, subject string, want int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for issue2090Mail(t, to, subject) < want {
		if time.Now().After(deadline) {
			t.Fatalf("%d email(s) to %s with %q within 30s, want %d", issue2090Mail(t, to, subject), to, subject, want)
		}
		time.Sleep(time.Second)
	}
}

// issue2090Asset saves an asset as the administrator, who owns it.
func issue2090Asset(t *testing.T, admin *client, label string) (string, string) {
	t.Helper()
	name := fmt.Sprintf("acc-2090 %s %d", label, time.Now().UnixNano())
	saved := admin.call("save_asset", map[string]any{
		"name": name, "content_type": "text/html", "content": "<p>acc-2090</p>", "description": "Acceptance #2090.",
	})
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("saving the asset: %v", saved)
	}
	t.Cleanup(func() { _, _, _ = admin.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
	return id, name
}

const issue2090Owner = "admin@example.com"

func TestIssue2090_ARequestFromTheLinkIsApprovedInOneStep(t *testing.T) {
	admin := connect(t)
	assetID, name := issue2090Asset(t, admin, "approve")
	token := issue1759SignIn(t, issue1759AnalystEmail, issue1759AnalystPass)
	base := "/api/v1/portal/assets/" + assetID

	if status, _ := issue1759Request(t, token, http.MethodGet, base, nil); status == http.StatusOK {
		t.Fatal("the analyst opened the asset before any share")
	}
	status, page := issue1759Request(t, token, http.MethodGet, base+"/access-request", nil)
	if status != http.StatusOK || page["has_access"] != false || page["owner_email"] != issue2090Owner {
		t.Fatalf("the request page: %d %v, want no access and the owner named", status, page)
	}
	if _, named := page["name"]; named {
		t.Errorf("the request page named the item with names not revealed: %v", page)
	}

	status, req := issue1759Request(t, token, http.MethodPost, base+"/access-requests", []byte(`{"message":"acc-2090 for the quarterly review"}`))
	reqID, _ := req["id"].(string)
	if status != http.StatusCreated || reqID == "" || req["status"] != "pending" {
		t.Fatalf("the request: %d %v", status, req)
	}
	issue2090WaitMail(t, issue2090Owner, name, 1)

	// A second request while one waits is the same request and notifies nobody.
	status, again := issue1759Request(t, token, http.MethodPost, base+"/access-requests", []byte(`{}`))
	if status != http.StatusOK || again["id"] != reqID {
		t.Errorf("a second request: %d %v, want 200 with %s", status, again, reqID)
	}
	status, again = issue1759Request(t, token, http.MethodPost, base+"/access-requests", nil)
	if status != http.StatusOK || again["id"] != reqID {
		t.Errorf("a second request with no body: %d %v, want 200 with %s", status, again, reqID)
	}
	_, page = issue1759Request(t, token, http.MethodGet, base+"/access-request", nil)
	if pending, _ := page["pending"].(map[string]any); pending["id"] != reqID {
		t.Errorf("the request page does not show the pending request: %v", page)
	}

	_, list := admin.rest(http.MethodGet, "/api/v1/portal/access-requests?target_type=asset&target_id="+assetID, http.NoBody)
	reqs, _ := list["requests"].([]any)
	if len(reqs) != 1 {
		t.Fatalf("the owner's list for the asset: %v, want the one request", list)
	}
	if r, _ := reqs[0].(map[string]any); r["target_name"] != name || r["requester_email"] != issue1759AnalystEmail {
		t.Errorf("the listed request: %v", r)
	}

	if status, out := admin.rest(http.MethodPost, "/api/v1/portal/access-requests/"+reqID+"/approve", jsonBody(t, map[string]any{"permission": "viewer"})); status != http.StatusOK || out["status"] != "approved" {
		t.Fatalf("approving as viewer: %d %v", status, out)
	}
	if status, opened := issue1759Request(t, token, http.MethodGet, base, nil); status != http.StatusOK {
		t.Errorf("the analyst opening the link after approval: %d %v", status, opened)
	}
	issue2090WaitMail(t, issue1759AnalystEmail, name, 1)
	time.Sleep(2 * time.Second)
	if n := issue2090Mail(t, issue2090Owner, name); n != 1 {
		t.Errorf("the owner received %d emails for the request, want 1", n)
	}
	if status, _ := admin.rest(http.MethodPost, "/api/v1/portal/access-requests/"+reqID+"/decline", http.NoBody); status != http.StatusConflict {
		t.Errorf("answering an answered request: %d, want 409", status)
	}
	if status, _ := issue1759Request(t, token, http.MethodPost, base+"/access-requests", []byte(`{}`)); status != http.StatusConflict {
		t.Errorf("a request from a person who now opens the item: %d, want 409", status)
	}
}

func TestIssue2090_AnEditorApprovalAndADecline(t *testing.T) {
	admin := connect(t)
	token := issue1759SignIn(t, issue1759AnalystEmail, issue1759AnalystPass)

	editID, _ := issue2090Asset(t, admin, "editor")
	_, req := issue1759Request(t, token, http.MethodPost, "/api/v1/portal/assets/"+editID+"/access-requests", []byte(`{}`))
	reqID, _ := req["id"].(string)
	if status, out := admin.rest(http.MethodPost, "/api/v1/portal/access-requests/"+reqID+"/approve", jsonBody(t, map[string]any{"permission": "editor"})); status != http.StatusOK || out["permission"] != "editor" {
		t.Fatalf("approving as editor: %d %v", status, out)
	}
	if status, out := issue1759Request(t, token, http.MethodGet, "/api/v1/portal/assets/"+editID, nil); status != http.StatusOK || out["share_permission"] != "editor" {
		t.Errorf("the analyst opening the asset after an editor approval: %d share_permission=%v", status, out["share_permission"])
	}

	declineID, name := issue2090Asset(t, admin, "decline")
	_, req = issue1759Request(t, token, http.MethodPost, "/api/v1/portal/assets/"+declineID+"/access-requests", nil)
	reqID, _ = req["id"].(string)
	if status, out := admin.rest(http.MethodPost, "/api/v1/portal/access-requests/"+reqID+"/decline", http.NoBody); status != http.StatusOK || out["status"] != "declined" {
		t.Fatalf("declining: %d %v", status, out)
	}
	issue2090WaitMail(t, issue1759AnalystEmail, name, 1)
	if status, _ := issue1759Request(t, token, http.MethodGet, "/api/v1/portal/assets/"+declineID, nil); status == http.StatusOK {
		t.Error("the analyst opened the asset after a decline")
	}
	if status, _ := issue1759Request(t, token, http.MethodGet, "/api/v1/portal/assets/no-such-asset/access-request", nil); status != http.StatusNotFound {
		t.Errorf("the request page for an item that does not exist: %d, want 404", status)
	}
}
