//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
)

// Issue #2583: an item's feedback panel lay over the item behind a backdrop, so
// a document could not be scrolled nor a deck paged until it was closed. The
// panel now sits beside the item, or under it on a narrow screen. The criteria
// run in Chromium (ui/e2e/feedback-drawer) on a real deck and a long document,
// at desktop width and at 390px.
//
// Wire forms: the browser's own wheel, key and click events, and the portal's
// POST of a thread, a JSON object of strings. save_asset takes its content as
// a string. No parameter admits a second form.

// asset2583 saves content as an asset and returns its id.
func asset2583(t *testing.T, c *client, contentType, content string) string {
	t.Helper()
	saved := c.call("save_asset", map[string]any{
		"name": marker2580("2583 item"), "content_type": contentType, "content": content, "description": "Acceptance #2583.",
	})
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("saving the asset: %v", saved)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
	return id
}

// assertPosted2583 holds that the comment posted at both widths is stored.
func assertPosted2583(t *testing.T, asset, message string) {
	t.Helper()
	got := platformQuery2532(t, fmt.Sprintf(
		`SELECT COUNT(*) FROM portal_thread_events e JOIN portal_threads t ON t.id = e.thread_id WHERE t.asset_id = '%s' AND e.body LIKE '%s%%'`,
		asset, message))
	if got != "2" {
		t.Errorf("%s comments were stored from the panel, want one per width", got)
	}
}

func TestIssue2583_ADeckPagesWhileFeedbackIsBeingWritten(t *testing.T) {
	asset := asset2583(t, connect(t), "text/html", issue1767Deck)
	message := marker2580("2583-deck")
	browser2580(t, "asset deck (1280|390)px", "FEEDBACK_API_KEY="+defaultDevAPIKey, "FEEDBACK_ASSET="+asset, "FEEDBACK_MESSAGE="+message)
	assertPosted2583(t, asset, message)
}

func TestIssue2583_ADocumentScrollsWhileFeedbackIsBeingWritten(t *testing.T) {
	var doc strings.Builder
	doc.WriteString("# A long document\n\n")
	for i := 1; i <= 80; i++ {
		fmt.Fprintf(&doc, "Paragraph %d of the long document.\n\n", i)
	}
	asset := asset2583(t, connect(t), "text/markdown", doc.String())
	message := marker2580("2583-doc")
	browser2580(t, "asset document (1280|390)px", "FEEDBACK_API_KEY="+defaultDevAPIKey, "FEEDBACK_ASSET="+asset, "FEEDBACK_MESSAGE="+message)
	assertPosted2583(t, asset, message)
}
