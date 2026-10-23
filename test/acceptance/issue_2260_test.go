//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Issue #2260: adding an asset to a collection from the asset viewer. The
// criteria run through the portal routes the viewer's Add to collection
// picker calls, against the running platform and its database.
//
// Wire forms: the append's body is an object with asset_id (a string) and
// section_id (a string, or absent for the last section), and both shapes are
// sent; the new collection's body carries asset_id as a string; search is a
// query string.

func issue2260Asset(t *testing.T, c *client, label string) string {
	t.Helper()
	out := c.call("save_asset", map[string]any{
		"name":         fmt.Sprintf("acceptance-2260-%s-%d", label, time.Now().UnixNano()),
		"content":      "# Quarterly revenue\n\nNorth 41, South 17.\n",
		"content_type": "text/markdown",
		"description":  "Acceptance #2260: an asset filed into a collection from its viewer.",
	})
	id, _ := out["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", out)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
	return id
}

// issue2260Collection makes a collection with two sections, the first holding
// one asset, and returns its id and the two section ids.
func issue2260Collection(t *testing.T, c *client, name, existing string) (string, string, string) {
	t.Helper()
	status, created := c.rest(http.MethodPost, "/api/v1/portal/collections", jsonBody(t, map[string]any{"name": name, "description": "kept as written"}))
	if status != http.StatusCreated {
		t.Fatalf("creating the collection: %d %v", status, created)
	}
	id, _ := created["id"].(string)
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/portal/collections/"+id, http.NoBody) })
	status, coll := c.rest(http.MethodPut, "/api/v1/portal/collections/"+id+"/sections", jsonBody(t, map[string]any{
		"sections": []any{
			map[string]any{"title": "Summary", "items": []any{map[string]any{"asset_id": existing}}},
			map[string]any{"title": "Appendix", "items": []any{}},
		},
	}))
	if status != http.StatusOK {
		t.Fatalf("setting the sections: %d %v", status, coll)
	}
	secs := coll["sections"].([]any)
	return id, secs[0].(map[string]any)["id"].(string), secs[1].(map[string]any)["id"].(string)
}

func issue2260Target(t *testing.T, c *client, assetID, collID, search string) map[string]any {
	t.Helper()
	path := "/api/v1/portal/assets/" + assetID + "/collection-targets"
	if search != "" {
		path += "?search=" + search
	}
	status, out := c.rest(http.MethodGet, path, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("collection-targets: %d %v", status, out)
	}
	for _, d := range out["data"].([]any) {
		if m := d.(map[string]any); m["id"] == collID {
			return m
		}
	}
	return nil
}

// TestIssue2260_AddingPutsTheAssetAtTheEndOfTheChosenSection: the picker's
// list holds the collection with its sections, the append lands at the end of
// the section and changes nothing else, a second add to that section is
// refused, and with no section the last one is used.
func TestIssue2260_AddingPutsTheAssetAtTheEndOfTheChosenSection(t *testing.T) {
	c := connect(t)
	existing := issue2260Asset(t, c, "existing")
	asset := issue2260Asset(t, c, "viewed")
	name := fmt.Sprintf("Acceptance 2260 %d", time.Now().UnixNano())
	collID, summary, appendix := issue2260Collection(t, c, name, existing)

	target := issue2260Target(t, c, asset, collID, "")
	if target == nil || len(target["sections"].([]any)) != 2 {
		t.Fatalf("the picker's list does not hold the collection with its two sections: %v", target)
	}
	if issue2260Target(t, c, asset, collID, "no-such-collection-name") != nil {
		t.Error("search did not narrow the list")
	}

	status, added := c.rest(http.MethodPost, "/api/v1/portal/collections/"+collID+"/items",
		jsonBody(t, map[string]any{"asset_id": asset, "section_id": summary}))
	if status != http.StatusCreated || added["section_title"] != "Summary" {
		t.Fatalf("append: %d %v", status, added)
	}

	status, coll := c.rest(http.MethodGet, "/api/v1/portal/collections/"+collID, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the collection: %d", status)
	}
	if coll["name"] != name || coll["description"] != "kept as written" {
		t.Errorf("the collection's name or description changed: %v / %v", coll["name"], coll["description"])
	}
	secs := coll["sections"].([]any)
	first := secs[0].(map[string]any)["items"].([]any)
	if len(first) != 2 || first[0].(map[string]any)["asset_id"] != existing || first[1].(map[string]any)["asset_id"] != asset {
		t.Errorf("Summary holds %v; want the existing item then the added one", first)
	}
	if len(secs[1].(map[string]any)["items"].([]any)) != 0 {
		t.Error("the other section changed")
	}

	status, _ = c.rest(http.MethodPost, "/api/v1/portal/collections/"+collID+"/items",
		jsonBody(t, map[string]any{"asset_id": asset, "section_id": summary}))
	if status != http.StatusConflict {
		t.Errorf("a second add to the same section answered %d; want 409", status)
	}

	marked := issue2260Target(t, c, asset, collID, "")
	if marked["sections"].([]any)[0].(map[string]any)["has_asset"] != true {
		t.Errorf("the picker does not mark the section that holds the asset: %v", marked)
	}

	status, added = c.rest(http.MethodPost, "/api/v1/portal/collections/"+collID+"/items",
		jsonBody(t, map[string]any{"asset_id": asset}))
	if status != http.StatusCreated || added["section_id"] != appendix {
		t.Errorf("an add naming no section landed in %v (%d); want the last section", added["section_id"], status)
	}
}

// TestIssue2260_NewCollectionHoldsTheAsset: New collection creates the
// collection with the asset as its first item, in one step.
func TestIssue2260_NewCollectionHoldsTheAsset(t *testing.T) {
	c := connect(t)
	asset := issue2260Asset(t, c, "new")
	status, created := c.rest(http.MethodPost, "/api/v1/portal/collections",
		jsonBody(t, map[string]any{"name": fmt.Sprintf("Acceptance 2260 new %d", time.Now().UnixNano()), "asset_id": asset}))
	if status != http.StatusCreated {
		t.Fatalf("creating with an asset: %d %v", status, created)
	}
	id, _ := created["id"].(string)
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/portal/collections/"+id, http.NoBody) })
	_, coll := c.rest(http.MethodGet, "/api/v1/portal/collections/"+id, http.NoBody)
	secs, _ := coll["sections"].([]any)
	if len(secs) != 1 {
		t.Fatalf("the new collection has %d sections; want one", len(secs))
	}
	items := secs[0].(map[string]any)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["asset_id"] != asset {
		t.Errorf("the new collection holds %v; want the asset", items)
	}
}

// TestIssue2260_SomeoneWhoCannotEditSeesNeitherTheCollectionNorTheWrite: a
// person with no edit share on the collection does not see it in their list
// and cannot add to it.
func TestIssue2260_SomeoneWhoCannotEditSeesNeitherTheCollectionNorTheWrite(t *testing.T) {
	admin := connect(t)
	existing := issue2260Asset(t, admin, "owner")
	collID, summary, _ := issue2260Collection(t, admin, fmt.Sprintf("Acceptance 2260 private %d", time.Now().UnixNano()), existing)

	peer := connectAs(t, devPeerAPIKey)
	theirs := issue2260Asset(t, peer, "peer")
	if issue2260Target(t, peer, theirs, collID, "") != nil {
		t.Error("a collection the person cannot edit is in their list")
	}
	status, _ := peer.rest(http.MethodPost, "/api/v1/portal/collections/"+collID+"/items",
		jsonBody(t, map[string]any{"asset_id": theirs, "section_id": summary}))
	if status != http.StatusForbidden {
		t.Errorf("an add by someone who cannot edit answered %d; want 403", status)
	}
}
