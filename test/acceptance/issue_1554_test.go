//go:build integration

package acceptance

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Issues #1554 and #1555: a resource's thumbnail is a captured image stored
// beside it, and a library's folders come from the server with exact counts.
//
// Both were the same defect in different clothes -- the portal deriving from
// the file, or from a page of the listing, what the server should have been
// asked for. A tile WAS the original object, scaled by CSS and blank past a
// cutoff; a folder count was "how many rows have arrived so far", which is why
// a library root offered a Load-more control over rows it never displayed.
//
// Nothing on a server rasterizes a document, so the capture itself happens in a
// browser and cannot be executed here. What is executed here is everything the
// browser talks to: what the platform reports as needing a capture, what it
// accepts, what it then serves, and what clearing one does.
//
// Wire forms: every parameter is typed in its schema and admits exactly one
// JSON form. manage_resource's action, filename, display_name, path,
// description, content, content_base64 and content_type are strings and tags is
// an array of strings, each sent below as a literal tools/call parameter of
// that form. The REST surface takes its variant and limit as query-string
// parameters, which have no second form, and the capture body is image/png
// bytes rather than JSON. Both spellings of the capture route are issued --
// with no variant and with variant=dark -- and both spellings of the facets
// route, narrowed and unnarrowed.

// onePixelPNG is the smallest thing the upload route will accept as a capture.
const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func unique1554() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// createResource1554 files a markdown resource and returns its id.
func createResource1554(t *testing.T, c *client, name, path string) string {
	t.Helper()
	out := c.call("manage_resource", map[string]any{
		"action":       "create",
		"filename":     name + ".md",
		"display_name": name,
		"path":         path,
		"description":  "Acceptance #1554: a file whose thumbnail is captured rather than drawn from the file.",
		"content":      "# " + name + "\n\nSome prose for the capture to render.\n",
		"content_type": "text/markdown",
		"tags":         []any{"acceptance-1554"},
	})
	id, _ := out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() {
		_, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody)
	})
	return id
}

// pendingIDs1554 returns the ids the platform reports as needing a capture.
func pendingIDs1554(t *testing.T, c *client) map[string]bool {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/resources/thumbnails/pending?limit=200", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET pending: status %d: %v", status, body)
	}
	ids := map[string]bool{}
	list, _ := body["resources"].([]any)
	for _, item := range list {
		r, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := r["id"].(string); id != "" {
			ids[id] = true
		}
	}
	return ids
}

// putCapture1554 uploads a capture the way a portal tab does, and returns the
// status so a refusal can be asserted as well as an acceptance.
func putCapture1554(t *testing.T, c *client, id, query, contentType string, body []byte) int {
	t.Helper()
	req, err := http.NewRequestWithContext(c.ctx, http.MethodPut,
		baseURL()+"/api/v1/resources/"+id+"/thumbnail"+query, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building the capture request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", contentType)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("uploading the capture: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close
	return res.StatusCode
}

// getCapture1554 reads a stored capture, returning the status and the bytes.
func getCapture1554(t *testing.T, c *client, id, query string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet,
		baseURL()+"/api/v1/resources/"+id+"/thumbnail"+query, http.NoBody)
	if err != nil {
		t.Fatalf("building the read: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("reading the capture: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(res.Body)
	return res.StatusCode, buf.Bytes()
}

// TestIssue1554_AResourcesThumbnailIsCapturedAndServed walks the whole cycle in
// the order a portal tab performs it.
func TestIssue1554_AResourcesThumbnailIsCapturedAndServed(t *testing.T) {
	c := connect(t)
	id := createResource1554(t, c, "acceptance-1554-"+unique1554(), "references")

	png, err := base64.StdEncoding.DecodeString(onePixelPNG)
	if err != nil {
		t.Fatalf("decoding the fixture PNG: %v", err)
	}

	// A resource with no capture is offered.
	if !pendingIDs1554(t, c)[id] {
		t.Fatalf("a resource with no thumbnail is not on the pending list")
	}

	// Nothing is served for it yet, which is what tells a card to draw its icon.
	if status, _ := getCapture1554(t, c, id, ""); status != http.StatusNotFound {
		t.Errorf("reading an uncaptured thumbnail: status %d, want 404", status)
	}

	// The capture the browser took.
	if status := putCapture1554(t, c, id, "", "image/png", png); status != http.StatusOK {
		t.Fatalf("uploading a capture: status %d, want 200", status)
	}

	// It is served back, byte for byte.
	status, body := getCapture1554(t, c, id, "")
	if status != http.StatusOK {
		t.Fatalf("reading the capture: status %d, want 200", status)
	}
	if !bytes.Equal(body, png) {
		t.Errorf("served %d bytes, want the %d uploaded", len(body), len(png))
	}

	// Markdown renders on a plain background, so it is captured twice: the
	// light pass alone leaves it pending on its dark variant.
	if !pendingIDs1554(t, c)[id] {
		t.Errorf("a themeable resource with only a light capture is not still pending")
	}
	if status := putCapture1554(t, c, id, "?variant=dark", "image/png", png); status != http.StatusOK {
		t.Fatalf("uploading the dark capture: status %d, want 200", status)
	}
	if pendingIDs1554(t, c)[id] {
		t.Errorf("a resource with both captures is still offered")
	}

	// Clearing one is the way back from a tile that is wrong.
	status, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id+"/thumbnail", http.NoBody)
	if status != http.StatusNoContent {
		t.Fatalf("clearing the capture: status %d, want 204", status)
	}
	if !pendingIDs1554(t, c)[id] {
		t.Errorf("a cleared tile is not offered again")
	}
	if got, _ := getCapture1554(t, c, id, ""); got != http.StatusNotFound {
		t.Errorf("reading a cleared thumbnail: status %d, want 404", got)
	}
}

// TestIssue1554_ARewrittenFileIsCapturedAgain is the case the timestamp exists
// for: a capture older than the file it came from is behind it.
func TestIssue1554_ARewrittenFileIsCapturedAgain(t *testing.T) {
	c := connect(t)
	name := "acceptance-1554-rewritten-" + unique1554()
	id := createResource1554(t, c, name, "references")

	png, _ := base64.StdEncoding.DecodeString(onePixelPNG)
	for _, q := range []string{"", "?variant=dark"} {
		if status := putCapture1554(t, c, id, q, "image/png", png); status != http.StatusOK {
			t.Fatalf("uploading %q: status %d", q, status)
		}
	}
	if pendingIDs1554(t, c)[id] {
		t.Fatalf("a fully captured resource is still offered")
	}

	// Replace the content. The capture now predates the file.
	c.call("manage_resource", map[string]any{
		"action":         "replace_content",
		"reference":      "mcp:resource:" + id,
		"content":        "# " + name + "\n\nRewritten.\n",
		"content_type":   "text/markdown",
		"change_summary": "Acceptance #1554: content moved on after the capture.",
	})

	if !pendingIDs1554(t, c)[id] {
		t.Errorf("a resource whose content moved on is not offered for re-capture")
	}
	// The tile it has keeps serving: one revision behind is worth more than no
	// image at all.
	if status, _ := getCapture1554(t, c, id, ""); status != http.StatusOK {
		t.Errorf("the superseded capture stopped serving: status %d", status)
	}
}

// TestIssue1554_TheCaptureRouteRefusesWhatItShould covers the two refusals a
// browser can provoke and the one an ordinary caller can.
func TestIssue1554_TheCaptureRouteRefusesWhatItShould(t *testing.T) {
	admin := connect(t)
	id := createResource1554(t, admin, "acceptance-1554-refusals-"+unique1554(), "references")

	if status := putCapture1554(t, admin, id, "", "text/plain", []byte("not a png")); status != http.StatusBadRequest {
		t.Errorf("a body that is not a PNG: status %d, want 400", status)
	}

	// A resource nobody may see answers the same way one that does not exist
	// does: which resources exist in a library the caller cannot reach is not
	// theirs to learn.
	person := connectAs(t, devPeerAPIKey)
	if status, _ := getCapture1554(t, person, id, ""); status != http.StatusNotFound {
		t.Errorf("a caller who cannot see the resource: status %d, want 404", status)
	}
}

// TestIssue1555_TheLibrarysFacetsComeFromTheServer is the other half: exact
// folder counts and the library's own tags, in one request.
func TestIssue1555_TheLibrarysFacetsComeFromTheServer(t *testing.T) {
	c := connect(t)
	id := unique1554()
	root := "references/acceptance-" + id

	// Three files, two levels: the parent must count everything beneath it.
	createResource1554(t, c, "acceptance-1555-a-"+id, root)
	createResource1554(t, c, "acceptance-1555-b-"+id, root)
	createResource1554(t, c, "acceptance-1555-c-"+id, root+"/deeper")

	status, body := c.rest(http.MethodGet, "/api/v1/resources/facets", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET facets: status %d: %v", status, body)
	}

	counts := map[string]float64{}
	folders, _ := body["folders"].([]any)
	for _, item := range folders {
		f, ok := item.(map[string]any)
		if !ok {
			continue
		}
		path, _ := f["path"].(string)
		count, _ := f["count"].(float64)
		counts[path] = count
	}
	if counts[root] != 3 {
		t.Errorf("count for %s = %v, want 3 (everything beneath it at every depth)", root, counts[root])
	}
	if counts[root+"/deeper"] != 1 {
		t.Errorf("count for %s/deeper = %v, want 1", root, counts[root+"/deeper"])
	}
	// The count is exact rather than "how many have arrived", which is the whole
	// point: nothing in the answer is a lower bound.
	if counts["references"] < 3 {
		t.Errorf("the parent folder does not count what is beneath it: %v", counts["references"])
	}

	tags, _ := body["tags"].([]any)
	found := false
	for _, tag := range tags {
		if s, _ := tag.(string); s == "acceptance-1554" {
			found = true
		}
	}
	if !found {
		t.Errorf("the library's tags do not include the one every file here carries: %v", tags)
	}

	// Narrowed to one library, under the same authority the listing runs.
	status, body = c.rest(http.MethodGet, "/api/v1/resources/facets?scope=global", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET narrowed facets: status %d: %v", status, body)
	}
	if _, ok := body["folders"].([]any); !ok {
		t.Errorf("a narrowed facets read answers with no folders array: %v", body)
	}
}
