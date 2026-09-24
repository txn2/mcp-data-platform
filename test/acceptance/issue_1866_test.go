//go:build integration

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1866: a persona member was offered only their own library in the
// upload dialogs, with nothing saying why, and could not read the script
// behind a resource they had been given.
//
// What these hold, against the running platform with two ordinary people of
// the collaborator persona (dev/platform.yaml's owner and peer keys): the
// boundary the upload dialogs now name is the server's (a member uploading into
// the persona library is refused, /me names the persona the dialog names, and
// moving a file the member owns into it is accepted); and a script's definition
// -- its source and history -- is readable by someone who does not own it,
// through manage_script and the portal routes, while its runs, its state, the
// roles its authors held and every action stay the owner's. The dialog's
// notice itself is a browser surface, checked on the running portal.
//
// Wire forms: manage_script's `command`, `name`, `owner_email`, `source`,
// `description` and `display_name` are typed strings in its schema, and
// run_script's `name` and `owner_email` too, so each admits one JSON form and
// is sent as that literal. The resource routes are multipart (form-encoded
// string fields ahead of a byte file part) and a JSON PATCH whose `scope` and
// `scope_id` are strings; each has that one form.

const (
	persona1866 = "collaborator"
	source1866  = "rows = [\"a\", \"b\"]\nprint(len(rows))\n"
	edited1866  = "rows = [\"a\", \"b\", \"c\"]\nprint(len(rows))\n"
)

// script1866 creates a script as the owner, edits it once so it has a
// history, and returns its name and id.
func script1866(t *testing.T, owner *client) (name, id string) {
	t.Helper()
	name = fmt.Sprintf("acc-1866-%d", time.Now().UnixNano())
	created := owner.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source1866,
		"description": "Acceptance #1866: a script another person reads.",
	})
	id, _ = created["id"].(string)
	if id == "" {
		t.Fatalf("manage_script create returned no id: %v", created)
	}
	t.Cleanup(func() {
		_, _, _ = owner.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})
	owner.call("manage_script", map[string]any{"command": "update", "name": name, "source": edited1866})
	return name, id
}

// TestIssue1866_AnotherPersonsScriptIsReadableThroughTheTool is the MCP half:
// the peer names the owner and reads the source, the content, the diff and the
// history, without the owner's live runs or the roles each version's author
// held.
func TestIssue1866_AnotherPersonsScriptIsReadableThroughTheTool(t *testing.T) {
	owner, peer := connectAs(t, devOwnerAPIKey), connectAs(t, devPeerAPIKey)
	name, _ := script1866(t, owner)
	named := func(command string) map[string]any {
		return map[string]any{"command": command, "name": name, "owner_email": devOwnerEmail}
	}

	got := peer.call("manage_script", named("get"))
	if got["source"] != edited1866 {
		t.Errorf("get: source = %q, want the owner's live source", got["source"])
	}
	if _, ok := got["live_runs"]; ok {
		t.Error("get: a reader who does not own the script was given its live runs")
	}
	if content, _ := peer.call("manage_script", named("get_content"))["content"].(string); !strings.Contains(content, `"c"`) {
		t.Errorf("get_content = %q, want the live source", content)
	}
	if diff := fmt.Sprint(peer.call("manage_script", named("diff"))); !strings.Contains(diff, `"c"`) {
		t.Errorf("diff = %s, want the edit between the two versions", diff)
	}
	versions, _ := peer.call("manage_script", named("versions"))["versions"].([]any)
	if len(versions) != 2 {
		t.Fatalf("versions: %d entries, want the create and the edit", len(versions))
	}
	for _, v := range versions {
		if row, _ := v.(map[string]any); row["author_roles"] != nil {
			t.Errorf("versions: a reader who does not own the script was shown its author's roles: %v", row)
		}
	}
	owned, _ := owner.call("manage_script", map[string]any{"command": "versions", "name": name})["versions"].([]any)
	if first, _ := owned[0].(map[string]any); first["author_roles"] == nil {
		t.Errorf("versions: the owner is not shown the roles their versions were saved with: %v", first)
	}
}

// TestIssue1866_ActingOnAnotherPersonsScriptStaysTheOwners holds the other
// side: every command that acts on the script, or reads what it did, is
// refused to the peer in words that say whose it is, and run_script is too.
func TestIssue1866_ActingOnAnotherPersonsScriptStaysTheOwners(t *testing.T) {
	owner, peer := connectAs(t, devOwnerAPIKey), connectAs(t, devPeerAPIKey)
	name, _ := script1866(t, owner)

	for _, command := range []string{"runs", "state", "update", "delete", "schedule_set"} {
		args := map[string]any{"command": command, "name": name, "owner_email": devOwnerEmail, "display_name": "taken"}
		res, text, err := peer.callRaw("manage_script", args)
		if err != nil {
			t.Fatalf("%s: %v", command, err)
		}
		if !res.IsError || !strings.Contains(text, "only a script's owner or an administrator") {
			t.Errorf("%s by a reader who does not own it: error=%v %q", command, res.IsError, text)
		}
	}
	res, text, err := peer.callRaw("run_script", map[string]any{"name": name, "owner_email": devOwnerEmail})
	if err != nil {
		t.Fatalf("run_script: %v", err)
	}
	if !res.IsError || !strings.Contains(text, "only a script's owner or an administrator") {
		t.Errorf("run_script by a reader who does not own it: error=%v %q", res.IsError, text)
	}
	if got := owner.call("manage_script", map[string]any{"command": "get", "name": name}); got["source"] != edited1866 {
		t.Errorf("the refused commands changed the script: %v", got["source"])
	}
}

// TestIssue1866_ThePortalScriptPageReadsTheDefinition is the portal half, over
// the routes the script page reads: the page's record carries the source with
// owned false, the history is served without the authors' roles, and the run
// history is not served at all.
func TestIssue1866_ThePortalScriptPageReadsTheDefinition(t *testing.T) {
	owner, peer := connectAs(t, devOwnerAPIKey), connectAs(t, devPeerAPIKey)
	_, id := script1866(t, owner)

	status, page := peer.rest(http.MethodGet, "/api/v1/portal/scripts/"+id, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET the script as its reader: status %d, %v", status, page)
	}
	if page["owned"] != false || page["source"] != edited1866 {
		t.Errorf("the script page's record: owned=%v source=%q; want false and the live source", page["owned"], page["source"])
	}

	status, history := peer.rest(http.MethodGet, "/api/v1/portal/scripts/"+id+"/versions", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET the history as its reader: status %d, %v", status, history)
	}
	rows, _ := history["data"].([]any)
	if len(rows) != 2 {
		t.Fatalf("the history has %d versions, want 2: %v", len(rows), history)
	}
	for _, r := range rows {
		row, _ := r.(map[string]any)
		if row["author_roles"] != nil {
			t.Errorf("the history shows its reader the author's roles: %v", row)
		}
		if src, _ := row["source"].(string); src == "" {
			t.Errorf("a version in the history carries no source: %v", row)
		}
	}

	for _, path := range []string{"/runs", "/produced", "/state"} {
		if status, body := peer.rest(http.MethodGet, "/api/v1/portal/scripts/"+id+path, http.NoBody); status != http.StatusNotFound {
			t.Errorf("GET %s as a reader who does not own the script: status %d, %v; want 404", path, status, body)
		}
	}
	if status, _ := owner.rest(http.MethodGet, "/api/v1/portal/scripts/"+id+"/runs", http.NoBody); status != http.StatusOK {
		t.Errorf("the owner's run history: status %d", status)
	}
}

// upload1866 uploads a small text file into a library as c, returning the
// status and the decoded answer.
func upload1866(t *testing.T, c *client, scope, scopeID string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	for k, v := range map[string]string{
		"scope": scope, "scope_id": scopeID, "path": "acceptance-1866",
		"display_name": "acc-1866-" + stamp, "description": "Acceptance #1866: where a persona member may upload.",
	} {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	part, err := w.CreateFormFile("file", "acc-1866-"+stamp+".txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("persona library acceptance\n"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(c.ctx, http.MethodPost, c.base+"/api/v1/resources", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out) //nolint:errcheck // a non-object body leaves out nil
	return res.StatusCode, out
}

// TestIssue1866_TheUploadBoundaryTheDialogsNameIsTheServers holds the facts
// the upload dialogs' notice states: the member's /me names the persona and
// carries no persona-admin role for it, an upload into that persona's library
// is refused, and the way the notice points to -- upload to your own library,
// then move it with Library -- is accepted.
func TestIssue1866_TheUploadBoundaryTheDialogsNameIsTheServers(t *testing.T) {
	peer := connectAs(t, devPeerAPIKey)

	status, me := peer.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
	if status != http.StatusOK || me["persona"] != persona1866 {
		t.Fatalf("/me: status %d persona %v; the dialog names the persona /me reports", status, me["persona"])
	}
	roles, _ := me["roles"].([]any)
	for _, r := range roles {
		if s, _ := r.(string); strings.Contains(s, "persona-admin:"+persona1866) {
			t.Fatalf("the peer holds %s; this criterion needs a member without the persona's admin role", s)
		}
	}

	if status, body := upload1866(t, peer, "persona", persona1866); status != http.StatusForbidden {
		t.Errorf("a member's upload into the persona library: status %d, %v; want 403", status, body)
	}

	self, _ := me["user_id"].(string)
	status, created := upload1866(t, peer, "user", self)
	id, _ := created["id"].(string)
	if status != http.StatusCreated || id == "" {
		t.Fatalf("an upload into the member's own library: status %d, %v", status, created)
	}
	t.Cleanup(func() { _, _ = peer.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	status, moved := peer.rest(http.MethodPatch, "/api/v1/resources/"+id,
		jsonBody(t, map[string]any{"scope": "persona", "scope_id": persona1866}))
	if status != http.StatusOK {
		t.Fatalf("moving the member's own file into the persona library: status %d, %v", status, moved)
	}
	status, row := peer.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody)
	if status != http.StatusOK || row["scope"] != "persona" || row["scope_id"] != persona1866 {
		t.Errorf("after the move the file is in %v/%v, want persona/%s", row["scope"], row["scope_id"], persona1866)
	}
}
