package knowledge

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/memory"
)

// memoryInsightAdapter implements InsightStore by delegating to a memory.Store.
type memoryInsightAdapter struct {
	store memory.Store
}

// NewMemoryInsightAdapter creates an InsightStore backed by a memory.Store.
func NewMemoryInsightAdapter(store memory.Store) InsightStore {
	return &memoryInsightAdapter{store: store}
}

// Insert creates a new insight record in the memory store.
func (a *memoryInsightAdapter) Insert(ctx context.Context, insight Insight) error {
	record := insightToRecord(insight)
	if err := a.store.Insert(ctx, record); err != nil {
		return fmt.Errorf("inserting insight record: %w", err)
	}
	return nil
}

// Get retrieves a single insight by ID.
func (a *memoryInsightAdapter) Get(ctx context.Context, id string) (*Insight, error) {
	record, err := a.store.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting insight record: %w", err)
	}
	insight := recordToInsight(*record)
	return &insight, nil
}

// List returns insights matching the given filter.
func (a *memoryInsightAdapter) List(ctx context.Context, filter InsightFilter) ([]Insight, int, error) {
	mf := memory.Filter{
		Category:  filter.Category,
		EntityURN: filter.EntityURN,
		CreatedBy: filter.CapturedBy,
		Source:    filter.Source,
		Since:     filter.Since,
		Until:     filter.Until,
		Limit:     filter.Limit,
		Offset:    filter.Offset,
	}

	// Map insight statuses to memory statuses.
	mf.Status = mapInsightStatusToMemory(filter.Status)

	// Confidence filtering is done post-fetch since memory.Filter doesn't have it.

	records, total, err := a.store.List(ctx, mf)
	if err != nil {
		return nil, 0, fmt.Errorf("listing insight records: %w", err)
	}

	insights := make([]Insight, 0, len(records))
	for _, r := range records {
		insight := recordToInsight(r)
		if filter.Confidence != "" && insight.Confidence != filter.Confidence {
			continue
		}
		insights = append(insights, insight)
	}

	return insights, total, nil
}

// UpdateStatus changes the review status of an insight.
func (a *memoryInsightAdapter) UpdateStatus(ctx context.Context, id, status, reviewedBy, reviewNotes string) error {
	meta := map[string]any{
		"reviewed_by":    reviewedBy,
		"review_notes":   reviewNotes,
		"insight_status": status,
	}
	if err := a.store.Update(ctx, id, memory.RecordUpdate{
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

// Stats returns aggregate counts of insights by category, confidence, and status.
func (a *memoryInsightAdapter) Stats(ctx context.Context, filter InsightFilter) (*InsightStats, error) {
	// Use List to build stats since memory.Store doesn't have a Stats method.
	mf := memory.Filter{
		Status: mapInsightStatusToMemory(filter.Status),
		Limit:  memory.MaxLimit,
	}

	records, total, err := a.store.List(ctx, mf)
	if err != nil {
		return nil, fmt.Errorf("listing records for insight stats: %w", err)
	}

	stats := &InsightStats{
		ByCategory:   make(map[string]int),
		ByConfidence: make(map[string]int),
		ByStatus:     make(map[string]int),
	}

	for _, r := range records {
		stats.ByCategory[r.Category]++
		stats.ByConfidence[r.Confidence]++
		stats.ByStatus[r.Status]++
	}
	stats.TotalPending = total

	return stats, nil
}

// MarkApplied records that an insight has been applied to the data platform.
func (a *memoryInsightAdapter) MarkApplied(ctx context.Context, id, appliedBy, changesetRef string) error {
	meta := map[string]any{
		"applied_by":    appliedBy,
		"changeset_ref": changesetRef,
	}
	// Mark as archived in memory (promoted/applied).
	if err := a.store.Update(ctx, id, memory.RecordUpdate{
		Metadata: meta,
	}); err != nil {
		return fmt.Errorf("marking insight applied: %w", err)
	}
	return nil
}

// Supersede marks older insights for an entity as superseded by a newer one.
func (a *memoryInsightAdapter) Supersede(ctx context.Context, entityURN, excludeID string) (int, error) {
	// List active records for this entity.
	records, _, err := a.store.List(ctx, memory.Filter{
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
		metadata["reviewed_by"] = insight.ReviewedBy
	}
	if insight.ReviewNotes != "" {
		metadata["review_notes"] = insight.ReviewNotes
	}
	if insight.AppliedBy != "" {
		metadata["applied_by"] = insight.AppliedBy
	}
	if insight.ChangesetRef != "" {
		metadata["changeset_ref"] = insight.ChangesetRef
	}

	relatedCols := make([]memory.RelatedColumn, len(insight.RelatedColumns))
	for i, rc := range insight.RelatedColumns {
		relatedCols[i] = memory.RelatedColumn{
			URN:       rc.URN,
			Column:    rc.Column,
			Relevance: rc.Relevance,
		}
	}

	return memory.Record{
		ID:             insight.ID,
		CreatedBy:      insight.CapturedBy,
		Persona:        insight.Persona,
		Dimension:      memory.DimensionKnowledge,
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
		Status:      mapMemoryStatusToInsight(record.Status),
	}

	// Extract RelatedColumns.
	for _, rc := range record.RelatedColumns {
		insight.RelatedColumns = append(insight.RelatedColumns, RelatedColumn{
			URN:       rc.URN,
			Column:    rc.Column,
			Relevance: rc.Relevance,
		})
	}

	// Extract metadata fields.
	if record.Metadata != nil {
		extractMetadataString(record.Metadata, "session_id", &insight.SessionID)
		extractMetadataString(record.Metadata, "reviewed_by", &insight.ReviewedBy)
		extractMetadataString(record.Metadata, "review_notes", &insight.ReviewNotes)
		extractMetadataString(record.Metadata, "applied_by", &insight.AppliedBy)
		extractMetadataString(record.Metadata, "changeset_ref", &insight.ChangesetRef)

		if sa, ok := record.Metadata["suggested_actions"]; ok {
			b, _ := json.Marshal(sa)
			_ = json.Unmarshal(b, &insight.SuggestedActions)
		}
	}

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
