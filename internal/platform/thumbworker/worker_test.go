package thumbworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/internal/headless"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/viewerlimit"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// --- fakes -----------------------------------------------------------------

type fakeDrawer struct {
	mu      sync.Mutex
	pingErr error
	pages   []headless.Page
	// results is consumed one per Render; past its end every render succeeds.
	results []error
	png     []byte
}

func (d *fakeDrawer) Ping(context.Context) error { return d.pingErr }

func (d *fakeDrawer) Render(_ context.Context, p headless.Page) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pages = append(d.pages, p)
	if len(d.results) > 0 {
		err := d.results[0]
		d.results = d.results[1:]
		if err != nil {
			return nil, err
		}
	}
	if d.png != nil {
		return d.png, nil
	}
	return []byte("PNG"), nil
}

type fakeBlobs struct {
	mu      sync.Mutex
	objects map[string][]byte
	deleted []string
	getErr  error
	putErr  error
}

func newBlobs() *fakeBlobs { return &fakeBlobs{objects: map[string][]byte{}} }

func (b *fakeBlobs) GetObject(_ context.Context, bucket, key string) (data []byte, contentType string, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.getErr != nil {
		return nil, "", b.getErr
	}
	data, ok := b.objects[bucket+"/"+key]
	if !ok {
		return nil, "", errors.New("no such object")
	}
	return data, "", nil
}

func (b *fakeBlobs) PutObject(_ context.Context, bucket, key string, data []byte, _ string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.putErr != nil {
		return b.putErr
	}
	b.objects[bucket+"/"+key] = data
	return nil
}

func (b *fakeBlobs) DeleteObject(_ context.Context, bucket, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deleted = append(b.deleted, bucket+"/"+key)
	delete(b.objects, bucket+"/"+key)
	return nil
}

// hold is one claim ended without a result.
type hold struct {
	hold     time.Duration
	attempts int
}

type fakeAssets struct {
	mu      sync.Mutex
	claims  [][]portaldomain.Asset
	byID    map[string]*portaldomain.Asset
	updates map[string]portaldomain.AssetUpdate
	holds   map[string]hold
	holdErr error
}

func (f *fakeAssets) HoldThumbnailWork(_ context.Context, id string, d time.Duration, attempts int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.holdErr != nil {
		return f.holdErr
	}
	if f.holds == nil {
		f.holds = map[string]hold{}
	}
	f.holds[id] = hold{d, attempts}
	return nil
}

func (f *fakeAssets) update(id string) (portaldomain.AssetUpdate, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.updates[id]
	return u, ok
}

func (f *fakeAssets) held(id string) (hold, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.holds[id]
	return h, ok
}

func (f *fakeAssets) ClaimThumbnailWork(context.Context, int, time.Duration, int) ([]portaldomain.Asset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.claims) == 0 {
		return nil, nil
	}
	c := f.claims[0]
	f.claims = f.claims[1:]
	return c, nil
}

func (f *fakeAssets) Update(_ context.Context, id string, u portaldomain.AssetUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updates == nil {
		f.updates = map[string]portaldomain.AssetUpdate{}
	}
	f.updates[id] = u
	return nil
}

func (f *fakeAssets) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	if a, ok := f.byID[id]; ok {
		return a, nil
	}
	return nil, errors.New("not found")
}

type fakeRefs struct {
	refs []assetrefs.Ref
	err  error
}

func (f fakeRefs) ListByAsset(context.Context, string) ([]assetrefs.Ref, error) { return f.refs, f.err }

type fakeCollections struct {
	claims    []portaldomain.CollectionThumbnailWork
	recorded  map[string][2]string
	failures  map[string][2]string
	holds     map[string]hold
	recordErr error
}

func (f *fakeCollections) RecordCollectionThumbnailFailure(_ context.Context, id, source, reason string) error {
	if f.recordErr != nil {
		return f.recordErr
	}
	if f.failures == nil {
		f.failures = map[string][2]string{}
	}
	f.failures[id] = [2]string{source, reason}
	return nil
}

func (f *fakeCollections) HoldCollectionThumbnailWork(_ context.Context, id string, d time.Duration, attempts int) error {
	if f.holds == nil {
		f.holds = map[string]hold{}
	}
	f.holds[id] = hold{d, attempts}
	return nil
}

func (f *fakeCollections) ClaimCollectionThumbnailWork(context.Context, time.Duration, int) ([]portaldomain.CollectionThumbnailWork, error) {
	c := f.claims
	f.claims = nil
	return c, nil
}

func (f *fakeCollections) RecordCollectionThumbnail(_ context.Context, id, key, source string) error {
	if f.recordErr != nil {
		return f.recordErr
	}
	if f.recorded == nil {
		f.recorded = map[string][2]string{}
	}
	f.recorded[id] = [2]string{key, source}
	return nil
}

type fakeResources struct {
	mu       sync.Mutex
	claims   []resource.Resource
	captures []resource.ThumbnailCapture
	holds    []hold
	failure  string
	failedAt time.Time
	setErr   error
	failErr  error
	claimErr error
	holdErr  error
}

func (f *fakeResources) HoldThumbnailWork(_ context.Context, _ string, d time.Duration, attempts int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.holdErr != nil {
		return f.holdErr
	}
	f.holds = append(f.holds, hold{d, attempts})
	return nil
}

func (f *fakeResources) captured() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.captures)
}

func (f *fakeResources) ClaimThumbnailWork(context.Context, int, time.Duration, int) ([]resource.Resource, error) {
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	c := f.claims
	f.claims = nil
	return c, nil
}

func (f *fakeResources) RecordThumbnailFailure(_ context.Context, _, reason string, at time.Time) error {
	if f.failErr != nil {
		return f.failErr
	}
	f.failure, f.failedAt = reason, at
	return nil
}

func (f *fakeResources) SetThumbnail(_ context.Context, _ string, t resource.ThumbnailCapture) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	f.captures = append(f.captures, t)
	return nil
}

// --- helpers ---------------------------------------------------------------

const bucket = "portal-assets"

func asset(id, ct string, version int) portaldomain.Asset {
	return portaldomain.Asset{
		ID: id, ContentType: ct, S3Bucket: bucket, CurrentVersion: version, Name: id,
		S3Key: "artifacts/owner/" + id + "/v" + fmt.Sprint(version) + "/content",
	}
}

func worker(d *fakeDrawer, assets *fakeAssets, blobs *fakeBlobs) *Worker {
	return New(Tuning{}, Deps{
		Drawer: d, Assets: assets, AssetBlobs: blobs, CollectionBucket: bucket,
		TileEntryURL: "/portal/view/_assets/tile-entry-X.js", TileCSS: ".x{}",
	})
}

func payload(t *testing.T, p headless.Page) map[string]any {
	t.Helper()
	doc := string(p.Document)
	start := strings.Index(doc, `id="tile-data">`) + len(`id="tile-data">`)
	end := strings.Index(doc[start:], "</script>")
	var data map[string]any
	if err := json.Unmarshal([]byte(doc[start:start+end]), &data); err != nil {
		t.Fatalf("tile payload is not JSON: %v", err)
	}
	return data
}

// --- assets ----------------------------------------------------------------

// An HTML document is drawn at page size, reduced to the stored tile size, in
// each color scheme: its own prefers-color-scheme rules answer to the scheme
// the renderer emulates, as they do in the viewer's frame (#1789).
func TestDrawAsset_AnHTMLDocumentIsDrawnAtPageSizeInBothSchemesAndRecorded(t *testing.T) {
	d, assets, blobs := &fakeDrawer{}, &fakeAssets{}, newBlobs()
	a := asset("a1", "text/html", 3)
	a.ThumbnailS3Key = "artifacts/owner/a1/v2/.thumbnail.png"
	blobs.objects[bucket+"/"+a.S3Key] = []byte("<p>hi</p>")
	worker(d, assets, blobs).drawAsset(context.Background(), a)

	if len(d.pages) != 2 || d.pages[0].Dark || !d.pages[1].Dark {
		t.Fatalf("rendered %d pages, want a light then a dark tile for HTML", len(d.pages))
	}
	for _, p := range d.pages {
		if p.Width != pageWidth || p.Height != pageHeight || p.Scale != pageScale {
			t.Errorf("an HTML document was drawn at %dx%d scale %v", p.Width, p.Height, p.Scale)
		}
		if w := int(float64(p.Width) * p.Scale); w != 800 {
			t.Errorf("an HTML tile is stored %d pixels wide, want 800", w)
		}
	}
	want := portaldomain.DeriveThumbnailKeyVariant(a.S3Key, portaldomain.ThumbnailVariantLight)
	if _, ok := blobs.objects[bucket+"/"+want]; !ok {
		t.Fatalf("the tile was not stored at %s", want)
	}
	wantDark := portaldomain.DeriveThumbnailKeyVariant(a.S3Key, portaldomain.ThumbnailVariantDark)
	if _, ok := blobs.objects[bucket+"/"+wantDark]; !ok {
		t.Fatalf("the dark tile was not stored at %s", wantDark)
	}
	u := assets.updates["a1"]
	if u.ThumbnailS3Key == nil || *u.ThumbnailS3Key != want || *u.ThumbnailVersion != 3 {
		t.Errorf("recorded key/version = %v/%v", u.ThumbnailS3Key, u.ThumbnailVersion)
	}
	if u.ThumbnailDarkS3Key == nil || *u.ThumbnailDarkS3Key != wantDark || *u.ThumbnailDarkVersion != 3 {
		t.Errorf("recorded dark key/version = %v/%v", u.ThumbnailDarkS3Key, u.ThumbnailDarkVersion)
	}
	if u.ThumbnailRenderer == nil || *u.ThumbnailRenderer != Renderer || *u.ThumbnailFailure != "" || *u.ThumbnailFailedVersion != 0 || !u.ReleaseThumbnailClaim {
		t.Errorf("a drawn tile must record the renderer, clear any failure and end the lease: %+v", u)
	}
	if len(blobs.deleted) != 1 || blobs.deleted[0] != bucket+"/"+a.ThumbnailS3Key {
		t.Errorf("deleted %v, want the superseded tile", blobs.deleted)
	}
}

func TestDrawAsset_AThemeableDocumentGetsBothSchemes(t *testing.T) {
	d, assets, blobs := &fakeDrawer{}, &fakeAssets{}, newBlobs()
	a := asset("a2", "text/markdown", 1)
	blobs.objects[bucket+"/"+a.S3Key] = []byte("# hi")
	worker(d, assets, blobs).drawAsset(context.Background(), a)

	if len(d.pages) != 2 || d.pages[0].Dark || !d.pages[1].Dark {
		t.Fatalf("pages = %d, want a light then a dark render", len(d.pages))
	}
	if p := d.pages[0]; p.Width != tileWidth || p.Scale != tileScale {
		t.Errorf("markdown was drawn at %dx%d scale %v, want the tile's own size at twice the density", p.Width, p.Height, p.Scale)
	}
	if !strings.Contains(string(d.pages[1].Document), `class="dark"`) {
		t.Error("the dark page does not carry the dark theme")
	}
	u := assets.updates["a2"]
	if u.ThumbnailS3Key == nil || u.ThumbnailDarkS3Key == nil {
		t.Fatalf("both variants must be recorded: %+v", u)
	}
}

func TestDrawAsset_ADocumentThatCannotBeDrawnIsRecordedAsSuch(t *testing.T) {
	d := &fakeDrawer{results: []error{errors.New("headless: the document did not finish drawing before the deadline")}}
	assets, blobs := &fakeAssets{}, newBlobs()
	a := asset("a3", "text/html", 4)
	blobs.objects[bucket+"/"+a.S3Key] = []byte("<script>while(true){}</script>")
	worker(d, assets, blobs).drawAsset(context.Background(), a)

	u := assets.updates["a3"]
	if u.ThumbnailFailure == nil || *u.ThumbnailFailure != "the document did not finish drawing before the deadline" {
		t.Fatalf("recorded failure = %v", u.ThumbnailFailure)
	}
	if *u.ThumbnailFailedVersion != 4 || u.ThumbnailRenderer != nil || u.ThumbnailS3Key != nil || !u.ReleaseThumbnailClaim {
		t.Errorf("a failure is dated to the version tried, stores nothing and ends the lease: %+v", u)
	}
}

func TestDrawAsset_ALightTileSurvivesADarkFailure(t *testing.T) {
	d := &fakeDrawer{results: []error{nil, errors.New("headless: TypeError")}}
	assets, blobs := &fakeAssets{}, newBlobs()
	a := asset("a4", "text/csv", 1)
	blobs.objects[bucket+"/"+a.S3Key] = []byte("a,b\n1,2\n")
	worker(d, assets, blobs).drawAsset(context.Background(), a)

	u := assets.updates["a4"]
	if u.ThumbnailS3Key == nil || u.ThumbnailDarkS3Key != nil || u.ThumbnailFailure == nil || *u.ThumbnailFailure != "TypeError" {
		t.Fatalf("want the light tile kept and the failure recorded: %+v", u)
	}
}

// An attempt that does not finish records nothing itself and says why, for
// the attempt to hold the asset back or give up on it (#1868).
func TestDrawAsset_AnUnfinishedAttemptSaysWhyAndRecordsNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*fakeDrawer, *fakeBlobs)
		want  string
	}{
		{"the renderer went away", func(d *fakeDrawer, _ *fakeBlobs) { d.results = []error{headless.ErrUnavailable} }, "not available"},
		{"the document cannot be read", func(_ *fakeDrawer, b *fakeBlobs) { b.getErr = errors.New("NoSuchKey") }, "the stored file could not be read"},
		{"the tile cannot be stored", func(_ *fakeDrawer, b *fakeBlobs) { b.putErr = errors.New("s3 down") }, "storing the tile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, assets, blobs := &fakeDrawer{}, &fakeAssets{}, newBlobs()
			a := asset("a5", "text/html", 1)
			blobs.objects[bucket+"/"+a.S3Key] = []byte("<p>x</p>")
			tc.setup(d, blobs)
			err := worker(d, assets, blobs).drawAsset(context.Background(), a)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("drawAsset = %v, want it to say %q", err, tc.want)
			}
			if _, ok := assets.updates["a5"]; ok {
				t.Fatal("an unfinished attempt recorded a result")
			}
		})
	}
}

// A light tile drawn before the dark one could not be is kept, and the claim
// is left for the attempt to end: releasing it here would clear the count
// that stops a document pinning the renderer forever (#1868).
func TestDrawAsset_ALightTileBeforeAnUnfinishedDarkOneKeepsTheClaim(t *testing.T) {
	d := &fakeDrawer{results: []error{nil, fmt.Errorf("headless: %w", headless.ErrUnavailable)}}
	assets, blobs := &fakeAssets{}, newBlobs()
	a := asset("a6", "text/html", 2)
	blobs.objects[bucket+"/"+a.S3Key] = []byte("<p>x</p>")
	if err := worker(d, assets, blobs).drawAsset(context.Background(), a); !errors.Is(err, headless.ErrUnavailable) {
		t.Fatalf("drawAsset = %v, want the dark variant's reason", err)
	}
	u := assets.updates["a6"]
	if u.ThumbnailS3Key == nil || u.ThumbnailDarkS3Key != nil || u.ThumbnailFailure == nil || *u.ThumbnailFailure != "" {
		t.Fatalf("want the light tile recorded and no failure: %+v", u)
	}
	if u.ReleaseThumbnailClaim {
		t.Error("the claim was released with the attempt unfinished")
	}
}

func TestDrawAsset_ATileTooLargeIsAFailure(t *testing.T) {
	d := &fakeDrawer{png: make([]byte, portaldomain.MaxThumbnailBytes+1)}
	assets, blobs := &fakeAssets{}, newBlobs()
	a := asset("a6", "text/html", 1)
	blobs.objects[bucket+"/"+a.S3Key] = []byte("<p>x</p>")
	worker(d, assets, blobs).drawAsset(context.Background(), a)
	if u := assets.updates["a6"]; u.ThumbnailFailure == nil || !strings.Contains(*u.ThumbnailFailure, "larger than a tile") {
		t.Fatalf("failure = %v", u.ThumbnailFailure)
	}
}

func TestDrawAsset_ReferencesPointAtTheReferenceRoute(t *testing.T) {
	d, assets, blobs := &fakeDrawer{}, &fakeAssets{}, newBlobs()
	a := asset("a7", "text/html", 1)
	blobs.objects[bucket+"/"+a.S3Key] = []byte(`<img src="mcp://global/brand/logo.svg">`)
	w := worker(d, assets, blobs)
	w.deps.Refs = fakeRefs{refs: []assetrefs.Ref{{AssetID: "a7", URI: "mcp://global/brand/logo.svg", RefToken: "tok123"}}}
	w.drawAsset(context.Background(), a)

	content, _ := payload(t, d.pages[0])["content"].(string)
	if !strings.Contains(content, "/portal/refs/a7/tok123") || strings.Contains(content, "mcp://") {
		t.Fatalf("the document's reference was not pointed at the reference route: %q", content)
	}
}

// A table's tile is its header row and its first rows, so the tile page is
// handed the head of the file rather than the whole of it (#1802). Before
// this, a CSV over 1 MB was never offered for capture at all -- the other
// 99.9% of it was parsed and discarded, which is what the bound refused.
func TestDrawAsset_ATableIsDrawnFromItsHead(t *testing.T) {
	d, assets, blobs := &fakeDrawer{}, &fakeAssets{}, newBlobs()
	a := asset("a20", "text/csv", 1)
	whole := csvOf(200_000)
	blobs.objects[bucket+"/"+a.S3Key] = []byte(whole)
	worker(d, assets, blobs).drawAsset(context.Background(), a)

	if len(d.pages) == 0 {
		t.Fatal("nothing was drawn")
	}
	content, _ := payload(t, d.pages[0])["content"].(string)
	if content == whole {
		t.Fatalf("the whole %d-byte document was handed to the tile page", len(whole))
	}
	if !strings.HasPrefix(whole, content) {
		t.Fatal("what the tile page was handed is not a prefix of the document")
	}
	// The rows the tile draws are all there.
	if !strings.HasPrefix(content, "id,name\n") || !strings.Contains(content, "10,row 10\n") {
		t.Errorf("the head does not hold the header and the first ten rows: %q", content[:min(80, len(content))])
	}
}

// Every other family is laid out in full to be drawn, so a prefix of one would
// be a document with its tail cut off.
func TestDrawAsset_ADocumentLaidOutInFullIsHandedWhole(t *testing.T) {
	d, assets, blobs := &fakeDrawer{}, &fakeAssets{}, newBlobs()
	a := asset("a21", "text/markdown", 1)
	whole := csvOf(200_000)
	blobs.objects[bucket+"/"+a.S3Key] = []byte(whole)
	worker(d, assets, blobs).drawAsset(context.Background(), a)

	if content, _ := payload(t, d.pages[0])["content"].(string); content != whole {
		t.Fatalf("the tile page was handed %d of %d bytes", len(content), len(whole))
	}
}

// The same of a managed resource, which is the half of the library a large CSV
// most often arrives in.
func TestDrawResource_ATableIsDrawnFromItsHead(t *testing.T) {
	d, blobs, res := &fakeDrawer{}, newBlobs(), &fakeResources{}
	r := resource.Resource{
		ID: "r20", MIMEType: "text/tab-separated-values", S3Key: "resources/r20/export.tsv",
		UpdatedAt: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC),
	}
	whole := csvOf(200_000)
	blobs.objects["res-bucket/"+r.S3Key] = []byte(whole)
	New(Tuning{}, Deps{Drawer: d, Resources: res, ResourceBlobs: blobs, ResourceBucket: "res-bucket"}).
		drawResource(context.Background(), r)

	if len(d.pages) == 0 {
		t.Fatal("nothing was drawn")
	}
	content, _ := payload(t, d.pages[0])["content"].(string)
	if content == whole || !strings.HasPrefix(whole, content) {
		t.Fatalf("the tile page was handed %d of %d bytes, and not as a prefix", len(content), len(whole))
	}
}

func TestDrawAsset_AnImageIsServedToThePageByURL(t *testing.T) {
	d, assets, blobs := &fakeDrawer{}, &fakeAssets{}, newBlobs()
	a := asset("a8", "image/png", 1)
	blobs.objects[bucket+"/"+a.S3Key] = []byte("\x89PNG-bytes")
	worker(d, assets, blobs).drawAsset(context.Background(), a)

	if len(d.pages) != 1 || d.pages[0].Dark {
		t.Fatalf("rendered %d pages, want one tile: a raster image is drawn as stored", len(d.pages))
	}
	p := d.pages[0]
	data := payload(t, p)
	if fromURL, _ := data["serveFromURL"].(bool); !fromURL || data["content"] != nil {
		t.Fatalf("an image must reach the page by URL, not inline: %v", data)
	}
	f, ok := p.Files(contentPath)
	if !ok || string(f.Body) != "\x89PNG-bytes" || f.ContentType != "image/png" {
		t.Fatalf("the page's content route served %v %q", ok, f.Body)
	}
}

// An SVG is drawn as stored: one tile, served in both schemes. It contains
// "xml" and is still not the themeable XML family.
func TestDrawAsset_AnSVGGetsOneTile(t *testing.T) {
	d, assets, blobs := &fakeDrawer{}, &fakeAssets{}, newBlobs()
	a := asset("a9", "image/svg+xml", 1)
	blobs.objects[bucket+"/"+a.S3Key] = []byte("<svg/>")
	worker(d, assets, blobs).drawAsset(context.Background(), a)

	if len(d.pages) != 1 || d.pages[0].Dark {
		t.Fatalf("rendered %d pages, want one light tile", len(d.pages))
	}
	if u := assets.updates["a9"]; u.ThumbnailS3Key == nil || u.ThumbnailDarkS3Key != nil {
		t.Errorf("an SVG must record one tile and no dark one: %+v", u)
	}
}

// --- the page --------------------------------------------------------------

func TestTileDocument_ContentCannotCloseTheScriptItIsIn(t *testing.T) {
	doc := string(tileDocument(map[string]any{"content": `</script><script>alert(1)</script>`}, false, "/e.js", ".a{}"))
	if strings.Count(doc, "</script>") != 2 {
		t.Fatalf("the payload closed its script element early:\n%s", doc)
	}
	if !strings.Contains(string(tileDocument(nil, false, "/e.js", "</style><b>")), `<\/style><b>`) {
		t.Error("a stylesheet containing </style could close the style element")
	}
}

func TestFiles_OnlyThePublicPrefixesAreAnsweredInProcess(t *testing.T) {
	routes := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/portal/refs/a1/tok":
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte("<svg/>"))
		case "/portal/vendor/reveal/reveal.js":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("reveal"))
		case "/api/v1/admin/system/info":
			_, _ = w.Write([]byte("secret"))
		default:
			http.NotFound(w, r)
		}
	})
	w := &Worker{deps: Deps{Routes: routes}}
	files := w.files(map[string]headless.File{"/content": {Body: []byte("c")}})

	if f, ok := files("/content"); !ok || string(f.Body) != "c" {
		t.Error("the page's own bytes were not served")
	}
	if f, ok := files("/portal/refs/a1/tok"); !ok || string(f.Body) != "<svg/>" || f.ContentType != "image/svg+xml" {
		t.Errorf("a reference was not answered in-process: %v %q", ok, f.Body)
	}
	if _, ok := files("/portal/vendor/reveal/reveal.js"); !ok {
		t.Error("the served runtime was not answered")
	}
	for _, p := range []string{"/api/v1/admin/system/info", "/portal/refs/../../api/v1/admin/system/info", "/portal/refs/missing"} {
		if _, ok := files(p); ok {
			t.Errorf("%s was answered; only the public prefixes may be", p)
		}
	}
	if _, ok := (&Worker{}).files(nil)("/portal/refs/a1/tok"); ok {
		t.Error("with no routes nothing but the page's own bytes can be answered")
	}
}

// --- resources -------------------------------------------------------------

func TestDrawResource_DatesTheTileByTheFileAsClaimed(t *testing.T) {
	d, blobs, res := &fakeDrawer{}, newBlobs(), &fakeResources{}
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	r := resource.Resource{ID: "r1", MIMEType: "text/markdown", S3Key: "resources/r1/notes.md", UpdatedAt: at}
	blobs.objects["res-bucket/"+r.S3Key] = []byte("# notes")
	w := New(Tuning{}, Deps{Drawer: d, Resources: res, ResourceBlobs: blobs, ResourceBucket: "res-bucket"})
	w.drawResource(context.Background(), r)

	if len(res.captures) != 2 {
		t.Fatalf("recorded %d captures, want light and dark", len(res.captures))
	}
	for _, c := range res.captures {
		if !c.CapturedAt.Equal(at) || c.Renderer != Renderer || c.S3Key != resource.ThumbnailKeyFor(r.S3Key, c.Variant) {
			t.Errorf("capture %+v: want it dated by the file's UpdatedAt, the renderer generation and the variant's key", c)
		}
	}
	// Both variants drawn, the claim ends with no hold and no attempts.
	if len(res.holds) != 1 || res.holds[0] != (hold{}) {
		t.Errorf("holds = %v, want the claim released once", res.holds)
	}
}

func TestDrawResource_AFailureIsRecordedAgainstTheFile(t *testing.T) {
	d := &fakeDrawer{results: []error{errors.New("headless: boom")}}
	blobs, res := newBlobs(), &fakeResources{}
	at := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	r := resource.Resource{ID: "r2", MIMEType: "text/html", S3Key: "resources/r2/x.html", UpdatedAt: at}
	blobs.objects["b/"+r.S3Key] = []byte("<p>x</p>")
	New(Tuning{}, Deps{Drawer: d, Resources: res, ResourceBlobs: blobs, ResourceBucket: "b"}).drawResource(context.Background(), r)
	if res.failure != "boom" || !res.failedAt.Equal(at) || len(res.captures) != 0 {
		t.Fatalf("failure %q at %v, captures %d", res.failure, res.failedAt, len(res.captures))
	}

	blobs.getErr = errors.New("NoSuchKey")
	res2 := &fakeResources{}
	err := New(Tuning{}, Deps{Drawer: &fakeDrawer{}, Resources: res2, ResourceBlobs: blobs, ResourceBucket: "b"}).drawResource(context.Background(), r)
	if err == nil || !strings.Contains(err.Error(), "the stored file could not be read: reading resources/r2/x.html: NoSuchKey") {
		t.Errorf("drawResource = %v, want the unreadable file named for the attempt", err)
	}
	if res2.failure != "" || len(res2.captures) != 0 || len(res2.holds) != 0 {
		t.Error("an unfinished attempt recorded a result or ended its own claim")
	}
}

// --- collections -----------------------------------------------------------

// A collection has a light mosaic of its members' light tiles and a dark one
// of their dark tiles, a member stored once lending its light tile to both
// (#1789).
func TestDrawCollection_AMosaicIsComposedFromItsMembersInEachScheme(t *testing.T) {
	d, blobs := &fakeDrawer{}, newBlobs()
	assets := &fakeAssets{byID: map[string]*portaldomain.Asset{
		"m1": {ID: "m1", S3Bucket: bucket, ThumbnailS3Key: "t/m1.png", ThumbnailDarkS3Key: "t/m1_dark.png"},
		"m2": {ID: "m2", S3Bucket: bucket, ThumbnailS3Key: "t/m2.png"},
		"m3": {ID: "m3", S3Bucket: bucket, ThumbnailS3Key: "t/m3-missing.png"},
	}}
	blobs.objects[bucket+"/t/m1.png"] = []byte("one")
	blobs.objects[bucket+"/t/m1_dark.png"] = []byte("one-dark")
	blobs.objects[bucket+"/t/m2.png"] = []byte("two")
	colls := &fakeCollections{}
	w := worker(d, assets, blobs)
	w.deps.Collections = colls
	source := "m1:1:1:1,m2:3:0:1,m3:1:1:1,gone:1:1:1"
	w.drawCollection(context.Background(), portaldomain.CollectionThumbnailWork{ID: "c1", Source: source})

	if len(d.pages) != 2 {
		t.Fatalf("composed %d mosaics, want a light and a dark one", len(d.pages))
	}
	for i, want := range [][2]string{{"one", "two"}, {"one-dark", "two"}} {
		p := d.pages[i]
		if !strings.Contains(string(p.Document), `class="m n2"`) || p.Width != tileWidth || p.Scale != tileScale {
			t.Fatalf("mosaic page = %s at %dx%d", p.Document, p.Width, p.Height)
		}
		for j, body := range want {
			if f, ok := p.Files(fmt.Sprintf("/m/%d.png", j)); !ok || string(f.Body) != body {
				t.Errorf("mosaic %d member %d was %q, want %q", i, j, f.Body, body)
			}
		}
	}
	for _, v := range []string{portaldomain.ThumbnailVariantLight, portaldomain.ThumbnailVariantDark} {
		if _, ok := blobs.objects[bucket+"/"+portaldomain.CollectionThumbnailKey("c1", v)]; !ok {
			t.Errorf("the %s mosaic was not stored", v)
		}
	}
	if rec := colls.recorded["c1"]; rec[0] != portaldomain.CollectionThumbnailKey("c1", portaldomain.ThumbnailVariantLight) || rec[1] != source {
		t.Errorf("recorded %v", rec)
	}
}

func TestDrawCollection_AMosaicWithNothingToDrawIsCleared(t *testing.T) {
	blobs := newBlobs()
	blobs.objects[bucket+"/old/mosaic.png"] = []byte("old")
	colls := &fakeCollections{}
	w := worker(&fakeDrawer{}, &fakeAssets{}, blobs)
	w.deps.Collections = colls
	w.drawCollection(context.Background(), portaldomain.CollectionThumbnailWork{ID: "c2", ThumbnailS3Key: "old/mosaic.png"})
	if rec, ok := colls.recorded["c2"]; !ok || rec != [2]string{"", ""} {
		t.Fatalf("recorded %v, want the tile cleared", rec)
	}
	want := []string{bucket + "/old/mosaic.png", bucket + "/" + portaldomain.CollectionThumbnailKey("c2", portaldomain.ThumbnailVariantDark)}
	if !slices.Equal(blobs.deleted, want) {
		t.Errorf("removed %v, want both mosaics %v", blobs.deleted, want)
	}
}

func TestMosaicPage_Layouts(t *testing.T) {
	for n := 1; n <= 4; n++ {
		tiles := make([][]byte, n)
		p := mosaicPage(tiles)
		if !strings.Contains(string(p.Document), fmt.Sprintf(`class="m n%d"`, n)) || strings.Count(string(p.Document), "<img") != n {
			t.Errorf("%d members: %s", n, p.Document)
		}
		if _, ok := p.Files("/elsewhere"); ok {
			t.Error("the mosaic page answered a path it was not given")
		}
	}
}

// --- the loop --------------------------------------------------------------

func TestPass_ClaimsNothingWhileTheRendererIsDown(t *testing.T) {
	d := &fakeDrawer{pingErr: errors.New("no renderer")}
	assets := &fakeAssets{claims: [][]portaldomain.Asset{{asset("a9", "text/html", 1)}}}
	w := worker(d, assets, newBlobs())
	if w.pass(context.Background()) {
		t.Fatal("a pass with the renderer down reported work")
	}
	if len(assets.claims) != 1 {
		t.Fatal("work was claimed that could not be drawn")
	}
	w.pass(context.Background()) // logged once, not twice
	d.pingErr = nil
	if !w.rendererAnswers(context.Background()) || w.unavailable {
		t.Error("the worker did not notice the renderer answering again")
	}
}

func TestWorker_StartDrainsWorkAndStops(t *testing.T) {
	d, blobs := &fakeDrawer{}, newBlobs()
	a := asset("a10", "text/html", 1)
	blobs.objects[bucket+"/"+a.S3Key] = []byte("<p>x</p>")
	r := resource.Resource{ID: "r10", MIMEType: "text/html", S3Key: "resources/r10/x.html"}
	blobs.objects["rb/"+r.S3Key] = []byte("<p>x</p>")
	assets := &fakeAssets{claims: [][]portaldomain.Asset{{a}}}
	res := &fakeResources{claims: []resource.Resource{r}}
	colls := &fakeCollections{}
	w := New(Tuning{Poll: 10 * time.Millisecond}, Deps{
		Drawer: d, Assets: assets, AssetBlobs: blobs, Collections: colls, CollectionBucket: bucket,
		Resources: res, ResourceBlobs: blobs, ResourceBucket: "rb",
	})
	w.Start(context.Background())
	deadline := time.Now().Add(2 * time.Second)
	for {
		assets.mu.Lock()
		_, drawnAsset := assets.updates["a10"]
		assets.mu.Unlock()
		if drawnAsset && res.captured() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the worker did not draw the claimed work")
		}
		time.Sleep(5 * time.Millisecond)
	}
	w.Stop()
	w.Stop() // a second stop is harmless
}

func TestOutcomeAndReason(t *testing.T) {
	if retry, reason := outcome(nil); retry || reason != "" {
		t.Error("no error is neither a retry nor a failure")
	}
	if retry, _ := outcome(fmt.Errorf("x: %w", headless.ErrUnavailable)); !retry {
		t.Error("an unavailable renderer must be retried")
	}
	if retry, _ := outcome(context.Canceled); !retry {
		t.Error("a canceled render must be retried")
	}
	long := errors.New("headless: headless: " + strings.Repeat("x", 2*maxReasonLength))
	if _, reason := outcome(long); len(reason) != maxReasonLength || strings.HasPrefix(reason, "headless") {
		t.Errorf("reason = %d chars %q...", len(reason), reason[:20])
	}
	// A document's own exception text can be in any script. The cut lands on a
	// character boundary, or the database would refuse the reason.
	accented := errors.New("headless: " + strings.Repeat("x", maxReasonLength-1) + "é tail")
	if _, reason := outcome(accented); !utf8.ValidString(reason) || len(reason) != maxReasonLength-1 {
		t.Errorf("reason cut to %d bytes, valid UTF-8 %v", len(reason), utf8.ValidString(reason))
	}
	if _, reason := outcome(errors.New("headless: nul\x00 and \xff bad")); reason != "nul and  bad" {
		t.Errorf("reason = %q, want the bytes a text column refuses removed", reason)
	}
}

// --- the failure branches ----------------------------------------------------

func TestDrawAsset_UnlistableReferencesDrawTheDocumentAsStored(t *testing.T) {
	d, assets, blobs := &fakeDrawer{}, &fakeAssets{}, newBlobs()
	a := asset("a11", "text/html", 1)
	blobs.objects[bucket+"/"+a.S3Key] = []byte(`<img src="mcp://global/x.svg">`)
	w := worker(d, assets, blobs)
	w.deps.Refs = fakeRefs{err: errors.New("refs down")}
	w.drawAsset(context.Background(), a)
	if content, _ := payload(t, d.pages[0])["content"].(string); !strings.Contains(content, "mcp://global/x.svg") {
		t.Fatalf("content = %q, want it drawn as stored", content)
	}
}

func TestDrawResource_AStoreThatCannotRecordIsLeftForTheLease(t *testing.T) {
	blobs := newBlobs()
	r := resource.Resource{ID: "r3", MIMEType: "text/html", S3Key: "resources/r3/x.html"}
	blobs.objects["b/"+r.S3Key] = []byte("<p>x</p>")

	res := &fakeResources{setErr: errors.New("db down")}
	New(Tuning{}, Deps{Drawer: &fakeDrawer{}, Resources: res, ResourceBlobs: blobs, ResourceBucket: "b"}).drawResource(context.Background(), r)
	if len(res.captures) != 0 {
		t.Error("a capture was reported recorded by a store that failed")
	}

	failing := &fakeResources{failErr: errors.New("db down")}
	New(Tuning{}, Deps{Drawer: &fakeDrawer{results: []error{errors.New("headless: boom")}}, Resources: failing, ResourceBlobs: blobs, ResourceBucket: "b"}).drawResource(context.Background(), r)
	if failing.failure != "" {
		t.Error("a failure was reported recorded by a store that failed")
	}
}

func TestDrawCollection_WhatCannotBeComposedIsLeftForTheLease(t *testing.T) {
	member := func() *fakeAssets {
		return &fakeAssets{byID: map[string]*portaldomain.Asset{"m1": {ID: "m1", S3Bucket: bucket, ThumbnailS3Key: "t/m1.png"}}}
	}
	seeded := func() *fakeBlobs {
		b := newBlobs()
		b.objects[bucket+"/t/m1.png"] = []byte("one")
		return b
	}
	work := portaldomain.CollectionThumbnailWork{ID: "c3", Source: "m1:1:1"}
	for _, tc := range []struct {
		name  string
		d     *fakeDrawer
		blobs func() *fakeBlobs
		colls *fakeCollections
	}{
		{"no member tile can be read", &fakeDrawer{}, newBlobs, &fakeCollections{}},
		{"the mosaic cannot be drawn", &fakeDrawer{results: []error{errors.New("headless: decode")}}, seeded, &fakeCollections{}},
		{"the dark mosaic cannot be drawn", &fakeDrawer{results: []error{nil, errors.New("headless: decode")}}, seeded, &fakeCollections{}},
		{"the renderer went away", &fakeDrawer{results: []error{headless.ErrUnavailable}}, seeded, &fakeCollections{}},
		{"the mosaic cannot be stored", &fakeDrawer{}, func() *fakeBlobs { b := seeded(); b.putErr = errors.New("s3 down"); return b }, &fakeCollections{}},
		{"the store cannot record it", &fakeDrawer{}, seeded, &fakeCollections{recordErr: errors.New("db down")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := worker(tc.d, member(), tc.blobs())
			w.deps.Collections = tc.colls
			w.drawCollection(context.Background(), work)
			if _, ok := tc.colls.recorded["c3"]; ok {
				t.Fatal("a mosaic that was not composed and stored was recorded")
			}
		})
	}

	blobs := newBlobs()
	colls := &fakeCollections{recordErr: errors.New("db down")}
	w := worker(&fakeDrawer{}, &fakeAssets{}, blobs)
	w.deps.Collections = colls
	w.drawCollection(context.Background(), portaldomain.CollectionThumbnailWork{ID: "c4", ThumbnailS3Key: "old.png"})
	if len(blobs.deleted) != 0 {
		t.Error("the old mosaic was deleted although the row still names it")
	}
}

func TestPass_AClaimThatFailsIsSurvived(t *testing.T) {
	w := New(Tuning{}, Deps{Drawer: &fakeDrawer{}, Resources: &fakeResources{claimErr: errors.New("db down")}})
	if w.pass(context.Background()) {
		t.Error("a pass whose claim failed reported work")
	}
}

func TestServeInProcess_ARouteThatWritesNothingIsA200(t *testing.T) {
	f, ok := serveInProcess(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), "/portal/refs/a/b")
	if !ok || len(f.Body) != 0 {
		t.Fatalf("an empty 200 = %v %q", ok, f.Body)
	}
	if _, ok := serveInProcess(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.WriteHeader(http.StatusOK)
	}), "/portal/refs/a/b"); ok {
		t.Error("a second WriteHeader overrode the first")
	}
}

// TestServeInProcess_IsNotCountedByTheViewerLimiter: every call the worker
// makes presents the loopback address, so counted, they share one bucket and a
// document's references are refused partway through it (#1791). A limiter of
// burst one in front of the route admits every call.
func TestServeInProcess_IsNotCountedByTheViewerLimiter(t *testing.T) {
	rl := viewerlimit.New(viewerlimit.Config{RequestsPerMinute: 1, BurstSize: 1}, nil)
	defer rl.Close()
	routes := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<svg/>"))
	}))
	for i := range 3 * assetrefs.MaxRefs {
		if f, ok := serveInProcess(routes, "/portal/refs/a/b"); !ok || string(f.Body) != "<svg/>" {
			t.Fatalf("in-process call %d was refused: ok=%v body=%q", i, ok, f.Body)
		}
	}
}

// A PDF reaches the page by URL, like a raster image, because its bytes are
// not a JSON string; pdf.js is handed the URL and reads the file itself
// (#1794). And it is drawn once: page one is the same picture in both schemes.
func TestDrawAsset_APDFIsServedToThePageByURLAndDrawnOnce(t *testing.T) {
	d, assets, blobs := &fakeDrawer{}, &fakeAssets{}, newBlobs()
	a := asset("a20", "application/pdf", 1)
	blobs.objects[bucket+"/"+a.S3Key] = []byte("%PDF-1.4 bytes")
	worker(d, assets, blobs).drawAsset(context.Background(), a)

	if len(d.pages) != 1 || d.pages[0].Dark {
		t.Fatalf("rendered %d pages, want one light tile", len(d.pages))
	}
	p := d.pages[0]
	data := payload(t, p)
	if fromURL, _ := data["serveFromURL"].(bool); !fromURL || data["content"] != nil {
		t.Fatalf("a PDF must reach the page by URL, not inline: %v", data)
	}
	f, ok := p.Files(contentPath)
	if !ok || string(f.Body) != "%PDF-1.4 bytes" || f.ContentType != "application/pdf" {
		t.Fatalf("the page's content route served %v %q %q", ok, f.Body, f.ContentType)
	}
	if u := assets.updates["a20"]; u.ThumbnailS3Key == nil || u.ThumbnailDarkS3Key != nil {
		t.Errorf("a PDF must record one tile and no dark one: %+v", u)
	}
}

// A PDF is laid out on the tile's own surface, not at page size: the tile page
// rasterizes page one onto a 400x300 canvas rather than letting the document
// size the viewport, which is what HTML and JSX do.
func TestDrawAsset_APDFIsDrawnAtTileGeometry(t *testing.T) {
	d, assets, blobs := &fakeDrawer{}, &fakeAssets{}, newBlobs()
	a := asset("a21", "application/pdf", 1)
	blobs.objects[bucket+"/"+a.S3Key] = []byte("%PDF-1.4 bytes")
	worker(d, assets, blobs).drawAsset(context.Background(), a)

	if p := d.pages[0]; p.Width != tileWidth || p.Height != tileHeight {
		t.Errorf("a PDF tile is %dx%d, want %dx%d", p.Width, p.Height, tileWidth, tileHeight)
	}
}
