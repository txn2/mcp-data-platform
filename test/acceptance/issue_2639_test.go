//go:build integration

package acceptance

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/resourceupload"
)

// Acceptance for #2639: an agent with only its MCP session gets a file larger
// than a tool call into the platform. manage_resource action=upload_link
// answers a single-use URL on the platform's own origin; one PUT of the file
// from the shell files it as a managed resource at the address it was issued
// for, as the caller; the archive then extracts into a folder. The link cannot
// be used twice, past its expiry, past its size, or for another address, and
// its token reaches no audit row and no stored link row.
//
// Wire forms: manage_resource takes action, filename, path, display_name,
// description, content_type and scope as strings and size_bytes and
// ttl_seconds as JSON numbers, one form each, sent as literal tools/call
// params; the PUT carries the file as its raw body with a Content-Length.

const bigArchive2639 = 200 << 20

// link2639 asks for an upload link at path/filename.
func link2639(t *testing.T, c *client, path, filename, contentType string, size int64, extra map[string]any) map[string]any {
	t.Helper()
	args := map[string]any{
		"action": "upload_link", "filename": filename, "path": path, "content_type": contentType, "size_bytes": size,
		"display_name": "Acceptance 2639 " + filename, "description": "A file loaded through an upload link (#2639).",
	}
	for k, v := range extra {
		args[k] = v
	}
	out := c.call("manage_resource", args)
	if u, _ := out["upload_url"].(string); !strings.Contains(u, resourceupload.RoutePrefix) {
		t.Fatalf("upload_link answered no upload URL: %v", out)
	}
	return out
}

// put2639 PUTs body to the link, length known.
func put2639(t *testing.T, link string, body io.Reader, length int64) (int, map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, link, body) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = length
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test
	if err != nil {
		t.Fatalf("PUT to the upload link: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // read below
	raw, _ := io.ReadAll(res.Body)
	out := map[string]any{"_raw": string(raw)}
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

// archive2639 is a zip of n bytes or more: one member of random-looking
// bytes, which zip stores rather than shrinks.
func archive2639(t *testing.T, n int) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "media/clip.bin", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	x := uint32(2463534242)
	for written := 0; written < n; written += len(chunk) {
		for i := range chunk {
			x ^= x << 13
			x ^= x >> 17
			x ^= x << 5
			chunk[i] = byte(x)
		}
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestIssue2639_ALargeArchiveReachesAFolderWithNoPerson: a 200 MB archive goes
// through one PUT to a link and lands as a managed resource at the address
// asked for; extract writes its member into a folder.
func TestIssue2639_ALargeArchiveReachesAFolderWithNoPerson(t *testing.T) {
	c := connect(t)
	folder := "acc-2639/" + unique1579()
	body := archive2639(t, bigArchive2639)
	link := link2639(t, c, folder, "media.zip", "application/zip", int64(len(body)), nil)
	status, got := put2639(t, link["upload_url"].(string), bytes.NewReader(body), int64(len(body)))
	if status != http.StatusCreated {
		t.Fatalf("the PUT answered %d: %v", status, got["_raw"])
	}
	ref, _ := got["reference"].(string)
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_resource", map[string]any{"action": "delete", "reference": ref, "force": true})
	})
	if size, _ := got["size_bytes"].(float64); int(size) != len(body) {
		t.Errorf("the resource holds %v bytes, not the %d sent", got["size_bytes"], len(body))
	}
	found := c.call("manage_resource", map[string]any{"action": "get", "reference": ref})
	if res, _ := found["resource"].(map[string]any); res["path"] != folder || res["filename"] != "media.zip" {
		t.Errorf("the archive is not at the address the link was issued for: %v", found)
	}
	extracted := c.call("manage_resource", map[string]any{"action": "extract", "reference": ref, "path": folder + "/out"})
	if !strings.Contains(fmt.Sprint(extracted), "clip.bin") {
		t.Errorf("the uploaded archive did not extract into the folder: %v", extracted)
	}
}

// TestIssue2639_ALinkIsOneWriteOfItsSizeBeforeItExpires: a second PUT, a PUT
// past the size, and a PUT after expiry are refused; the link writes only the
// address it was issued for.
func TestIssue2639_ALinkIsOneWriteOfItsSizeBeforeItExpires(t *testing.T) {
	c := connect(t)
	folder := "acc-2639/" + unique1579()
	small := []byte("day,high\nmon,71\n")
	link := link2639(t, c, folder, "weather.csv", "text/csv", int64(len(small)), nil)["upload_url"].(string)
	status, got := put2639(t, link, bytes.NewReader(small), int64(len(small)))
	if status != http.StatusCreated {
		t.Fatalf("the first PUT answered %d: %v", status, got["_raw"])
	}
	ref, _ := got["reference"].(string)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_resource", map[string]any{"action": "delete", "reference": ref}) })
	if status, _ = put2639(t, link, bytes.NewReader(small), int64(len(small))); status != http.StatusNotFound {
		t.Errorf("a second PUT to one link answered %d, not 404", status)
	}

	tooBig := link2639(t, c, folder, "big.csv", "text/csv", 8, nil)["upload_url"].(string)
	if status, got = put2639(t, tooBig, bytes.NewReader(small), int64(len(small))); status != http.StatusRequestEntityTooLarge {
		t.Errorf("a PUT past the link's size answered %d: %v", status, got["_raw"])
	}
	if found := c.call("manage_resource", map[string]any{"action": "get", "path": folder, "filename": "big.csv"}); found["found"] != false {
		t.Errorf("a refused PUT filed something: %v", found)
	}

	expiring := link2639(t, c, folder, "late.csv", "text/csv", 100, map[string]any{"ttl_seconds": 1})["upload_url"].(string)
	time.Sleep(1500 * time.Millisecond)
	if status, _ = put2639(t, expiring, bytes.NewReader(small), int64(len(small))); status != http.StatusNotFound {
		t.Errorf("a PUT after the link expired answered %d", status)
	}

	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	other := link2639(t, c, folder, "other.csv", "text/csv", 100, nil)["upload_url"].(string)
	if !strings.HasPrefix(other, u.Scheme+"://"+u.Host+resourceupload.RoutePrefix) || other == link {
		t.Errorf("links are not each their own on the platform's origin: %s %s", link, other)
	}
}

// TestIssue2639_TheTokenIsStoredNowhere: the link's token, as written,
// base64-encoded and URL-escaped, appears in no audit row and no stored link
// row; the link is kept by the token's SHA-256 alone.
func TestIssue2639_TheTokenIsStoredNowhere(t *testing.T) {
	c := connect(t)
	link := link2639(t, c, "acc-2639/"+unique1579(), "secret.csv", "text/csv", 100, nil)["upload_url"].(string)
	token := link[strings.LastIndex(link, "/")+1:]
	db := devDB(t)
	forms := map[string]string{
		"as written": token, "base64": base64.StdEncoding.EncodeToString([]byte(token)), "url-escaped": url.PathEscape(token),
	}
	for form, v := range forms {
		for _, q := range []string{
			`SELECT count(*) FROM audit_logs WHERE parameters::text LIKE '%' || $1 || '%' OR COALESCE(error_message, '') LIKE '%' || $1 || '%'`,
			`SELECT count(*) FROM resource_upload_links WHERE row_to_json(resource_upload_links)::text LIKE '%' || $1 || '%'`,
		} {
			var n int
			if err := db.QueryRowContext(t.Context(), q, v).Scan(&n); err != nil {
				t.Fatalf("searching for the token: %v", err)
			}
			if n != 0 {
				t.Errorf("the token (%s) is stored: %s", form, q)
			}
		}
	}
	var kept int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM resource_upload_links WHERE token_sha256 = $1`, resourceupload.Hash(token)).Scan(&kept); err != nil || kept != 1 {
		t.Errorf("the link is not kept by its token's hash: %d %v", kept, err)
	}
}
