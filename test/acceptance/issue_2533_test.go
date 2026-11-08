//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2533: on an S3 connection configured read_only, s3_object refused put,
// copy and delete but still answered presign method=PUT with an upload URL that
// wrote the object, so a caller the flag was meant to hold back could overwrite
// any key the connection's credentials reach. A PUT presign is refused there
// now, with the refusal put gets, while a GET presign is still issued. A
// managed script's draft run treats a PUT presign as the write it is.
//
// Every criterion runs against the dev stack (make dev), whose SeaweedFS backs
// dev-s3 (writable) and dev-s3-readonly (the same store, read-only).
//
// Wire forms: s3_object's method is a string whose schema admits "", GET, PUT,
// get and put; the refusal is checked for PUT and put, the issued link for GET,
// get and the method omitted. expires_in is sent as a JSON number.
// manage_script's command, name, description and source are strings, and
// allow_writes is omitted, the default a draft is refused under.

const issue2533Purpose = "Acceptance for #2533: a read-only S3 connection issues no upload link."

// TestIssue2533_ReadOnlyConnectionRefusesAnUploadLink: presign method=PUT on
// the read-only connection is refused naming the connection, in both cases,
// returns no URL, and writes nothing to the key.
func TestIssue2533_ReadOnlyConnectionRefusesAnUploadLink(t *testing.T) {
	c := connect(t)
	requireS3(t, c)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	existing := "acceptance-2533/" + stamp + "/backup.dump"
	c.putText(issue1591Writable, existing, "the backup")
	fresh := "acceptance-2533/" + stamp + "/never-written.txt"

	for _, method := range []string{"PUT", "put"} {
		for _, key := range []string{existing, fresh} {
			refusal := c.objectErr("presign", issue1591ReadOnly, key, map[string]any{"method": method, "expires_in": 120})
			want := fmt.Sprintf(`connection "%s" is read-only: s3_object action "presign" with method PUT is not permitted`, issue1591ReadOnly)
			if !strings.Contains(refusal, want) {
				t.Errorf("presign method=%s on the read-only connection: %q; want %q", method, refusal, want)
			}
			if strings.Contains(refusal, "X-Amz-") || strings.Contains(refusal, "http") {
				t.Errorf("the refusal carries a URL: %q", refusal)
			}
		}
	}
	if got := c.object("get", issue1591Writable, existing, nil)["content"]; got != "the backup" {
		t.Errorf("the existing object changed: %v", got)
	}
	if refusal := c.objectErr("metadata", issue1591Writable, fresh, nil); !strings.Contains(refusal, "failed to get object metadata") {
		t.Errorf("a key no link was issued for exists: %s", refusal)
	}
}

// TestIssue2533_ReadOnlyConnectionStillIssuesADownloadLink: a GET presign on
// the read-only connection is issued, in each form, and serves the object.
func TestIssue2533_ReadOnlyConnectionStillIssuesADownloadLink(t *testing.T) {
	c := connect(t)
	requireS3(t, c)
	key := fmt.Sprintf("acceptance-2533/%d/report.txt", time.Now().UnixNano())
	c.putText(issue1591Writable, key, "quarterly report")

	for _, extra := range []map[string]any{{"method": "GET"}, {"method": "get"}, {}} {
		extra["expires_in"] = 120
		link := c.object("presign", issue1591ReadOnly, key, extra)
		if link["method"] != "GET" {
			t.Fatalf("presign %v: %v", extra, link)
		}
		if body := httpBody(t, http.MethodGet, link["url"].(string), nil); body != "quarterly report" {
			t.Errorf("the download link (%v) serves %q", extra, body)
		}
	}
}

// TestIssue2533_ListConnectionsSaysAnUploadLinkIsAWrite: list_connections'
// description, which already says what read_only means, names the upload link.
func TestIssue2533_ListConnectionsSaysAnUploadLinkIsAWrite(t *testing.T) {
	c := connect(t)
	tools, err := c.session.ListTools(c.ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	descriptions := map[string]string{}
	for _, tool := range tools.Tools {
		descriptions[tool.Name] = tool.Description
	}
	if d := descriptions["list_connections"]; !strings.Contains(d, "an S3 upload link (s3_object presign with method PUT)") {
		t.Errorf("list_connections does not name the upload link: %q", d)
	}
	if d := descriptions[issue1591ObjectTool]; !strings.Contains(d, "presign with method PUT are refused on a read-only connection") {
		t.Errorf("s3_object does not say a PUT presign is refused on a read-only connection: %q", d)
	}
}

// draftPresignsAnUpload2533 asks for an upload link on the writable
// connection, so what refuses it in a draft is the write barrier, not the
// connection.
const draftPresignsAnUpload2533 = `
def main():
    """Asks for an upload link to one key and prints its method."""
    link = platform.call("s3_object", {
        "action": "presign",
        "method": "PUT",
        "connection": "dev-s3",
        "bucket": "portal-assets",
        "key": "acceptance-2533/draft-upload.txt",
        "expires_in": 60,
        "purpose": "Acceptance #2533: an upload link is a write.",
    })
    print("method=" + str(link.get("method", "")))
`

// TestIssue2533_ADraftRunIsRefusedAnUploadLink: a draft run without
// allow_writes stops at a PUT presign and names it as the write.
func TestIssue2533_ADraftRunIsRefusedAnUploadLink(t *testing.T) {
	c := connect(t)
	requireS3(t, c)
	const name = "acceptance-2533-draft-upload"
	_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	created := c.saveScript(map[string]any{
		"command":     "create",
		"name":        name,
		"description": "Acceptance #2533: a draft does not mint an upload link.",
		"source":      draftPresignsAnUpload2533,
	}, nil)
	if status, _ := created["status"].(string); status == "invalid" {
		t.Fatalf("manage_script create refused %q: %v", name, created["findings"])
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name})
	})

	ran := c.call("manage_script", map[string]any{"command": "run_draft", "name": name})
	if status, _ := ran["status"].(string); status != "failed" {
		t.Fatalf("a draft asking for an upload link must stop there, got %v: %v", ran["status"], ran)
	}
	refused, _ := ran["refused_write"].(map[string]any)
	if got, _ := refused["call"].(string); got != "s3_object action=presign method=PUT" {
		t.Errorf("refused_write.call = %q, want s3_object action=presign method=PUT (%v)", got, ran)
	}
	if log, _ := ran["log"].(string); strings.Contains(log, "method=PUT") {
		t.Errorf("the draft was handed an upload link: %q", log)
	}
}
