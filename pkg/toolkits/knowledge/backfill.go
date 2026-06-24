package knowledge

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// backfillPageBatch is the page-list batch size. It is the store's maximum list
// limit (clampSearchLimit caps anything above 100 back down to the default), so
// the backfill must page through with Offset rather than request all at once.
var backfillPageBatch = 100

// backfillChangesetLimit caps changesets read per page when re-deriving promoted
// refs. A page accrues one changeset per promotion, so this is far above any real
// count; it is the changeset store's maximum (EffectiveLimit caps above 100).
const backfillChangesetLimit = 100

// logKeyError is the structured-log key for an error value.
const logKeyError = "error"

// backfillSentinelName keys this backfill's row in platform_backfills.
const backfillSentinelName = "kp_entity_refs_v1"

// RunGuardedBackfill runs BackfillPageRefs once, guarded by the platform_backfills
// sentinel: if the sentinel is absent it runs the backfill and records it. It is
// idempotent and leaves the sentinel unset on failure so it retries on the next
// start. Failures are logged, never fatal. Intended to be called in a goroutine at
// startup.
func (t *Toolkit) RunGuardedBackfill(ctx context.Context, db *sql.DB, pages knowledgepage.Store) {
	if db == nil {
		return
	}
	var exists bool
	if err := db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM platform_backfills WHERE name = $1)`, backfillSentinelName).Scan(&exists); err != nil {
		slog.WarnContext(ctx, "knowledge-page ref backfill: sentinel check failed", logKeyError, err)
		return
	}
	if exists {
		return
	}
	stats, err := t.BackfillPageRefs(ctx, pages)
	if err != nil {
		slog.WarnContext(ctx, "knowledge-page ref backfill failed", logKeyError, err)
		return
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO platform_backfills (name) VALUES ($1) ON CONFLICT (name) DO NOTHING`, backfillSentinelName); err != nil {
		slog.WarnContext(ctx, "knowledge-page ref backfill: marking sentinel failed", logKeyError, err)
		return
	}
	slog.InfoContext(ctx, "knowledge-page ref backfill complete",
		"pages_scanned", stats.PagesScanned, "inline_refs", stats.InlineRefs, "promoted_refs", stats.PromotedRefs)
}

// BackfillStats reports what a reference backfill touched.
type BackfillStats struct {
	PagesScanned int `json:"pages_scanned"`
	InlineRefs   int `json:"inline_refs"`
	PromotedRefs int `json:"promoted_refs"`
}

// BackfillPageRefs re-derives entity references for existing knowledge pages, so
// pages that predate the reference feature get their references without a manual
// re-save (#664 Phase 5). For each live page it:
//
//  1. re-scans the body for inline references and reconciles them as source=inline
//     (the same reconcile a page save performs); and
//  2. re-derives the references the page's source insights carried, via the
//     page's changesets, and adds them as source=promoted.
//
// It is idempotent (the inline reconcile is a source-scoped replace, the promoted
// add unions) and best-effort per page: a page whose reference cannot be written
// (for example an inline mention of a since-deleted entity, the same case the live
// save path swallows) is logged and skipped, so one bad page never aborts the
// pass. Only a failure to list pages is returned. Pages are processed in batches
// because the store caps a single list at backfillPageBatch.
func (t *Toolkit) BackfillPageRefs(ctx context.Context, pages knowledgepage.Store) (BackfillStats, error) {
	var stats BackfillStats
	for offset := 0; ; offset += backfillPageBatch {
		batch, _, err := pages.List(ctx, knowledgepage.Filter{Limit: backfillPageBatch, Offset: offset})
		if err != nil {
			return stats, fmt.Errorf("listing pages for backfill: %w", err)
		}
		for i := range batch {
			t.backfillPage(ctx, pages, batch[i], &stats)
		}
		if len(batch) < backfillPageBatch {
			return stats, nil
		}
	}
}

// backfillPage reconciles one page's inline and promoted references, best-effort:
// a per-page write failure is logged and skipped rather than aborting the pass.
func (t *Toolkit) backfillPage(ctx context.Context, pages knowledgepage.Store, page knowledgepage.Page, stats *BackfillStats) {
	stats.PagesScanned++

	inline := knowledgepage.ScanBodyRefs(page.Body)
	if err := pages.ReplaceEntityRefsBySource(ctx, page.ID, knowledgepage.RefSourceInline, inline); err != nil {
		slog.WarnContext(ctx, "backfill: reconciling inline refs failed", "page_id", page.ID, logKeyError, err)
	} else {
		stats.InlineRefs += len(inline)
	}

	urns, err := t.pageSourceURNs(ctx, page.Slug)
	if err != nil {
		slog.WarnContext(ctx, "backfill: gathering source URNs failed", "page_id", page.ID, logKeyError, err)
		return
	}
	if len(urns) > 0 {
		if err := pages.AddEntityRefs(ctx, page.ID, promotedRefsFromURNs(urns)); err != nil {
			slog.WarnContext(ctx, "backfill: adding promoted refs failed", "page_id", page.ID, logKeyError, err)
			return
		}
		stats.PromotedRefs += len(urns)
	}
}

// pageSourceURNs gathers the de-duplicated entity URNs the page's source insights
// carried, found via the page's changesets (target_urn = "kp:<slug>"). A page with
// no slug or no recoverable changeset/insight yields none.
func (t *Toolkit) pageSourceURNs(ctx context.Context, slug string) ([]string, error) {
	if t.changesetStore == nil || slug == "" {
		return nil, nil
	}
	css, _, err := t.changesetStore.ListChangesets(ctx, ChangesetFilter{
		EntityURN: pageTargetPrefix + slug,
		Limit:     backfillChangesetLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("listing changesets for page %q: %w", slug, err)
	}
	seen := map[string]struct{}{}
	var urns []string
	for _, cs := range css {
		urns = t.appendInsightURNs(ctx, cs.SourceInsightIDs, seen, urns)
	}
	return urns, nil
}

// appendInsightURNs appends the de-duplicated entity URNs carried by the given
// insights (skipping drained/deleted ones) to urns, recording each in seen.
func (t *Toolkit) appendInsightURNs(ctx context.Context, insightIDs []string, seen map[string]struct{}, urns []string) []string {
	for _, insightID := range insightIDs {
		ins, err := t.store.Get(ctx, insightID)
		if err != nil || ins == nil {
			continue // a drained or deleted insight is simply unrecoverable
		}
		urns = appendUnique(urns, seen, insightReferenceURNs(ins))
	}
	return urns
}

// appendUnique appends each value not already in seen to dst, recording it.
func appendUnique(dst []string, seen map[string]struct{}, values []string) []string {
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		dst = append(dst, v)
	}
	return dst
}
