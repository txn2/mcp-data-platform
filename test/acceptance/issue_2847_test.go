//go:build integration

package acceptance

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2847: an agent hands a file in Resources to a connection
// that takes a file by URL. manage_resource action=download_link answers a
// URL on the platform's public address that anybody holding it downloads the
// file from with no sign-in: a single download (the default), which ends after
// its first full download or an hour, or a link that works for six hours. A
// HEAD request or a byte-range read does not spend a single download. The
// portal's Copy link makes the same links.
//
// Wire forms: manage_resource takes action, reference, path, filename,
// content and expires as strings (expires is an enum of two), sent as literal
// tools/call params; the portal route is
// GET /api/v1/resources/{id}/download-link with expires as a query parameter;
// the link is fetched with plain GET and HEAD requests carrying no credential,
// as a connection's upstream fetches it. (The dev stack's api-test fixture has
// no endpoint that downloads a URL, so it cannot stand in for that upstream.)

// issue2847File files a PDF-shaped file in the caller's library and returns
// its reference and bytes.
func issue2847File(t *testing.T, c *client) (ref, folder, body string) {
	t.Helper()
	folder = "acc-2847/" + unique1579()
	body = "%PDF-1.7\n% acceptance 2847\n"
	out := c.call("manage_resource", map[string]any{
		"action": "create", "path": folder, "filename": "invoice.pdf", "display_name": "Acceptance 2847",
		"description": "A file handed to a connection by URL (#2847).", "content": body, "content_type": "application/pdf",
	})
	ref, _ = out["reference"].(string)
	if ref == "" {
		t.Fatalf("create: %v", out)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_resource", map[string]any{"action": "delete", "reference": ref}) })
	return ref, folder, body
}

// fetch2847 GETs link with no credential.
func fetch2847(t *testing.T, link string) (int, http.Header, string) {
	t.Helper()
	return request2847(t, http.MethodGet, link, "")
}

// request2847 sends method to link with no credential, and a Range header
// when rangeHeader is set.
func request2847(t *testing.T, method, link, rangeHeader string) (int, http.Header, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, link, http.NoBody) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatal(err)
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatalf("GET the download link: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // read below
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, string(raw)
}

// TestIssue2847_ASingleDownload: the default link, made by reference, ends in
// the file's name and expires within the hour; a HEAD request and a byte-range
// read pass without spending it; the first full GET with no credential
// downloads the file's bytes as a download; a second answers 403.
func TestIssue2847_ASingleDownload(t *testing.T) {
	c := connect(t)
	ref, _, body := issue2847File(t, c)
	out := c.call("manage_resource", map[string]any{"action": "download_link", "reference": ref})
	link, _ := out["url"].(string)
	if !strings.HasSuffix(link, "/invoice.pdf") || out["reference"] != ref || out["expires"] != "single_download" {
		t.Fatalf("download_link answered %v", out)
	}
	expires, err := time.Parse(time.RFC3339, fmt.Sprint(out["expires_at"]))
	if err != nil || time.Until(expires) < 55*time.Minute || time.Until(expires) > 61*time.Minute {
		t.Errorf("a single download expires at %v (%v), not an hour from now", out["expires_at"], err)
	}
	if status, _, _ := request2847(t, http.MethodHead, link, ""); status != http.StatusOK {
		t.Errorf("a HEAD request answered %d", status)
	}
	if status, _, got := request2847(t, http.MethodGet, link, "bytes=0-3"); status != http.StatusPartialContent || got != body[:4] {
		t.Errorf("a byte-range read answered %d %q", status, got)
	}
	status, header, got := fetch2847(t, link)
	if status != http.StatusOK || got != body {
		t.Fatalf("the full download answered %d %q", status, got)
	}
	if !strings.Contains(header.Get("Content-Disposition"), "attachment") {
		t.Errorf("served as %q, not a download", header.Get("Content-Disposition"))
	}
	if status, _, _ = fetch2847(t, link); status != http.StatusForbidden {
		t.Errorf("a second full download answered %d", status)
	}
}

// TestIssue2847_ASixHourLink: a link made by address with expires=6h downloads
// the file every time and expires six hours out; a kind there is none of is
// refused.
func TestIssue2847_ASixHourLink(t *testing.T) {
	c := connect(t)
	ref, folder, body := issue2847File(t, c)
	out := c.call("manage_resource", map[string]any{"action": "download_link", "path": folder, "filename": "invoice.pdf", "expires": "6h"})
	link, _ := out["url"].(string)
	if out["reference"] != ref || out["expires"] != "6h" {
		t.Fatalf("download_link answered %v", out)
	}
	expires, err := time.Parse(time.RFC3339, fmt.Sprint(out["expires_at"]))
	if err != nil || time.Until(expires) < 355*time.Minute || time.Until(expires) > 361*time.Minute {
		t.Errorf("a six-hour link expires at %v (%v)", out["expires_at"], err)
	}
	for i := range 3 {
		if status, _, got := fetch2847(t, link); status != http.StatusOK || got != body {
			t.Fatalf("download %d answered %d %q", i+1, status, got)
		}
	}
	res, text, err := c.callRaw("manage_resource", map[string]any{"action": "download_link", "reference": ref, "expires": "2h"})
	if err != nil || res == nil || !res.IsError {
		t.Errorf("expires=2h: %v %s", err, text)
	}
}

// TestIssue2847_ALinkIsBoundToItsFile: an altered token answers 403, a name
// that is not the file's 404, and a person who cannot read the file is
// refused a link.
func TestIssue2847_ALinkIsBoundToItsFile(t *testing.T) {
	c := connect(t)
	ref, _, _ := issue2847File(t, c)
	link := c.call("manage_resource", map[string]any{"action": "download_link", "reference": ref, "expires": "6h"})["url"].(string)
	i := strings.LastIndex(link, "/")
	tok := link[:i]
	if status, _, _ := fetch2847(t, tok[:len(tok)-3]+"AAA"+link[i:]); status != http.StatusForbidden {
		t.Errorf("an altered link answered %d", status)
	}
	if status, _, _ := fetch2847(t, tok+"/other.pdf"); status != http.StatusNotFound {
		t.Errorf("a link naming another file answered %d", status)
	}
	analyst := connectAs(t, "acme-analyst-key")
	if res, text, err := analyst.callRaw("manage_resource", map[string]any{"action": "download_link", "reference": ref}); err != nil || res == nil || !res.IsError {
		t.Errorf("a person who cannot read the file got a link: %v %s", err, text)
	}
}

// TestIssue2847_CopyLinkInThePortal: the portal route makes each kind of link
// for a file the caller can read, and refuses a kind there is none of.
func TestIssue2847_CopyLinkInThePortal(t *testing.T) {
	c := connect(t)
	ref, _, body := issue2847File(t, c)
	id := strings.TrimPrefix(ref, "mcp:resource:")
	status, out := c.rest(http.MethodGet, "/api/v1/resources/"+id+"/download-link?expires=6h", http.NoBody)
	if status != http.StatusOK || out["expires"] != "6h" {
		t.Fatalf("Copy link for six hours answered %d: %v", status, out)
	}
	if status, _, got := fetch2847(t, fmt.Sprint(out["url"])); status != http.StatusOK || got != body {
		t.Errorf("the portal's link answered %d %q", status, got)
	}
	status, out = c.rest(http.MethodGet, "/api/v1/resources/"+id+"/download-link", http.NoBody)
	if status != http.StatusOK || out["expires"] != "single_download" {
		t.Fatalf("Copy link with no kind answered %d: %v", status, out)
	}
	if status, _ = c.rest(http.MethodGet, "/api/v1/resources/"+id+"/download-link?expires=2h", http.NoBody); status != http.StatusBadRequest {
		t.Errorf("expires=2h answered %d", status)
	}
}
