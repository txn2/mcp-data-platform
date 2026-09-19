//go:build integration

package acceptance

import (
	"bytes"
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
// Since #1787 the platform draws every tile itself, so what is executed here
// is the whole of it: each of the seven gets a tile in each color scheme
// without anyone opening it, on the resource side and the asset side, and the
// families the viewer renders and the tile page deliberately does not draw are
// never tried.
//
// Wire forms: manage_resource's action, filename, display_name, path,
// description, content and content_type are typed strings in its schema and
// tags is an array of strings, so each admits exactly one JSON form and each is
// sent below as a literal tools/call parameter of that form. save_asset's name,
// content, content_type and description are typed strings likewise. The tile
// read takes its variant as a query-string parameter, which has no second form.

func unique1754() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// newlyRenderable is the seven families the viewer renders and nothing drew, with the extension and body each is filed under.
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
		"description":  "Acceptance #1754: a file the viewer renders and the platform must draw.",
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
		"description":  "Acceptance #1754: an asset the viewer renders and the platform must draw.",
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

// readCapture1754 reads a stored tile back, returning the status and bytes.
func readCapture1754(t *testing.T, c *client, path, query string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet,
		baseURL()+path+"/thumbnail"+query, http.NoBody)
	if err != nil {
		t.Fatalf("building the tile read: %v", err)
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

// TestIssue1754_EveryFamilyTheViewerRendersGetsATileInEachScheme is the
// criterion on the resource side: each of the seven is drawn on the platform's
// own background, so each carries a tile per color scheme rather than one image
// for both, and the two are different pictures.
func TestIssue1754_EveryFamilyTheViewerRendersGetsATileInEachScheme(t *testing.T) {
	c := connectFor(t, 5*tileWait1787)

	ids := map[string]string{}
	for _, f := range newlyRenderable {
		ids[f.name] = createResource1754(t, c, f.ext, f.contentType, f.body)
	}
	for _, f := range newlyRenderable {
		path := "/api/v1/resources/" + ids[f.name]
		awaitResourceTile1787(t, c, ids[f.name], true)
		_, light := readCapture1754(t, c, path, "")
		status, dark := readCapture1754(t, c, path, "?variant=dark")
		if status != http.StatusOK {
			t.Errorf("%s: reading the dark tile: status %d", f.name, status)
			continue
		}
		if !bytes.HasPrefix(light, []byte("\x89PNG")) || !bytes.HasPrefix(dark, []byte("\x89PNG")) {
			t.Errorf("%s: a tile is not a PNG (light %d bytes, dark %d bytes)", f.name, len(light), len(dark))
		}
		if bytes.Equal(light, dark) {
			t.Errorf("%s: the light and dark tiles are the same picture", f.name)
		}
	}
}

// TestIssue1754_AnAssetOfEveryFamilyGetsATileInEachScheme is the same on the
// asset side: the rule is a property of the content, not of the kind it is
// stored as.
func TestIssue1754_AnAssetOfEveryFamilyGetsATileInEachScheme(t *testing.T) {
	c := connectFor(t, 5*tileWait1787)

	ids := map[string]string{}
	for _, f := range newlyRenderable {
		ids[f.name] = createAsset1754(t, c, f.contentType, f.body)
	}
	for _, f := range newlyRenderable {
		awaitAssetTile1787(t, c, ids[f.name], true)
	}
}

// TestIssue1754_TheFamiliesNothingDrawsAreNeverTried is the declared
// exclusions: a file the renderer cannot draw is left with its content-type
// icon rather than tried and recorded as a failure.
func TestIssue1754_TheFamiliesNothingDrawsAreNeverTried(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)

	undrawn := []struct{ name, ext, contentType, body string }{
		{"pdf", ".pdf", "application/pdf", "%PDF-1.7\nnot really a PDF, but declared as one\n"},
		{"zip", ".zip", "application/zip", "PK\x03\x04 not really an archive\n"},
	}
	ids := map[string]string{}
	for _, f := range undrawn {
		ids[f.name] = createResource1754(t, c, f.ext, f.contentType, f.body)
	}
	// A file filed after them that the renderer does draw: once it has its
	// tile, the renderer has passed over the two before it.
	drawn := createResource1754(t, c, ".txt", "text/plain", "drawn after the two it must skip\n")
	awaitResourceTile1787(t, c, drawn, false)

	for _, f := range undrawn {
		status, row := c.rest(http.MethodGet, "/api/v1/resources/"+ids[f.name], http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("GET the %s resource: status %d: %v", f.name, status, row)
		}
		if key, _ := row["thumbnail_s3_key"].(string); key != "" {
			t.Errorf("a %s resource was given a tile, which nothing draws: %q", f.name, key)
		}
		if failure, _ := row["thumbnail_failure"].(string); failure != "" {
			t.Errorf("a %s resource was tried and recorded as a failure: %q", f.name, failure)
		}
	}
}
