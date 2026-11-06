//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #2501: links to websites open from HTML, JSX and Markdown assets. The
// asset frame stays sandboxed allow-scripts allow-downloads; a link the reader
// clicks to an http:, https: or mailto: address is posted to the viewer page,
// which opens it in a new tab with noopener and noreferrer, only for the frame
// it rendered, only those three schemes, and only while the reader's click is
// a current user activation. What is held here, on the asset page signed in
// and on a public share signed out, in Chrome:
//
//   - an HTML asset's plain link, target="_blank" link and window.open from a
//     click each open a new tab, its mailto: link is handed to the mail
//     client, and the page stays where it was; a JSX asset's the same;
//   - a Markdown asset's external link opens a new tab, and the page stays;
//   - a frame posting javascript: or data:, and a " javascript:" link, open
//     nothing;
//   - a script that opens links with no click opens nothing, and one click
//     opens one tab however many it asks for;
//   - the opened page's window.opener is null and its request carries no
//     Referer.
//
// The browser half is ui/e2e/asset-links-live/asset-links-live.spec.ts, run
// with LINKS_BASE_URL, LINKS_API_KEY and LINKS_CASES.
//
// Wire forms: save_asset's `content` is a string (an HTML document, a JSX
// module, a Markdown document); `name`, `content_type` and `purpose` are typed
// strings. manage_asset's `action`, `asset_id`, `access_mode`, `expires_in`
// and `purpose` are strings.

const issue2501Purpose = "Acceptance for #2501: links to websites open from assets."

// issue2501HTML carries every link the criteria click, and on load does what
// a hostile document would: window.open with no click, and forged messages
// naming refused schemes. The last message tells the spec it has loaded.
const issue2501HTML = `<!DOCTYPE html><html><head><title>Links</title></head><body>
<p><a id="plain" href="https://example.com/plain">plain</a></p>
<p><a id="blank" href="https://example.com/blank" target="_blank">blank</a></p>
<p><a id="mail" href="mailto:someone@example.com?subject=Hi">mail</a></p>
<p><button id="opener" onclick="window.open('https://example.com/opened')">open</button></p>
<p><button id="loop" onclick="for (var i = 0; i < 5; i++) window.open('https://example.com/loop' + i)">loop</button></p>
<p><a id="js" href=" javascript:document.title='ran'">js</a></p>
<script>
for (var i = 0; i < 5; i++) window.open("https://example.com/unprompted" + i);
parent.postMessage({ type: "mcp:open-link", url: "javascript:alert(1)" }, "*");
parent.postMessage({ type: "mcp:open-link", url: "data:text/html,<b>x</b>" }, "*");
parent.postMessage({ type: "links-test-loaded" }, "*");
</script>
</body></html>`

// issue2501JSX is the JSX counterpart: the same links and the same unprompted
// opens, made when the component mounts.
const issue2501JSX = `import { useEffect } from "react";

export default function Links() {
  useEffect(() => {
    for (let i = 0; i < 5; i++) window.open("https://example.com/unprompted" + i);
    window.parent.postMessage({ type: "mcp:open-link", url: "javascript:alert(1)" }, "*");
    window.parent.postMessage({ type: "mcp:open-link", url: "data:text/html,<b>x</b>" }, "*");
    window.parent.postMessage({ type: "links-test-loaded" }, "*");
  }, []);
  return (
    <div>
      <p><a id="plain" href="https://example.com/jsx">site</a></p>
      <p><a id="mail" href="mailto:someone@example.com">mail</a></p>
      <p><a id="js" href=" javascript:document.title='ran'">js</a></p>
    </div>
  );
}
`

const issue2501Markdown = "# Links\n\nSee [the site](https://example.com/md) or [mail us](mailto:someone@example.com).\n"

// issue2501Kinds is what each kind of asset is saved as.
var issue2501Kinds = map[string]struct{ content, contentType string }{
	"html":     {issue2501HTML, "text/html"},
	"jsx":      {issue2501JSX, "text/jsx"},
	"markdown": {issue2501Markdown, "text/markdown"},
}

// linksCase is one asset the browser suite opens; its JSON names are the
// suite's (ui/e2e/asset-links-live/asset-links-live.spec.ts).
type linksCase struct {
	Check       string `json:"check"`
	Kind        string `json:"kind"`
	Asset       string `json:"asset"`
	PublicShare string `json:"publicShare"`
}

// issue2501Save saves one asset of the kind and returns its id.
func issue2501Save(t *testing.T, c *client, kind string) string {
	t.Helper()
	k, ok := issue2501Kinds[kind]
	if !ok {
		t.Fatalf("no asset of kind %q", kind)
	}
	saved := c.call("save_asset", map[string]any{
		"name":         fmt.Sprintf("Acceptance 2501 %s links %d", kind, time.Now().UnixNano()),
		"content":      k.content,
		"content_type": k.contentType,
		"purpose":      issue2501Purpose,
	})
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id, "purpose": issue2501Purpose})
	})
	return id
}

// issue2501PublicShare makes a public link share of the asset and returns its
// viewer path.
func issue2501PublicShare(t *testing.T, c *client, assetID string) string {
	t.Helper()
	share := c.call("manage_asset", map[string]any{
		"action": "share", "asset_id": assetID, "access_mode": "public", "expires_in": "1h", "purpose": issue2501Purpose,
	})
	u, _ := share["share_url"].(string)
	i := strings.Index(u, "/portal/")
	if i < 0 {
		t.Fatalf("share returned no viewer URL: %v", share)
	}
	return u[i:]
}

// issue2501Cases saves one asset per kind and names the check to run on each.
func issue2501Cases(t *testing.T, c *client, check string, kinds ...string) []linksCase {
	t.Helper()
	out := make([]linksCase, 0, len(kinds))
	for _, kind := range kinds {
		id := issue2501Save(t, c, kind)
		out = append(out, linksCase{Check: check, Kind: kind, Asset: id, PublicShare: issue2501PublicShare(t, c, id)})
	}
	return out
}

// runLinksSuite opens every case in a real browser on the asset page signed in
// and on its public share signed out.
func runLinksSuite(t *testing.T, cases []linksCase) {
	t.Helper()
	raw, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.Abs("../..")
	cmd := exec.Command("npx", "playwright", "test", "--config", "e2e/asset-links-live/playwright.config.ts")
	cmd.Dir = filepath.Join(root, "ui")
	cmd.Env = append(os.Environ(), "LINKS_BASE_URL="+baseURL(), "LINKS_API_KEY="+devAPIKey(), "LINKS_CASES="+string(raw))
	out, err := cmd.CombinedOutput()
	t.Logf("playwright:\n%s", out)
	if err != nil {
		t.Fatalf("a link in an asset did not behave where it is read: %v", err)
	}
}

// TestIssue2501_AnHTMLAssetsLinksTargetBlankWindowOpenAndMailtoOpenAndThePageStays:
// the plain link, the target="_blank" link and window.open from a click each
// open a new tab, the mailto: link is handed to the mail client, and the page
// stays, signed in and on a public share signed out.
func TestIssue2501_AnHTMLAssetsLinksTargetBlankWindowOpenAndMailtoOpenAndThePageStays(t *testing.T) {
	c := connect(t)
	cases := issue2501Cases(t, c, "opens", "html")
	cases = append(cases, issue2501Cases(t, c, "mailto", "html")...)
	runLinksSuite(t, cases)
}

// TestIssue2501_AJSXAssetsLinkOpensANewTabAndMailtoReachesTheMailClient: the
// same for a JSX asset.
func TestIssue2501_AJSXAssetsLinkOpensANewTabAndMailtoReachesTheMailClient(t *testing.T) {
	c := connect(t)
	cases := issue2501Cases(t, c, "opens", "jsx")
	cases = append(cases, issue2501Cases(t, c, "mailto", "jsx")...)
	runLinksSuite(t, cases)
}

// TestIssue2501_AMarkdownAssetsExternalLinkOpensANewTabAndThePageStays: the
// Markdown asset's link to another site opens a new tab and the page stays.
func TestIssue2501_AMarkdownAssetsExternalLinkOpensANewTabAndThePageStays(t *testing.T) {
	c := connect(t)
	runLinksSuite(t, issue2501Cases(t, c, "opens", "markdown"))
}

// TestIssue2501_AFramePostingJavascriptOrDataOpensNothing: the forged
// javascript: and data: messages the document posts on load, and a click on a
// " javascript:" link, open nothing and leave the page in place.
func TestIssue2501_AFramePostingJavascriptOrDataOpensNothing(t *testing.T) {
	c := connect(t)
	runLinksSuite(t, issue2501Cases(t, c, "refuses", "html", "jsx"))
}

// TestIssue2501_AScriptOpeningLinksWithNoClickOpensNothing: five window.open
// calls on load, with no click, open nothing; one click asking for five opens
// one tab.
func TestIssue2501_AScriptOpeningLinksWithNoClickOpensNothing(t *testing.T) {
	c := connect(t)
	runLinksSuite(t, issue2501Cases(t, c, "unprompted", "html", "jsx"))
}

// TestIssue2501_TheOpenedPageHasNoOpenerAndItsRequestNoReferer: the page a
// link opens sees window.opener null and an empty document.referrer, and the
// request that loaded it carried no Referer, from all three kinds.
func TestIssue2501_TheOpenedPageHasNoOpenerAndItsRequestNoReferer(t *testing.T) {
	c := connect(t)
	runLinksSuite(t, issue2501Cases(t, c, "noopener", "html", "jsx", "markdown"))
}
