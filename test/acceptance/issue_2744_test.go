//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2744: an external account saw no way to upload to My
// Resources and nothing said which setting enables it. A person of an external
// persona saved as "Read their own folder" (resources shared, uploads off)
// reads /me with uploads off, and an upload to their own folder is refused
// 403 naming their persona and the setting that turns uploads on, not as a
// CSRF failure; saved as "Read and upload to their own folder" (resources
// shared, uploads on) the same person's /me reads uploads on and the same
// upload is accepted into their own folder.
//
// Wire forms: the admin persona POST and PUT take portal as an object whose
// resources is a string and uploads a JSON boolean (sent false and true);
// the upload is the multipart form POST /api/v1/resources takes, with scope,
// scope_id, path and display_name as text fields and the file as a part.

// portal2744 reads the portal block a person's /me answers.
func portal2744(t *testing.T, s externalSession) map[string]any {
	t.Helper()
	status, me := s.do(t, http.MethodGet, "/api/v1/portal/me", nil)
	if status != http.StatusOK {
		t.Fatalf("/me answered %d", status)
	}
	p, _ := me["portal"].(map[string]any)
	return p
}

// setPortal2744 saves a persona's portal block as the persona editor's
// Resources row does: a level and whether its people upload.
func setPortal2744(t *testing.T, admin *client, p persona2290, uploads bool) {
	t.Helper()
	body := map[string]any{
		"name": p.name, "display_name": "Acceptance 2744", "roles": []any{p.role}, "external": true,
		"allow_tools": []any{"*"}, "allow_connections": []any{"*"},
		"portal": map[string]any{"resources": "shared", "uploads": uploads},
	}
	if status, out := admin.rest(http.MethodPut, "/api/v1/admin/personas/"+p.name, jsonBody(t, body)); status != http.StatusOK {
		t.Fatalf("saving %s with uploads %v: %d %v", p.name, uploads, status, out)
	}
}

// TestIssue2744_TheRefusalNamesTheSettingAndTurningItOnLetsThePersonUpload
// covers both choices of the Resources row on one person.
func TestIssue2744_TheRefusalNamesTheSettingAndTurningItOnLetsThePersonUpload(t *testing.T) {
	admin := connect(t)
	p := newPersona2290(t, admin, "client-2744", map[string]any{
		"external": true, "portal": map[string]any{"resources": "shared", "uploads": false},
	})
	s := newPerson2290(t, admin, p, "up2744")

	if got := portal2744(t, s); got["external"] != true || got["uploads"] != false || got["resources"] != "shared" {
		t.Fatalf("/me portal = %v, want an external person reading their own folder with uploads off", got)
	}
	status, out := issue2290Upload(t, s, "user", s.userID)
	refusal := fmt.Sprint(out)
	if status != http.StatusForbidden {
		t.Fatalf("an upload with uploads off answered %d %v, want 403", status, out)
	}
	if strings.Contains(refusal, "CSRF") || !strings.Contains(refusal, "uploads are not turned on for your persona ("+p.name+")") ||
		!strings.Contains(refusal, "Read and upload to their own folder") {
		t.Errorf("the refusal reads %q; it should name the persona and the setting that turns uploads on", refusal)
	}

	setPortal2744(t, admin, p, true)
	deadline := time.Now().Add(20 * time.Second)
	for portal2744(t, s)["uploads"] != true {
		if time.Now().After(deadline) {
			t.Fatalf("/me never read uploads on after the persona was saved: %v", portal2744(t, s))
		}
		time.Sleep(250 * time.Millisecond)
	}
	status, up := issue2290Upload(t, s, "user", s.userID)
	id, _ := up["id"].(string)
	if status != http.StatusCreated || id == "" {
		t.Fatalf("an upload with uploads on answered %d %v, want 201", status, up)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	if up["scope"] != "user" || up["scope_id"] != s.userID {
		t.Errorf("the upload landed in %v %v, want their own folder", up["scope"], up["scope_id"])
	}
}
