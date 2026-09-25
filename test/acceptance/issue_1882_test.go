//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Issue #1882: an Excel workbook written by a script's platform.export showed
// the text of its zip bytes as its tile. The rule deciding which types get a
// tile matched a family's name anywhere in the type, and every Office Open XML
// type contains "xml", so a workbook, a Word document and a presentation were
// offered to the renderer as XML.
//
// What these hold, against the running platform and its renderer: the Excel
// workbook asset a script's platform.export writes, and an .xlsx, .docx and
// .pptx resource, are never drawn and never tried -- no tile, no failure --
// while the renderer draws the files around them; and an XML asset and an XML
// dialect named by its +xml suffix still get a tile. save_asset accepts neither
// a Word or PowerPoint type nor an XML dialect, so the workbook export is the
// one path that stores an Office asset, and the dialect is filed as a resource.
// A tile stored before the change is cleared by migration 000162, whose real
// Postgres test is pkg/database/migrate/migrate_office_tiles_realpg_test.go:
// the running server is already past it, so it has nothing to clear here.
//
// The worker draws one document at a time, newest first. Each undrawn file is
// filed before a control file, so the control is claimed first and, once it is
// drawn, the worker has asked for its next document -- which is the Office file
// if it is owed one. A second control filed after the first is drawn is then
// behind anything the worker took in between, so once it is drawn the Office
// file's row says what the worker did with it.
//
// Wire forms: manage_script's `command`, `name`, `description` and `source`
// are typed strings and `params` an array of objects; run_script's `name` is a
// string, `args` an object and `wait_seconds` an integer; manage_resource's
// `action`, `filename`, `display_name`, `path`, `description`, `content` and
// `content_type` are typed strings and `tags` an array of strings; save_asset's
// `name`, `content`, `content_type` and `description` are typed strings;
// manage_asset's `action` and `asset_id` are strings. Each admits one JSON form
// and is sent in it as a literal tools/call parameter.

// officeTypes1882 are the three Office Open XML types the criteria name.
var officeTypes1882 = []struct{ name, ext, contentType string }{
	{"xlsx", ".xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
	{"docx", ".docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	{"pptx", ".pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
}

// zipBody1882 is the head of a zip container. The type is what the rule
// decides on, and what an Office file stores is a zip.
const zipBody1882 = "PK\x03\x04\x14\x00\x06\x00[Content_Types].xml xl/worksheets/sheet1.xml\n"

// issue1882Source is a script exporting a one-sheet workbook, the path the
// reported asset was written by. %q is the output name.
const issue1882Source = `
platform.export(%q, {"sheets": [{"name": "Summary", "rows": [{"region": "west", "sales": 1200}]}]}, format="xlsx")
`

func unique1882() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// exportWorkbook1882 runs a script that writes an Excel workbook asset and
// returns the asset's id.
func exportWorkbook1882(t *testing.T, c *client) string {
	t.Helper()
	name := "acc-1882-" + unique1882()
	c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": fmt.Sprintf(issue1882Source, name),
		"description": "Acceptance #1882: a workbook written by platform.export gets no tile.",
		"params":      []any{map[string]any{"name": "day", "type": "string"}},
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })

	out := c.call("run_script", map[string]any{"name": name, "args": map[string]any{}, "wait_seconds": 120})
	if status, _ := out["status"].(string); status != "succeeded" {
		t.Fatalf("the export run did not succeed: %v", out)
	}
	outputs, _ := out["outputs"].([]any)
	if len(outputs) != 1 {
		t.Fatalf("outputs = %v; want the one workbook", out["outputs"])
	}
	output, _ := outputs[0].(map[string]any)
	id, _ := output["asset_id"].(string)
	if id == "" {
		t.Fatalf("the export output names no asset: %v", output)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
	return id
}

// awaitAssetControls1882 files a control asset, waits for its tile, then files
// and waits for a second, so every asset filed before them has been claimed if
// it was owed.
func awaitAssetControls1882(t *testing.T, c *client) {
	t.Helper()
	for i := range 2 {
		id := saveAsset1787(t, c, "text/plain", fmt.Sprintf("control %d, drawn after the Office files\n", i))
		awaitAssetTile1787(t, c, id, false)
	}
}

// awaitResourceControls1882 is the same for resources.
func awaitResourceControls1882(t *testing.T, c *client) {
	t.Helper()
	for i := range 2 {
		id := createResource1754(t, c, ".txt", "text/plain", fmt.Sprintf("control %d, drawn after the Office files\n", i))
		awaitResourceTile1787(t, c, id, false)
	}
}

// assertUndrawn1882 fails when a row carries a tile or a recorded failure.
func assertUndrawn1882(t *testing.T, what string, row map[string]any) {
	t.Helper()
	for _, key := range []string{"thumbnail_s3_key", "thumbnail_dark_s3_key", "thumbnail_failure"} {
		if v, _ := row[key].(string); v != "" {
			t.Errorf("%s has %s = %q; an Office file is never drawn or tried", what, key, v)
		}
	}
}

// TestIssue1882_AnExportedWorkbookAssetIsNeverDrawn: the workbook a script's
// platform.export writes, the asset the ticket was reported against, keeps its
// content-type icon.
func TestIssue1882_AnExportedWorkbookAssetIsNeverDrawn(t *testing.T) {
	c := connectFor(t, 5*tileWait1787)

	id := exportWorkbook1882(t, c)
	awaitAssetControls1882(t, c)

	row := assetRow1787(t, c, id)
	if got, _ := row["content_type"].(string); got != officeTypes1882[0].contentType {
		t.Fatalf("the workbook asset is stored as %q; want %q", got, officeTypes1882[0].contentType)
	}
	assertUndrawn1882(t, "the workbook asset", row)
}

// TestIssue1882_OfficeResourcesAreNeverDrawn: the same of managed resources.
func TestIssue1882_OfficeResourcesAreNeverDrawn(t *testing.T) {
	c := connectFor(t, 5*tileWait1787)

	ids := map[string]string{}
	for _, o := range officeTypes1882 {
		ids[o.name] = createResource1754(t, c, o.ext, o.contentType, zipBody1882)
	}
	awaitResourceControls1882(t, c)

	for _, o := range officeTypes1882 {
		status, row := c.rest(http.MethodGet, "/api/v1/resources/"+ids[o.name], http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("GET the %s resource: status %d: %v", o.name, status, row)
		}
		if got, _ := row["mime_type"].(string); got != o.contentType {
			t.Fatalf("the %s resource is stored as %q; want %q", o.name, got, o.contentType)
		}
		assertUndrawn1882(t, "the "+o.name+" resource", row)
	}
}

// TestIssue1882_XMLStillGetsATile: the XML family is drawn by its own type and
// by a dialect's +xml suffix, in both schemes.
func TestIssue1882_XMLStillGetsATile(t *testing.T) {
	c := connectFor(t, 5*tileWait1787)

	const xml = "<report><row id=\"1\">41208</row></report>\n"
	const atom = "<?xml version=\"1.0\"?><feed xmlns=\"http://www.w3.org/2005/Atom\"><title>t</title></feed>\n"
	asset := saveAsset1787(t, c, "application/xml", xml)
	dialect := createResource1754(t, c, ".atom", "application/atom+xml", atom)

	awaitAssetTile1787(t, c, asset, true)
	awaitResourceTile1787(t, c, dialect, true)
}
