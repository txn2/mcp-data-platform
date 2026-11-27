//go:build integration

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2844: an external service delivers its output into a
// person's Resources folder through an upload link. A link lives up to a day,
// takes several files each named in the URL, and a PUT that files nothing can
// be sent again; the person makes, sees and revokes links on the Resources
// page; and a saved script's run can ask for one to hand a service.
//
// Wire forms: manage_resource takes action, path, content_type and scope as
// strings and size_bytes, max_files and ttl_seconds as JSON numbers, one form
// each (the schema types them integer), sent as literal tools/call params;
// each PUT carries a file as its raw body with a Content-Length; the portal
// routes take one JSON object (POST /api/v1/resources/upload-links) or none
// (GET /api/v1/resources/upload-links, POST .../{id}/revoke).

// TestIssue2844_ALinkTakesSeveralFilesNamedInTheURL: a link for three files
// with no filename lives a day, files each PUT to upload_url/<name> in the
// folder, says how many more it takes, and refuses a fourth.
func TestIssue2844_ALinkTakesSeveralFilesNamedInTheURL(t *testing.T) {
	c := connect(t)
	folder := "acc-2844/" + unique1579()
	out := c.call("manage_resource", map[string]any{
		"action": "upload_link", "path": folder, "content_type": "text/csv",
		"size_bytes": 1000, "max_files": 3, "ttl_seconds": 86400,
	})
	link, _ := out["upload_url"].(string)
	if out["name_in_url"] != true || out["max_files"] != float64(3) || link == "" {
		t.Fatalf("upload_link: %v", out)
	}
	expires, err := time.Parse(time.RFC3339, fmt.Sprint(out["expires_at"]))
	if err != nil || time.Until(expires) < 23*time.Hour {
		t.Fatalf("a link asked to live a day expires at %v (%v)", out["expires_at"], err)
	}
	body := []byte("day,high\nmon,71\n")
	for i, want := range []float64{2, 1, 0} {
		name := fmt.Sprintf("part-%d.csv", i+1)
		status, got := put2639(t, link+"/"+name, bytes.NewReader(body), int64(len(body)))
		if status != http.StatusCreated || got["filename"] != name || got["files_remaining"] != want {
			t.Fatalf("PUT %s answered %d: %v", name, status, got["_raw"])
		}
		ref, _ := got["reference"].(string)
		t.Cleanup(func() { _, _, _ = c.callRaw("manage_resource", map[string]any{"action": "delete", "reference": ref}) })
	}
	if status, _ := put2639(t, link+"/part-4.csv", bytes.NewReader(body), int64(len(body))); status != http.StatusNotFound {
		t.Errorf("a fourth PUT to a link for three answered %d, not 404", status)
	}
	if status, got := put2639(t, link, bytes.NewReader(body), int64(len(body))); status != http.StatusNotFound && status != http.StatusBadRequest {
		t.Errorf("a PUT with no name answered %d: %v", status, got["_raw"])
	}
	listed := c.call("manage_resource", map[string]any{"action": "list", "path": folder})
	if raw, _ := json.Marshal(listed); !strings.Contains(string(raw), "part-1.csv") || !strings.Contains(string(raw), "part-3.csv") {
		t.Errorf("the folder does not hold the files: %s", raw)
	}
}

// TestIssue2844_AFailedPutCanBeSentAgain: a PUT past the size files nothing
// and keeps the link's one place, so the same file sent at its size is filed.
func TestIssue2844_AFailedPutCanBeSentAgain(t *testing.T) {
	c := connect(t)
	folder := "acc-2844/" + unique1579()
	link := c.call("manage_resource", map[string]any{
		"action": "upload_link", "path": folder, "filename": "retry.csv", "content_type": "text/csv", "size_bytes": 20,
	})["upload_url"].(string)
	big := bytes.Repeat([]byte("x"), 40)
	if status, got := put2639(t, link, bytes.NewReader(big), int64(len(big))); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("a PUT past the size answered %d: %v", status, got["_raw"])
	}
	small := []byte("day,high\nmon,71\n")
	status, got := put2639(t, link, bytes.NewReader(small), int64(len(small)))
	if status != http.StatusCreated {
		t.Fatalf("the PUT sent again answered %d: %v", status, got["_raw"])
	}
	ref, _ := got["reference"].(string)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_resource", map[string]any{"action": "delete", "reference": ref}) })
	if status, _ = put2639(t, link, bytes.NewReader(small), int64(len(small))); status != http.StatusNotFound {
		t.Errorf("a PUT after the link's one file answered %d", status)
	}
}

// TestIssue2844_ThePortalMakesListsAndRevokesALink: the Resources page's
// routes make a link as the caller, list it without its token, and revoke
// it, after which a PUT answers 404 and the list no longer has it.
func TestIssue2844_ThePortalMakesListsAndRevokesALink(t *testing.T) {
	c := connect(t)
	folder := "acc-2844/" + unique1579()
	status, made := c.rest(http.MethodPost, "/api/v1/resources/upload-links", jsonBody(t, map[string]any{
		"path": folder, "content_type": "text/csv", "size_bytes": 1000, "max_files": 2, "ttl_seconds": 7200,
	}))
	if status != http.StatusCreated {
		t.Fatalf("making a link answered %d: %v", status, made)
	}
	id, _ := made["id"].(string)
	link, _ := made["upload_url"].(string)
	token := link[strings.LastIndex(link, "/")+1:]

	_, listed := c.rest(http.MethodGet, "/api/v1/resources/upload-links", http.NoBody)
	raw, _ := json.Marshal(listed)
	if !strings.Contains(string(raw), id) || strings.Contains(string(raw), token) {
		t.Fatalf("the list holds %s; want the link %s and never its token", raw, id)
	}
	if status, _ = c.rest(http.MethodPost, "/api/v1/resources/upload-links/"+id+"/revoke", http.NoBody); status != http.StatusNoContent {
		t.Fatalf("revoking answered %d", status)
	}
	body := []byte("a,b\n1,2\n")
	if status, _ = put2639(t, link+"/late.csv", bytes.NewReader(body), int64(len(body))); status != http.StatusNotFound {
		t.Errorf("a PUT to a revoked link answered %d", status)
	}
	_, listed = c.rest(http.MethodGet, "/api/v1/resources/upload-links", http.NoBody)
	if raw, _ = json.Marshal(listed); strings.Contains(string(raw), id) {
		t.Errorf("a revoked link is still listed: %s", raw)
	}
	other := connectAs(t, "acme-analyst-key")
	if status, _ = other.rest(http.MethodPost, "/api/v1/resources/upload-links/"+id+"/revoke", http.NoBody); status != http.StatusNotFound {
		t.Errorf("another person revoking the link answered %d", status)
	}
}

// TestIssue2844_APortalLinkForOneNamedFile: a link the portal makes for one
// named file, with no display name (the portal sends none), files the PUT
// under that name, named for itself.
func TestIssue2844_APortalLinkForOneNamedFile(t *testing.T) {
	c := connect(t)
	folder := "acc-2844/" + unique1579()
	status, made := c.rest(http.MethodPost, "/api/v1/resources/upload-links", jsonBody(t, map[string]any{
		"path": folder, "filename": "weekly.csv", "content_type": "text/csv", "size_bytes": 1000, "max_files": 1, "ttl_seconds": 900,
	}))
	if status != http.StatusCreated || made["name_in_url"] == true {
		t.Fatalf("making a link for one named file answered %d: %v", status, made)
	}
	body := []byte("day,high\nmon,71\n")
	status, got := put2639(t, fmt.Sprint(made["upload_url"]), bytes.NewReader(body), int64(len(body)))
	if status != http.StatusCreated || got["filename"] != "weekly.csv" {
		t.Fatalf("the PUT answered %d: %v", status, got["_raw"])
	}
	ref, _ := got["reference"].(string)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_resource", map[string]any{"action": "delete", "reference": ref}) })
	found := c.call("manage_resource", map[string]any{"action": "get", "reference": ref})
	if raw, _ := json.Marshal(found); !strings.Contains(string(raw), `"display_name":"weekly.csv"`) {
		t.Errorf("the file is not named for itself: %s", raw)
	}
}

// issue2844Script asks for a link a service can PUT one file to, and prints
// it.
const issue2844Script = `
def main():
    """Asks for an upload link to hand a service, and prints it."""
    out = platform.call("manage_resource", {
        "action": "upload_link",
        "path": "acc-2844-script",
        "filename": "from-service.csv",
        "content_type": "text/csv",
        "size_bytes": 1000,
        "ttl_seconds": 3600,
    })
    print("link=" + out["upload_url"])
`

// TestIssue2844_AScriptRunCanAskForALink: a saved script's run gets a link
// rather than a refusal, and a file PUT to it from outside is filed.
func TestIssue2844_AScriptRunCanAskForALink(t *testing.T) {
	c := connect(t)
	const name = "acceptance-2844-link"
	_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	created := c.saveScript(map[string]any{
		"command": "create", "name": name, "source": issue2844Script,
		"description": "Acceptance #2844: a run asks for an upload link to hand a service.",
	}, nil)
	if status, _ := created["status"].(string); status == "invalid" {
		t.Fatalf("manage_script create refused the script: %v", created["findings"])
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	ran := c.call("manage_script", map[string]any{"command": "run_draft", "name": name, "allow_writes": true})
	raw, _ := json.Marshal(ran)
	if ran["status"] != "succeeded" {
		t.Fatalf("the run did not succeed: %s", raw)
	}
	m := regexp.MustCompile(`link=(https?://[^"\\\s]+)`).FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatalf("the run printed no link: %s", raw)
	}
	body := []byte("a,b\n1,2\n")
	status, got := put2639(t, m[1], bytes.NewReader(body), int64(len(body)))
	if status != http.StatusCreated {
		t.Fatalf("a PUT to the run's link answered %d: %v", status, got["_raw"])
	}
	ref, _ := got["reference"].(string)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_resource", map[string]any{"action": "delete", "reference": ref}) })
}
