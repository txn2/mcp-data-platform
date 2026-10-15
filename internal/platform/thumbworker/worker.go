// Package thumbworker draws every tile the platform shows. It claims the
// assets, resources and collections the stores say are owed one, renders each
// in the headless browser beside the platform, and records what it drew or why
// it could not (#1787).
//
// A tile used to be drawn in whichever portal tab happened to be open, by a
// JavaScript reimplementation of CSS painting, and was wrong wherever that
// reimplementation fell short. Here the browser itself paints it, through the
// same renderers the viewer uses, and a document nobody opens still gets one.
package thumbworker

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/headless"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/pglisten"
	"github.com/txn2/mcp-data-platform/internal/thumbdue"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Renderer is the generation of the renderer this worker draws as. A tile
// drawn by an older generation still serves and is drawn again; raising this
// redraws every tile in the library.
//
// 2 draws at twice the density and gives HTML and JSX a dark tile (#1789).
// 3 captures SVG and raster images with their transparent areas transparent
// (#1991); migration 000172 stamped every other tile with 3, so only those
// families were drawn again.
const Renderer = 3

const (
	defaultPoll = 5 * time.Second
	// maxIdlePoll is the longest an idle worker waits between passes, its
	// wait doubling from Poll while it finds nothing (#2084). A row that
	// becomes due wakes it through NOTIFY; the poll is for a missed signal
	// and for a hold coming due.
	maxIdlePoll = time.Minute
	// clearBatch is how many due rows of each kind one pass takes out of the
	// due set when the renderer owes them nothing (#2084).
	clearBatch           = 1000
	defaultConcurrency   = 1
	defaultRenderTimeout = 45 * time.Second
	defaultMaxAttempts   = 5
	defaultRetryBackoff  = time.Minute
	// maxRetryBackoff caps how long a document is held back between attempts.
	maxRetryBackoff = time.Hour
	// backoffFactor is how much longer each hold is than the one before.
	backoffFactor = 4
	// leaseMargin is added to the lease a batch needs when none is set, so a
	// slow store write does not hand a row still being drawn to another
	// replica.
	leaseMargin = time.Minute
	// teardownTimeout is what a render can take past its own deadline while
	// the renderer disposes of the page (headless.disposeTimeout).
	teardownTimeout = 5 * time.Second
	// storageTimeout bounds one read or write of an object around a render.
	storageTimeout = 30 * time.Second
	// maxReasonLength bounds a recorded failure reason. The reason can carry a
	// document's own exception text, which is the document's to make long.
	maxReasonLength = 500
)

// Structured-log keys.
const (
	logKeyError      = "error"
	logKeyCollection = "collection"
)

// Tuning paces the worker. The zero value is the defaults; Config.Tuning is
// how a deployment's section becomes one.
type Tuning struct {
	// Poll is how long an idle worker waits before asking for work again.
	Poll time.Duration
	// Lease is how long a claimed row is held. It must outlast drawing the
	// batch it was claimed in; a replica that dies mid-render loses its lease
	// at this.
	Lease time.Duration
	// Batch is how many rows of each kind one pass claims.
	Batch int
	// Concurrency is how many documents are drawn at once.
	Concurrency int
	// RenderTimeout bounds one render. A document that has not reported itself
	// drawn by then is recorded as not drawable.
	RenderTimeout time.Duration
	// MaxAttempts is how many claims a document is given before one that
	// never finishes is recorded as not drawable (#1868).
	MaxAttempts int
	// RetryBackoff is the hold after a document's first unfinished attempt.
	RetryBackoff time.Duration
}

func (c Tuning) withDefaults() Tuning {
	if c.Poll <= 0 {
		c.Poll = defaultPoll
	}
	if c.Concurrency <= 0 {
		c.Concurrency = defaultConcurrency
	}
	if c.Batch <= 0 {
		c.Batch = c.Concurrency
	}
	if c.RenderTimeout <= 0 {
		c.RenderTimeout = defaultRenderTimeout
	}
	if c.Lease <= 0 {
		c.Lease = leaseFor(c.Batch, c.Concurrency, c.RenderTimeout) + leaseMargin
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = defaultMaxAttempts
	}
	if c.RetryBackoff <= 0 {
		c.RetryBackoff = defaultRetryBackoff
	}
	return c
}

// leaseFor is the longest drawing a batch can take: the rounds it is drawn in
// at concurrency, each the longest one document takes -- its file read, and
// for each of its two variants a render to its deadline, the page's teardown
// and the tile's write.
func leaseFor(batch, concurrency int, renderTimeout time.Duration) time.Duration {
	rounds := (batch + concurrency - 1) / concurrency
	variant := renderTimeout + teardownTimeout + storageTimeout
	return time.Duration(rounds) * (storageTimeout + 2*variant)
}

// backoff is the hold after a document's attempts-th unfinished attempt:
// RetryBackoff, four times longer each attempt after, at most an hour.
func (c Tuning) backoff(attempts int) time.Duration {
	hold := c.RetryBackoff
	for i := 1; i < attempts && hold < maxRetryBackoff; i++ {
		hold *= backoffFactor
	}
	return min(hold, maxRetryBackoff)
}

// Deps is what the worker draws with and where it records the result. Assets
// is required; collections and resources are drawn when their stores are
// present.
type Deps struct {
	Drawer Drawer

	Assets      AssetWork
	Refs        RefLister
	AssetBlobs  Blobs
	Collections CollectionWork
	// CollectionBucket is where a collection's mosaic is stored, and
	// CollectionPrefix the portal key prefix it is stored under (#1903).
	CollectionBucket string
	CollectionPrefix string

	Resources      resource.ThumbnailWork
	ResourceBlobs  Blobs
	ResourceBucket string

	// Scripts is the script tile store (#1909). A script's tile is its flow
	// diagram, stored beside the collection mosaics in AssetBlobs under
	// CollectionBucket and CollectionPrefix.
	Scripts ScriptWork

	// Routes is the platform's own HTTP routes. The page a tile is drawn from
	// loads the viewer's chunks, the served slide runtime and a document's
	// declared references from the platform, and those requests are answered
	// by calling these routes in-process.
	Routes http.Handler
	// TileEntryURL and TileCSS are the tile page's script and stylesheet.
	TileEntryURL string
	TileCSS      string

	// DSN is the database a LISTEN connection is opened on, so a row
	// becoming due wakes the worker (#2084); empty, it only polls.
	DSN string
}

// Worker draws owed tiles until stopped.
type Worker struct {
	cfg  Tuning
	deps Deps

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
	wake     chan struct{}

	mu          sync.Mutex
	unavailable bool
	// lastUnfinished names the last document whose attempt did not finish,
	// for the log line that says the renderer stopped answering.
	lastUnfinished string
}

// New returns a worker. It does nothing until Start.
func New(cfg Tuning, deps Deps) *Worker {
	deps.AssetBlobs, deps.ResourceBlobs = thumbnailBlobs(deps.AssetBlobs), thumbnailBlobs(deps.ResourceBlobs)
	return &Worker{
		cfg: cfg.withDefaults(), deps: deps,
		stop: make(chan struct{}), done: make(chan struct{}), wake: make(chan struct{}, 1),
	}
}

// Notify wakes an idle worker for a pass now: a row became due (#2084).
func (w *Worker) Notify() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Start runs the worker until ctx ends or Stop is called. A nil worker, which
// is what a deployment with nothing to draw has, does nothing.
func (w *Worker) Start(ctx context.Context) {
	if w == nil {
		return
	}
	w.registerBacklog(bgloop.Metrics())
	go w.run(ctx)
}

// Stop ends the worker and waits for the render in progress, if any.
func (w *Worker) Stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() { close(w.stop) })
	<-w.done
}

func (w *Worker) run(ctx context.Context) {
	defer close(w.done)
	closeListener := w.listen(ctx)
	defer closeListener()
	w.sweep(ctx)
	busy, idle := false, w.cfg.Poll
	bgloop.Run(ctx, bgloop.Loop{
		Name: bgloop.NameThumbnailWorker, Immediate: true, Stop: w.stop, Wake: w.wake, SpanPerUnit: true,
		Next: func() time.Duration {
			if busy {
				// A pass that found work drains the backlog without waiting.
				return 0
			}
			wait := idle
			idle = nextIdle(idle, w.cfg.Poll)
			return wait
		},
		Body: func(ctx context.Context) error {
			busy = w.pass(ctx)
			if busy || bgloop.Woken(ctx) {
				idle = w.cfg.Poll
			}
			return nil
		},
	})
}

// listen opens the LISTEN connection that wakes the worker when a row becomes
// due (#2084) and returns what closes it. It is opened beside the loop rather
// than before it: pq's Listen waits for a connection, and a database that is
// not answering must leave the worker polling, not waiting. The close waits
// for an open still under way, so a connection that comes up after the worker
// stopped is closed too.
func (w *Worker) listen(ctx context.Context) func() {
	if w.deps.DSN == "" {
		return func() {}
	}
	started := make(chan *pglisten.Listener, 1)
	go func() {
		l := pglisten.New(w.deps.DSN, thumbdue.NotifyChannel, w)
		if err := l.Start(ctx); err != nil {
			slog.WarnContext(ctx, "thumbnails: not woken by new work; polling only", logKeyError, logsan.SanitizeForLog(err.Error()))
			close(started)
			return
		}
		started <- l
	}()
	return func() {
		go func() {
			if l, ok := <-started; ok {
				l.Stop()
			}
		}()
	}
}

// nextIdle is the wait after an idle one: twice as long, at most maxIdlePoll
// or poll when that is longer.
func nextIdle(wait, poll time.Duration) time.Duration {
	return min(2*wait, max(maxIdlePoll, poll))
}

// sweep marks every row of each kind due once per owed rule (#2084), before
// the first pass, so a document a changed rule now owes a tile is read.
func (w *Worker) sweep(ctx context.Context) {
	sweeps := map[string]func(context.Context) (int64, error){}
	if w.deps.Assets != nil {
		sweeps[kindAsset] = func(ctx context.Context) (int64, error) { return w.deps.Assets.SweepThumbnailWork(ctx, Renderer) }
	}
	if w.deps.Resources != nil {
		sweeps[kindResource] = func(ctx context.Context) (int64, error) { return w.deps.Resources.SweepThumbnailWork(ctx, Renderer) }
	}
	if w.deps.Collections != nil {
		sweeps[kindCollection] = w.deps.Collections.SweepCollectionThumbnailWork
	}
	for kind, sweep := range sweeps {
		marked, err := sweep(ctx)
		switch {
		case err != nil:
			slog.ErrorContext(ctx, "thumbnails: marking rows for the current rule failed", "kind", kind, logKeyError, logsan.SanitizeForLog(err.Error()))
		case marked > 0:
			slog.InfoContext(ctx, "thumbnails: rows marked for the current rule", "kind", kind, "rows", marked)
		}
	}
}

// pass claims and draws one batch of each kind and reports whether it found
// any work. It claims nothing while the renderer does not answer, so rows are
// not leased by a replica that cannot draw them.
func (w *Worker) pass(ctx context.Context) bool {
	if !w.rendererAnswers(ctx) {
		return false
	}
	// The due rows owed nothing leave the due set first; a full batch says
	// more may wait.
	cleared := w.clear(ctx) >= clearBatch
	// Each kind is drawn whatever the one before it found.
	assets := w.drawBatch(ctx, w.claimAssets(ctx))
	resources := w.drawBatch(ctx, w.claimResources(ctx))
	collections := w.drawBatch(ctx, w.claimCollections(ctx))
	scripts := w.drawBatch(ctx, w.claimScripts(ctx))
	return cleared || assets || resources || collections || scripts
}

// clear takes the due rows each kind owes nothing out of the due set and
// returns the most one kind cleared.
func (w *Worker) clear(ctx context.Context) int64 {
	var most int64
	note := func(kind string, n int64, err error) {
		if err != nil {
			slog.ErrorContext(ctx, "thumbnails: clearing settled rows failed", "kind", kind, logKeyError, logsan.SanitizeForLog(err.Error()))
		}
		most = max(most, n)
	}
	if w.deps.Assets != nil {
		n, err := w.deps.Assets.ClearThumbnailWork(ctx, Renderer, clearBatch)
		note(kindAsset, n, err)
	}
	if w.deps.Resources != nil {
		n, err := w.deps.Resources.ClearThumbnailWork(ctx, Renderer, clearBatch)
		note(kindResource, n, err)
	}
	if w.deps.Collections != nil {
		n, err := w.deps.Collections.ClearCollectionThumbnailWork(ctx, clearBatch)
		note(kindCollection, n, err)
	}
	return most
}

// claimAssets claims one batch of assets.
func (w *Worker) claimAssets(ctx context.Context) []job {
	if w.deps.Assets == nil {
		return nil
	}
	assets, err := w.deps.Assets.ClaimThumbnailWork(ctx, Renderer, w.cfg.Lease, w.cfg.Batch)
	logClaim("assets", err)
	jobs := make([]job, 0, len(assets))
	for i := range assets {
		jobs = append(jobs, w.assetJob(assets[i]))
	}
	return jobs
}

// claimResources is claimAssets for managed resources.
func (w *Worker) claimResources(ctx context.Context) []job {
	if w.deps.Resources == nil {
		return nil
	}
	resources, err := w.deps.Resources.ClaimThumbnailWork(ctx, Renderer, w.cfg.Lease, w.cfg.Batch)
	logClaim("resources", err)
	jobs := make([]job, 0, len(resources))
	for i := range resources {
		jobs = append(jobs, w.resourceJob(resources[i]))
	}
	return jobs
}

// claimCollections is claimAssets for collection mosaics.
func (w *Worker) claimCollections(ctx context.Context) []job {
	if w.deps.Collections == nil {
		return nil
	}
	collections, err := w.deps.Collections.ClaimCollectionThumbnailWork(ctx, w.cfg.Lease, w.cfg.Batch)
	logClaim("collections", err)
	jobs := make([]job, 0, len(collections))
	for _, c := range collections {
		jobs = append(jobs, w.collectionJob(c))
	}
	return jobs
}

// rendererAnswers pings the renderer, logging once when it stops answering and
// once when it answers again rather than on every pass. When the last
// document whose attempt did not finish is known, the line names it: a
// renderer that stops answering right after one is most often still busy
// with that document rather than down (#1868).
func (w *Worker) rendererAnswers(ctx context.Context) bool {
	err := w.deps.Drawer.Ping(ctx)
	bgloop.Metrics().RecordThumbnailRenderer(ctx, err == nil)
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case err != nil && !w.unavailable:
		w.unavailable = true
		slog.WarnContext(ctx, "thumbnails: the renderer does not answer; tiles are not being drawn",
			logKeyError, logsan.SanitizeForLog(err.Error()),
			"last_unfinished", logsan.SanitizeForLog(w.lastUnfinished))
	case err == nil && w.unavailable:
		w.unavailable = false
		slog.InfoContext(ctx, "thumbnails: the renderer answers again; drawing owed tiles")
	}
	return err == nil
}

func logClaim(kind string, err error) {
	if err != nil {
		slog.Error("thumbnails: claiming work failed", "kind", kind, logKeyError, logsan.SanitizeForLog(err.Error()))
	}
}

// renderCtx bounds one render.
func (w *Worker) renderCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, w.cfg.RenderTimeout)
}

// outcome classifies a render error: the renderer being unavailable, or a
// public file the document needs not arriving (#2122), is retried with
// backoff; anything else is the document's and is recorded.
func outcome(err error) (retry bool, reason string) {
	if err == nil {
		return false, ""
	}
	if errors.Is(err, headless.ErrUnavailable) || errors.Is(err, headless.ErrIncomplete) || errors.Is(err, context.Canceled) {
		return true, ""
	}
	return false, failureReason(err)
}

// failureReason is the recorded text of a document's failure, which a reader
// sees beside the missing tile: the renderer's own sentence, without the
// prefixes the error picked up on its way here, bounded.
func failureReason(err error) string {
	msg := err.Error()
	for {
		trimmed := strings.TrimPrefix(strings.TrimPrefix(msg, "rendering: "), "headless: ")
		if trimmed == msg {
			break
		}
		msg = trimmed
	}
	// The reason is stored in a text column, which refuses NUL and invalid
	// UTF-8, and a document chooses its own exception text.
	msg = strings.ToValidUTF8(strings.ReplaceAll(msg, "\x00", ""), "")
	if len(msg) > maxReasonLength {
		// Cut on a character boundary: a split character is bytes the database
		// refuses, which would leave the failure unrecorded and the document
		// retried forever.
		cut := maxReasonLength
		for cut > 0 && !utf8.RuneStart(msg[cut]) {
			cut--
		}
		msg = msg[:cut]
	}
	return msg
}
