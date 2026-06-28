package knowledge

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/memory"
)

// Metadata keys used when round-tripping insights through memory.Record.Metadata.
const (
	metaKeyReviewedBy  = "reviewed_by"
	metaKeyReviewNotes = "review_notes"
	// metaKeyInsightStatus is the shared insight-overlay key (see pkg/memory),
	// so memory_capture and this adapter agree on where review state lives.
	metaKeyInsightStatus = memory.MetaKeyInsightStatus
	metaKeyChangesetRef  = "changeset_ref"
)

// memoryInsightAdapter implements InsightStore by delegating to a memory.Store.
// It is a knowledge-dimension view of the shared memory store: every query it
// issues is scoped to dimension via the dimension field, so callers can never
// see (or supersede) memory records from other dimensions.
type memoryInsightAdapter struct {
	store     memory.Store
	dimension string
}

// NewMemoryInsightAdapter creates an InsightStore backed by a memory.Store.
func NewMemoryInsightAdapter(store memory.Store) InsightStore {
	return &memoryInsightAdapter{store: store, dimension: memory.DimensionKnowledge}
}

// Insert creates a new insight record in the memory store.
func (a *memoryInsightAdapter) Insert(ctx context.Context, insight Insight) error {
	record := insightToRecord(insight)
	if err := a.store.Insert(ctx, record); err != nil {
		return fmt.Errorf("inserting insight record: %w", err)
	}
	return nil
}

// Get retrieves a single insight by ID. It is dimension-scoped like List and
// Search (the adapter is a knowledge-dimension view of the shared memory store): a
// record from another dimension is a memory, not an insight, so it is reported
// not-found rather than returned mislabeled. This keeps the by-id read consistent
// with the search paths, so an mcp:insight:<id> reference that actually names a
// non-knowledge memory record cannot resolve as an insight (#699).
func (a *memoryInsightAdapter) Get(ctx context.Context, id string) (*Insight, error) {
	record, err := a.store.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting insight record: %w", err)
	}
	if record.Dimension != a.dimension {
		return nil, fmt.Errorf("insight %s: %w", id, memory.ErrRecordNotFound)
	}
	insight := recordToInsight(*record)
	return &insight, nil
}

// List returns insights matching the given filter.
func (a *memoryInsightAdapter) List(ctx context.Context, filter InsightFilter) ([]Insight, int, error) {
	mf := memory.Filter{
		Dimension: a.dimension,
		Category:  filter.Category,
		EntityURN: filter.EntityURN,
		CreatedBy: filter.CapturedBy,
		Source:    filter.Source,
		Since:     filter.Since,
		Until:     filter.Until,
		Limit:     filter.Limit,
		Offset:    filter.Offset,
	}

	// Map the insight status onto its memory status. This mapping is lossy:
	// pending, approved and applied all collapse to memory StatusActive, so
	// the store fetch alone cannot distinguish them. The exact insight status
	// is recovered per record below and post-filtered.
	mf.Status = mapInsightStatusToMemory(filter.Status)

	records, total, err := a.store.List(ctx, mf)
	if err != nil {
		return nil, 0, fmt.Errorf("listing insight records: %w", err)
	}

	insights := make([]Insight, 0, len(records))
	for _, r := range records {
		insight := recordToInsight(r)
		// Confidence and the exact insight status are filtered post-fetch:
		// memory.Filter has no confidence field, and its status enum is
		// coarser than the insight status (see the lossy mapping above).
		if filter.Confidence != "" && insight.Confidence != filter.Confidence {
			continue
		}
		if filter.Status != "" && insight.Status != filter.Status {
			continue
		}
		insights = append(insights, insight)
	}

	return insights, total, nil
}

// InsightSearchQuery parameterizes a relevance-ranked insight search. It
// is owner-scoped (CapturedBy) and, like List, restricted to the knowledge
// dimension by the adapter. Embedding drives semantic (hybrid) ranking
// when non-empty; a nil/empty Embedding selects the lexical-only path used
// when no embedding provider is configured. The caller (the portal search
// handler) owns the embedder and precomputes Embedding so the adapter does
// not depend on an embedding provider.
type InsightSearchQuery struct {
	QueryText  string
	Embedding  []float32
	CapturedBy string
	Status     string
	Limit      int
}

// ScoredInsight pairs an insight with its search relevance score.
type ScoredInsight struct {
	Insight Insight
	Score   float64
}

// InsightSearcher is the optional relevance-search capability of an
// InsightStore. Only the memory-backed adapter implements it; the legacy
// SQL store and the noop store do not. Both the recall_insight tool and the
// portal insight-search route type-assert the wired store against this to
// gate registration, so the capability is declared once here next to the
// query and result types it uses.
type InsightSearcher interface {
	Search(ctx context.Context, q InsightSearchQuery) ([]ScoredInsight, error)
}

// SearchableInsightStore is an InsightStore that also supports relevance
// search. Only the memory-backed adapter satisfies it; the legacy SQL store and
// the noop store implement InsightStore but not InsightSearcher. The unified
// search wiring asserts the wired store against this so the insights search
// provider gets both the entity-keyed lookup (List, filtered by EntityURN) and
// the relevance search (Search) from one value.
type SearchableInsightStore interface {
	InsightStore
	InsightSearcher
}

// Search returns the caller's knowledge-dimension insights ranked by
// relevance to the query. It delegates to the shared memory search
// primitives (HybridSearch when an embedding is supplied, LexicalSearch
// otherwise), enforcing the same owner + knowledge-dimension scope as
// List, then maps the scored records back to insights. As in List, the
// exact insight status is recovered per record and post-filtered, because
// the memory status enum is coarser than the insight status.
func (a *memoryInsightAdapter) Search(ctx context.Context, q InsightSearchQuery) ([]ScoredInsight, error) {
	var (
		scored []memory.ScoredRecord
		err    error
	)
	memStatus := mapInsightStatusToMemory(q.Status)
	if len(q.Embedding) > 0 {
		scored, err = a.store.HybridSearch(ctx, memory.HybridQuery{
			Embedding: q.Embedding,
			QueryText: q.QueryText,
			CreatedBy: q.CapturedBy,
			Dimension: a.dimension,
			Status:    memStatus,
			Limit:     q.Limit,
		})
	} else {
		scored, err = a.store.LexicalSearch(ctx, memory.LexicalQuery{
			QueryText: q.QueryText,
			CreatedBy: q.CapturedBy,
			Dimension: a.dimension,
			Status:    memStatus,
			Limit:     q.Limit,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("searching insight records: %w", err)
	}

	results := make([]ScoredInsight, 0, len(scored))
	for i := range scored {
		insight := recordToInsight(scored[i].Record)
		if q.Status != "" && insight.Status != q.Status {
			continue
		}
		results = append(results, ScoredInsight{Insight: insight, Score: scored[i].Score})
	}
	return results, nil
}

// UpdateStatus changes the review status of an insight.
func (a *memoryInsightAdapter) UpdateStatus(ctx context.Context, id, status, reviewedBy, reviewNotes string) error {
	meta := map[string]any{
		metaKeyReviewedBy:    reviewedBy,
		metaKeyReviewNotes:   reviewNotes,
		metaKeyInsightStatus: status,
	}
	if err := a.store.Update(ctx, id, memory.RecordUpdate{
		// Persist the mapped memory status to the column, not just the metadata:
		// a rejected insight maps to archived, and recall filters on the status
		// column, so without this a rejected insight stays active and keeps
		// surfacing in memory_recall.
		Status:   mapInsightStatusToMemory(status),
		Metadata: meta,
	}); err != nil {
		return fmt.Errorf("updating insight status: %w", err)
	}
	return nil
}

// Update modifies fields on an existing insight.
func (a *memoryInsightAdapter) Update(ctx context.Context, id string, updates InsightUpdate) error {
	if err := a.store.Update(ctx, id, memory.RecordUpdate{
		Content:    updates.InsightText,
		Category:   updates.Category,
		Confidence: updates.Confidence,
	}); err != nil {
		return fmt.Errorf("updating insight: %w", err)
	}
	return nil
}

// Stats returns aggregate counts of insights by category, confidence, and
// status. The memory.Store has no Stats method, so we page through the
// matching records and tally them. The filter must be scoped the same way
// List scopes (owner + knowledge dimension); otherwise the totals would
// count other users' records and non-knowledge memory dimensions, leaving
// the stat card and the list disagreeing.
func (a *memoryInsightAdapter) Stats(ctx context.Context, filter InsightFilter) (*InsightStats, error) {
	mf := memory.Filter{
		Dimension: a.dimension,
		CreatedBy: filter.CapturedBy,
		Status:    mapInsightStatusToMemory(filter.Status),
		Limit:     memory.MaxLimit,
	}

	stats := &InsightStats{
		ByCategory:   make(map[string]int),
		ByConfidence: make(map[string]int),
		ByStatus:     make(map[string]int),
	}

	for {
		records, _, err := a.store.List(ctx, mf)
		if err != nil {
			return nil, fmt.Errorf("listing records for insight stats: %w", err)
		}
		for i := range records {
			// Recover the insight status (pending/approved/applied/...) from the
			// lossy memory status, so the keys match what callers and the postgres
			// store produce.
			st := resolveInsightStatus(records[i])
			// Scope every tally to the requested status, the way the postgres store
			// does (its WHERE filters all three group-bys). The memory status filter
			// is lossy: a Status=pending request maps to memory.StatusActive, which
			// also returns approved and applied records (mapInsightStatusToMemory).
			// Without this gate ByStatus/by_category/by_confidence span every active
			// status while TotalPending counts only pending, so the counts disagree
			// with each other and with postgres (#688). An empty filter (the portal
			// "my stats" path, #515) still tallies every status.
			if filter.Status != "" && st != filter.Status {
				continue
			}
			stats.ByStatus[st]++
			stats.ByCategory[records[i].Category]++
			stats.ByConfidence[records[i].Confidence]++
		}
		if len(records) < memory.MaxLimit {
			break
		}
		mf.Offset += memory.MaxLimit
	}
	stats.TotalPending = stats.ByStatus[StatusPending]

	return stats, nil
}

// MarkApplied records that an insight has been applied to the data platform.
func (a *memoryInsightAdapter) MarkApplied(ctx context.Context, id, appliedBy, changesetRef string) error {
	meta := map[string]any{
		colAppliedBy:        appliedBy,
		metaKeyChangesetRef: changesetRef,
		// Persist the applied status explicitly. The memory status of an
		// applied insight stays StatusActive (see mapInsightStatusToMemory),
		// so without this override resolveInsightStatus would report applied
		// insights as pending, inflating the pending count and leaving the
		// applied count at zero (mirrors MarkRolledBack / UpdateStatus).
		metaKeyInsightStatus: StatusApplied,
	}
	// Deliberately metadata-only: applied maps to active, and a legitimately
	// applied insight (approved -> applied) is already active, so the column
	// needs no change. Force-writing active here would resurrect a previously
	// archived insight (e.g. apply called on a rejected id, which the apply path
	// does not transition-validate) back into recall, re-opening #579.
	if err := a.store.Update(ctx, id, memory.RecordUpdate{
		Metadata: meta,
	}); err != nil {
		return fmt.Errorf("marking insight applied: %w", err)
	}
	return nil
}

// MarkRolledBack transitions an applied insight to rolled_back in the memory store.
func (a *memoryInsightAdapter) MarkRolledBack(ctx context.Context, id, rolledBackBy string) error {
	meta := map[string]any{
		metaKeyReviewedBy:    rolledBackBy,
		metaKeyInsightStatus: StatusRolledBack,
	}
	if err := a.store.Update(ctx, id, memory.RecordUpdate{
		// Rolled-back insights map to archived; persist it to the status column
		// so they stop surfacing in memory_recall (same fix as UpdateStatus).
		Status:   mapInsightStatusToMemory(StatusRolledBack),
		Metadata: meta,
	}); err != nil {
		return fmt.Errorf("marking insight rolled back: %w", err)
	}
	return nil
}

// Supersede marks older insights for an entity as superseded by a newer one.
func (a *memoryInsightAdapter) Supersede(ctx context.Context, entityURN, excludeID string) (int, error) {
	// List active records for this entity. Scoped to the knowledge dimension
	// so we never supersede a non-knowledge memory record that happens to
	// reference the same entity URN.
	records, _, err := a.store.List(ctx, memory.Filter{
		Dimension: a.dimension,
		EntityURN: entityURN,
		Status:    memory.StatusActive,
		Limit:     memory.MaxLimit,
	})
	if err != nil {
		return 0, fmt.Errorf("listing records for supersede: %w", err)
	}

	count := 0
	for _, r := range records {
		if r.ID == excludeID {
			continue
		}
		if err := a.store.Supersede(ctx, r.ID, excludeID); err != nil {
			return count, fmt.Errorf("superseding %s: %w", r.ID, err)
		}
		count++
	}

	return count, nil
}

// insightToRecord converts an Insight to a memory.Record.
func insightToRecord(insight Insight) memory.Record {
	metadata := map[string]any{}
	if len(insight.SuggestedActions) > 0 {
		metadata["suggested_actions"] = insight.SuggestedActions
	}
	if insight.SessionID != "" {
		metadata["session_id"] = insight.SessionID
	}
	if insight.ReviewedBy != "" {
		metadata[metaKeyReviewedBy] = insight.ReviewedBy
	}
	if insight.ReviewNotes != "" {
		metadata[metaKeyReviewNotes] = insight.ReviewNotes
	}
	if insight.AppliedBy != "" {
		metadata[colAppliedBy] = insight.AppliedBy
	}
	if insight.ChangesetRef != "" {
		metadata[metaKeyChangesetRef] = insight.ChangesetRef
	}

	relatedCols := make([]memory.RelatedColumn, len(insight.RelatedColumns))
	for i, rc := range insight.RelatedColumns {
		relatedCols[i] = memory.RelatedColumn{
			URN:       rc.URN,
			Column:    rc.Column,
			Relevance: rc.Relevance,
		}
	}

	sinkClass := insight.SinkClass
	if sinkClass == "" {
		sinkClass = memory.DeriveSinkClass(memory.DimensionKnowledge, len(insight.EntityURNs) > 0)
	}

	return memory.Record{
		ID:             insight.ID,
		CreatedBy:      insight.CapturedBy,
		Persona:        insight.Persona,
		Dimension:      memory.DimensionKnowledge,
		SinkClass:      sinkClass,
		Content:        insight.InsightText,
		Category:       insight.Category,
		Confidence:     insight.Confidence,
		Source:         insight.Source,
		EntityURNs:     insight.EntityURNs,
		RelatedColumns: relatedCols,
		Metadata:       metadata,
		Status:         mapInsightStatusToMemory(insight.Status),
	}
}

// sinkClassOrDerived returns the record's stored sink_class, or derives it from
// the dimension and entity URNs when the column is empty (rows captured before
// #633 added the column; the migration backfills most, this covers any straggler).
func sinkClassOrDerived(record memory.Record) string {
	if record.SinkClass != "" {
		return record.SinkClass
	}
	dim := record.Dimension
	if dim == "" {
		dim = memory.DimensionKnowledge
	}
	return memory.DeriveSinkClass(dim, len(record.EntityURNs) > 0)
}

// recordToInsight converts a memory.Record to an Insight.
func recordToInsight(record memory.Record) Insight {
	insight := Insight{
		ID:          record.ID,
		CreatedAt:   record.CreatedAt,
		CapturedBy:  record.CreatedBy,
		Persona:     record.Persona,
		Source:      record.Source,
		Category:    record.Category,
		InsightText: record.Content,
		Confidence:  record.Confidence,
		EntityURNs:  record.EntityURNs,
		Status:      resolveInsightStatus(record),
		// Derive the sink-class for rows captured before the column existed
		// (pre-#633 NULL/empty), so callers always see a populated class.
		SinkClass: sinkClassOrDerived(record),
	}

	// Extract RelatedColumns.
	for _, rc := range record.RelatedColumns {
		insight.RelatedColumns = append(insight.RelatedColumns, RelatedColumn{
			URN:       rc.URN,
			Column:    rc.Column,
			Relevance: rc.Relevance,
		})
	}

	extractInsightMetadata(record.Metadata, &insight)

	// Ensure non-nil slices.
	if insight.EntityURNs == nil {
		insight.EntityURNs = []string{}
	}
	if insight.RelatedColumns == nil {
		insight.RelatedColumns = []RelatedColumn{}
	}
	if insight.SuggestedActions == nil {
		insight.SuggestedActions = []SuggestedAction{}
	}

	return insight
}

// extractMetadataString extracts a string value from metadata.
// resolveInsightStatus determines the insight status from a memory record,
// preferring an explicit insight_status in metadata over the memory status mapping.
func resolveInsightStatus(record memory.Record) string {
	status := mapMemoryStatusToInsight(record.Status)
	if record.Metadata == nil {
		return status
	}
	// Prefer explicit insight_status (set by UpdateStatus/approve/reject).
	if v, ok := record.Metadata[metaKeyInsightStatus]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	// Fall back to legacy_status (set by migration from knowledge_insights).
	if v, ok := record.Metadata["legacy_status"]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return status
}

// extractInsightMetadata populates insight fields from the record metadata map.
func extractInsightMetadata(meta map[string]any, insight *Insight) {
	if meta == nil {
		return
	}
	extractMetadataString(meta, "session_id", &insight.SessionID)
	extractMetadataString(meta, metaKeyReviewedBy, &insight.ReviewedBy)
	extractMetadataString(meta, metaKeyReviewNotes, &insight.ReviewNotes)
	extractMetadataString(meta, colAppliedBy, &insight.AppliedBy)
	extractMetadataString(meta, metaKeyChangesetRef, &insight.ChangesetRef)

	if sa, ok := meta["suggested_actions"]; ok {
		b, _ := json.Marshal(sa)
		_ = json.Unmarshal(b, &insight.SuggestedActions)
	}
}

func extractMetadataString(meta map[string]any, key string, target *string) {
	if v, ok := meta[key]; ok {
		if s, ok := v.(string); ok {
			*target = s
		}
	}
}

// mapInsightStatusToMemory converts insight statuses to memory statuses.
func mapInsightStatusToMemory(status string) string {
	switch status {
	case StatusPending, StatusApproved:
		return memory.StatusActive
	case StatusRejected, StatusRolledBack:
		return memory.StatusArchived
	case StatusSuperseded:
		return memory.StatusSuperseded
	case StatusApplied:
		return memory.StatusActive // Applied insights stay active in memory
	default:
		return status
	}
}

// mapMemoryStatusToInsight converts memory statuses back to insight statuses.
func mapMemoryStatusToInsight(status string) string {
	switch status {
	case memory.StatusActive:
		return StatusPending
	case memory.StatusArchived:
		return StatusRejected
	case memory.StatusSuperseded:
		return StatusSuperseded
	default:
		return status
	}
}

// Verify interface compliance.
var _ InsightStore = (*memoryInsightAdapter)(nil)
