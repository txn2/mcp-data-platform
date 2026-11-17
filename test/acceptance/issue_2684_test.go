//go:build integration

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Acceptance for #2684: an administrator grants a person or a persona write on
// a Global folder and everything beneath it. The grantee uploads there through
// the portal's REST upload and through manage_resource, makes folders and
// deletes inside it, and is refused outside it (a sibling, a name that only
// starts the same, another folder), refused renaming the granted folder and
// moving out of it, and allowed moving inside it. A rename by an
// administrator carries the grant; a removed grant refuses the next write; a
// persona grant reaches a member; an external persona is refused at save; the
// grant list is the administrator's, and a grantee reads only their own
// folders.
//
// Wire forms: the admin grant routes take a JSON object of strings (folder,
// email or persona); the folder routes take JSON objects of strings (scope,
// path, from, to); the upload is multipart/form-data with string fields
// (scope, path, display_name, description) before the file part; manage_resource takes
// action, scope, path, filename, display_name, description, content and
// content_type as strings, sent as literal tools/call params.

const grants2684 = "/api/v1/admin/resources/folder-grants"

// folder2684 makes a Global folder as the administrator and removes it, with
// whatever is left in it, when the test ends.
func folder2684(t *testing.T, admin *client, path string) {
	t.Helper()
	status, out := admin.rest(http.MethodPost, "/api/v1/resources/folders", jsonBody(t, map[string]any{"scope": "global", "path": path}))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("making Global folder %s: %d %v", path, status, out)
	}
}

// grant2684 grants write on folder to a person or persona and returns its id.
func grant2684(t *testing.T, admin *client, folder, email, persona string) string {
	t.Helper()
	status, out := admin.rest(http.MethodPost, grants2684, jsonBody(t, map[string]any{"folder": folder, "email": email, "persona": persona}))
	if status != http.StatusCreated {
		t.Fatalf("granting %s%s write on %s: %d %v", email, persona, folder, status, out)
	}
	id, _ := out["id"].(string)
	t.Cleanup(func() { _, _ = admin.rest(http.MethodDelete, grants2684+"/"+id, http.NoBody) })
	return id
}

// upload2684 sends a file to a Global folder the way the portal's upload does
// and returns the status and, when stored, the resource id.
func upload2684(t *testing.T, c *client, path, name string) (int, string) {
	t.Helper()
	body := new(bytes.Buffer)
	w := multipart.NewWriter(body)
	for field, value := range map[string]string{"scope": "global", "path": path, "display_name": name, "description": "Acceptance #2684."} {
		_ = w.WriteField(field, value)
	}
	part, _ := w.CreateFormFile("file", name+".csv")
	_, _ = part.Write([]byte("a,b\n1,2\n"))
	_ = w.Close()
	req, _ := http.NewRequestWithContext(c.ctx, http.MethodPost, c.base+"/api/v1/resources", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatalf("uploading: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	out := map[string]any{}
	_ = json.NewDecoder(res.Body).Decode(&out)
	id, _ := out["id"].(string)
	return res.StatusCode, id
}

func TestIssue2684_AGranteeWritesInsideTheFolderAndNowhereElse(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	root := "g2684-" + unique1568()
	sibling := root + "2"
	folder2684(t, admin, root)
	folder2684(t, admin, sibling)
	folder2684(t, admin, "o2684-"+unique1568())

	if status, _ := upload2684(t, owner, root+"/q3", "before"); status != http.StatusForbidden {
		t.Fatalf("an upload before any grant answered %d, not 403", status)
	}
	grant2684(t, admin, root, devOwnerEmail, "")

	status, id := upload2684(t, owner, root+"/q3", "summary")
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("the grantee's REST upload under the granted folder answered %d", status)
	}
	out := owner.call("manage_resource", map[string]any{
		"action": "create", "scope": "global", "path": root + "/q3", "filename": "notes.md",
		"display_name": "acc2684 notes " + unique1568(), "description": "Acceptance #2684.", "content": "# notes\n", "content_type": "text/markdown",
	})
	if out["resource_id"] == nil {
		t.Fatalf("the grantee's manage_resource create under the granted folder stored nothing: %v", out)
	}

	for _, outside := range []string{sibling, "e2684-elsewhere", root + "-old"} {
		if status, _ := upload2684(t, owner, outside, "outside"); status != http.StatusForbidden {
			t.Errorf("an upload to %s answered %d, not 403", outside, status)
		}
	}
	res, text, _ := owner.callRaw("manage_resource", map[string]any{
		"action": "create", "scope": "global", "path": sibling, "filename": "x.md",
		"display_name": "acc2684 outside " + unique1568(), "description": "Acceptance #2684.", "content": "x", "content_type": "text/markdown",
	})
	if !res.IsError || !strings.Contains(text, root) {
		t.Errorf("a manage_resource create outside the grant was not refused naming the folder the caller can write: %s", text)
	}

	if status, _ := owner.rest(http.MethodPost, "/api/v1/resources/folders", jsonBody(t, map[string]any{"scope": "global", "path": root + "/q3/drafts"})); status != http.StatusCreated && status != http.StatusOK {
		t.Errorf("the grantee making a folder inside the grant answered %d", status)
	}
	if status, _ := owner.rest(http.MethodPost, "/api/v1/resources/folders/move", jsonBody(t, map[string]any{"scope": "global", "from": root, "to": root + "-renamed"})); status != http.StatusForbidden {
		t.Errorf("the grantee renaming the granted folder answered %d, not 403", status)
	}
	if status, _ := owner.rest(http.MethodPost, "/api/v1/resources/folders/move", jsonBody(t, map[string]any{"scope": "global", "from": root + "/q3", "to": sibling + "/q3"})); status != http.StatusForbidden {
		t.Errorf("the grantee moving a folder out of the grant answered %d, not 403", status)
	}
	if status, body := owner.rest(http.MethodPost, "/api/v1/resources/folders/move", jsonBody(t, map[string]any{"scope": "global", "from": root + "/q3", "to": root + "/2026/q3"})); status != http.StatusOK {
		t.Errorf("the grantee moving a folder inside the grant answered %d %v", status, body)
	}
	if status, _ := owner.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody); status != http.StatusOK && status != http.StatusNoContent {
		t.Errorf("the grantee deleting a file inside the grant answered %d", status)
	}

	status, mine := owner.rest(http.MethodGet, "/api/v1/resources/folder-grants/mine", http.NoBody)
	if status != http.StatusOK || !strings.Contains(fmt.Sprint(mine["folders"]), root) {
		t.Errorf("the grantee's own folders: %d %v", status, mine)
	}
	if status, _ := owner.rest(http.MethodGet, grants2684+"?folder="+url.QueryEscape(root), http.NoBody); status != http.StatusForbidden && status != http.StatusUnauthorized {
		t.Errorf("a grantee reading who holds grants answered %d, not refused", status)
	}
}

func TestIssue2684_TheGrantFollowsARenameAndEndsWhenRemoved(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	root := "f2684-" + unique1568()
	folder2684(t, admin, root)
	id := grant2684(t, admin, root, devOwnerEmail, "")

	renamed := root + "-renamed"
	if status, body := admin.rest(http.MethodPost, "/api/v1/resources/folders/move", jsonBody(t, map[string]any{"scope": "global", "from": root, "to": renamed})); status != http.StatusOK {
		t.Fatalf("the administrator renaming the folder answered %d %v", status, body)
	}
	if status, _ := upload2684(t, owner, renamed, "after-rename"); status != http.StatusCreated && status != http.StatusOK {
		t.Errorf("the grantee's upload under the renamed folder answered %d", status)
	}
	if status, _ := upload2684(t, owner, root, "old-name"); status != http.StatusForbidden {
		t.Errorf("an upload under the folder's old name answered %d, not 403", status)
	}
	status, listed := admin.rest(http.MethodGet, grants2684+"?folder="+url.QueryEscape(renamed), http.NoBody)
	if status != http.StatusOK || !strings.Contains(fmt.Sprint(listed["grants"]), devOwnerEmail) {
		t.Errorf("the renamed folder's grants: %d %v", status, listed)
	}

	if status, _ := admin.rest(http.MethodDelete, grants2684+"/"+id, http.NoBody); status != http.StatusOK {
		t.Fatalf("removing the grant answered %d", status)
	}
	if status, _ := upload2684(t, owner, renamed, "after-revoke"); status != http.StatusForbidden {
		t.Errorf("an upload after the grant was removed answered %d, not 403", status)
	}
}

func TestIssue2684_APersonaGrantAndAnExternalPersona(t *testing.T) {
	admin := connect(t)
	peer := connectAs(t, devPeerAPIKey)
	root := "p2684-" + unique1568()
	folder2684(t, admin, root)
	grant2684(t, admin, root, "", "collaborator")
	if status, _ := upload2684(t, peer, root, "member"); status != http.StatusCreated && status != http.StatusOK {
		t.Errorf("a member of the granted persona uploading answered %d", status)
	}
	status, out := admin.rest(http.MethodPost, grants2684, jsonBody(t, map[string]any{"folder": root, "persona": "client"}))
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out), "external") {
		t.Errorf("a grant to an external persona answered %d %v, not a refusal naming it external", status, out)
	}
}
