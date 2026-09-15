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

// Issue #1754: if a browser can render it in the viewer, it can have a tile.
//
// The renderer registry says how content is presented and every viewing surface
// resolves through it; what gets a thumbnail was a second, hand-kept list, and
// it had become a subset. YAML, XML, SQL, Python, JavaScript, CSS and TSV are
// laid out by the viewer every day and were never offered a capture, so each
// kept a content-type icon forever.
//
// Nothing on a server rasterizes a document -- the image is made in a browser,
// which is exercised against a real one in ui/e2e/thumbnails/capture.spec.ts --
// so what is executed here is everything the browser talks to: which files the
// platform offers for capture, that it takes and serves both variants of a
// capture for each of the seven, and that the families the viewer renders and
// the capturer deliberately does not draw are still never offered.
//
// Wire forms: manage_resource's action, filename, display_name, path,
// description, content and content_type are typed strings in its schema and
// tags is an array of strings, so each admits exactly one JSON form and each is
// sent below as a literal tools/call parameter of that form. save_asset's name,
// content, content_type and description are typed strings likewise. The pending
// reads take their limit as a query-string parameter, which has no second form,
// and the capture upload carries the PNG as a request body with no JSON at all.

func unique1754() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// newlyRenderable is the seven families the viewer renders and the capturer did
// not draw, with the extension and body each is filed under.
var newlyRenderable = []struct {
	name        string
	ext         string
	contentType string
	body        string
}{
	{"yaml", ".yaml", "application/yaml", "server:\n  address: \":8080\"\n  name: platform\n"},
	{"xml", ".xml", "application/xml", "<report><row id=\"1\">41208</row></report>\n"},
	{"sql", ".sql", "application/sql", "SELECT customer_id, sum(total)\nFROM orders\nGROUP BY 1;\n"},
	{"python", ".py", "text/x-python", "def rows(conn):\n    return conn.execute(\"select 1\")\n"},
	{"javascript", ".js", "text/javascript", "export const total = (rows) => rows.length;\n"},
	{"css", ".css", "text/css", ".card { padding: 1rem; border-radius: 8px; }\n"},
	{"tsv", ".tsv", "text/tab-separated-values", "region\trevenue\nwest\t41208\neast\t38112\n"},
}

// createResource1754 files one resource under the declaration given and returns
// its id.
func createResource1754(t *testing.T, c *client, ext, declared, body string) string {
	t.Helper()
	name := "acceptance-1754-" + unique1754()
	out := c.call("manage_resource", map[string]any{
		"action":       "create",
		"filename":     name + ext,
		"display_name": name,
		"path":         "acceptance-1754",
		"description":  "Acceptance #1754: a file the viewer renders and the capturer must draw.",
		"content":      body,
		"content_type": declared,
		"tags":         []any{"acceptance-1754"},
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

// createAsset1754 saves one asset under the declaration given and returns its id.
func createAsset1754(t *testing.T, c *client, declared, body string) string {
	t.Helper()
	out := c.call("save_asset", map[string]any{
		"name":         "acceptance-1754-" + unique1754(),
		"content":      body,
		"content_type": declared,
		"description":  "Acceptance #1754: an asset the viewer renders and the capturer must draw.",
	})
	id, _ := out["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id})
	})
	return id
}

// pendingResources1754 returns the resource ids the platform offers for capture.
func pendingResources1754(t *testing.T, c *client) map[string]bool {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/resources/thumbnails/pending?limit=200", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET the resource pending list: status %d: %v", status, body)
	}
	return idsOf1754(body["resources"])
}

// pendingAssets1754 returns the asset ids the platform offers for capture.
func pendingAssets1754(t *testing.T, c *client) map[string]bool {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/portal/thumbnails/pending?limit=200", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET the asset pending list: status %d: %v", status, body)
	}
	return idsOf1754(body["data"])
}

func idsOf1754(list any) map[string]bool {
	ids := map[string]bool{}
	items, _ := list.([]any)
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := row["id"].(string); id != "" {
			ids[id] = true
		}
	}
	return ids
}

// storeCapture1754 uploads a PNG to a target's capture route the way a portal
// tab does, and returns the status.
func storeCapture1754(t *testing.T, c *client, path, query string, png []byte) int {
	t.Helper()
	req, err := http.NewRequestWithContext(c.ctx, http.MethodPut,
		baseURL()+path+"/thumbnail"+query, bytes.NewReader(png))
	if err != nil {
		t.Fatalf("building the capture upload: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "image/png")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("uploading the capture: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close
	return res.StatusCode
}

// readCapture1754 reads a stored capture back, returning the status and bytes.
func readCapture1754(t *testing.T, c *client, path, query string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet,
		baseURL()+path+"/thumbnail"+query, http.NoBody)
	if err != nil {
		t.Fatalf("building the capture read: %v", err)
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

func fixturePNG1754(t *testing.T) []byte {
	t.Helper()
	png, err := base64.StdEncoding.DecodeString(onePixelPNG)
	if err != nil {
		t.Fatalf("decoding the fixture PNG: %v", err)
	}
	return png
}

// TestIssue1754_EveryFamilyTheViewerRendersIsOfferedACapture is the first
// criterion on the resource side: each of the seven is offered the work it was
// never offered.
func TestIssue1754_EveryFamilyTheViewerRendersIsOfferedACapture(t *testing.T) {
	c := connect(t)

	ids := map[string]string{}
	for _, f := range newlyRenderable {
		ids[f.name] = createResource1754(t, c, f.ext, f.contentType, f.body)
	}

	pending := pendingResources1754(t, c)
	for _, f := range newlyRenderable {
		if !pending[ids[f.name]] {
			t.Errorf("a %s resource (%s) is not offered for capture", f.name, f.contentType)
		}
	}
}

// TestIssue1754_AnAssetOfEveryFamilyIsOfferedACapture is the same on the asset
// side: the rule is a property of the content, not of the kind it is stored as.
func TestIssue1754_AnAssetOfEveryFamilyIsOfferedACapture(t *testing.T) {
	c := connect(t)

	ids := map[string]string{}
	for _, f := range newlyRenderable {
		ids[f.name] = createAsset1754(t, c, f.contentType, f.body)
	}

	pending := pendingAssets1754(t, c)
	for _, f := range newlyRenderable {
		if !pending[ids[f.name]] {
			t.Errorf("a %s asset (%s) is not offered for capture", f.name, f.contentType)
		}
	}
}

// TestIssue1754_BothVariantsOfACaptureAreStoredAndServed is the light-and-dark
// half of the criterion: each of the seven is drawn on the platform's own
// background, so each carries a capture per color scheme rather than one image
// for both, and a file holding only the light one is still offered the work.
func TestIssue1754_BothVariantsOfACaptureAreStoredAndServed(t *testing.T) {
	c := connect(t)
	png := fixturePNG1754(t)

	for _, f := range newlyRenderable {
		id := createResource1754(t, c, f.ext, f.contentType, f.body)
		path := "/api/v1/resources/" + id

		if status := storeCapture1754(t, c, path, "", png); status != http.StatusOK {
			t.Fatalf("%s: storing the light capture: status %d", f.name, status)
		}
		// Themeable: the light capture alone does not satisfy it, so the file is
		// still offered the dark one.
		if !pendingResources1754(t, c)[id] {
			t.Errorf("%s: a file with only its light capture is not offered the dark one", f.name)
		}

		if status := storeCapture1754(t, c, path, "?variant=dark", png); status != http.StatusOK {
			t.Fatalf("%s: storing the dark capture: status %d", f.name, status)
		}
		if pendingResources1754(t, c)[id] {
			t.Errorf("%s: a file holding both captures is still offered the work", f.name)
		}

		for _, variant := range []string{"", "?variant=dark"} {
			status, body := readCapture1754(t, c, path, variant)
			if status != http.StatusOK {
				t.Errorf("%s: reading the capture%s: status %d", f.name, variant, status)
				continue
			}
			if !bytes.Equal(body, png) {
				t.Errorf("%s: the capture%s served %d bytes, want the %d stored",
					f.name, variant, len(body), len(png))
			}
		}
	}
}

// TestIssue1754_TheFamiliesNothingDrawsAreStillNotOffered is the declared
// exclusions, through the surface that would otherwise hand a browser work it
// cannot do: the pending list is a bounded window, so a library of files no
// capture can complete starves the documents behind them.
func TestIssue1754_TheFamiliesNothingDrawsAreStillNotOffered(t *testing.T) {
	c := connect(t)

	undrawn := []struct{ name, ext, contentType, body string }{
		{"pdf", ".pdf", "application/pdf", "%PDF-1.7\nnot really a PDF, but declared as one\n"},
		{"zip", ".zip", "application/zip", "PK\x03\x04 not really an archive\n"},
	}
	ids := map[string]string{}
	for _, f := range undrawn {
		ids[f.name] = createResource1754(t, c, f.ext, f.contentType, f.body)
	}

	pending := pendingResources1754(t, c)
	for _, f := range undrawn {
		if pending[ids[f.name]] {
			t.Errorf("a %s resource is offered for a capture no browser can complete", f.name)
		}
	}
}
