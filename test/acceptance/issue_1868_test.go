//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1868: one document held the tile worker indefinitely. Its infinite
// CSS animation over a box-shadow and SVG blur never let a software-painting
// renderer go idle, the render surfaced as the renderer being unavailable,
// that outcome recorded nothing, and the same row was claimed on every pass
// while everything else owed a tile waited. Two resources whose stored objects
// were gone were re-read every two minutes forever the same way.
//
// What these hold, against the running platform and the renderer the dev stack
// starts beside it (dev/platform.yaml sets thumbnails.max_attempts to 2 and
// thumbnails.retry_backoff to 5s so the bound is reached in seconds): the
// ticket's document is drawn, light and dark, with its animations frozen; a
// plain document saved after it is drawn too, so the queue moved; and a
// resource whose object is gone is recorded as not drawable, with the read's
// reason on the record the Thumbnail panel reads, and leaves the queue.
//
// Wire forms: save_asset's name, content, content_type and description,
// manage_resource's action, filename, display_name, path, description,
// content and content_type, and s3_object's action, connection, bucket, key
// and purpose are typed strings, so each admits one JSON form and is sent as
// that literal. The resource PATCH takes display_name as a string.

// pinned1868 is the document from the ticket: no script, an infinite pulse
// over a box-shadow, and feGaussianBlur filters, 50 KB in the original.
const pinned1868 = `<!DOCTYPE html><html><head><style>
body{margin:0;font:15px sans-serif;background:#0f172a;color:#e2e8f0}
.card{margin:24px;padding:24px;border-radius:12px;background:#1e293b;filter:url(#soft)}
.live{display:inline-block;width:14px;height:14px;border-radius:50%;background:#22c55e;animation:pulse 2s infinite}
@keyframes pulse{0%{box-shadow:0 0 0 0 rgba(34,197,94,.7)}70%{box-shadow:0 0 0 24px rgba(34,197,94,0)}100%{box-shadow:0 0 0 0 rgba(34,197,94,0)}}
.glow{height:220px;margin:24px;border-radius:12px;background:#334155;filter:url(#glow);animation:pulse 2s infinite}
</style></head><body>
<svg width="0" height="0"><filter id="soft"><feGaussianBlur stdDeviation="2"/></filter><filter id="glow"><feGaussianBlur stdDeviation="18"/></filter></svg>
<div class="card"><span class="live"></span> National Retail Intelligence</div>
<div class="glow"></div><div class="glow"></div>
</body></html>`

// TestIssue1868_ADocumentThatPinnedTheRendererIsDrawn holds the first two
// expectations: the document draws at all, in both schemes, and a document
// saved after it is drawn as well rather than waiting behind it.
func TestIssue1868_ADocumentThatPinnedTheRendererIsDrawn(t *testing.T) {
	c := connect(t)
	pinned := saveAsset1787(t, c, "text/html", pinned1868)
	row := awaitAssetTile1787(t, c, pinned, true)
	if n := intField1787(row, "thumbnail_failed_version"); n != 0 {
		t.Errorf("the document was recorded as failed at version %d", n)
	}
	for _, variant := range []string{"light", "dark"} {
		img := tile1787(t, c, "/api/v1/portal/assets/"+pinned, variant)
		if b := img.Bounds(); b.Dx() != 800 || b.Dy() != 600 {
			t.Errorf("the %s tile is %dx%d, want 800x600", variant, b.Dx(), b.Dy())
		}
	}

	after := saveAsset1787(t, c, "text/html", `<!DOCTYPE html><p style="font:20px sans-serif">After the pinned document</p>`)
	start := time.Now()
	awaitAssetTile1787(t, c, after, true)
	if waited := time.Since(start); waited > 60*time.Second {
		t.Errorf("a plain document saved after the pinned one waited %s for its tiles", waited)
	}
}

// resourceRow1868 reads a resource the way the portal's Thumbnail panel does.
func resourceRow1868(t *testing.T, c *client, id string) map[string]any {
	t.Helper()
	status, row := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET resource %s: status %d, %v", id, status, row)
	}
	return row
}

// TestIssue1868_AResourceWhoseObjectIsGoneLeavesTheQueue holds the third: the
// object behind a drawn resource is removed, the resource is changed so a tile
// is owed, and within the configured attempts the failure is recorded with the
// read's reason, dated to the file as it stands, which is what keeps it out of
// the claim (TestResourceStore_ThumbnailAttempts_RealDB holds that exclusion).
func TestIssue1868_AResourceWhoseObjectIsGoneLeavesTheQueue(t *testing.T) {
	c := connect(t)
	stamp := unique1787()
	out := c.call("manage_resource", map[string]any{
		"action":       "create",
		"filename":     "acc-1868-" + stamp + ".csv",
		"display_name": "acc-1868-" + stamp,
		"path":         "acceptance-1868",
		"description":  "Acceptance #1868: a file whose stored object is gone.",
		"content":      "region,revenue\nwest,41208\neast,38112\n",
		"content_type": "text/csv",
	})
	id, _ := out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	awaitResourceTile1787(t, c, id, true)

	c.call("s3_object", map[string]any{
		"action": "delete", "connection": "dev-resources",
		"bucket": "managed-resources", "key": resourceS3Key1775(t, c, id),
		"purpose": "Acceptance #1868: a resource whose stored object is gone must leave the tile queue.",
	})
	if status, body := c.rest(http.MethodPatch, "/api/v1/resources/"+id,
		jsonBody(t, map[string]any{"display_name": "acc-1868-renamed-" + stamp})); status != http.StatusOK {
		t.Fatalf("renaming the resource so a tile is owed: status %d, %v", status, body)
	}

	// Two attempts five seconds apart, each found on a worker poll.
	deadline := time.Now().Add(90 * time.Second)
	var failure string
	for failure == "" {
		row := resourceRow1868(t, c, id)
		failure, _ = row["thumbnail_failure"].(string)
		if failure != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no failure recorded for a resource whose object is gone after 90s: %v", row)
		}
		time.Sleep(time.Second)
	}
	for _, want := range []string{"could not finish drawing this after 2 attempts", "the stored file could not be read"} {
		if !strings.Contains(failure, want) {
			t.Errorf("recorded failure %q does not say %q", failure, want)
		}
	}
	row := resourceRow1868(t, c, id)
	updated, _ := row["updated_at"].(string)
	failedAt, _ := row["thumbnail_failed_at"].(string)
	if !drawnSince1787(failedAt, updated) {
		t.Errorf("the failure (%s) is not dated to the file as it stands (%s), so the panel would not show it", failedAt, updated)
	}

}
