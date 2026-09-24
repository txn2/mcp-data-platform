package thumbworker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/headless"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// unavailable is a renderer that stopped answering mid-render, as the draw of
// the document in #1868 surfaced it.
var errRendererGone = fmt.Errorf("headless: the connection to the renderer closed: %w", headless.ErrUnavailable)

// claimed is an asset as the claim returns it: charged attempts.
func claimed(id string, attempts int) portaldomain.Asset {
	a := asset(id, "text/html", 3)
	a.ThumbnailAttempts = attempts
	return a
}

func seededWorker(d *fakeDrawer, assets *fakeAssets, ids ...string) *Worker {
	blobs := newBlobs()
	for _, id := range ids {
		blobs.objects[bucket+"/"+asset(id, "text/html", 3).S3Key] = []byte("<p>x</p>")
	}
	return worker(d, assets, blobs)
}

// TestAttempt_AnUnfinishedAttemptIsHeldBackLongerEachTime holds #1868: a
// document whose draw did not finish is not claimed again at once, and keeps
// its count, so the next claim is its next attempt.
func TestAttempt_AnUnfinishedAttemptIsHeldBackLongerEachTime(t *testing.T) {
	for attempts, want := range map[int]time.Duration{1: time.Minute, 2: 4 * time.Minute, 3: 16 * time.Minute, 4: time.Hour} {
		assets := &fakeAssets{}
		w := seededWorker(&fakeDrawer{results: []error{errRendererGone}}, assets, "a1")
		w.attempt(context.Background(), w.assetJob(claimed("a1", attempts)))
		if h, ok := assets.held("a1"); !ok || h != (hold{want, attempts}) {
			t.Errorf("attempt %d: held %+v, want %s keeping the count", attempts, h, want)
		}
		if _, ok := assets.update("a1"); ok {
			t.Errorf("attempt %d: a result was recorded before the attempts ran out", attempts)
		}
	}
}

// TestAttempt_TheLastUnfinishedAttemptIsRecordedAsNotDrawable holds #1868: the
// document that pinned the renderer leaves the queue, with the reason beside
// its missing tile, dated to the version it was tried at.
func TestAttempt_TheLastUnfinishedAttemptIsRecordedAsNotDrawable(t *testing.T) {
	assets := &fakeAssets{}
	w := seededWorker(&fakeDrawer{results: []error{errRendererGone}}, assets, "a1")
	w.attempt(context.Background(), w.assetJob(claimed("a1", defaultMaxAttempts)))
	u, ok := assets.update("a1")
	if !ok || u.ThumbnailFailure == nil {
		t.Fatal("the last attempt recorded nothing")
	}
	want := "the renderer could not finish drawing this after 5 attempts; the last: the connection to the renderer closed: the renderer is not available"
	if *u.ThumbnailFailure != want {
		t.Errorf("reason = %q, want %q", *u.ThumbnailFailure, want)
	}
	if *u.ThumbnailFailedVersion != 3 || !u.ReleaseThumbnailClaim {
		t.Errorf("the failure is dated to the version tried and ends the claim: %+v", u)
	}
	if _, held := assets.held("a1"); held {
		t.Error("a document given up on was also held")
	}
}

// TestAttempt_AClaimPastTheLastAttemptIsNotDrawn: a replica that died drawing
// the last attempt left the claim charged; the next claim records the
// document without drawing it again.
func TestAttempt_AClaimPastTheLastAttemptIsNotDrawn(t *testing.T) {
	d, assets := &fakeDrawer{}, &fakeAssets{}
	w := seededWorker(d, assets, "a1")
	w.attempt(context.Background(), w.assetJob(claimed("a1", defaultMaxAttempts+1)))
	if len(d.pages) != 0 {
		t.Fatal("a document past its attempts was drawn again")
	}
	if u, _ := assets.update("a1"); u.ThumbnailFailure == nil || !strings.Contains(*u.ThumbnailFailure, "an attempt ended with the replica drawing it") {
		t.Errorf("recorded %+v", u)
	}
}

// TestAttempt_AFinishedAttemptEndsItsOwnClaim: a drawn document and one whose
// own failure was recorded are neither held nor given up on.
func TestAttempt_AFinishedAttemptEndsItsOwnClaim(t *testing.T) {
	for _, results := range [][]error{nil, {errors.New("headless: TypeError")}} {
		assets := &fakeAssets{}
		w := seededWorker(&fakeDrawer{results: results}, assets, "a1")
		w.attempt(context.Background(), w.assetJob(claimed("a1", defaultMaxAttempts)))
		u, _ := assets.update("a1")
		if !u.ReleaseThumbnailClaim {
			t.Errorf("results %v: the claim was not ended with the result", results)
		}
		if _, held := assets.held("a1"); held {
			t.Errorf("results %v: a finished attempt was held", results)
		}
	}
}

// TestAttempt_AResourceWhoseFileIsGoneLeavesTheQueue holds #1868: a resource
// whose object is gone (NoSuchKey) was re-read every two minutes forever; it
// is held, then recorded with the read's error.
func TestAttempt_AResourceWhoseFileIsGoneLeavesTheQueue(t *testing.T) {
	blobs := newBlobs()
	at := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	r := resource.Resource{ID: "r1", MIMEType: "text/html", S3Key: "resources/r1/x.html", UpdatedAt: at, ThumbnailAttempts: 1}
	res := &fakeResources{}
	w := New(Tuning{}, Deps{Drawer: &fakeDrawer{}, Resources: res, ResourceBlobs: blobs, ResourceBucket: "b"})
	w.attempt(context.Background(), w.resourceJob(r))
	if len(res.holds) != 1 || res.holds[0] != (hold{time.Minute, 1}) || res.failure != "" {
		t.Fatalf("first attempt: holds %v failure %q", res.holds, res.failure)
	}
	r.ThumbnailAttempts = defaultMaxAttempts
	w.attempt(context.Background(), w.resourceJob(r))
	if !strings.Contains(res.failure, "the stored file could not be read") || !res.failedAt.Equal(at) {
		t.Errorf("last attempt recorded %q at %v", res.failure, res.failedAt)
	}
}

// TestAttempt_ACollectionThatCannotBeComposedLeavesTheQueue holds #1868 for
// mosaics: one the renderer refuses is recorded against its source at once,
// and one whose member tiles cannot be read is held, then recorded.
func TestAttempt_ACollectionThatCannotBeComposedLeavesTheQueue(t *testing.T) {
	member := &fakeAssets{byID: map[string]*portaldomain.Asset{"m1": {ID: "m1", S3Bucket: bucket, ThumbnailS3Key: "t/m1.png"}}}
	seeded := newBlobs()
	seeded.objects[bucket+"/t/m1.png"] = []byte("one")
	work := portaldomain.CollectionThumbnailWork{ID: "c1", Source: "m1:1:1:2", Attempts: 1}

	colls := &fakeCollections{}
	w := worker(&fakeDrawer{results: []error{errors.New("headless: decode")}}, member, seeded)
	w.deps.Collections = colls
	w.attempt(context.Background(), w.collectionJob(work))
	if f := colls.failures["c1"]; f != [2]string{"m1:1:1:2", "decode"} {
		t.Errorf("a refused mosaic recorded %v", f)
	}

	colls = &fakeCollections{}
	w = worker(&fakeDrawer{}, member, newBlobs())
	w.deps.Collections = colls
	w.attempt(context.Background(), w.collectionJob(work))
	if colls.holds["c1"] != (hold{time.Minute, 1}) {
		t.Errorf("unreadable members held %v", colls.holds["c1"])
	}
	work.Attempts = defaultMaxAttempts
	w.attempt(context.Background(), w.collectionJob(work))
	if f := colls.failures["c1"]; !strings.Contains(f[1], "none of the member tiles") {
		t.Errorf("unreadable members at the last attempt recorded %v", f)
	}
}

// pingAfter is a drawer whose renderer stops answering after n pings.
type pingAfter struct {
	fakeDrawer
	n     atomic.Int32
	limit int32
}

func (p *pingAfter) Ping(context.Context) error {
	if p.n.Add(1) > p.limit {
		return errors.New("no renderer")
	}
	return nil
}

// TestDrawBatch_ARendererThatStopsAnsweringHandsTheRestBack holds #1868: a
// renderer that stops answering is not the next document's doing, so the rows
// not yet tried go back with their attempt returned, undrawn.
func TestDrawBatch_ARendererThatStopsAnsweringHandsTheRestBack(t *testing.T) {
	d := &pingAfter{limit: 1}
	assets := &fakeAssets{}
	w := seededWorker(&d.fakeDrawer, assets, "a1", "a2", "a3")
	w.deps.Drawer = d
	w.drawBatch(context.Background(), []job{
		w.assetJob(claimed("a1", 1)), w.assetJob(claimed("a2", 2)), w.assetJob(claimed("a3", 1)),
	})
	if len(d.pages) != 2 {
		t.Fatalf("drew %d pages, want only the first document's two", len(d.pages))
	}
	if h, _ := assets.held("a2"); h != (hold{0, 1}) {
		t.Errorf("a2 held %+v, want handed back with its attempt returned", h)
	}
	if h, _ := assets.held("a3"); h != (hold{0, 0}) {
		t.Errorf("a3 held %+v, want handed back with its attempt returned", h)
	}
}

// TestDrawBatch_AStoppedWorkerHandsTheRestBack: a worker asked to stop draws
// nothing more of its batch and returns what it claimed.
func TestDrawBatch_AStoppedWorkerHandsTheRestBack(t *testing.T) {
	d, assets := &fakeDrawer{}, &fakeAssets{}
	w := seededWorker(d, assets, "a1")
	close(w.stop)
	w.drawBatch(context.Background(), []job{w.assetJob(claimed("a1", 1))})
	if len(d.pages) != 0 {
		t.Fatal("a stopped worker drew")
	}
	if h, _ := assets.held("a1"); h != (hold{0, 0}) {
		t.Errorf("held %+v, want handed back", h)
	}
}

// TestAttempt_AStopMidDrawIsNotTheDocuments: a render cut short because the
// worker is stopping hands the row back rather than charging it.
func TestAttempt_AStopMidDrawIsNotTheDocuments(t *testing.T) {
	assets := &fakeAssets{}
	w := seededWorker(&fakeDrawer{results: []error{context.Canceled}}, assets, "a1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.attempt(ctx, w.assetJob(claimed("a1", 2)))
	if h, _ := assets.held("a1"); h != (hold{0, 1}) {
		t.Errorf("held %+v, want handed back with its attempt returned", h)
	}
}

// TestAttempt_AClaimThatCannotBeEndedIsLeftToItsLease: a hold the store
// refuses leaves the lease to lapse, which is logged, not fatal.
func TestAttempt_AClaimThatCannotBeEndedIsLeftToItsLease(t *testing.T) {
	assets := &fakeAssets{holdErr: errors.New("db down")}
	w := seededWorker(&fakeDrawer{results: []error{errRendererGone}}, assets, "a1")
	w.attempt(context.Background(), w.assetJob(claimed("a1", 1)))
	if _, ok := assets.update("a1"); ok {
		t.Error("a result was recorded")
	}
}

// blockingDrawer holds each render until as many as want are in flight.
type blockingDrawer struct {
	fakeDrawer
	want     int
	inFlight atomic.Int32
	peak     atomic.Int32
	all      chan struct{}
	once     sync.Once
}

func (b *blockingDrawer) Render(ctx context.Context, p headless.Page) ([]byte, error) {
	n := b.inFlight.Add(1)
	defer b.inFlight.Add(-1)
	for {
		peak := b.peak.Load()
		if n <= peak || b.peak.CompareAndSwap(peak, n) {
			break
		}
	}
	if int(n) >= b.want {
		b.once.Do(func() { close(b.all) })
	}
	select {
	case <-b.all:
	case <-time.After(2 * time.Second):
	}
	return b.fakeDrawer.Render(ctx, p)
}

// TestDrawBatch_DrawsConcurrencyAtOnce holds the tuning #1868 adds: a
// replica draws as many documents at once as it is configured to, and no more.
func TestDrawBatch_DrawsConcurrencyAtOnce(t *testing.T) {
	d := &blockingDrawer{want: 2, all: make(chan struct{})}
	assets := &fakeAssets{}
	blobs := newBlobs()
	for _, id := range []string{"a1", "a2", "a3"} {
		blobs.objects[bucket+"/"+asset(id, "text/html", 3).S3Key] = []byte("<p>x</p>")
	}
	w := New(Tuning{Concurrency: 2}, Deps{
		Drawer: d, Assets: assets, AssetBlobs: blobs, CollectionBucket: bucket,
		TileEntryURL: "/portal/view/_assets/tile-entry-X.js", TileCSS: ".x{}",
	})
	w.drawBatch(context.Background(), []job{
		w.assetJob(claimed("a1", 1)), w.assetJob(claimed("a2", 1)), w.assetJob(claimed("a3", 1)),
	})
	if got := d.peak.Load(); got != 2 {
		t.Errorf("at most %d renders were in flight, want 2", got)
	}
	for _, id := range []string{"a1", "a2", "a3"} {
		if u, _ := assets.update(id); u.ThumbnailS3Key == nil {
			t.Errorf("%s was not drawn", id)
		}
	}
}

// TestConfig_Tuning holds the `thumbnails:` pacing keys: every default when
// nothing is set, the batch following concurrency, the lease worked out to
// outlast the batch, and the values that cannot work refused.
func TestConfig_Tuning(t *testing.T) {
	got, err := Config{}.Tuning()
	if err != nil {
		t.Fatalf("an empty section: %v", err)
	}
	perDocument := storageTimeout + 2*(defaultRenderTimeout+teardownTimeout+storageTimeout)
	if got.Concurrency != 1 || got.Batch != 1 || got.RenderTimeout != defaultRenderTimeout ||
		got.Poll != defaultPoll || got.MaxAttempts != defaultMaxAttempts || got.RetryBackoff != time.Minute ||
		got.Lease != perDocument+leaseMargin {
		t.Errorf("defaults = %+v", got)
	}
	if got.Lease != 4*time.Minute+10*time.Second {
		t.Errorf("default lease = %s; docs/server/configuration.md says 4m10s", got.Lease)
	}

	got, err = Config{Concurrency: 3, Batch: 7, RenderTimeout: 20 * time.Second, MaxAttempts: 2, RetryBackoff: 5 * time.Second}.Tuning()
	if err != nil {
		t.Fatal(err)
	}
	// 7 rows at 3 at a time is 3 rounds of a 30s read and two variants of
	// 20s + 5s + 30s, 140s each, plus the minute's margin.
	if got.Lease != 8*time.Minute || got.MaxAttempts != 2 {
		t.Errorf("tuned = %+v", got)
	}

	for name, c := range map[string]Config{
		"negative concurrency": {Concurrency: -1},
		"negative timeout":     {RenderTimeout: -time.Second},
	} {
		if _, err := c.Tuning(); !errors.Is(err, errNegative) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := (Config{Batch: 4, Lease: 2 * time.Minute}).Tuning(); err == nil || !strings.Contains(err.Error(), "lease 2m0s is shorter than drawing a batch of 4") {
		t.Errorf("a lease the batch outlasts: %v", err)
	}
}

// TestTuning_Backoff: four times longer each attempt, capped at an hour.
func TestTuning_Backoff(t *testing.T) {
	c := Tuning{RetryBackoff: 30 * time.Second}
	for attempts, want := range map[int]time.Duration{1: 30 * time.Second, 2: 2 * time.Minute, 3: 8 * time.Minute, 5: time.Hour, 40: time.Hour} {
		if got := c.backoff(attempts); got != want {
			t.Errorf("backoff(%d) = %s, want %s", attempts, got, want)
		}
	}
}
