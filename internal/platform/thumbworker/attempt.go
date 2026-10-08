package thumbworker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/headless"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// A claim charges the row one attempt, and an attempt ends in one of three
// ways (#1868):
//
//   - it finished: a tile, or a failure the document owns (it threw, it did
//     not finish drawing by the deadline), was recorded, and the claim ended
//     with it;
//   - it did not finish -- the renderer stopped answering or closed the page
//     mid-render, or the stored file could not be read or the tile written --
//     and the row is held back, longer each time, keeping its count;
//   - it did not finish for the MaxAttempts-th time, and the row is recorded
//     as not drawable with the last reason, which leaves the queue until the
//     document changes or its tile is asked for again.

// job is one claimed row: how to draw it, and how to end its claim when the
// attempt does not finish.
type job struct {
	// name is "asset <id>", for the log.
	name string
	// attempts is what the claim charged, counting itself.
	attempts int
	// draw draws and records the row. It returns nil once the attempt
	// finished -- including when the result could not be written, which the
	// lease then retries -- and why it did not finish otherwise.
	draw func(ctx context.Context) error
	// hold ends the claim without a result: the row is not claimed again
	// until hold has passed, and carries attempts into the next claim.
	hold func(ctx context.Context, hold time.Duration, attempts int) error
	// fail records the row as not drawable, ending the claim.
	fail func(ctx context.Context, reason string) error
}

// logKeyDocument names the claimed row a log line is about.
const logKeyDocument = "document"

// errNeverFinished is the attempt a replica took on a row and never returned
// from -- it was stopped or killed mid-render -- which the claim charged.
var errNeverFinished = errors.New("an attempt ended with the replica drawing it")

// drawBatch draws the claimed rows, Concurrency at a time, and reports
// whether there were any. Before each it asks the renderer whether it
// answers: a renderer that stopped answering is not the next document's
// doing, so that row and every one after it are handed back with their
// attempt returned. So are the rows left when the worker is stopped.
func (w *Worker) drawBatch(ctx context.Context, jobs []job) bool {
	sem := make(chan struct{}, w.cfg.Concurrency)
	var wg sync.WaitGroup
	for i, j := range jobs {
		// A slot is taken before the renderer is asked, so the question is
		// asked of a renderer this worker is not already drawing a
		// document in up to its concurrency.
		sem <- struct{}{}
		if w.stopping(ctx) || !w.rendererAnswers(ctx) {
			<-sem
			for _, rest := range jobs[i:] {
				w.handBack(ctx, rest)
			}
			break
		}
		wg.Go(func() {
			defer func() { <-sem }()
			_ = bgloop.Unit(ctx, bgloop.NameThumbnailRender, func(ctx context.Context) error {
				return w.attempt(ctx, j)
			})
		})
	}
	wg.Wait()
	return len(jobs) > 0
}

// stopping reports whether the worker has been asked to stop.
func (w *Worker) stopping(ctx context.Context) bool {
	select {
	case <-w.stop:
		return true
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// attempt draws one claimed row and ends its claim. The attempt is timed by
// the row's kind, and one that drew no tile is counted by why and returned.
func (w *Worker) attempt(ctx context.Context, j job) error {
	kind := j.kind()
	ctx = withKind(ctx, kind)
	m := bgloop.Metrics()
	if j.attempts > w.cfg.MaxAttempts {
		// The claim that would have been the last was never returned from.
		w.giveUp(ctx, j, errNeverFinished)
		m.RecordThumbnailFailure(ctx, kind, failUndrawable)
		return errNeverFinished
	}
	start := time.Now()
	err := j.draw(ctx)
	m.RecordThumbnailRender(ctx, kind, time.Since(start))
	if err == nil {
		return nil
	}
	w.mu.Lock()
	w.lastUnfinished = j.name
	w.mu.Unlock()
	if ctx.Err() != nil {
		// The worker is stopping; this attempt is not the document's, and
		// not a failure of the unit either.
		w.handBack(ctx, j)
		return nil //nolint:nilerr // a stop is not the attempt's failure
	}
	if j.attempts >= w.cfg.MaxAttempts {
		w.giveUp(ctx, j, err)
		m.RecordThumbnailFailure(ctx, kind, failUndrawable)
		return err
	}
	m.RecordThumbnailFailure(ctx, kind, unfinishedReason(err))
	hold := w.cfg.backoff(j.attempts)
	slog.InfoContext(ctx, "thumbnails: an attempt did not finish; holding the document back",
		logKeyDocument, logsan.SanitizeForLog(j.name), "attempt", j.attempts, "hold", hold.String(),
		logKeyError, logsan.SanitizeForLog(err.Error()))
	w.endClaim(ctx, j, hold, j.attempts)
	return err
}

// Why an attempt drew no tile, as thumbnail_render_failures_total{reason}
// counts it (#1897).
const (
	// failDocument is a failure the document owns: the page threw or did
	// not finish drawing. It is recorded on the row.
	failDocument = "document"
	// failRenderer is an attempt the renderer did not finish.
	failRenderer = "renderer"
	// failStorage is an attempt whose stored file could not be read or
	// whose tile could not be written.
	failStorage = "storage"
	// failUndrawable is a row recorded as not drawable once its attempts
	// ran out.
	failUndrawable = "undrawable"
)

// unfinishedReason classifies why an attempt did not finish.
func unfinishedReason(err error) string {
	if errors.Is(err, headless.ErrUnavailable) {
		return failRenderer
	}
	return failStorage
}

// kind is the row's family (asset, resource, collection, script), the first
// word of its name.
func (j job) kind() string {
	kind, _, _ := strings.Cut(j.name, " ")
	return kind
}

// kindKey carries the family of the row an attempt is drawing, so the draw
// that records a document's own failure can count it under that family.
type kindKey struct{}

func withKind(ctx context.Context, kind string) context.Context {
	return context.WithValue(ctx, kindKey{}, kind)
}

// recordDocumentFailure counts a failure the document owns under the family
// of the row being drawn.
func recordDocumentFailure(ctx context.Context) {
	kind, _ := ctx.Value(kindKey{}).(string)
	bgloop.Metrics().RecordThumbnailFailure(ctx, kind, failDocument)
}

// handBack ends the claim on a row that was not tried, returning the attempt
// the claim charged.
func (w *Worker) handBack(ctx context.Context, j job) {
	w.endClaim(ctx, j, 0, max(0, j.attempts-1))
}

func (*Worker) endClaim(ctx context.Context, j job, hold time.Duration, attempts int) {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storageTimeout)
	defer cancel()
	if err := j.hold(sctx, hold, attempts); err != nil {
		slog.ErrorContext(ctx, "thumbnails: ending a claim failed; it lapses with its lease",
			logKeyDocument, logsan.SanitizeForLog(j.name), logKeyError, logsan.SanitizeForLog(err.Error()))
	}
}

// giveUp records a row whose attempts never finished as not drawable.
func (w *Worker) giveUp(ctx context.Context, j job, last error) {
	// The last reason is trimmed of its prefixes before it is placed, and the
	// whole bounded after.
	reason := failureReason(fmt.Errorf("the renderer could not finish drawing this after %d attempts; the last: %s",
		w.cfg.MaxAttempts, failureReason(last)))
	slog.WarnContext(ctx, "thumbnails: recording a document as not drawable after its attempts",
		logKeyDocument, logsan.SanitizeForLog(j.name), "reason", logsan.SanitizeForLog(reason))
	if err := j.fail(ctx, reason); err != nil {
		slog.ErrorContext(ctx, "thumbnails: recording a failure failed", logKeyDocument, logsan.SanitizeForLog(j.name),
			logKeyError, logsan.SanitizeForLog(err.Error()))
	}
}

// assetJob is the job that draws one claimed asset.
func (w *Worker) assetJob(a portaldomain.Asset) job {
	return job{
		name:     "asset " + a.ID,
		attempts: a.ThumbnailAttempts,
		draw:     func(ctx context.Context) error { return w.drawAsset(ctx, a) },
		hold: func(ctx context.Context, hold time.Duration, attempts int) error {
			return w.deps.Assets.HoldThumbnailWork(ctx, a.ID, hold, attempts) //nolint:wrapcheck // logged by the caller with the document named
		},
		fail: func(ctx context.Context, reason string) error {
			version := a.CurrentVersion
			return w.deps.Assets.Update(ctx, a.ID, portaldomain.AssetUpdate{ //nolint:wrapcheck // logged by the caller with the document named
				ThumbnailFailure: &reason, ThumbnailFailedVersion: &version, ReleaseThumbnailClaim: true,
			})
		},
	}
}

// resourceJob is the job that draws one claimed resource.
func (w *Worker) resourceJob(r resource.Resource) job {
	return job{
		name:     "resource " + r.ID,
		attempts: r.ThumbnailAttempts,
		draw:     func(ctx context.Context) error { return w.drawResource(ctx, r) },
		hold: func(ctx context.Context, hold time.Duration, attempts int) error {
			return w.deps.Resources.HoldThumbnailWork(ctx, r.ID, hold, attempts) //nolint:wrapcheck // logged by the caller with the document named
		},
		fail: func(ctx context.Context, reason string) error {
			return w.deps.Resources.RecordThumbnailFailure(ctx, r.ID, reason, r.UpdatedAt) //nolint:wrapcheck // logged by the caller with the document named
		},
	}
}

// collectionJob is the job that composes one claimed collection's mosaic.
func (w *Worker) collectionJob(c portaldomain.CollectionThumbnailWork) job {
	return job{
		name:     "collection " + c.ID,
		attempts: c.Attempts,
		draw:     func(ctx context.Context) error { return w.drawCollection(ctx, c) },
		hold: func(ctx context.Context, hold time.Duration, attempts int) error {
			return w.deps.Collections.HoldCollectionThumbnailWork(ctx, c.ID, hold, attempts) //nolint:wrapcheck // logged by the caller with the document named
		},
		fail: func(ctx context.Context, reason string) error {
			return w.deps.Collections.RecordCollectionThumbnailFailure(ctx, c.ID, c.Source, reason) //nolint:wrapcheck // logged by the caller with the document named
		},
	}
}
