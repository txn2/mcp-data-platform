package memory

import (
	"context"
	"log/slog"

	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
)

// SettleRecall runs the recall-first check for a capture whose embedding has
// just been written (#1987). The index job calls it with the vector it wrote,
// so the check costs no embed of its own.
//
// A capture is stored before it is embedded, and two restatements captured
// close together can be embedded in either order. So the rule is about the
// records rather than about which job ran first: of two records at or above
// the supersede threshold, the NEWER supersedes the older. When this record is
// the newer, it supersedes every restated record (all of them, so a capture
// arriving over an already-duplicated pair consolidates the whole set); when a
// restated record is newer, this one is superseded by the best such match.
// Records in the suggest band are written to this record's metadata as
// similar_existing.
//
// Only a record marked recall_check=pending is checked, once: a re-embed after
// an edit or a model swap is not a new capture. Best-effort throughout; a
// failure is logged and leaves the record pending, where the next write of its
// embedding checks it again.
func (t *Toolkit) SettleRecall(ctx context.Context, id string, emb []float32) {
	if t.recallChecker == nil || len(emb) == 0 {
		return
	}
	rec, err := t.store.Get(ctx, id)
	if err != nil {
		slog.Debug("memory recall: record not readable", "id", id, logKeyError, err)
		return
	}
	if rec.Metadata[memstore.MetaKeyRecallCheck] != memstore.RecallCheckPending {
		return
	}
	var similar []RecallMatch
	if rec.Status != memstore.StatusSuperseded && rec.Status != memstore.StatusArchived {
		matches, err := t.recallChecker.Matches(ctx, RecallQuery{
			Embedding:   emb,
			EntityURNs:  rec.EntityURNs,
			CallerEmail: rec.CreatedBy,
			MinScore:    recallSuggestThreshold,
		})
		if err != nil {
			slog.Warn("memory recall: similarity check failed; leaving the record pending", "id", id, logKeyError, err)
			return
		}
		similar = t.applyRecall(ctx, rec, matches)
	}
	meta := map[string]any{memstore.MetaKeyRecallCheck: memstore.RecallCheckDone}
	if len(similar) > 0 {
		meta[memstore.MetaKeySimilarExisting] = similar
	}
	if err := t.store.Update(ctx, id, memstore.RecordUpdate{Metadata: meta}); err != nil {
		slog.Warn("memory recall: failed to record the check", "id", id, logKeyError, err)
	}
}

// applyRecall supersedes the older of each restating pair and returns the
// matches in the suggest band. Matches arrive best first.
func (t *Toolkit) applyRecall(ctx context.Context, rec *memstore.Record, matches []RecallMatch) []RecallMatch {
	var similar []RecallMatch
	newer := ""
	for _, m := range matches {
		switch {
		case m.ID == "" || m.ID == rec.ID:
		case m.Score < recallSupersedeThreshold:
			similar = append(similar, m)
		case isNewer(m, rec):
			if newer == "" {
				newer = m.ID
			}
		default:
			t.supersede(ctx, m.ID, rec.ID)
		}
	}
	if newer != "" {
		t.supersede(ctx, rec.ID, newer)
	}
	return similar
}

// supersede marks oldID superseded by newID, logging a failure: the capture is
// already stored, and a record left active is a duplicate the owner can still
// consolidate.
func (t *Toolkit) supersede(ctx context.Context, oldID, newID string) {
	if err := t.store.Supersede(ctx, oldID, newID); err != nil {
		slog.Warn("memory recall: failed to supersede", "old", oldID, "new", newID, logKeyError, err)
	}
}

// isNewer reports whether the match was captured after rec. Two records stored
// in the same instant are ordered by id, so the two checks of a pair agree on
// which one wins.
func isNewer(m RecallMatch, rec *memstore.Record) bool {
	if !m.CreatedAt.Equal(rec.CreatedAt) {
		return m.CreatedAt.After(rec.CreatedAt)
	}
	return m.ID > rec.ID
}
