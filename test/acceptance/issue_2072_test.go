//go:build integration

package acceptance

// Issue #2072: an HTML asset larger than 1 MB never got a thumbnail, and
// nothing said why: its thumbnail fields read exactly as a tile not drawn yet.
//
// What this holds, against the running platform with the dev stack's default
// thumbnails section: a 1.3 MB HTML asset reports thumbnail_skipped
// "over_source_limit" with thumbnail_source_limit 1048576, on manage_asset get
// and list and on the portal's asset route; a small asset beside it reports
// neither.
//
// Wire forms: save_asset's `content`, `content_type` and `name` and
// manage_asset's `action` and `asset_id` are typed string and admit that one
// JSON form; each is sent as a literal tools/call param.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

const issue2072Limit = 1 << 20

// issue2072Save saves an HTML asset of about size bytes and removes it when
// the test ends.
func issue2072Save(t *testing.T, c *client, name string, size int) string {
	t.Helper()
	body := "<!doctype html><html><body><table>" +
		strings.Repeat("<tr><td>row</td><td>0123456789</td></tr>", size/40) +
		"</table></body></html>"
	out := c.call("save_asset", map[string]any{"name": name, "content_type": "text/html", "content": body})
	id, _ := out["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset: %v", out)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
	return id
}

// issue2072Skip reads the two fields off a decoded asset.
func issue2072Skip(asset map[string]any) (string, float64) {
	reason, _ := asset["thumbnail_skipped"].(string)
	limit, _ := asset["thumbnail_source_limit"].(float64)
	return reason, limit
}

// TestIssue2072_AnAssetPastTheBoundSaysItIsSkipped is the ticket's second
// proposal on every surface an asset is read from.
func TestIssue2072_AnAssetPastTheBoundSaysItIsSkipped(t *testing.T) {
	c := connect(t)
	stamp := time.Now().UnixNano()
	big := issue2072Save(t, c, fmt.Sprintf("acc-2072-dashboard-%d", stamp), 1_300_000)
	small := issue2072Save(t, c, fmt.Sprintf("acc-2072-small-%d", stamp), 150_000)

	got := c.call("manage_asset", map[string]any{"action": "get", "asset_id": big})
	if reason, limit := issue2072Skip(got); reason != "over_source_limit" || limit != issue2072Limit {
		t.Errorf("manage_asset get: thumbnail_skipped=%q thumbnail_source_limit=%v; want over_source_limit, %d", reason, limit, issue2072Limit)
	}
	if size, _ := got["size_bytes"].(float64); size <= issue2072Limit {
		t.Fatalf("the asset is %v bytes; the criterion needs one past %d", size, issue2072Limit)
	}
	if reason, limit := issue2072Skip(c.call("manage_asset", map[string]any{"action": "get", "asset_id": small})); reason != "" || limit != 0 {
		t.Errorf("a 150 KB asset reports thumbnail_skipped=%q limit=%v; want neither", reason, limit)
	}

	list := c.call("manage_asset", map[string]any{"action": "list", "limit": 50})
	items, _ := list["assets"].([]any)
	found := false
	for _, it := range items {
		a, _ := it.(map[string]any)
		if a["id"] == big {
			found = true
			if reason, _ := issue2072Skip(a); reason != "over_source_limit" {
				t.Errorf("manage_asset list: thumbnail_skipped=%q", reason)
			}
		}
	}
	if !found {
		t.Errorf("manage_asset list did not carry the asset %s", big)
	}

	status, asset := c.rest(http.MethodGet, "/api/v1/portal/assets/"+big, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET asset: HTTP %d %v", status, asset)
	}
	if reason, limit := issue2072Skip(asset); reason != "over_source_limit" || limit != issue2072Limit {
		t.Errorf("portal asset route: thumbnail_skipped=%q thumbnail_source_limit=%v", reason, limit)
	}
}
