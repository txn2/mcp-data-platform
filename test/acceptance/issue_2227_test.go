//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Issue #2227: internal and shared feedback threads. A person of an external
// persona (#2290: signed in through the identity provider) never sees an internal thread: not in the list, the
// sign-off count, or by its id. A thread marked shared reaches them, and a
// thread they open is shared.
//
// Wire forms: the thread create body's visibility as the strings "internal"
// and "shared" and absent; the thread PATCH body's visibility as the string
// "shared"; the reply body as a JSON object with a body string.

// issue2227Setup is an external person signed in through the identity
// provider, with feedback on, and three assets the administrator shared with
// them as editor.
type issue2227Setup struct {
	session externalSession
	shared  []string
}

func newIssue2227(t *testing.T) issue2227Setup {
	t.Helper()
	admin := connect(t)
	_, email, s := issue2257ExternalAccount(t, admin, "2227", map[string]any{"portal": map[string]any{"feedback": true}})
	out := issue2227Setup{session: s}
	for i := range 3 {
		id := issue2290Asset(t, admin, fmt.Sprintf("2227 item %d", i))
		issue2290Share(t, admin, id, map[string]any{"shared_with_email": email, "permission": "editor"})
		out.shared = append(out.shared, id)
	}
	return out
}

func issue2227Threads(t *testing.T, raw map[string]any) string {
	t.Helper()
	b, _ := json.Marshal(raw)
	return string(b)
}

// An internal thread on an asset shared with an external person is hidden
// from them; marked shared, they see it and reply to it.
func TestIssue2227_AnInternalThreadIsHiddenUntilItIsShared(t *testing.T) {
	s := newIssue2227(t)
	admin := connect(t)
	assetID := s.shared[0]

	status, created := admin.rest(http.MethodPost, "/api/v1/portal/threads", jsonBody(t, map[string]any{
		"kind": "comment", "target_type": "asset", "asset_id": assetID, "body": "acc-2227 internal review note",
	}))
	threadID, _ := created["id"].(string)
	if status != http.StatusCreated || created["visibility"] != "internal" {
		t.Fatalf("an internal user's thread opened as %d %v, want internal by default", status, created)
	}
	if status, approval := admin.rest(http.MethodPost, "/api/v1/portal/threads/"+threadID+"/events", jsonBody(t, map[string]any{
		"event_type": "approval", "body": "acc-2227 signed off internally",
	})); status != http.StatusCreated {
		t.Fatalf("an internal sign-off: %d %v", status, approval)
	}

	_, list := s.session.do(t, http.MethodGet, "/api/v1/portal/threads?asset_id="+assetID, nil)
	if strings.Contains(issue2227Threads(t, list), threadID) {
		t.Errorf("the external user's list holds the internal thread %s", threadID)
	}
	if status, _ := s.session.do(t, http.MethodGet, "/api/v1/portal/threads/"+threadID, nil); status != http.StatusNotFound {
		t.Errorf("the internal thread by id: %d, want 404", status)
	}
	status, signoff := s.session.do(t, http.MethodGet, "/api/v1/portal/assets/"+assetID+"/signoff", nil)
	if status != http.StatusOK || signoff["signed_off"] != float64(0) {
		t.Errorf("the external user's sign-off: %d %v, want the internal approval left out", status, signoff)
	}

	if status, out := admin.rest(http.MethodPatch, "/api/v1/portal/threads/"+threadID, jsonBody(t, map[string]any{"visibility": "shared"})); status != http.StatusOK || out["visibility"] != "shared" {
		t.Fatalf("marking the thread shared: %d %v", status, out)
	}
	if status, thread := s.session.do(t, http.MethodGet, "/api/v1/portal/threads/"+threadID, nil); status != http.StatusOK {
		t.Fatalf("the shared thread by id: %d %v", status, thread)
	}
	if status, out := s.session.do(t, http.MethodPost, "/api/v1/portal/threads/"+threadID+"/events", map[string]any{
		"event_type": "comment", "body": "acc-2227 reply from the client",
	}); status != http.StatusCreated {
		t.Fatalf("the external user's reply: %d %v", status, out)
	}
	if status, out := s.session.do(t, http.MethodPatch, "/api/v1/portal/threads/"+threadID, map[string]any{"visibility": "internal"}); status == http.StatusOK {
		t.Errorf("the external user changed who sees the thread: %v", out)
	}
}

// A thread the external person opens is shared, whatever they ask, and the
// asset's owner sees it.
func TestIssue2227_AThreadAnExternalUserOpensIsShared(t *testing.T) {
	s := newIssue2227(t)
	admin := connect(t)
	assetID := s.shared[1]
	status, created := s.session.do(t, http.MethodPost, "/api/v1/portal/threads", map[string]any{
		"kind": "question", "target_type": "asset", "asset_id": assetID, "body": "acc-2227 question from the client",
		"visibility": "internal",
	})
	threadID, _ := created["id"].(string)
	if status != http.StatusCreated || created["visibility"] != "shared" {
		t.Fatalf("the external user's thread: %d %v, want shared", status, created)
	}
	_, list := admin.rest(http.MethodGet, "/api/v1/portal/threads?asset_id="+assetID, http.NoBody)
	if !strings.Contains(issue2227Threads(t, list), threadID) {
		t.Errorf("the owner's list does not hold the external user's thread %s", threadID)
	}
}

// An internal user chooses shared when opening a thread; the external user
// sees it at once.
func TestIssue2227_AThreadOpenedSharedReachesTheExternalUser(t *testing.T) {
	s := newIssue2227(t)
	admin := connect(t)
	assetID := s.shared[2]
	_, created := admin.rest(http.MethodPost, "/api/v1/portal/threads", jsonBody(t, map[string]any{
		"kind": "comment", "target_type": "asset", "asset_id": assetID, "body": "acc-2227 for the client", "visibility": "shared",
	}))
	threadID, _ := created["id"].(string)
	if created["visibility"] != "shared" {
		t.Fatalf("opened as %v, want shared", created["visibility"])
	}
	_, list := s.session.do(t, http.MethodGet, "/api/v1/portal/threads?asset_id="+assetID, nil)
	if !strings.Contains(issue2227Threads(t, list), threadID) {
		t.Errorf("the external user's list does not hold the shared thread %s", threadID)
	}
}
