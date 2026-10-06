//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #2028: fetch of a knowledge page lists only the references the caller
// can open. The owner key saves two assets, the administrator promotes a page
// citing both, and the owner shares one with the peer key; the peer fetches
// the page.
//
// Wire forms: save_asset's `name`, `content`, `content_type` and `description`
// are strings; apply_knowledge's `action`, `sink` and `confirm` are typed
// string/string/boolean and `page` an object whose `references` is an array
// of strings; fetch's `reference` and `purpose` are strings. Each admits that
// one form, sent as a literal tools/call param.

func asset2028(t *testing.T, owner *client, label string) string {
	t.Helper()
	saved := owner.call("save_asset", map[string]any{
		"name": fmt.Sprintf("acc-2028-%s-%d", label, time.Now().UnixNano()), "content": "# " + label + "\n",
		"content_type": "text/markdown", "description": "Acceptance #2028: a page cites this.",
	})
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset id: %v", saved)
	}
	t.Cleanup(func() { _, _, _ = owner.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
	return id
}

func TestIssue2028_AFetchedPageWithholdsAReferenceTheReaderCannotOpen(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	shared, private := asset2028(t, owner, "shared"), asset2028(t, owner, "private")
	status, share := owner.rest("POST", "/api/v1/portal/assets/"+shared+"/shares",
		jsonBody(t, map[string]any{"shared_with_email": devPeerEmailAddr, "permission": "viewer"}))
	if status != 200 && status != 201 {
		t.Fatalf("share the asset with the peer: HTTP %d: %v", status, share)
	}
	pageID := promote1855(t, admin, "The weekly reports.", []any{"mcp:asset:" + shared, "mcp:asset:" + private})

	doc := peer.call("fetch", map[string]any{
		"reference": "mcp:knowledge_page:" + pageID,
		"purpose":   "Acceptance #2028: the page's references as the peer may open them.",
	})
	raw := fmt.Sprintf("%v", doc)
	if strings.Contains(raw, private) {
		t.Errorf("the page hands the peer the id of an asset not shared with them: %v", doc)
	}
	if !strings.Contains(raw, "mcp:asset:"+shared) {
		t.Errorf("the asset shared with the peer is not listed: %v", doc)
	}
	document, _ := doc["document"].(map[string]any)
	if document["references_withheld"] != float64(1) {
		t.Errorf("references_withheld = %v, want 1", document["references_withheld"])
	}

	doc = owner.call("fetch", map[string]any{
		"reference": "mcp:knowledge_page:" + pageID,
		"purpose":   "Acceptance #2028: the page's references as their owner opens them.",
	})
	if raw := fmt.Sprintf("%v", doc); !strings.Contains(raw, "mcp:asset:"+private) || strings.Contains(raw, "references_withheld") {
		t.Errorf("the owner does not get every reference: %v", doc)
	}
}
