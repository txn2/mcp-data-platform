//go:build integration

package acceptance

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1802: a CSV over 1 MB gets a thumbnail.
//
// A table's tile is its header row and its first rows -- the tile page keeps
// ten of them -- but the whole document used to be parsed and copied through a
// JavaScript string to produce it, so every table was held to the 1 MB bound
// that exists to keep the renderer from holding a whole document. A CSV is the
// family most likely to be large: a 5 MB export opened in the viewer as a
// sortable table in well under a second and sat in the grid as a content-type
// icon beside a 7 KB CSV that had a proper tile.
//
// The platform now hands the tile page the head of a table and nothing else,
// and the bound rises for the family the way it rose for PDF.
//
// These criteria run through the real surface on the local stack. Files are
// created and replaced with the tools and routes a person uses, the platform
// draws them on its own in the renderer beside it, and the stored PNGs are
// read back from the route the portal reads and compared pixel by pixel.
// Nothing here draws anything itself.
//
// They need the renderer the dev stack starts beside the platform. A platform
// with no renderer reachable draws nothing, and every criterion fails on its
// wait rather than skipping: a gate that skips is a gate that was not run.
//
// Wire forms. manage_resource's action, filename, display_name, path,
// description, content_base64 and content_type are typed strings in its schema
// (pkg/toolkits/portal/schemas.go), and save_asset's name, content,
// content_type and description are too, so each admits exactly one JSON form
// and each is sent below as a literal tools/call parameter of that form. The
// tile route takes its variant as a query-string parameter, which has no
// second form. A replacement file goes through the resource content route as a
// multipart form, which is the form the portal's own upload posts and the only
// one that route takes.

// tileWait1802 bounds how long a criterion waits for the platform to draw a
// tile on its own. The worker polls on an interval, and a render that hangs is
// abandoned at the renderer's own deadline well inside this.
const tileWait1802 = 150 * time.Second

const (
	// headRows1802 is the head of a table the platform hands the tile page:
	// 64 records, which is the header row and 63 rows here.
	headRows1802 = 63
	// bigRows1802 makes a document of about 3 MB, comfortably past the 1 MB
	// bound every family but these is held to.
	bigRows1802 = 60_000
	// assetRows1802 makes an asset of about 1.6 MB. A table's rows here are
	// about 27 bytes, and the criterion checks the size it actually stored
	// rather than trusting this arithmetic.
	assetRows1802 = 60_000
	// oversize1802 is past thumbtypes.LargeSourceLimit (32 MB), which is the
	// bound a table is held to now.
	oversize1802 = 34 << 20
)

func unique1802() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// table1802 is a table of n rows under one header, with word in every row so
// two tables can be told apart by what their tiles show.
func table1802(word string, n int, sep string) string {
	var b strings.Builder
	b.WriteString(strings.Join([]string{"id", "subject", "amount"}, sep) + "\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%d%s%s-%04d%s%d.00\n", i, sep, word, i, sep, i*7)
	}
	return b.String()
}

// createCSVResource1802 puts a small table in the library through the tool a
// person calls, and returns its id.
func createCSVResource1802(t *testing.T, c *client, body, contentType, ext string) string {
	t.Helper()
	name := "acceptance-1802-" + unique1802()
	out := c.call("manage_resource", map[string]any{
		"action":         "create",
		"filename":       name + ext,
		"display_name":   name,
		"path":           "acceptance-1802",
		"description":    "Acceptance #1802: a table the platform draws a tile of.",
		"content_base64": base64.StdEncoding.EncodeToString([]byte(body)),
		"content_type":   contentType,
	})
	id, _ := out["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", out)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })
	return id
}

// resourceRow1802 reads a resource the way the library page does.
func resourceRow1802(t *testing.T, c *client, id string) map[string]any {
	t.Helper()
	status, row := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET resource %s: status %d: %v", id, status, row)
	}
	return row
}

// awaitResourceTile1802 waits until the platform has drawn the file as it
// stands -- a capture recorded later than the one named, which is how a redraw
// of replaced content is told from the tile the file already had -- and fails
// with the recorded reason the moment it says it could not.
func awaitResourceTile1802(t *testing.T, c *client, id, after string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(tileWait1802)
	for {
		row := resourceRow1802(t, c, id)
		if failure, _ := row["thumbnail_failure"].(string); failure != "" {
			t.Fatalf("the platform could not draw resource %s: %s", id, failure)
		}
		if drawn, _ := row["thumbnail_captured_at"].(string); drawn != "" && drawn != after {
			return row
		}
		if time.Now().After(deadline) {
			t.Fatalf("no tile for resource %s after %s. Is the renderer running beside the platform?", id, tileWait1802)
		}
		time.Sleep(time.Second)
	}
}

// putTable1802 replaces a resource's file through the content route the
// portal's own upload posts to, and reports the size the platform stored.
func putTable1802(t *testing.T, c *client, id, filename, body string) int64 {
	t.Helper()
	form := new(bytes.Buffer)
	writer := multipart.NewWriter(form)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("building the upload: %v", err)
	}
	if _, err := part.Write([]byte(body)); err != nil {
		t.Fatalf("writing the upload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("closing the upload: %v", err)
	}
	req, err := http.NewRequestWithContext(c.ctx, http.MethodPost,
		baseURL()+"/api/v1/resources/"+id+"/content", form)
	if err != nil {
		t.Fatalf("building the upload: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		t.Fatalf("uploading the table: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusCreated {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(res.Body)
		t.Fatalf("uploading the table: status %d: %s", res.StatusCode, buf.String())
	}
	row := resourceRow1802(t, c, id)
	size, _ := row["size_bytes"].(float64)
	return int64(size)
}

// tile1802 reads a stored tile and decodes it, failing unless it is a PNG.
func tile1802(t *testing.T, c *client, path string) image.Image {
	t.Helper()
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, baseURL()+path, http.NoBody)
	if err != nil {
		t.Fatalf("building the read of %s: %v", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- the platform under test, named by the suite's own environment
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", path, res.StatusCode, buf.Bytes())
	}
	img, err := png.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("the stored tile at %s is not a PNG: %v", path, err)
	}
	return img
}

// differs1802 is the fraction of pixels in which two tiles disagree. Two tiles
// of the same rows are the same picture; two tiles of different rows are not.
func differs1802(t *testing.T, a, b image.Image) float64 {
	t.Helper()
	if a.Bounds() != b.Bounds() {
		t.Fatalf("the two tiles are different sizes: %v and %v", a.Bounds(), b.Bounds())
	}
	bounds, total, differing := a.Bounds(), 0, 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			total++
			ar, ag, ab, aa := a.At(x, y).RGBA()
			br, bg, bb, ba := b.At(x, y).RGBA()
			if ar != br || ag != bg || ab != bb || aa != ba {
				differing++
			}
		}
	}
	if total == 0 {
		t.Fatal("the tiles hold no pixels")
	}
	return float64(differing) / float64(total)
}

// TestIssue1802_ALargeTableIsDrawnFromItsHead is criterion 1: a table of about
// 3 MB gets a tile, and it is the same picture as the tile of the 64 records
// at its head. That equality is what says the platform drew the head rather
// than the whole document -- and that the tile a reader gets is unchanged.
func TestIssue1802_ALargeTableIsDrawnFromItsHead(t *testing.T) {
	c := connectFor(t, 3*tileWait1802)
	headOnly := table1802("invoice", headRows1802, ",")
	id := createCSVResource1802(t, c, headOnly, "text/csv", ".csv")
	row := awaitResourceTile1802(t, c, id, "")
	headTile := tile1802(t, c, "/api/v1/resources/"+id+"/thumbnail")
	drawnAt, _ := row["thumbnail_captured_at"].(string)

	// The same 64 records, with 60,000 more behind them.
	big := headOnly + table1802("invoice", bigRows1802, ",")[len("id,subject,amount\n"):]
	if size := putTable1802(t, c, id, "big.csv", big); size <= 1<<20 {
		t.Fatalf("the replacement stored %d bytes; this criterion needs a file past the 1 MB bound", size)
	}
	after := awaitResourceTile1802(t, c, id, drawnAt)
	if mime, _ := after["mime_type"].(string); !strings.Contains(mime, "csv") {
		t.Fatalf("the replacement is stored as %q; this criterion no longer tests a table", mime)
	}

	bigTile := tile1802(t, c, "/api/v1/resources/"+id+"/thumbnail")
	if got := differs1802(t, headTile, bigTile); got > 0.01 {
		t.Fatalf("the 3 MB table's tile differs from the tile of its first 64 records in %.1f%% of its pixels; "+
			"it is not being drawn from the head of the file", 100*got)
	}
}

// TestIssue1802_ATilesShowsThisTableNotAGenericOne is criterion 2: two large
// tables get two different tiles. A blank or generic table tile would pass
// criterion 1 for any document; this is what says the tile is the rows.
func TestIssue1802_ATilesShowsThisTableNotAGenericOne(t *testing.T) {
	c := connectFor(t, 3*tileWait1802)
	tiles := map[string]image.Image{}
	for _, word := range []string{"invoice", "shipment"} {
		id := createCSVResource1802(t, c, table1802(word, headRows1802, ","), "text/csv", ".csv")
		row := awaitResourceTile1802(t, c, id, "")
		drawnAt, _ := row["thumbnail_captured_at"].(string)
		if size := putTable1802(t, c, id, word+".csv", table1802(word, bigRows1802, ",")); size <= 1<<20 {
			t.Fatalf("the %s table stored %d bytes; this criterion needs a file past the 1 MB bound", word, size)
		}
		awaitResourceTile1802(t, c, id, drawnAt)
		tiles[word] = tile1802(t, c, "/api/v1/resources/"+id+"/thumbnail")
	}
	if got := differs1802(t, tiles["invoice"], tiles["shipment"]); got < 0.01 {
		t.Fatalf("two large tables with different rows drew tiles differing in %.2f%% of their pixels; "+
			"the tile is not showing the rows", 100*got)
	}
}

// TestIssue1802_ALargeTableGetsBothColorSchemes is criterion 3: a table is
// drawn on the portal's own surface, so it has a tile per color scheme. A file
// owed a dark tile nothing draws is offered for capture forever.
func TestIssue1802_ALargeTableGetsBothColorSchemes(t *testing.T) {
	c := connectFor(t, 3*tileWait1802)
	id := createCSVResource1802(t, c, table1802("ledger", headRows1802, ","), "text/csv", ".csv")
	row := awaitResourceTile1802(t, c, id, "")
	drawnAt, _ := row["thumbnail_captured_at"].(string)
	putTable1802(t, c, id, "ledger.csv", table1802("ledger", bigRows1802, ","))
	after := awaitResourceTile1802(t, c, id, drawnAt)

	if dark, _ := after["thumbnail_dark_s3_key"].(string); dark == "" {
		t.Fatalf("the large table stored no dark capture: %v", after)
	}
	light := tile1802(t, c, "/api/v1/resources/"+id+"/thumbnail")
	darkTile := tile1802(t, c, "/api/v1/resources/"+id+"/thumbnail?variant=dark")
	if got := differs1802(t, light, darkTile); got < 0.5 {
		t.Fatalf("the dark tile differs from the light one in %.1f%% of its pixels; it is the same picture", 100*got)
	}
}

// TestIssue1802_ALargeTSVIsDrawnToo is criterion 4: TSV takes the same path.
// It is the other half of the family, and the ticket raises the bound for both.
func TestIssue1802_ALargeTSVIsDrawnToo(t *testing.T) {
	c := connectFor(t, 3*tileWait1802)
	headOnly := table1802("delivery", headRows1802, "\t")
	id := createCSVResource1802(t, c, headOnly, "text/tab-separated-values", ".tsv")
	row := awaitResourceTile1802(t, c, id, "")
	headTile := tile1802(t, c, "/api/v1/resources/"+id+"/thumbnail")
	drawnAt, _ := row["thumbnail_captured_at"].(string)

	big := headOnly + table1802("delivery", bigRows1802, "\t")[len("id\tsubject\tamount\n"):]
	if size := putTable1802(t, c, id, "big.tsv", big); size <= 1<<20 {
		t.Fatalf("the replacement stored %d bytes; this criterion needs a file past the 1 MB bound", size)
	}
	awaitResourceTile1802(t, c, id, drawnAt)
	if got := differs1802(t, headTile, tile1802(t, c, "/api/v1/resources/"+id+"/thumbnail")); got > 0.01 {
		t.Fatalf("the large TSV's tile differs from the tile of its first 64 records in %.1f%% of its pixels", 100*got)
	}
}

// TestIssue1802_ALargeCSVAssetGetsATile is criterion 5: the Assets gallery
// draws one too. Assets and managed resources are offered work by two separate
// queries, and the bound is applied in both.
func TestIssue1802_ALargeCSVAssetGetsATile(t *testing.T) {
	c := connectFor(t, 3*tileWait1802)
	saved := c.call("save_asset", map[string]any{
		"name":         "acceptance-1802-asset-" + unique1802(),
		"content":      table1802("order", assetRows1802, ","),
		"content_type": "text/csv",
		"description":  "Acceptance #1802: a CSV asset past the bound every other family has.",
	})
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id})
	})

	deadline := time.Now().Add(tileWait1802)
	for {
		status, row := c.rest(http.MethodGet, "/api/v1/portal/assets/"+id, http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("GET asset %s: status %d: %v", id, status, row)
		}
		if size, _ := row["size_bytes"].(float64); int64(size) <= 1<<20 {
			t.Fatalf("the asset holds %v bytes; this criterion needs one past the 1 MB bound", size)
		}
		if failure, _ := row["thumbnail_failure"].(string); failure != "" {
			t.Fatalf("the platform could not draw asset %s: %s", id, failure)
		}
		version, _ := row["current_version"].(float64)
		if drawn, _ := row["thumbnail_version"].(float64); drawn >= version && drawn > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no tile for the CSV asset %s after %s. Is the renderer running beside the platform?", id, tileWait1802)
		}
		time.Sleep(time.Second)
	}
	tile1802(t, c, "/api/v1/portal/assets/"+id+"/thumbnail")
}

// TestIssue1802_ATablePastTheRaisedBoundKeepsItsIcon is criterion 6: the
// source bound rises for this family and still exists. A table past it is
// never offered for capture -- no tile and no recorded failure, because
// nothing was attempted -- which is what leaves the card showing its
// content-type icon.
func TestIssue1802_ATablePastTheRaisedBoundKeepsItsIcon(t *testing.T) {
	c := connectFor(t, 3*tileWait1802)
	id := createCSVResource1802(t, c, table1802("archive", headRows1802, ","), "text/csv", ".csv")
	row := awaitResourceTile1802(t, c, id, "")
	oldTile, _ := row["thumbnail_captured_at"].(string)

	// The tile it already has is of the old file, so what is asserted is that
	// no NEW one is drawn.
	var b strings.Builder
	b.WriteString(table1802("archive", headRows1802, ","))
	filler := table1802("archive", 20_000, ",")
	for b.Len() < oversize1802 {
		b.WriteString(filler)
	}
	if size := putTable1802(t, c, id, "archive.csv", b.String()); size < oversize1802 {
		t.Fatalf("the replacement stored %d bytes, want at least %d; this criterion no longer tests the bound",
			size, oversize1802)
	}

	// Give the worker several polling rounds to prove it does NOT take the
	// work, rather than proving it has not taken it yet.
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		after := resourceRow1802(t, c, id)
		if drawn, _ := after["thumbnail_captured_at"].(string); drawn != oldTile {
			t.Fatalf("a table of %d bytes was drawn at %s; it is past the bound and must never be offered", oversize1802, drawn)
		}
		if failure, _ := after["thumbnail_failure"].(string); failure != "" {
			t.Fatalf("a table past the bound recorded the failure %q; it must never be offered at all", failure)
		}
		time.Sleep(3 * time.Second)
	}
}
