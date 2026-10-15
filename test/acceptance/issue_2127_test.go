//go:build integration

package acceptance

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2127: an HTML asset can offer its declared files for
// download. save_asset accepts an anchor with a download attribute naming a
// declared reference, a managed resource by its mcp:// URI or an asset by
// mcp:asset:<id>, without reporting it as a link between assets; the portal
// and the public share serve it rewritten to a URL that answers with the
// file as an attachment; and a plain anchor to a reference is still refused.
// That a click inside the sandboxed frame downloads -- a declared file, and a
// CSV the page builds from a Blob -- is held in a browser by
// ui/e2e/public-viewer/downloads.spec.ts against this stack.
//
// Wire forms: save_asset's content, content_type and name are strings and
// references a list of strings; manage_resource's content_base64 and
// content_type are strings; manage_asset share's access_mode and expires_in
// are strings. Each is sent as literal params.

const csv2127 = "region,total\nwest,4\neast,9\n"

// downloadHref2127 is the href the served content gives the download anchor.
var downloadHref2127 = regexp.MustCompile(`<a [^>]*href="([^"]*/portal/refs/[^"]+\?download=1)"[^>]*>`)

// dataFiles2127 makes a CSV managed resource and a CSV asset, and returns
// their reference strings.
func dataFiles2127(t *testing.T, c *client) (resourceURI, assetRef string) {
	t.Helper()
	stamp := unique1794()
	out := c.call("manage_resource", map[string]any{
		"action": "create", "filename": "orders-" + stamp + ".csv", "display_name": "orders-" + stamp,
		"path": "acceptance-2127", "description": "Acceptance #2127: a data file a report offers for download.",
		"content_base64": base64.StdEncoding.EncodeToString([]byte(csv2127)), "content_type": "text/csv",
	})
	resourceID, _ := out["resource_id"].(string)
	if resourceID == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+resourceID, http.NoBody) })
	_, row := c.rest(http.MethodGet, "/api/v1/resources/"+resourceID, http.NoBody)
	resourceURI, _ = row["uri"].(string)
	if resourceURI == "" {
		t.Fatalf("the CSV resource carries no uri: %v", row)
	}
	data := c.call("save_asset", map[string]any{
		"name": "acc-2127-data-" + stamp, "content_type": "text/csv", "content": csv2127,
	})
	id, _ := data["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", data)
	}
	deleteAfter1875(t, c, id)
	return resourceURI, "mcp:asset:" + id
}

// assetContent2127 reads an asset's served content as its owner.
func assetContent2127(t *testing.T, c *client, assetID string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, c.base+"/api/v1/portal/assets/"+assetID+"/content", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-API-Key", c.apiKey)
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("GET content: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // read below
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET content answered %d: %s", res.StatusCode, body)
	}
	return string(body)
}

// assertDownloads2127 holds every download anchor in served content to a URL
// that answers, with no session, the CSV as an attachment.
func assertDownloads2127(t *testing.T, where, content string, want int) {
	t.Helper()
	found := downloadHref2127.FindAllStringSubmatch(content, -1)
	if len(found) != want {
		t.Fatalf("%s: %d download anchors were rewritten to an attachment URL, want %d:\n%s", where, len(found), want, content)
	}
	for _, m := range found {
		res, err := http.Get(m[1]) // #nosec G107 -- the platform under test
		if err != nil {
			t.Fatalf("%s: GET %s: %v", where, m[1], err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: GET %s answered %d", where, m[1], res.StatusCode)
		}
		if cd := res.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
			t.Errorf("%s: %s is served %q, not as an attachment", where, m[1], cd)
		}
		if string(body) != csv2127 {
			t.Errorf("%s: %s served %q, want the CSV", where, m[1], body)
		}
	}
}

// TestIssue2127_ADownloadAnchorToADeclaredFileIsAcceptedAndServesTheFile:
// a report declares a CSV resource and a CSV asset and offers both for
// download; the save reports no link and no undeclared reference, and the
// portal read and the public share both serve each as an attachment.
func TestIssue2127_ADownloadAnchorToADeclaredFileIsAcceptedAndServesTheFile(t *testing.T) {
	c := connect(t)
	resourceURI, assetRef := dataFiles2127(t, c)
	html := `<!DOCTYPE html><html><body><h1>Orders</h1>
<p><a href="` + resourceURI + `" download>Download the orders</a></p>
<p><a download="orders.csv" href="` + assetRef + `">Download the asset's copy</a></p>
<script>fetch("` + resourceURI + `").then(function (r) { return r.text(); });</script>
</body></html>`
	res, text, err := c.callRaw("save_asset", map[string]any{
		"name": "acc-2127-report-" + unique1794(), "content_type": "text/html", "content": html,
		"references": []any{resourceURI, assetRef},
	})
	if err != nil {
		t.Fatalf("save_asset: %v", err)
	}
	if res.IsError {
		t.Fatalf("save_asset refused a download anchor to a declared file: %s", text)
	}
	if strings.Contains(text, "undeclared_references") || strings.Contains(text, "links between assets") {
		t.Fatalf("save_asset reported the download anchors: %s", text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("save_asset answered with no JSON: %v: %s", err, text)
	}
	assetID, _ := out["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("save_asset returned no asset_id: %s", text)
	}
	deleteAfter1875(t, c, assetID)

	content := assetContent2127(t, c, assetID)
	assertDownloads2127(t, "the portal read", content, 2)
	if strings.Contains(content, resourceURI) || strings.Contains(content, assetRef) {
		t.Errorf("the portal read left a reference unrewritten:\n%s", content)
	}
	if f := regexp.MustCompile(`fetch\("([^"]+)"\)`).FindStringSubmatch(content); len(f) != 2 ||
		!strings.Contains(f[1], "/portal/refs/") || strings.Contains(f[1], "download=1") {
		t.Errorf("the page's fetch of the same file was not given the plain reference URL:\n%s", content)
	}

	share := c.call("manage_asset", map[string]any{
		"action": "share", "asset_id": assetID, "access_mode": "public", "expires_in": "1h",
	})
	shareURL, _ := share["share_url"].(string)
	if !strings.Contains(shareURL, "/portal/view/") {
		t.Fatalf("share returned no viewer URL: %v", share)
	}
	status, shared := anonymousGet(t, shareURL+"/content")
	if status != http.StatusOK {
		t.Fatalf("the public share's content answered %d", status)
	}
	assertDownloads2127(t, "the public share", shared, 2)
}

// TestIssue2127_APlainLinkToAReferenceIsStillRefused: without a download
// attribute an anchor to a reference is a link between assets, refused as
// before, and nothing is stored.
func TestIssue2127_APlainLinkToAReferenceIsStillRefused(t *testing.T) {
	c := connect(t)
	_, assetRef := dataFiles2127(t, c)
	name := "acc-2127-link-" + unique1794()
	refused, text := save1875(t, c, name, "text/html", `<p><a href="`+assetRef+`">the data</a></p>`)
	if !refused || !strings.Contains(text, "links between assets are not supported") {
		t.Fatalf("a plain link to a reference was not refused: %s", text)
	}
	if !strings.Contains(text, "download") {
		t.Errorf("the refusal does not say a download attribute offers the file: %s", text)
	}
	if n := assetsNamed1875(t, c, name); n != 0 {
		t.Fatalf("a refused save stored %d assets", n)
	}
}
