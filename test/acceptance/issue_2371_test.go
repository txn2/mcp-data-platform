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

// Issue #2371: manage_feedback could not open a feedback thread, so an agent
// could not comment on an asset that had none. action=create opens one through
// the portal's own POST /api/v1/portal/threads, served as the caller, on the
// portal's terms.
//
// Wire forms: action, asset_id, collection_id, prompt_id, knowledge_page_id,
// target_type, body, kind, title and visibility as JSON strings; anchor as a
// JSON object and as a string holding one; target_version as a JSON number;
// requires_resolution as a JSON boolean. Each is sent as literal tools/call
// params.

// issue2371Commenter is a person whose persona reaches every tool and every
// connection except platform-admin, through a key of its own.
type issue2371Commenter struct {
	persona string
	client  *client
}

func newIssue2371Commenter(t *testing.T, admin *client) issue2371Commenter {
	t.Helper()
	suffix := time.Now().UnixNano()
	name := fmt.Sprintf("acc-2371-%d", suffix)
	role := fmt.Sprintf("acc_2371_%d", suffix)
	status, out := admin.rest(http.MethodPost, "/api/v1/admin/personas", jsonBody(t, map[string]any{
		"name": name, "display_name": "Acceptance 2371 commenter", "roles": []any{role},
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"}, "deny_connections": []any{"platform-admin"},
	}))
	if status != http.StatusCreated {
		t.Fatalf("creating persona %s: %d %v", name, status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name, http.NoBody) })
	status, out = admin.rest(http.MethodPost, "/api/v1/admin/auth/keys", jsonBody(t, map[string]any{
		"name": name, "roles": []any{role}, "description": "Acceptance #2371 commenter",
	}))
	key, _ := out["key"].(string)
	if (status != http.StatusCreated && status != http.StatusOK) || key == "" {
		t.Fatalf("creating key %s: %d %v", name, status, out)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/auth/keys/"+name, http.NoBody) })
	return issue2371Commenter{persona: name, client: connectAs(t, key)}
}

// issue2371Create calls manage_feedback action=create with literal params and
// returns the thread it answers with.
func issue2371Create(t *testing.T, c *client, args map[string]any) map[string]any {
	t.Helper()
	args["action"] = "create"
	res, text, err := c.callRaw("manage_feedback", args)
	if err != nil || res.IsError {
		t.Fatalf("manage_feedback create %v: %v %s", args, err, text)
	}
	var thread map[string]any
	if err := json.Unmarshal([]byte(text), &thread); err != nil {
		t.Fatalf("the created thread is not JSON: %v\n%s", err, text)
	}
	if id, _ := thread["id"].(string); id == "" {
		t.Fatalf("manage_feedback create returned no thread id: %s", text)
	}
	return thread
}

// issue2371Thread reads a thread back through the portal as the administrator.
func issue2371Thread(t *testing.T, admin *client, id string) map[string]any {
	t.Helper()
	status, thread := admin.rest(http.MethodGet, "/api/v1/portal/threads/"+id, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/portal/threads/%s: %d %v", id, status, thread)
	}
	return thread
}

// A person whose persona does not reach platform-admin comments on an asset
// shared with them that has no threads: the thread opens, the portal shows it
// on the asset authored by that person, and they read and reply to it.
func TestIssue2371_ACommentOpensAThreadOnAnAssetThatHasNone(t *testing.T) {
	admin := connect(t)
	commenter := newIssue2371Commenter(t, admin)
	if listed := commenter.client.call("list_connections", map[string]any{}); strings.Contains(fmt.Sprint(listed), "platform-admin") {
		t.Fatalf("the commenter's persona reaches platform-admin, so the criterion proves nothing: %v", listed)
	}
	assetID := issue2290Asset(t, admin, "2371 no threads")
	issue2290Share(t, admin, assetID, map[string]any{"shared_with_persona": commenter.persona, "permission": "viewer"})
	if _, before := admin.rest(http.MethodGet, "/api/v1/portal/threads?asset_id="+assetID, http.NoBody); before["total"] != float64(0) {
		t.Fatalf("the asset already has threads: %v", before)
	}
	_, me := commenter.client.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)

	thread := issue2371Create(t, commenter.client, map[string]any{
		"asset_id": assetID, "body": "acc-2371 is the March total final?", "kind": "question",
		"title": "acc-2371 March", "visibility": "internal", "anchor": map[string]any{"section": "totals"},
		"target_version": 1, "requires_resolution": true,
	})
	id, _ := thread["id"].(string)
	if thread["author_id"] != me["user_id"] || thread["kind"] != "question" || thread["requires_resolution"] != true {
		t.Errorf("the thread %v is not the commenter's question (user %v)", thread, me["user_id"])
	}
	_, list := admin.rest(http.MethodGet, "/api/v1/portal/threads?asset_id="+assetID, http.NoBody)
	if list["total"] != float64(1) || !strings.Contains(fmt.Sprint(list["data"]), id) {
		t.Fatalf("the portal's threads on the asset: %v, want the new thread", list)
	}
	shown := issue2371Thread(t, admin, id)
	if shown["author_id"] != me["user_id"] || shown["title"] != "acc-2371 March" {
		t.Errorf("the portal shows %v, want it authored by %v", shown, me["user_id"])
	}

	got := commenter.client.call("manage_feedback", map[string]any{"action": "get", "thread_id": id})
	if !strings.Contains(fmt.Sprint(got["events"]), "acc-2371 is the March total final?") {
		t.Errorf("the author reading their thread: %v", got)
	}
	reply := commenter.client.call("manage_feedback", map[string]any{"action": "reply", "thread_id": id, "body": "acc-2371 follow-up"})
	if reply["body"] != "acc-2371 follow-up" {
		t.Errorf("the author's reply: %v", reply)
	}
}

// The same call opens a thread on a collection, a prompt, a knowledge page and
// the standalone channel, with the anchor sent as a string holding an object.
func TestIssue2371_EveryTargetTakesAThread(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, "acme-owner-key")

	coll := owner.call("manage_asset", map[string]any{"action": "create_collection", "name": fmt.Sprintf("acc-2371 %d", time.Now().UnixNano())})
	collID, _ := coll["id"].(string)
	if collID == "" {
		collID, _ = coll["collection_id"].(string)
	}
	if collID == "" {
		t.Fatalf("creating the collection: %v", coll)
	}
	t.Cleanup(func() {
		_, _, _ = owner.callRaw("manage_asset", map[string]any{"action": "delete_collection", "collection_id": collID})
	})
	prompt := createPrompt1586(t, admin, fmt.Sprintf("acc-2371-%d", time.Now().UnixNano()), "acc-2371 prompt body")
	status, pages := admin.rest(http.MethodGet, "/api/v1/portal/knowledge-pages", http.NoBody)
	list, _ := pages["pages"].([]any)
	if status != http.StatusOK || len(list) == 0 {
		t.Fatalf("listing knowledge pages: %d %v", status, pages)
	}
	page, _ := list[0].(map[string]any)
	pageID, _ := page["id"].(string)

	for _, tc := range []struct {
		target string
		args   map[string]any
	}{
		{"collection", map[string]any{"collection_id": collID}},
		{"prompt", map[string]any{"prompt_id": prompt.id}},
		{"knowledge_page", map[string]any{"knowledge_page_id": pageID}},
		{"standalone", map[string]any{"target_type": "standalone"}},
	} {
		tc.args["body"] = "acc-2371 on a " + tc.target
		tc.args["anchor"] = `{"line": 3}`
		thread := issue2371Create(t, owner, tc.args)
		shown := issue2371Thread(t, admin, thread["id"].(string))
		if shown["target_type"] != tc.target || shown["author_email"] != "asset.owner@example.com" {
			t.Errorf("%s: the portal shows %v", tc.target, shown)
		}
		if anchor, _ := shown["anchor"].(map[string]any); anchor["line"] != float64(3) {
			t.Errorf("%s: anchor %v, want the object the string held", tc.target, shown["anchor"])
		}
	}
}

// A person without access to the target gets the refusal the portal gives
// them for the same request.
func TestIssue2371_ARefusalIsThePortals(t *testing.T) {
	owner := connectAs(t, "acme-owner-key")
	peer := connectAs(t, "acme-peer-key")
	assetID := issue2290Asset(t, owner, "2371 not shared")

	status, refusal := peer.rest(http.MethodPost, "/api/v1/portal/threads", jsonBody(t, map[string]any{
		"kind": "comment", "target_type": "asset", "asset_id": assetID, "body": "acc-2371 uninvited",
	}))
	detail, _ := refusal["detail"].(string)
	if status != http.StatusForbidden || detail == "" {
		t.Fatalf("the portal's answer to the peer: %d %v, want a 403 with a detail", status, refusal)
	}
	res, text, err := peer.callRaw("manage_feedback", map[string]any{"action": "create", "asset_id": assetID, "body": "acc-2371 uninvited"})
	if err != nil || !res.IsError {
		t.Fatalf("the peer opened a thread on an asset not shared with them: %v %s", err, text)
	}
	if want := fmt.Sprintf("%s (HTTP %d)", detail, status); !strings.Contains(text, want) {
		t.Errorf("the tool's refusal %q does not carry the portal's %q", text, want)
	}
}

// A reply with no thread_id names the action that starts a thread.
func TestIssue2371_AReplyWithoutAThreadNamesCreate(t *testing.T) {
	c := connectAs(t, "acme-owner-key")
	res, text, err := c.callRaw("manage_feedback", map[string]any{"action": "reply", "asset_id": "anything", "body": "acc-2371"})
	if err != nil || !res.IsError || !strings.Contains(text, "action=create") {
		t.Errorf("reply without thread_id: %v %q, want an error naming action=create", err, text)
	}
}
