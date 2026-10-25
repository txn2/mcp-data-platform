//go:build integration

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2258: managed resources are shared through portal_shares like assets,
// and an external persona can let its people upload to their own folder,
// which #2290 leaves shared with no one on arrival.
//
// Wire forms: the share body as a JSON object with shared_with_email or
// shared_with_persona strings and permission as "viewer" and "editor"; the
// external persona's portal.uploads as the boolean true, and absent; the
// upload as a multipart form with the file part last, sent with an API key and
// with an identity-provider session cookie and CSRF token.

// issue2258Share shares a file as c.
func issue2258Share(t *testing.T, c *client, id string, body map[string]any) (int, map[string]any) {
	t.Helper()
	return c.rest(http.MethodPost, "/api/v1/resources/"+id+"/shares", jsonBody(t, body))
}

// issue2258SessionUpload uploads a CSV as the signed-in session to its own
// folder.
func issue2258SessionUpload(t *testing.T, s externalSession, label string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	name := fmt.Sprintf("acceptance-2258-%s-%d", label, time.Now().UnixNano())
	_, me := s.do(t, http.MethodGet, "/api/v1/portal/me", nil)
	own, _ := me["user_id"].(string)
	for k, v := range map[string]string{
		"scope": "user", "scope_id": own, "path": "deliveries", "display_name": name, "description": "Acceptance #2258: a delivery.",
	} {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	part, err := filePart1631(w, name+".csv", "text/csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("sku,units\nA1,3\nB2,5\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL()+"/api/v1/resources", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-CSRF-Token", s.csrf)
	res, err := s.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if id, _ := out["id"].(string); id != "" {
		admin := connect(t)
		t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	}
	return res.StatusCode, out
}

// issue2258Shared lists what is shared with the caller.
func issue2258Shared(t *testing.T, get func(path string) (int, map[string]any)) string {
	t.Helper()
	status, out := get("/api/v1/resources/shared-with-me")
	if status != http.StatusOK {
		t.Fatalf("shared-with-me: %d %v", status, out)
	}
	return stringOf(out)
}

// A person shares a file they uploaded with another by address: the recipient
// reads it and its content and finds it shared with them; a third person
// gets 404; deleting the file removes its shares.
func TestIssue2258_ShareAFileWithAPerson(t *testing.T) {
	admin := connect(t)
	owner := newIssue2241Member(t, admin, "2258-own", nil)
	reader := newIssue2241Member(t, admin, "2258-read", nil)
	third := newIssue2241Member(t, admin, "2258-third", nil)
	id := issue2257Upload(t, owner.client, "user", "", "share")
	_, me := reader.client.rest(http.MethodGet, "/api/v1/portal/me", http.NoBody)
	readerEmail, _ := me["email"].(string)
	if readerEmail == "" {
		t.Fatalf("the reader has no address: %v", me)
	}
	if status, out := issue2258Share(t, owner.client, id, map[string]any{"shared_with_email": readerEmail, "permission": "viewer"}); status != http.StatusCreated {
		t.Fatalf("sharing: %d %v", status, out)
	}
	for _, path := range []string{"/api/v1/resources/" + id, "/api/v1/resources/" + id + "/content"} {
		if status, _ := rawGet(t, baseURL(), path, reader.key); status != http.StatusOK {
			t.Errorf("GET %s as the recipient: %d", path, status)
		}
		if status, _ := rawGet(t, baseURL(), path, third.key); status != http.StatusNotFound {
			t.Errorf("GET %s as a third person: %d, want 404", path, status)
		}
	}
	if listed := issue2258Shared(t, func(p string) (int, map[string]any) { return reader.client.rest(http.MethodGet, p, http.NoBody) }); !strings.Contains(listed, id) {
		t.Errorf("shared-with-me does not list the file: %s", listed)
	}

	if status, _ := owner.client.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody); status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("deleting the file answered %d", status)
	}
	var n int
	if err := issue2257DB(t).QueryRowContext(t.Context(), `SELECT count(*) FROM portal_shares WHERE resource_id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d shares outlived the file", n)
	}
}

// A person of an external persona given an editor share opens the file as a
// viewer: a change is refused.
func TestIssue2258_AnExternalRecipientIsAViewer(t *testing.T) {
	admin := connect(t)
	_, email, s := issue2257ExternalAccount(t, admin, "2258-view", nil)
	id := issue2257Upload(t, admin, "user", "", "editor")
	if status, out := issue2258Share(t, admin, id, map[string]any{"shared_with_email": email, "permission": "editor"}); status != http.StatusCreated {
		t.Fatalf("sharing: %d %v", status, out)
	}
	if status, _ := s.do(t, http.MethodGet, "/api/v1/resources/"+id, nil); status != http.StatusOK {
		t.Fatalf("the external recipient cannot read the file (%d)", status)
	}
	if status, out := s.do(t, http.MethodPatch, "/api/v1/resources/"+id, map[string]any{"display_name": "changed"}); status != http.StatusForbidden {
		t.Errorf("an external recipient's change: %d %v, want 403", status, out)
	}
}

// A person whose persona withholds sharing is refused sharing a file.
func TestIssue2258_SharingOffRefusesSharingAFile(t *testing.T) {
	admin := connect(t)
	m := newIssue2241Member(t, admin, "2258-quiet", map[string]any{"allow_sharing": false})
	id := issue2257Upload(t, m.client, "user", "", "quiet")
	if status, out := issue2258Share(t, m.client, id, map[string]any{"shared_with_email": "someone@example.org"}); status != http.StatusForbidden {
		t.Fatalf("sharing with sharing off: %d %v, want 403", status, out)
	}
}

// An external persona that lets its people upload: its person uploads a CSV
// to their own folder, and it is shared with no one on arrival (#2290): a
// member of another persona gets 404 and the file has no share. The uploader
// lists their own uploads and what is shared with them, never Global.
func TestIssue2258_AnExternalUploadIsSharedWithNoOne(t *testing.T) {
	admin := connect(t)
	other := newIssue2241Member(t, admin, "2258-other", nil)
	globalID := issue2257Upload(t, admin, "global", "", "global")
	_, email, s := issue2257ExternalAccount(t, admin, "2258-up", map[string]any{"portal": map[string]any{
		"resources": "shared", "uploads": true,
	}})
	status, up := issue2258SessionUpload(t, s, "delivery")
	id, _ := up["id"].(string)
	if status != http.StatusCreated || id == "" {
		t.Fatalf("the external upload: %d %v", status, up)
	}
	if up["uploader_email"] != email {
		t.Errorf("uploader_email = %v, want %s", up["uploader_email"], email)
	}
	if status, _ := rawGet(t, baseURL(), "/api/v1/resources/"+id, other.key); status != http.StatusNotFound {
		t.Errorf("another persona's member: %d, want 404", status)
	}
	_, shares := admin.rest(http.MethodGet, "/api/v1/resources/"+id+"/shares", http.NoBody)
	if text := stringOf(shares); strings.Contains(text, "shared_with") {
		t.Errorf("the upload was shared on arrival: %v", shares)
	}

	status, list := s.do(t, http.MethodGet, "/api/v1/resources", nil)
	if status != http.StatusOK {
		t.Fatalf("the uploader's listing: %d %v", status, list)
	}
	text := stringOf(list)
	if !strings.Contains(text, id) || strings.Contains(text, globalID) {
		t.Errorf("the uploader's listing holds its upload %v and Global %v: %s", strings.Contains(text, id), strings.Contains(text, globalID), text)
	}
	issue2258Shared(t, func(p string) (int, map[string]any) { return s.do(t, http.MethodGet, p, nil) })
}

// The same account with uploads None is refused an upload.
func TestIssue2258_NoUploadsRefusesAnUpload(t *testing.T) {
	admin := connect(t)
	_, _, s := issue2257ExternalAccount(t, admin, "2258-none", map[string]any{"portal": map[string]any{"resources": "shared"}})
	if status, out := issue2258SessionUpload(t, s, "refused"); status != http.StatusForbidden {
		t.Fatalf("an upload with uploads None: %d %v, want 403", status, out)
	}
}
