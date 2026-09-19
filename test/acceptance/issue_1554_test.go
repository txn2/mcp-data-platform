//go:build integration

package acceptance

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Issues #1554 and #1555: a resource's thumbnail is an image stored beside it,
// and a library's folders come from the server with exact counts.
//
// Both were the same defect in different clothes -- the portal deriving from
// the file, or from a page of the listing, what the server should have been
// asked for. A tile WAS the original object, scaled by CSS and blank past a
// cutoff; a folder count was "how many rows have arrived so far", which is why
// a library root offered a Load-more control over rows it never displayed.
//
// Since #1787 the platform draws the tile itself, in the headless renderer
// beside it, so the whole cycle runs here: a file gets its tiles without
// anyone opening it, they are served back, a rewrite is drawn again, and a
// clear is drawn again.
//
// Wire forms: every parameter is typed in its schema and admits exactly one
// JSON form. manage_resource's action, filename, display_name, path,
// description, content, content_type and reference are strings and tags is an
// array of strings, each sent below as a literal tools/call parameter of that
// form. The REST surface takes its variant as a query-string parameter, which
// has no second form; both spellings of the tile route are issued -- with no
// variant and with variant=dark -- and both spellings of the facets route,
// narrowed and unnarrowed.

// onePixelPNG is the smallest valid PNG, for criteria that need a file whose
// bytes are an image.
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
		"description":  "Acceptance #1554: a file whose thumbnail is drawn by the platform.",
		"content":      "# " + name + "\n\nSome prose for the renderer to draw.\n",
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

// getCapture1554 reads a stored tile, returning the status and the bytes.
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
		t.Fatalf("reading the tile: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(res.Body)
	return res.StatusCode, buf.Bytes()
}

// TestIssue1554_AResourcesThumbnailIsDrawnAndServed walks the whole cycle: a
// file gets both tiles without anyone opening it, each is served back as the
// PNG the platform stored, and a clear is drawn again.
func TestIssue1554_AResourcesThumbnailIsDrawnAndServed(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	id := createResource1554(t, c, "acceptance-1554-"+unique1554(), "references")

	// Markdown renders on a plain background, so it is drawn twice.
	awaitResourceTile1787(t, c, id, true)
	for _, q := range []string{"", "?variant=dark"} {
		status, body := getCapture1554(t, c, id, q)
		if status != http.StatusOK {
			t.Fatalf("reading the %q tile: status %d, want 200", q, status)
		}
		if !bytes.HasPrefix(body, []byte("\x89PNG")) {
			t.Errorf("the %q tile is not a PNG: %d bytes starting %q", q, len(body), body[:min(8, len(body))])
		}
	}

	// Clearing one is the way back from a tile that is wrong: both are gone at
	// once, and both are drawn again.
	status, _ := c.rest(http.MethodDelete, "/api/v1/resources/"+id+"/thumbnail", http.NoBody)
	if status != http.StatusNoContent {
		t.Fatalf("clearing the tile: status %d, want 204", status)
	}
	if got, _ := getCapture1554(t, c, id, ""); got != http.StatusNotFound {
		t.Errorf("reading a cleared tile: status %d, want 404", got)
	}
	awaitResourceTile1787(t, c, id, true)
}

// TestIssue1554_ARewrittenFileIsDrawnAgain is the case the timestamp exists
// for: a tile older than the file it came from is behind it, and the platform
// draws the file as it now stands.
func TestIssue1554_ARewrittenFileIsDrawnAgain(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	name := "acceptance-1554-rewritten-" + unique1554()
	id := createResource1554(t, c, name, "references")
	before := awaitResourceTile1787(t, c, id, true)

	// Replace the content. The tile now predates the file.
	c.call("manage_resource", map[string]any{
		"action":         "replace_content",
		"reference":      "mcp:resource:" + id,
		"content":        "# " + name + "\n\nRewritten.\n",
		"content_type":   "text/markdown",
		"change_summary": "Acceptance #1554: content moved on after the tile was drawn.",
	})

	// The tile it has keeps serving until the new one lands: one revision behind
	// is worth more than no image at all.
	if status, _ := getCapture1554(t, c, id, ""); status != http.StatusOK {
		t.Errorf("the superseded tile stopped serving: status %d", status)
	}
	after := awaitResourceTile1787(t, c, id, true)
	if before["thumbnail_captured_at"] == after["thumbnail_captured_at"] {
		t.Errorf("the tile is still stamped %v, the file it was drawn from before the rewrite", after["thumbnail_captured_at"])
	}
}

// TestIssue1554_ATileIsReadOnlyByThoseWhoMaySeeTheFile covers the refusal an
// ordinary caller can provoke. A resource nobody may see answers the same way
// one that does not exist does: which resources exist in a library the caller
// cannot reach is not theirs to learn.
func TestIssue1554_ATileIsReadOnlyByThoseWhoMaySeeTheFile(t *testing.T) {
	admin := connectFor(t, 3*tileWait1787)
	id := createResource1554(t, admin, "acceptance-1554-refusals-"+unique1554(), "references")
	awaitResourceTile1787(t, admin, id, false)

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
