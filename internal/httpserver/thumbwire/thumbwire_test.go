package thumbwire

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// claims records which stores the worker asked for work.
type claims struct {
	mu    sync.Mutex
	kinds map[string]bool
	seen  chan struct{}
}

func newClaims() *claims { return &claims{kinds: map[string]bool{}, seen: make(chan struct{}, 16)} }

func (c *claims) note(kind string) {
	c.mu.Lock()
	c.kinds[kind] = true
	c.mu.Unlock()
	c.seen <- struct{}{}
}

func (c *claims) asked(kind string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.kinds[kind]
}

type workingAssets struct {
	portal.AssetStore
	c *claims
}

func (a workingAssets) ClaimThumbnailWork(context.Context, int, time.Duration, int) ([]portaldomain.Asset, error) {
	a.c.note("assets")
	return nil, nil
}

func (workingAssets) HoldThumbnailWork(context.Context, string, time.Duration, int) error {
	return nil
}

type workingCollections struct {
	portal.CollectionStore
	c *claims
}

func (w workingCollections) ClaimCollectionThumbnailWork(context.Context, time.Duration, int) ([]portaldomain.CollectionThumbnailWork, error) {
	w.c.note("collections")
	return nil, nil
}

func (workingCollections) RecordCollectionThumbnail(context.Context, string, string, string) error {
	return nil
}

func (workingCollections) RecordCollectionThumbnailFailure(context.Context, string, string, string) error {
	return nil
}

func (workingCollections) HoldCollectionThumbnailWork(context.Context, string, time.Duration, int) error {
	return nil
}

type workingResources struct {
	resource.Store
	c *claims
}

func (r workingResources) ClaimThumbnailWork(context.Context, int, time.Duration, int) ([]resource.Resource, error) {
	r.c.note("resources")
	return nil, nil
}

func (workingResources) RecordThumbnailFailure(context.Context, string, string, time.Time) error {
	return nil
}

func (workingResources) HoldThumbnailWork(context.Context, string, time.Duration, int) error {
	return nil
}

type blobs struct{}

func (blobs) PutObject(context.Context, string, string, []byte, string) error { return nil }
func (blobs) PutObjectStream(context.Context, string, string, io.Reader, string) (int64, error) {
	return 0, nil
}

func (blobs) GetObject(context.Context, string, string) (data []byte, contentType string, err error) {
	return nil, "", nil
}
func (blobs) DeleteObject(context.Context, string, string) error { return nil }
func (blobs) Close() error                                       { return nil }

// tileEntry stands for the tile page a built UI embeds.
const tileEntry = "/portal/view/_assets/tile-entry-x.js"

// fakeSource is a platform with whichever stores a case gives it.
type fakeSource struct {
	cfg         platform.Config
	assets      portal.AssetStore
	assetBlobs  portal.S3Client
	collections portal.CollectionStore
	resources   resource.Store
	resBlobs    resource.S3Client
}

func (f *fakeSource) Config() *platform.Config                      { return &f.cfg }
func (f *fakeSource) PortalAssetStore() portal.AssetStore           { return f.assets }
func (f *fakeSource) PortalS3Client() portal.S3Client               { return f.assetBlobs }
func (*fakeSource) PortalContentRefStore() assetrefs.Store          { return nil }
func (f *fakeSource) PortalCollectionStore() portal.CollectionStore { return f.collections }
func (f *fakeSource) ResourceStore() resource.Store                 { return f.resources }
func (f *fakeSource) ResourceS3Client() resource.S3Client           { return f.resBlobs }

// A renderer that answers discovery, which is all the worker asks of it before
// claiming work.
func answeringRenderer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"webSocketDebuggerUrl":"ws://`+r.Host+`/devtools/browser/x"}`)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// With every store present, the worker draws in the configured renderer and
// asks each of the three stores for work.
func TestBuild_AsksEveryStoreThroughTheConfiguredRenderer(t *testing.T) {
	c := newClaims()
	src := &fakeSource{
		assets:      workingAssets{c: c},
		assetBlobs:  blobs{},
		collections: workingCollections{c: c},
		resources:   workingResources{c: c},
		resBlobs:    blobs{},
	}
	src.cfg.Thumbnails.RendererURL = answeringRenderer(t)

	w := assemble(src, http.NewServeMux(), tileEntry)
	if w == nil {
		t.Fatal("a platform with every store must get a worker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	w.Start(ctx)
	defer w.Stop()
	for range 3 {
		select {
		case <-c.seen:
		case <-ctx.Done():
			t.Fatalf("worker asked only assets=%v resources=%v collections=%v",
				c.asked("assets"), c.asked("resources"), c.asked("collections"))
		}
	}
	for _, kind := range []string{"assets", "resources", "collections"} {
		if !c.asked(kind) {
			t.Errorf("the worker never asked the %s store for work", kind)
		}
	}
}

// Without a resource store that hands out work, or without resource storage,
// resources are left out and everything else is still drawn.
func TestBuild_ResourcesNeedTheirStoreAndTheirStorage(t *testing.T) {
	for name, src := range map[string]*fakeSource{
		"a store that cannot hand out work": {resources: struct{ resource.Store }{}, resBlobs: blobs{}},
		"no resource storage":               {resources: workingResources{c: newClaims()}},
	} {
		t.Run(name, func(t *testing.T) {
			c := newClaims()
			src.assets, src.assetBlobs = workingAssets{c: c}, blobs{}
			src.cfg.Thumbnails.RendererURL = answeringRenderer(t)
			w := assemble(src, http.NewServeMux(), tileEntry)
			if w == nil {
				t.Fatal("assets alone are still drawn")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			w.Start(ctx)
			select {
			case <-c.seen:
			case <-ctx.Done():
				t.Fatal("the asset store was never asked for work")
			}
			w.Stop()
			if c.asked("resources") {
				t.Error("resources were claimed without somewhere to store their tiles")
			}
		})
	}
}

// Nothing is drawn where there is nothing to draw with.
func TestBuild_NoWorkerWithoutSomethingToDraw(t *testing.T) {
	off := false
	disabled := &fakeSource{assets: workingAssets{c: newClaims()}, assetBlobs: blobs{}}
	disabled.cfg.Thumbnails.Enabled = &off
	// A section Config.Validate refuses is refused here too (#1868).
	untunable := &fakeSource{assets: workingAssets{c: newClaims()}, assetBlobs: blobs{}}
	untunable.cfg.Thumbnails.Concurrency = -1
	for name, src := range map[string]*fakeSource{
		"turned off":                      disabled,
		"a section that does not tune":    untunable,
		"an asset store that cannot work": {assets: struct{ portal.AssetStore }{}, assetBlobs: blobs{}},
		"no asset storage":                {assets: workingAssets{c: newClaims()}},
	} {
		t.Run(name, func(t *testing.T) {
			if w := assemble(src, http.NewServeMux(), tileEntry); w != nil {
				t.Fatal("expected no worker")
			}
		})
	}
	if assemble(&fakeSource{assets: workingAssets{c: newClaims()}, assetBlobs: blobs{}}, http.NewServeMux(), "") != nil {
		t.Fatal("a build without a tile page must get no worker")
	}
	if Build(nil, http.NewServeMux()) != nil {
		t.Fatal("a nil platform must get no worker")
	}
	// A nil worker is safe to start and stop, which is what Serve does with it.
	none := Build(nil, nil)
	none.Start(context.Background())
	none.Stop()
}
