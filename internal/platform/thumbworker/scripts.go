package thumbworker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow"
	"github.com/txn2/mcp-data-platform/internal/platform/scripttiles"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// FlowTileType is the content type a script's flow graph reaches the tile page
// as (#1909). It is no file's type: the page's Tile draws it with the same
// layout and card drawing the Flow tab uses, fit to the tile. It deliberately
// does not end in +json: a tile page that does not know it answers that
// nothing draws it, which is recorded, rather than drawing the graph's JSON as
// if it were a document.
const FlowTileType = "application/vnd.mcp-data-platform.flow-graph"

// LibraryTileType is the content type a library's tile reaches the tile page
// as (#1970): a library makes no platform calls, so it has no diagram to draw,
// and its tile names it instead. The content is libraryTile's JSON.
const LibraryTileType = "application/vnd.mcp-data-platform.library"

// libraryTile is what a library's tile is drawn from, beside the name the tile
// page is always handed.
type libraryTile struct {
	Version int `json:"version"`
}

// ScriptRenderer is the generation of a script's flow tile, apart from
// Renderer so that redrawing every script tile does not redraw every asset's.
// It starts where Renderer stood when scripts were first drawn.
//
// 3 draws the Structure view, the order the script runs in (#1972). 4 draws a
// library as a library rather than as an empty diagram (#1970).
const ScriptRenderer = 4

// orphanBatch is how many deleted scripts' tiles one pass removes.
const orphanBatch = 50

// ScriptWork is what the worker asks of the script tile store.
type ScriptWork interface {
	Claim(ctx context.Context, renderer int, lease time.Duration, limit int) ([]scripttiles.Work, error)
	Record(ctx context.Context, scriptID string, version int, key string, renderer int) error
	RecordFailure(ctx context.Context, scriptID string, version int, reason string) error
	Hold(ctx context.Context, scriptID string, hold time.Duration, attempts int) error
	Orphans(ctx context.Context, limit int) ([]scripttiles.Orphan, error)
	Forget(ctx context.Context, scriptID string) error
}

// claimScripts is claimAssets for script flow tiles.
func (w *Worker) claimScripts(ctx context.Context) []job {
	if w.deps.Scripts == nil {
		return nil
	}
	w.sweepScripts(ctx)
	work, err := w.deps.Scripts.Claim(ctx, ScriptRenderer, w.cfg.Lease, w.cfg.Batch)
	logClaim("scripts", err)
	jobs := make([]job, 0, len(work))
	for _, s := range work {
		jobs = append(jobs, w.scriptJob(s))
	}
	return jobs
}

// scriptJob is the job that draws one claimed script's tile.
func (w *Worker) scriptJob(s scripttiles.Work) job {
	return job{
		name:     "script " + s.ScriptID,
		attempts: s.Attempts,
		draw:     func(ctx context.Context) error { return w.drawScript(ctx, s) },
		hold: func(ctx context.Context, hold time.Duration, attempts int) error {
			return w.deps.Scripts.Hold(ctx, s.ScriptID, hold, attempts) //nolint:wrapcheck // logged by the caller with the document named
		},
		fail: func(ctx context.Context, reason string) error {
			return w.deps.Scripts.RecordFailure(ctx, s.ScriptID, s.Version, reason) //nolint:wrapcheck // logged by the caller with the document named
		},
	}
}

// drawScript draws a script's flow diagram, light and dark, and records it
// against the version it was drawn from. A version that does not parse is
// recorded as not drawable, with the parse error as the reason; a script with
// no platform calls draws its empty diagram, and a library its name and
// version.
func (w *Worker) drawScript(ctx context.Context, s scripttiles.Work) error {
	g := scriptflow.Derive(s.Source)
	if !g.OK {
		w.failScript(ctx, s, parseReason(g))
		return nil
	}
	src, err := scriptTileSource(s, g)
	if err != nil {
		return err
	}
	t := target{
		src:    src,
		bucket: w.deps.CollectionBucket,
		blobs:  w.deps.AssetBlobs,
		keyFor: func(variant string) string { return scripttiles.Key(w.deps.CollectionPrefix, s.ScriptID, variant) },
	}
	_, reason, unfinished := w.drawVariants(ctx, t,
		[]string{portaldomain.ThumbnailVariantLight, portaldomain.ThumbnailVariantDark})
	if unfinished != nil {
		return unfinished
	}
	if reason != "" {
		w.failScript(ctx, s, reason)
		return nil
	}
	key := scripttiles.Key(w.deps.CollectionPrefix, s.ScriptID, scripttiles.VariantLight)
	if err := w.deps.Scripts.Record(ctx, s.ScriptID, s.Version, key, ScriptRenderer); err != nil {
		slog.ErrorContext(ctx, "thumbnails: recording a script's tile failed", logKeyScript, logsan.SanitizeForLog(s.ScriptID), logKeyError, logsan.SanitizeForLog(err.Error()))
	}
	return nil
}

// scriptTileSource is what the tile page is handed for a script: its flow
// graph, or for a library the version its tile names.
func scriptTileSource(s scripttiles.Work, g scriptflow.Graph) (tileSource, error) {
	if s.Library {
		payload, err := json.Marshal(libraryTile{Version: s.Version})
		if err != nil {
			return tileSource{}, fmt.Errorf("encoding the library tile: %w", err)
		}
		return tileSource{contentType: LibraryTileType, content: payload, name: s.Name}, nil
	}
	payload, err := json.Marshal(g)
	if err != nil {
		return tileSource{}, fmt.Errorf("encoding the flow graph: %w", err)
	}
	return tileSource{contentType: FlowTileType, content: payload, name: s.Name}, nil
}

// logKeyScript names a script in the log.
const logKeyScript = "script"

func (w *Worker) failScript(ctx context.Context, s scripttiles.Work, reason string) {
	if err := w.deps.Scripts.RecordFailure(ctx, s.ScriptID, s.Version, reason); err != nil {
		slog.ErrorContext(ctx, "thumbnails: recording a script's failure failed", logKeyScript, logsan.SanitizeForLog(s.ScriptID), logKeyError, logsan.SanitizeForLog(err.Error()))
	}
}

// parseReason is why a version that does not parse has no tile: its first
// error, with the line it is on.
func parseReason(g scriptflow.Graph) string {
	if len(g.Findings) == 0 {
		return "the source does not parse"
	}
	f := g.Findings[0]
	if f.Line > 0 {
		return fmt.Sprintf("the source does not parse: line %d: %s", f.Line, f.Message)
	}
	return "the source does not parse: " + f.Message
}

// sweepScripts removes the stored tiles of scripts that were deleted, then
// their rows, so a deleted script leaves no object behind. A tile that cannot
// be removed keeps its row and is tried again on a later pass.
func (w *Worker) sweepScripts(ctx context.Context) {
	orphans, err := w.deps.Scripts.Orphans(ctx, orphanBatch)
	if err != nil {
		slog.ErrorContext(ctx, "thumbnails: listing deleted scripts' tiles failed", logKeyError, logsan.SanitizeForLog(err.Error()))
		return
	}
	for _, o := range orphans {
		if o.Key != "" && !w.removeScriptTiles(ctx, o.Key) {
			continue
		}
		if err := w.deps.Scripts.Forget(ctx, o.ScriptID); err != nil {
			slog.ErrorContext(ctx, "thumbnails: forgetting a deleted script's tile failed", logKeyScript, logsan.SanitizeForLog(o.ScriptID), logKeyError, logsan.SanitizeForLog(err.Error()))
		}
	}
}

// removeScriptTiles deletes both variants of one stored tile, reporting
// whether both are gone.
func (w *Worker) removeScriptTiles(ctx context.Context, lightKey string) bool {
	for _, key := range []string{lightKey, scripttiles.DarkKey(lightKey)} {
		sctx, cancel := context.WithTimeout(ctx, storageTimeout)
		err := w.deps.AssetBlobs.DeleteObject(sctx, w.deps.CollectionBucket, key)
		cancel()
		if err != nil {
			slog.ErrorContext(ctx, "thumbnails: removing a deleted script's tile failed", "key", logsan.SanitizeForLog(key), logKeyError, logsan.SanitizeForLog(err.Error()))
			return false
		}
	}
	return true
}
