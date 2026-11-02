//go:build integration

package acceptance

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Issue #2395: binary content the portal serves is read by range from object
// storage, never whole, on the reference route an HTML asset's mcp:// files
// are served through and on every content GET with or without a Range header.
// What is held here, through the surfaces a reader and an agent meet:
//
//   - an HTML asset referencing an uploaded video plays and seeks in the portal
//     and on a public share signed out, and the storage reads during playback
//     are range reads (storage_operations_total{operation="get_range"} moves,
//     {operation="get"} does not);
//   - downloading a large resource and a large asset without a Range header
//     does not grow the serving replica's memory by the file's size;
//   - a conditional GET carrying the ETag a response gave answers 304 on each
//     changed route;
//   - a text asset with references is still served rewritten.
//
// Wire forms: save_asset's `content` is a string (an HTML document) and
// `references` a JSON array of strings, sent as literal tools/call params;
// `name`, `content_type` and `purpose` are typed string and sent once each.
// trino_export's `sql`, `format`, `name` and `purpose` are strings.

const issue2395Purpose = "Acceptance for #2395: binary content read by range, never whole."

// issue2395Upload files a resource under a display name of its own and
// returns its id and mcp:// URI.
func issue2395Upload(t *testing.T, c *client, filename, mime string, content []byte) (id, uri string) {
	t.Helper()
	body := new(bytes.Buffer)
	w := multipart.NewWriter(body)
	for field, value := range map[string]string{
		"scope": "global", "path": "acceptance-2395",
		"display_name": fmt.Sprintf("acceptance-2395-%s-%d", filename, time.Now().UnixNano()),
		"description":  "Acceptance #2395: a file served by range.",
	} {
		_ = w.WriteField(field, value)
	}
	head := make(textproto.MIMEHeader)
	head.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	head.Set("Content-Type", mime)
	part, _ := w.CreatePart(head)
	_, _ = part.Write(content)
	_ = w.Close()
	req, _ := http.NewRequestWithContext(c.ctx, http.MethodPost, baseURL()+"/api/v1/resources", body)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		t.Fatalf("uploading %s: %v", filename, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK {
		t.Fatalf("uploading %s: %d %s", filename, res.StatusCode, raw)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	id, _ = out["id"].(string)
	uri, _ = out["uri"].(string)
	if id == "" || uri == "" {
		t.Fatalf("the upload returned no id or uri: %s", raw)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	return id, uri
}

// issue2395Clip is a 20-second H.264 clip (testdata/issue2395-clip.mp4, drawn
// once with ffmpeg's testsrc), long enough to seek in.
func issue2395Clip(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "issue2395-clip.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// issue2395RefsPath is the reference route address the served document names
// for a file it references.
var issue2395RefsPath = regexp.MustCompile(`/portal/refs/[A-Za-z0-9_-]+/[A-Za-z0-9_-]+`)

// issue2395ServedRefs reads an asset's served content and returns the
// reference addresses it names.
func issue2395ServedRefs(t *testing.T, c *client, assetID string) []string {
	t.Helper()
	status, body := issue2395Get(t, c, "/api/v1/portal/assets/"+assetID+"/content", nil)
	if status != http.StatusOK {
		t.Fatalf("reading the served content: %d", status)
	}
	refs := issue2395RefsPath.FindAllString(string(body), -1)
	if len(refs) == 0 {
		t.Fatalf("the served content names no reference address:\n%s", body)
	}
	return refs
}

// issue2395Get issues an authenticated GET and returns the status and body.
func issue2395Get(t *testing.T, c *client, path string, header map[string]string) (int, []byte) {
	t.Helper()
	status, body, _ := issue2395Do(t, c, path, header)
	return status, body
}

// issue2395GetAt issues an authenticated GET to one replica's own address.
func issue2395GetAt(t *testing.T, c *client, base, path string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequestWithContext(c.ctx, http.MethodGet, base+path, http.NoBody)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	res, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		t.Fatalf("GET %s%s: %v", base, path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, body
}

// issue2395Do issues an authenticated GET and returns the status, body and
// response headers.
func issue2395Do(t *testing.T, c *client, path string, header map[string]string) (int, []byte, http.Header) {
	t.Helper()
	req, _ := http.NewRequestWithContext(c.ctx, http.MethodGet, baseURL()+path, http.NoBody)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, body, res.Header
}

// issue2395Storage sums storage_operations_total over every replica for one
// purpose and operation.
func issue2395Storage(t *testing.T, purpose, operation string) float64 {
	t.Helper()
	var total float64
	for _, line := range strings.Split(scrapeRaw(t), "\n") {
		if !strings.HasPrefix(line, "storage_operations_total{") ||
			!strings.Contains(line, `purpose="`+purpose+`"`) || !strings.Contains(line, `operation="`+operation+`"`) {
			continue
		}
		fields := strings.Fields(line)
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err == nil {
			total += v
		}
	}
	return total
}

// issue2395ReplicaA is the first replica's own address, not the proxy's, so a
// download is served by the process whose metrics are read.
func issue2395ReplicaA() string {
	port := os.Getenv("DEV_API_PORT")
	if port == "" {
		port = defaultDevPort
	}
	return "http://localhost:" + port
}

// issue2395ReplicaRSS is the first replica's resident memory, from its own
// /metrics (process_resident_memory_bytes), wherever that process runs.
func issue2395ReplicaRSS(t *testing.T) func() int64 {
	t.Helper()
	target := metricsURLs()[0]
	return func() int64 {
		for _, line := range strings.Split(scrapeOne(t, target), "\n") {
			if v, ok := strings.CutPrefix(line, "process_resident_memory_bytes "); ok {
				f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
				return int64(f)
			}
		}
		return 0
	}
}

// issue2395ProcessRSS is a local process's resident memory in bytes.
func issue2395ProcessRSS(pid string) func() int64 {
	return func() int64 {
		out, err := exec.Command("ps", "-o", "rss=", "-p", pid).Output() // #nosec G204 -- a pid this suite started
		if err != nil {
			return 0
		}
		kb, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		return kb * 1024
	}
}

// issue2395PeakGrowth samples resident memory while fn runs and returns how
// far it rose above where it started.
func issue2395PeakGrowth(sample func() int64, fn func()) int64 {
	start := sample()
	var peak int64
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				if v := sample(); v > 0 {
					mu.Lock()
					peak = max(peak, v)
					mu.Unlock()
				}
			}
		}
	}()
	fn()
	close(done)
	mu.Lock()
	defer mu.Unlock()
	return peak - start
}

// TestIssue2395_AReferencedVideoPlaysAndSeeksByRange: the clip a deck
// references plays and seeks on the asset page and a public share signed out,
// and every storage read the reference route made was a range read.
func TestIssue2395_AReferencedVideoPlaysAndSeeksByRange(t *testing.T) {
	c := connect(t)
	_, uri := issue2395Upload(t, c, "clip.mp4", "video/mp4", issue2395Clip(t))
	deck := saveEmbedDeck(t, c, "Acceptance 2395 clip", fmt.Sprintf(
		`<!DOCTYPE html><html><head><title>Clip</title></head><body><video id="clip" controls muted playsinline src=%q style="width:640px"></video></body></html>`, uri),
		[]string{uri}, issue2395Purpose)
	refs := issue2395ServedRefs(t, c, deck)

	gets, ranges := issue2395Storage(t, "resources", "get"), issue2395Storage(t, "resources", "get_range")
	status, part, header := issue2395Do(t, c, refs[0], map[string]string{"Range": "bytes=1000-1999"})
	if status != http.StatusPartialContent || len(part) != 1000 {
		t.Fatalf("a range of the clip answered %d with %d bytes", status, len(part))
	}
	if !strings.HasPrefix(header.Get("Content-Range"), "bytes 1000-1999/") {
		t.Errorf("Content-Range = %q", header.Get("Content-Range"))
	}
	runEmbedSuite(t, []embedCase{{
		Name: "a referenced clip plays and seeks", Asset: deck, PublicShare: issue2373Share(t, c, deck, "public"),
		Videos: []embedVideo{{Selector: "#clip", Plays: true, Seeks: true}},
	}})
	if got := issue2395Storage(t, "resources", "get"); got != gets {
		t.Errorf("storage_operations_total{purpose=resources,operation=get} moved from %.0f to %.0f: the clip was read whole", gets, got)
	}
	if got := issue2395Storage(t, "resources", "get_range"); got <= ranges {
		t.Errorf("storage_operations_total{purpose=resources,operation=get_range} did not move (%.0f): no range read served the clip", got)
	}
}

// TestIssue2395_ALargeDownloadDoesNotHoldTheFile: a resource and an asset near
// the dev stack's 250 MB ceiling download whole without a Range header from
// the first replica, and its resident memory, read from its own /metrics, does
// not rise by the file's size.
func TestIssue2395_ALargeDownloadDoesNotHoldTheFile(t *testing.T) {
	c := connect(t)
	const size = 200 << 20
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	resID, _ := issue2395Upload(t, c, "large.bin", "application/octet-stream", data)
	replica, rss := issue2395ReplicaA(), issue2395ReplicaRSS(t)
	if rss() <= 0 {
		t.Fatalf("%s carries no process_resident_memory_bytes; memory cannot be measured", metricsURLs()[0])
	}

	var status int
	var got []byte
	growth := issue2395PeakGrowth(rss, func() {
		status, got = issue2395GetAt(t, c, replica, "/api/v1/resources/"+resID+"/content")
	})
	if status != http.StatusOK || !bytes.Equal(got, data) {
		t.Fatalf("the resource download answered %d with %d bytes, want the %d stored", status, len(got), size)
	}
	t.Logf("resource download: resident memory rose %d MiB for a %d MiB file", growth>>20, size>>20)
	if growth > size/2 {
		t.Errorf("serving a %d MiB resource raised the replica's memory by %d MiB: the file was held", size>>20, growth>>20)
	}

	asset := issue2395LargeAsset(t, c, data)
	growth = issue2395PeakGrowth(rss, func() {
		status, got = issue2395GetAt(t, c, replica, "/api/v1/portal/assets/"+asset+"/content")
	})
	if status != http.StatusOK || !bytes.Equal(got, data) {
		t.Fatalf("the asset download answered %d with %d bytes, want the %d stored", status, len(got), size)
	}
	t.Logf("asset download: resident memory rose %d MiB for a %d MiB file", growth>>20, size>>20)
	if growth > size/2 {
		t.Errorf("serving a %d MiB asset raised the replica's memory by %d MiB: the file was held", size>>20, growth>>20)
	}
}

// issue2395LargeAsset is a binary asset holding data: a Parquet export whose
// object is replaced in place with data, the way #1833's criterion makes a
// large asset, which also holds that the size served is the store's.
func issue2395LargeAsset(t *testing.T, c *client, data []byte) string {
	t.Helper()
	out := c.call("trino_export", map[string]any{
		"sql": "SELECT BIGINT '1' AS id", "format": "parquet",
		"name": fmt.Sprintf("Acceptance 2395 large %d", time.Now().UnixNano()), "purpose": issue2395Purpose,
	})
	id, _ := out["asset_id"].(string)
	if id == "" {
		t.Fatalf("trino_export wrote no asset: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/portal/assets/"+id, http.NoBody) })
	status, asset := c.rest(http.MethodGet, "/api/v1/portal/assets/"+id, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the asset: %d %v", status, asset)
	}
	bucket, _ := asset["s3_bucket"].(string)
	key, _ := asset["s3_key"].(string)
	issue1833PutObject(t, bucket, key, data)
	return id
}

// TestIssue2395_AConditionalGetAnswers304OnEachRoute: the ETag a streamed
// response carries is answered with 304 on the resource, resource version,
// asset, asset version, public share and reference routes.
func TestIssue2395_AConditionalGetAnswers304OnEachRoute(t *testing.T) {
	c := connect(t)
	clip := issue2395Clip(t)
	resID, uri := issue2395Upload(t, c, "etag.mp4", "video/mp4", clip)
	deck := saveEmbedDeck(t, c, "Acceptance 2395 etag", fmt.Sprintf(`<video src=%q></video>`, uri), []string{uri}, issue2395Purpose)
	asset := issue2395LargeAsset(t, c, clip)
	share := issue2373Share(t, c, asset, "public")

	routes := map[string]string{
		"resource":         "/api/v1/resources/" + resID + "/content",
		"resource version": "/api/v1/resources/" + resID + "/versions/1/content",
		"asset":            "/api/v1/portal/assets/" + asset + "/content",
		"asset version":    "/api/v1/portal/assets/" + asset + "/versions/1/content",
		"public share":     strings.TrimSuffix(share, "/") + "/content",
		"reference":        issue2395ServedRefs(t, c, deck)[0],
	}
	for name, path := range routes {
		status, _, header := issue2395Do(t, c, path, nil)
		tag := header.Get("ETag")
		if status != http.StatusOK || tag == "" {
			t.Errorf("%s: %d with ETag %q", name, status, tag)
			continue
		}
		again, body, _ := issue2395Do(t, c, path, map[string]string{"If-None-Match": tag})
		if again != http.StatusNotModified || len(body) != 0 {
			t.Errorf("%s: a conditional GET with %s answered %d with %d bytes, want 304", name, tag, again, len(body))
		}
	}
}

// TestIssue2395_ATextAssetIsStillServedRewritten: a document's mcp:// URIs are
// rewritten to reference addresses as before.
func TestIssue2395_ATextAssetIsStillServedRewritten(t *testing.T) {
	c := connect(t)
	_, uri := issue2395Upload(t, c, "note.png", "image/png", issue2395PNG)
	deck := saveEmbedDeck(t, c, "Acceptance 2395 text", fmt.Sprintf(`<html><body><img src=%q></body></html>`, uri), []string{uri}, issue2395Purpose)
	_, body := issue2395Get(t, c, "/api/v1/portal/assets/"+deck+"/content", nil)
	if strings.Contains(string(body), uri) || !issue2395RefsPath.Match(body) {
		t.Errorf("the document was not served rewritten:\n%s", body)
	}
}

// issue2395PNG is a one-pixel PNG.
var issue2395PNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0xf0,
	0x1f, 0x00, 0x05, 0x00, 0x01, 0xff, 0x89, 0x99, 0x3d, 0x1d, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
	0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}
