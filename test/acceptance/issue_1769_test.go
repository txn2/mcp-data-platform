//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1769: a deck's controls share the row its page already has, the frame
// fills the page, and the served runtime's overview and print view are offered
// as controls. What is held here, through the surface an agent and a reader
// actually meet: the presentations page an agent is pointed at names Overview
// and Export PDF and no longer says the print mode is out of reach; and the
// share page of a deck carries, in its header, the slot the viewer bundle
// renders those controls into, before the info toggle, and no longer sizes the
// frame to a fraction of the viewport.
//
// The controls' behavior in a browser (the row they share on the asset page,
// the frame ending inside the viewport, the overview state, the print document
// laid out one slide per page) is held by ui/e2e/interactive/deck-controls
// against the mock library and ui/e2e/public-viewer against this stack; the
// transcript at build/1769/acceptance.md records all three runs.
//
// Wire forms: fetch's `reference` and `purpose`, save_asset's `name`,
// `content` and `content_type`, and manage_asset's `action`, `asset_id`,
// `access_mode` and `expires_in` are `{"type":"string"}` in their schemas, so
// each admits exactly one JSON form and is sent as a JSON string.

const issue1769Purpose = "Acceptance for #1769: proving the presentations page and the share page carry the deck's overview and PDF export."

// TestIssue1769_ThePageNamesOverviewAndExportPDF: an agent asked for a deck
// reads that the portal offers the overview and a PDF export, and is no longer
// told the print mode is unreachable.
func TestIssue1769_ThePageNamesOverviewAndExportPDF(t *testing.T) {
	c := connect(t)
	body := issue1767Page(t, c)
	for _, want := range []string{"**Overview**", "**Export PDF**", "one slide per page", "Save as PDF"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not say %q", want)
		}
	}
	for _, stale := range []string{"print-to-PDF", "query string", "no PDF"} {
		if strings.Contains(body, stale) {
			t.Errorf("the page still says %q, which the export makes false", stale)
		}
	}
}

// TestIssue1769_TheSharePageCarriesTheControlSlotInItsHeader: the page a
// reader holding a public link is served has, in its header and before the
// info toggle, the slot the viewer renders Present, Overview and Export PDF
// into, and the frame is no longer floored at a fraction of the viewport.
func TestIssue1769_TheSharePageCarriesTheControlSlotInItsHeader(t *testing.T) {
	c := connect(t)
	saved := c.call("save_asset", map[string]any{
		"name":         "Acceptance deck 1769 " + time.Now().Format("150405.000"),
		"content":      issue1767Deck,
		"content_type": "text/html",
		"purpose":      issue1769Purpose,
	})
	assetID, _ := saved["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": assetID, "purpose": issue1769Purpose})
	})

	share := c.call("manage_asset", map[string]any{
		"action":      "share",
		"asset_id":    assetID,
		"access_mode": "public",
		"expires_in":  "1h",
		"purpose":     issue1769Purpose,
	})
	shareURL, _ := share["share_url"].(string)
	if !strings.Contains(shareURL, "/portal/view/") {
		t.Fatalf("share returned no viewer URL: %v", share)
	}
	viewPath := shareURL[strings.Index(shareURL, "/portal/view/"):]

	resp, body := issue1767Get(t, c.base, viewPath)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s answered %d", viewPath, resp.StatusCode)
	}
	page := string(body)
	header := strings.Index(page, `class="header"`)
	slot := strings.Index(page, `id="content-actions"`)
	info := strings.Index(page, `id="info-toggle"`)
	if header < 0 || slot < 0 || info < 0 {
		t.Fatalf("the share page lacks the header (%d), the controls slot (%d) or the info toggle (%d)", header, slot, info)
	}
	if !(header < slot && slot < info) {
		t.Errorf("the controls slot is not in the header before the info toggle: header %d, slot %d, info %d", header, slot, info)
	}
	if strings.Contains(page, "min-height: 60vh") {
		t.Errorf("the share page still floors the frame at 60vh; the frame fills the page now")
	}
	if !strings.Contains(page, "/portal/view/_assets/") {
		t.Fatalf("the share page carries no viewer bundle; nothing would render the controls")
	}
}
