//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"testing"
	"time"
)

// Issue #1903: objects the platform wrote and then lost track of, or wrote
// where configuration said it should not.
//
// What these hold, against the dev stack's own buckets, read back with the
// s3_list tool a person calls:
//
//   - A managed resource revised after each draw, then deleted, leaves no
//     object under its key directory; while it lives, only the current head
//     carries tiles (a redraw removes the previous head's).
//   - A trino_export replayed with its idempotency key stores one content
//     object for the asset.
//   - Every writer of asset content stores it under portal.s3_prefix: the
//     portal's create, content edit and copy, the admin console's content
//     edit, save_asset, and the collection mosaic. The dev stack sets no
//     prefix, so it is the default, "artifacts/"; the portal routes wrote a
//     fixed "portal/" before.
//
// Wire forms: manage_resource's action, filename, display_name, path, content,
// content_type and reference, trino_export's sql, format, name and idempotency_key,
// save_asset's name, content and content_type, and s3_list's connection,
// bucket and prefix are typed strings, so each admits one JSON form and is
// sent as a literal tools/call parameter of it. manage_asset's sections is a
// typed array of objects. The portal and admin content routes take the raw
// body; the portal create route takes a JSON object.

const (
	issue1903Purpose        = "Acceptance for #1903: platform objects are removed with what names them and written under the configured prefix."
	issue1903Prefix         = "artifacts/"
	issue1903ResourceConn   = "dev-resources"
	issue1903ResourceBucket = "managed-resources"
	issue1903PortalConn     = "dev-s3"
	issue1903PortalBucket   = "portal-assets"
)

// issue1903List lists every key under prefix through the s3_list tool.
func issue1903List(t *testing.T, c *client, conn, bucket, prefix string) []string {
	t.Helper()
	res, text, err := c.callRaw("s3_list", map[string]any{
		"connection": conn, "bucket": bucket, "prefix": prefix, "purpose": issue1903Purpose,
	})
	if err != nil || res.IsError {
		t.Fatalf("s3_list %s/%s: %v %s", bucket, prefix, err, text)
	}
	var out struct {
		Objects []struct {
			Key string `json:"key"`
		} `json:"objects"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("s3_list result is not the expected object: %v\n%s", err, text)
	}
	keys := make([]string, 0, len(out.Objects))
	for _, o := range out.Objects {
		keys = append(keys, o.Key)
	}
	return keys
}

func issue1903IsTile(key string) bool {
	return strings.Contains(path.Base(key), "thumbnail")
}

// TestIssue1903_ADeletedResourceLeavesNothingUnderItsKeyDirectory is the
// first acceptance sentence.
func TestIssue1903_ADeletedResourceLeavesNothingUnderItsKeyDirectory(t *testing.T) {
	c := connectFor(t, 4*tileWait1787)
	name := "acceptance-1903-" + unique1787()
	out := c.call("manage_resource", map[string]any{
		"action": "create", "filename": name + ".csv", "display_name": name, "path": "acceptance-1903",
		"description": "Acceptance #1903: a file revised after each draw, then deleted.",
		"content":     "region,revenue\nwest,1\n", "content_type": "text/csv",
	})
	id, _ := out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	deleted := false
	t.Cleanup(func() {
		if !deleted {
			_, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody)
		}
	})
	awaitResourceTile1787(t, c, id, true)
	for i, body := range []string{"region,revenue\nwest,2\n", "region,revenue\nwest,3\n"} {
		c.call("manage_resource", map[string]any{
			"action": "replace_content", "reference": "mcp:resource:" + id, "content": body, "content_type": "text/csv",
		})
		row := awaitResourceTile1787(t, c, id, true)
		if i == 1 {
			issue1903OnlyTheHeadHasTiles(t, c, id, row)
		}
	}

	_, row := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody)
	dir := issue1903KeyDir(t, row, id)
	if status, body := c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody); status != http.StatusNoContent && status != http.StatusOK {
		t.Fatalf("DELETE resource %s: HTTP %d %v", id, status, body)
	}
	deleted = true
	if left := issue1903List(t, c, issue1903ResourceConn, issue1903ResourceBucket, dir); len(left) != 0 {
		t.Errorf("objects left under %s after the resource was deleted: %v", dir, left)
	}
}

// issue1903OnlyTheHeadHasTiles checks that a redraw removed the tiles drawn
// beside the previous heads: every tile under the resource's directory sits
// beside the key the row names.
func issue1903OnlyTheHeadHasTiles(t *testing.T, c *client, id string, row map[string]any) {
	t.Helper()
	head, _ := row["s3_key"].(string)
	keys := issue1903List(t, c, issue1903ResourceConn, issue1903ResourceBucket, issue1903KeyDir(t, row, id))
	headTiles := 0
	for _, key := range keys {
		if issue1903IsTile(key) && path.Dir(key) != path.Dir(head) {
			t.Errorf("tile %s sits beside an earlier revision; the head is %s", key, head)
		}
		if issue1903IsTile(key) && path.Dir(key) == path.Dir(head) {
			headTiles++
		}
	}
	// The listing has to show what it is about, or every check above and the
	// empty listing after the delete would pass on a listing that read nothing.
	if !slices.Contains(keys, head) || headTiles == 0 {
		t.Fatalf("the listing under the resource's directory %v does not show its head %s and its tiles", keys, head)
	}
}

// issue1903KeyDir is the directory every object of the resource is stored
// under: its head key up to and including its id.
func issue1903KeyDir(t *testing.T, row map[string]any, id string) string {
	t.Helper()
	key, _ := row["s3_key"].(string)
	i := strings.Index(key, "/"+id+"/")
	if i < 0 {
		t.Fatalf("resource %s key %q does not contain its id", id, key)
	}
	return key[:i+len(id)+2]
}

// TestIssue1903_AReplayedKeyedExportStoresOneObject is the second sentence.
func TestIssue1903_AReplayedKeyedExportStoresOneObject(t *testing.T) {
	c := connect(t)
	key := "acc-1903-" + unique1787()
	var ids []string
	for range 2 {
		out := c.call("trino_export", map[string]any{
			"sql": "SELECT 1 AS n", "format": "csv", "name": "acceptance-1903-" + key,
			"idempotency_key": key, "purpose": issue1903Purpose,
		})
		id, _ := out["asset_id"].(string)
		if id == "" {
			t.Fatalf("trino_export returned no asset_id: %v", out)
		}
		ids = append(ids, id)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": ids[0]}) })
	if ids[0] != ids[1] {
		t.Fatalf("the replay made a second asset: %v", ids)
	}
	row := assetRow1787(t, c, ids[0])
	s3Key, _ := row["s3_key"].(string)
	var content []string
	for _, k := range issue1903List(t, c, issue1903PortalConn, issue1903PortalBucket, path.Dir(s3Key)+"/") {
		if !issue1903IsTile(k) {
			content = append(content, k)
		}
	}
	if len(content) != 1 {
		t.Errorf("the replayed export's directory holds %d content objects %v; want the one its asset names", len(content), content)
	}
}

// TestIssue1903_EveryAssetWriterUsesTheConfiguredPrefix is the third sentence,
// for every route that writes asset content.
func TestIssue1903_EveryAssetWriterUsesTheConfiguredPrefix(t *testing.T) {
	c := connectFor(t, 3*tileWait1787)
	under := func(t *testing.T, what, key string) {
		t.Helper()
		if !strings.HasPrefix(key, issue1903Prefix) {
			t.Errorf("%s stored %q, outside %q", what, key, issue1903Prefix)
		}
	}

	status, created := c.rest(http.MethodPost, "/api/v1/portal/assets", jsonBody(t, map[string]any{
		"name": "acceptance-1903-portal-" + unique1787(), "content_type": "text/markdown", "content": "# created in the portal\n",
	}))
	if status != http.StatusCreated {
		t.Fatalf("portal create: HTTP %d %v", status, created)
	}
	portalID, _ := created["id"].(string)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": portalID}) })
	under(t, "the portal's create", fmt.Sprint(created["s3_key"]))

	for route, what := range map[string]string{
		"/api/v1/portal/assets/" + portalID + "/content": "the portal's content edit",
		"/api/v1/admin/assets/" + portalID + "/content":  "the admin console's content edit",
	} {
		if status, body := c.rest(http.MethodPut, route, strings.NewReader("# edited through "+what+"\n")); status != http.StatusOK {
			t.Fatalf("%s: HTTP %d %v", what, status, body)
		}
		under(t, what, fmt.Sprint(assetRow1787(t, c, portalID)["s3_key"]))
	}

	status, copied := c.rest(http.MethodPost, "/api/v1/portal/assets/"+portalID+"/copy", http.NoBody)
	if status != http.StatusCreated {
		t.Fatalf("portal copy: HTTP %d %v", status, copied)
	}
	copyID, _ := copied["id"].(string)
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": copyID}) })
	under(t, "the portal's copy", fmt.Sprint(copied["s3_key"]))

	saved := saveAsset1789(t, c, "text/html", "<!DOCTYPE html><html><body><h1>saved</h1></body></html>")
	under(t, "save_asset", fmt.Sprint(assetRow1787(t, c, saved)["s3_key"]))

	awaitAssetTile1787(t, c, saved, false)
	out := c.call("manage_asset", map[string]any{
		"action": "create_collection", "name": "acceptance-1903-" + unique1787(),
		"description": "Acceptance #1903: a collection whose mosaic is stored under the prefix.",
		"sections":    []any{map[string]any{"title": "Members", "items": []any{map[string]any{"asset_id": saved}}}},
	})
	coll, _ := out["collection_id"].(string)
	if coll == "" {
		t.Fatalf("create_collection returned no id: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete_collection", "collection_id": coll})
	})
	deadline := time.Now().Add(tileWait1787)
	for {
		_, row := c.rest(http.MethodGet, "/api/v1/portal/collections/"+coll, http.NoBody)
		if key, _ := row["thumbnail_s3_key"].(string); key != "" {
			under(t, "the collection mosaic", key)
			if want := issue1903Prefix + "collections/" + coll + "/"; !strings.HasPrefix(key, want) {
				t.Errorf("the mosaic is stored at %q, want it under %q", key, want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no mosaic for collection %s after %s", coll, tileWait1787)
		}
		time.Sleep(time.Second)
	}
}
