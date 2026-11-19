//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
)

// Issue #2705: on a phone an asset's feedback thread could not be read: the
// panel was a fixed 55% sheet, and inside it the reply box, always open,
// squeezed the comments to a strip about one line tall. The sheet now opens at
// 60% and goes full size from its handle, the reply box is one row until
// tapped, and the thread scrolls as one surface. A layout is only measured in
// a real browser, so the criteria run in Chromium (ui/e2e/feedback-drawer,
// the cases named "sheet") against the dev stack, at 390x844 and 375x667 and
// at 1280 for the unchanged desktop column.
//
// Wire forms: the browser's own clicks, taps and navigations. The thread and
// its comments are filed through the portal API, whose bodies are JSON objects
// of strings; no parameter admits a second form.

// longThread2705 is a thread on a new asset holding three comments of several
// lines each, and the text that names it.
func longThread2705(t *testing.T, admin *client) (asset, marker string) {
	t.Helper()
	asset = issue2290Asset(t, admin, "2705 phone sheet")
	marker = marker2580("2705")
	id := openThread2580(t, admin, map[string]any{"kind": "suggestion", "target_type": "asset", "asset_id": asset, "body": marker})
	paragraph := strings.Repeat("The totals on this page do not match the export we sent the board last week, and the difference is in the western region. ", 4)
	for i := range 3 {
		status, event := admin.rest(http.MethodPost, threads2580+"/"+id+"/events", jsonBody(t, map[string]any{
			"body": paragraph + "\n\nComment " + string(rune('A'+i)) + ": " + paragraph,
		}))
		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("adding comment %d: %d %v", i+1, status, event)
		}
	}
	return asset, marker
}

// TestIssue2705_APhoneReadsAThreadAndReplies: at both phone sizes the
// comments take at least half the sheet with the reply box folded, no scroll
// strip is under a third of the sheet, the sheet goes full size and back with
// the size kept between threads, and replying keeps the reply, its buttons and
// the last comment on screen; at 1280 the panel is the full-height column with
// the reply box open, as before.
func TestIssue2705_APhoneReadsAThreadAndReplies(t *testing.T) {
	asset, marker := longThread2705(t, connect(t))
	browser2580(t, "^.*sheet (390|375|1280)", "FEEDBACK_API_KEY="+defaultDevAPIKey, "FEEDBACK_ASSET="+asset, "FEEDBACK_LONG_MARKER="+marker)
}
