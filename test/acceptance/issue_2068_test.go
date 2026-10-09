//go:build integration

package acceptance

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq" // the lock criterion holds the fetch lock from a session of its own
)

// Issue #2068: a map is an HTML asset drawn on a map runtime and a street
// basemap the platform serves itself. What is held here, through the surfaces
// an administrator, an agent and a reader meet:
//
//   - with maps off, the settings API says so, the archive route answers 404,
//     and the maps knowledge page says the deployment has no basemap;
//   - adding a bounding-box region queues a fetch that moves through fetching
//     to ready, extracting from the real Protomaps build, and the archive the
//     bucket then holds reports the requested bounds and zooms in its header;
//   - a second replica does not fetch while the fetch lock is held;
//   - a fetch from an unreachable source fails with the reason, and the
//     region's ready archive keeps serving;
//   - a .pmtiles file put in the bucket by hand is ready with no fetch;
//   - a route map saved with save_asset gets a thumbnail showing the basemap
//     and the route, light and dark, drawn by the platform's own renderer;
//   - the route map exports to PDF with the map drawn, not a blank canvas;
//   - a choropleth from the served us-atlas boundaries is drawn with maps off;
//   - fetch returns the maps page and platform_info names it.
//
// The share pages and the browser-side checks (no request leaves the
// platform's origin, no policy refusal, drawing with every other host
// unreachable, http and https) are held by ui/e2e/public-viewer/maps.spec.ts
// against the same stack; the transcript at build/2068/acceptance.md records
// both runs.
//
// Wire forms: platform_info takes an empty object and an absent object; both
// are sent. fetch's `reference` and `purpose`, and save_asset's `name`,
// `content`, `content_type` and `description` are `{"type":"string"}` in their
// schemas, so each admits one JSON form and is sent as a JSON string. The maps
// admin routes are REST, not tools; each body is sent as the JSON object its
// route decodes, and a bounds field as an object of four numbers, the one form
// the route accepts.

const (
	issue2068MapsPath = "/api/v1/admin/settings/maps"
	issue2068PageRef  = "mcp:knowledge_page:platform-maps"
	issue2068Purpose  = "Acceptance for #2068: proving a map can be drawn on the basemap this deployment serves."
	issue2068LockKey  = 4713210301
	issue2068Bucket   = "managed-resources"
)

// issue2068Box is a few blocks of downtown San Francisco, extracted to a low
// zoom so the fetch is seconds; the source is the full planet build.
var issue2068Box = map[string]any{"min_lon": -122.42, "min_lat": 37.77, "max_lon": -122.39, "max_lat": 37.79}

type issue2068Region struct {
	ID            string `json:"id"`
	Origin        string `json:"origin"`
	State         string `json:"state"`
	Error         string `json:"error"`
	TotalBytes    int64  `json:"total_bytes"`
	ProgressBytes int64  `json:"progress_bytes"`
	Archive       *struct {
		Build   string `json:"build"`
		MaxZoom int    `json:"max_zoom"`
		Size    int64  `json:"size_bytes"`
	} `json:"archive"`
}

type issue2068View struct {
	Settings struct {
		Enabled   bool   `json:"enabled"`
		MaxZoom   int    `json:"max_zoom"`
		SourceURL string `json:"source_url"`
	} `json:"settings"`
	Regions []issue2068Region `json:"regions"`
}

// issue2068Do is an authenticated admin request against one base URL.
func issue2068Do(t *testing.T, base, method, path string, body any) (int, []byte) {
	t.Helper()
	var r io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, base+path, r)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	req.Header.Set("X-API-Key", devAPIKey())
	req.Header.Set("Content-Type", "application/json")
	res, err := (&http.Client{Timeout: 3 * time.Minute}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close() //nolint:errcheck // read below
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

func issue2068GetView(t *testing.T) issue2068View {
	t.Helper()
	status, raw := issue2068Do(t, baseURL(), http.MethodGet, issue2068MapsPath, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: %d %s", issue2068MapsPath, status, raw)
	}
	var v issue2068View
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decoding the maps view: %v\n%s", err, raw)
	}
	return v
}

func issue2068GetRegion(t *testing.T, id string) (issue2068Region, bool) {
	t.Helper()
	for _, r := range issue2068GetView(t).Regions {
		if r.ID == id {
			return r, true
		}
	}
	return issue2068Region{}, false
}

// issue2068Settings writes the settings, keeping the others as they are.
func issue2068Settings(t *testing.T, base string, enabled bool, maxZoom int, source string) {
	t.Helper()
	status, raw := issue2068Do(t, base, http.MethodPut, issue2068MapsPath, map[string]any{
		"enabled": enabled, "s3_connection": "", "bucket": "", "max_zoom": maxZoom, "source_url": source,
	})
	if status != http.StatusOK {
		t.Fatalf("PUT %s: %d %s", issue2068MapsPath, status, raw)
	}
}

// issue2068Restore puts the settings back the way the test found them.
func issue2068Restore(t *testing.T) {
	t.Helper()
	before := issue2068GetView(t).Settings
	t.Cleanup(func() {
		issue2068Settings(t, baseURL(), before.Enabled, before.MaxZoom, before.SourceURL)
	})
}

func issue2068AddBox(t *testing.T, id string) {
	t.Helper()
	status, raw := issue2068Do(t, baseURL(), http.MethodPost, issue2068MapsPath+"/regions", map[string]any{
		"id": id, "name": "Acceptance " + id, "bounds": issue2068Box,
	})
	if status != http.StatusCreated {
		t.Fatalf("adding region %s: %d %s", id, status, raw)
	}
	t.Cleanup(func() {
		if st, body := issue2068Do(t, baseURL(), http.MethodDelete, issue2068MapsPath+"/regions/"+id, nil); st != http.StatusNoContent {
			t.Logf("deleting region %s: %d %s", id, st, body)
		}
	})
}

// issue2068Await polls the region until it reaches want, failing on any
// other settled state.
func issue2068Await(t *testing.T, id, want string, within time.Duration) issue2068Region {
	t.Helper()
	deadline := time.Now().Add(within)
	var seen []string
	for {
		r, ok := issue2068GetRegion(t, id)
		if !ok {
			t.Fatalf("region %s is gone", id)
		}
		if len(seen) == 0 || seen[len(seen)-1] != r.State {
			seen = append(seen, r.State)
		}
		if r.State == want {
			t.Logf("region %s: %s", id, strings.Join(seen, " -> "))
			return r
		}
		if (r.State == "ready" || r.State == "failed") && r.State != want {
			t.Fatalf("region %s settled %s (%s), want %s; states seen %v", id, r.State, r.Error, want, seen)
		}
		if time.Now().After(deadline) {
			t.Fatalf("region %s did not reach %s within %s; states seen %v", id, want, within, seen)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// issue2068Archive is an unauthenticated ranged read of a region's archive,
// what a map in a share frame sends.
func issue2068Archive(t *testing.T, id, rng string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, baseURL()+"/portal/maps/"+id+".pmtiles", http.NoBody)
	if rng != "" {
		req.Header.Set("Range", rng)
	}
	req.Header.Set("Origin", "null")
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("reading the %s archive: %v", id, err)
	}
	defer res.Body.Close() //nolint:errcheck // read below
	body, _ := io.ReadAll(res.Body)
	return res, body
}

// issue2068Page fetches the maps page as an agent does.
func issue2068Page(t *testing.T, c *client) string {
	t.Helper()
	res, text, err := c.callRaw("fetch", map[string]any{"reference": issue2068PageRef, "purpose": issue2068Purpose})
	if err != nil || res.IsError {
		t.Fatalf("fetch %s: %v %s", issue2068PageRef, err, text)
	}
	var out struct {
		Found    bool `json:"found"`
		Document struct {
			Body string `json:"body"`
		} `json:"document"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil || !out.Found {
		t.Fatalf("fetch %s: %v\n%s", issue2068PageRef, err, text)
	}
	return out.Document.Body
}

func issue2068Name(prefix string) string {
	return prefix + "-" + strconv.FormatInt(time.Now().UnixNano()%1_000_000_000, 36)
}

func TestIssue2068_DisabledServesNothingAndSaysSo(t *testing.T) {
	c := connect(t)
	issue2068Restore(t)
	issue2068Settings(t, baseURL(), false, 14, "")

	if v := issue2068GetView(t); v.Settings.Enabled {
		t.Fatal("the settings API reports maps enabled after turning them off")
	}
	if res, _ := issue2068Archive(t, "san-francisco", "bytes=0-126"); res.StatusCode != http.StatusNotFound {
		t.Errorf("with maps off the archive route answered %d, want 404", res.StatusCode)
	}
	page := issue2068Page(t, c)
	if !strings.Contains(page, "This deployment has no basemap.") {
		t.Errorf("with maps off the maps page does not say there is no basemap:\n%s", page[:min(len(page), 1500)])
	}
}

func TestIssue2068_ABoxRegionIsFetchedIntoTheBucket(t *testing.T) {
	issue2068Restore(t)
	issue2068Settings(t, baseURL(), true, 10, "")
	id := issue2068Name("acc-box")
	issue2068AddBox(t, id)

	r := issue2068Await(t, id, "ready", 3*time.Minute)
	if r.Archive == nil || r.Archive.MaxZoom != 10 || r.Archive.Build == "" {
		t.Fatalf("ready region's archive = %+v", r.Archive)
	}

	res, head := issue2068Archive(t, id, "bytes=0-126")
	if res.StatusCode != http.StatusPartialContent || len(head) != 127 || string(head[:7]) != "PMTiles" {
		t.Fatalf("the archive's header read = %d, %d bytes %q", res.StatusCode, len(head), head[:min(len(head), 7)])
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("the archive route did not answer an opaque origin: %v", res.Header)
	}
	minZ, maxZ := head[100], head[101]
	e7 := func(off int) float64 { return float64(int32(binary.LittleEndian.Uint32(head[off:]))) / 1e7 } //nolint:gosec // the header's two's-complement field
	if minZ != 0 || maxZ != 10 {
		t.Errorf("header zooms = %d..%d, want 0..10", minZ, maxZ)
	}
	got := []float64{e7(102), e7(106), e7(110), e7(114)}
	want := []float64{-122.42, 37.77, -122.39, 37.79}
	for i := range want {
		if d := got[i] - want[i]; d > 1e-6 || d < -1e-6 {
			t.Errorf("header bounds = %v, want %v", got, want)
			break
		}
	}
}

func TestIssue2068_ASecondReplicaDoesNotFetchWhileTheLockIsHeld(t *testing.T) {
	issue2068Restore(t)
	issue2068Settings(t, baseURL(), true, 10, "")
	pair := replicas(t)

	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("a session for the lock: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	var got bool
	// A replica mid-pass holds it briefly; wait it out.
	for i := 0; i < 60 && !got; i++ {
		if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", issue2068LockKey).Scan(&got); err != nil {
			t.Fatalf("taking the fetch lock: %v", err)
		}
		if !got {
			time.Sleep(time.Second)
		}
	}
	if !got {
		t.Fatal("the fetch lock was never free to take")
	}
	released := false
	release := func() {
		if !released {
			_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", issue2068LockKey)
			released = true
		}
	}
	t.Cleanup(release)

	id := issue2068Name("acc-lock")
	issue2068AddBox(t, id)
	// Wake both replicas' fetch loops: a settings write starts a pass on the
	// replica that took it.
	for _, r := range pair {
		issue2068Settings(t, r.base, true, 10, "")
	}
	for i := 0; i < 6; i++ {
		time.Sleep(time.Second)
		if r, _ := issue2068GetRegion(t, id); r.State != "queued" {
			t.Fatalf("with the fetch lock held elsewhere, region %s moved to %s", id, r.State)
		}
	}

	release()
	issue2068Settings(t, pair[1].base, true, 10, "")
	issue2068Await(t, id, "ready", 3*time.Minute)
}

func TestIssue2068_AnUnreachableSourceFailsAndTheArchiveKeepsServing(t *testing.T) {
	issue2068Restore(t)
	issue2068Settings(t, baseURL(), true, 10, "")
	id := issue2068Name("acc-fail")
	issue2068AddBox(t, id)
	ready := issue2068Await(t, id, "ready", 3*time.Minute)

	issue2068Settings(t, baseURL(), true, 10, "http://127.0.0.1:1/planet.pmtiles")
	if status, raw := issue2068Do(t, baseURL(), http.MethodPost, issue2068MapsPath+"/regions/"+id+"/refresh", nil); status != http.StatusAccepted {
		t.Fatalf("refresh: %d %s", status, raw)
	}
	failed := issue2068Await(t, id, "failed", 3*time.Minute)
	if !strings.Contains(failed.Error, "127.0.0.1:1") {
		t.Errorf("the failure does not say what failed: %q", failed.Error)
	}
	if failed.Archive == nil || failed.Archive.Size != ready.Archive.Size {
		t.Errorf("the failed refresh changed the archive being served: %+v", failed.Archive)
	}
	if res, head := issue2068Archive(t, id, "bytes=0-126"); res.StatusCode != http.StatusPartialContent || string(head[:7]) != "PMTiles" {
		t.Errorf("after the failed refresh the archive answered %d", res.StatusCode)
	}
}

func TestIssue2068_AnArchiveUploadedByHandIsReadyWithNoFetch(t *testing.T) {
	issue2068Restore(t)
	issue2068Settings(t, baseURL(), true, 14, "")
	id := issue2068Name("acc-upload")
	key := "maps/uploads/" + id + ".pmtiles"
	src, err := filepath.Abs("../../dev/seed-content/san-francisco.pmtiles")
	if err != nil {
		t.Fatal(err)
	}
	s3 := func(args ...string) {
		t.Helper()
		port := os.Getenv("DEV_S3_TLS_PORT")
		if port == "" {
			port = "9443"
		}
		ca, _ := filepath.Abs("../../dev/.tls/minio/public.crt")
		cmd := exec.Command("aws", append([]string{"--endpoint-url", "https://localhost:" + port, "s3"}, args...)...)
		cmd.Env = append(os.Environ(), "AWS_ACCESS_KEY_ID=dev-access-key", "AWS_SECRET_ACCESS_KEY=dev-secret-key", "AWS_CA_BUNDLE="+ca)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("aws s3 %v: %v\n%s", args, err, out)
		}
	}
	s3("cp", "--only-show-errors", src, "s3://"+issue2068Bucket+"/"+key)
	t.Cleanup(func() { s3("rm", "--only-show-errors", "s3://"+issue2068Bucket+"/"+key) })

	// The next pass lists it; a settings write starts one now.
	issue2068Settings(t, baseURL(), true, 14, "")
	deadline := time.Now().Add(90 * time.Second)
	for {
		if r, ok := issue2068GetRegion(t, id); ok {
			if r.Origin != "upload" || r.State != "ready" || r.Archive == nil || r.Archive.MaxZoom != 12 {
				t.Fatalf("the uploaded archive is listed as %+v", r)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the uploaded archive %s was not listed", key)
		}
		time.Sleep(time.Second)
	}
	if res, head := issue2068Archive(t, id, "bytes=0-126"); res.StatusCode != http.StatusPartialContent || string(head[:7]) != "PMTiles" {
		t.Errorf("the uploaded region's archive answered %d", res.StatusCode)
	}
}

// issue2068Tile waits for an asset's tile in one variant and decodes it.
func issue2068Tile(t *testing.T, c *client, assetID, variant string) image.Image {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for {
		status, raw := issue2068Do(t, baseURL(), http.MethodGet, "/api/v1/portal/assets/"+assetID+"/thumbnail?variant="+variant, nil)
		if status == http.StatusOK {
			img, err := png.Decode(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("the %s tile is not a PNG: %v", variant, err)
			}
			return img
		}
		if st, asset := c.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID, http.NoBody); st == http.StatusOK {
			if reason, _ := asset["thumbnail_failure"].(string); reason != "" {
				t.Fatalf("the renderer recorded the asset as not drawable: %s", reason)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s tile for %s within 3m (last status %d)", variant, assetID, status)
		}
		time.Sleep(2 * time.Second)
	}
}

// issue2068Count counts pixels within tol of a color, and the distinct colors
// (quantized) the tile holds.
func issue2068Count(img image.Image, r0, g0, b0 int, tol int) (near, distinct int) {
	seen := map[[3]int]bool{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			ri, gi, bi := int(r>>8), int(g>>8), int(bl>>8)
			if issue2068Abs(ri-r0) <= tol && issue2068Abs(gi-g0) <= tol && issue2068Abs(bi-b0) <= tol {
				near++
			}
			seen[[3]int{ri / 16, gi / 16, bi / 16}] = true
		}
	}
	return near, len(seen)
}

func issue2068Abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestIssue2068_ARouteMapsThumbnailShowsTheBasemapAndTheRoute(t *testing.T) {
	issue2068Restore(t)
	issue2068Settings(t, baseURL(), true, 14, "")
	c := connect(t)
	doc, err := os.ReadFile("../../dev/seed-content/asset-008.html")
	if err != nil {
		t.Fatal(err)
	}
	out := c.call("save_asset", map[string]any{
		"name": issue2068Name("Acceptance route map"), "content_type": "text/html", "content": string(doc),
		"description": "Route map for the #2068 acceptance run",
	})
	assetID, _ := out["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("save_asset returned no asset_id: %v", out)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/portal/assets/"+assetID, http.NoBody) })

	for _, variant := range []string{"light", "dark"} {
		img := issue2068Tile(t, c, assetID, variant)
		route, distinct := issue2068Count(img, 0xd6, 0x33, 0x6c, 24)
		if route < 200 {
			t.Errorf("the %s tile shows %d pixels of the route's color; the route was not drawn", variant, route)
		}
		if distinct < 40 {
			t.Errorf("the %s tile holds %d distinct colors; a basemap of streets, water and labels holds far more", variant, distinct)
		}
		t.Logf("%s tile: %d route pixels, %d distinct colors", variant, route, distinct)
	}
}

// TestIssue2068_ARouteMapExportsToPDFWithTheMapDrawn: Export PDF prints the
// document in the platform's renderer once it reports ready. A map draws its
// tiles after the document loads, and printed at load the map is an empty
// canvas: that PDF was 18,848 bytes, the title and the attribution alone. The
// print step now waits for the map to finish drawing, so the PDF carries the
// drawn map as an image, hundreds of kilobytes.
func TestIssue2068_ARouteMapExportsToPDFWithTheMapDrawn(t *testing.T) {
	issue2068Restore(t)
	issue2068Settings(t, baseURL(), true, 14, "")
	c := connect(t)
	doc, err := os.ReadFile("../../dev/seed-content/asset-008.html")
	if err != nil {
		t.Fatal(err)
	}
	out := c.call("save_asset", map[string]any{
		"name": issue2068Name("Acceptance route map PDF"), "content_type": "text/html", "content": string(doc),
		"description": "Route map PDF for the #2068 acceptance run",
	})
	assetID, _ := out["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("save_asset returned no asset_id: %v", out)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/portal/assets/"+assetID, http.NoBody) })

	status, pdf := issue2068Do(t, baseURL(), http.MethodGet, "/api/v1/portal/assets/"+assetID+"/pdf", nil)
	if status != http.StatusOK || !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatalf("the PDF export answered %d with %d bytes", status, len(pdf))
	}
	if len(pdf) < 150_000 {
		t.Errorf("the PDF is %d bytes; a map printed before it drew is about 19,000, one printed drawn several hundred thousand", len(pdf))
	}
	t.Logf("route map PDF: %d bytes", len(pdf))
}

func TestIssue2068_AChoroplethIsDrawnWithMapsOff(t *testing.T) {
	issue2068Restore(t)
	issue2068Settings(t, baseURL(), false, 14, "")
	c := connect(t)
	doc, err := os.ReadFile("../../dev/seed-content/asset-009.html")
	if err != nil {
		t.Fatal(err)
	}
	out := c.call("save_asset", map[string]any{
		"name": issue2068Name("Acceptance choropleth"), "content_type": "text/html", "content": string(doc),
		"description": "Choropleth for the #2068 acceptance run",
	})
	assetID, _ := out["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("save_asset returned no asset_id: %v", out)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/portal/assets/"+assetID, http.NoBody) })

	img := issue2068Tile(t, c, assetID, "light")
	// California and Texas are the darkest step.
	if dark, _ := issue2068Count(img, 0x1d, 0x4e, 0xd8, 20); dark < 500 {
		t.Errorf("the choropleth tile shows %d pixels of its darkest step; the states were not drawn", dark)
	}
}

func TestIssue2068_AnAgentIsPointedAtTheMapsPage(t *testing.T) {
	issue2068Restore(t)
	issue2068Settings(t, baseURL(), true, 14, "")
	c := connect(t)
	for _, args := range []map[string]any{{}, nil} {
		res, text, err := c.callRaw("platform_info", args)
		if err != nil || res.IsError {
			t.Fatalf("platform_info(%v): %v %s", args, err, text)
		}
		if !strings.Contains(text, issue2068PageRef) || !strings.Contains(text, "a map: HTML on the served map runtime") {
			t.Errorf("platform_info(%v) does not name the maps page for a map request", args)
		}
	}
	page := issue2068Page(t, c)
	for _, want := range []string{"/portal/vendor/maplibre/maplibre-gl.js", "/portal/maps/san-francisco.pmtiles", "OpenStreetMap"} {
		if !strings.Contains(page, want) {
			t.Errorf("the maps page does not name %s", want)
		}
	}
	if strings.Contains(page, "This deployment has no basemap.") {
		t.Error("with maps on and a region ready the page says there is no basemap")
	}
}
