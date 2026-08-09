package memory

import (
	"context"
	"fmt"
	"time"
)

// defaultRecallLimit is the default number of memory snippets returned per entity recall.
const defaultRecallLimit = 5

// Snippet is a lightweight memory representation for cross-enrichment.
type Snippet struct {
	ID         string    `json:"id"`
	Content    string    `json:"content"`
	Dimension  string    `json:"dimension"`
	Category   string    `json:"category"`
	Confidence string    `json:"confidence"`
	CreatedAt  time.Time `json:"created_at"`
	// EntityURNs are the catalog entities the record is linked to. The recall is
	// keyed by one entity at a time, but a record may name several, and the
	// enrichment path resolves them to say which table would settle a pushed
	// claim (#1220).
	EntityURNs []string `json:"entity_urns,omitempty"`
	// InsightStatus is the record's explicit review marker, empty when it
	// carries none. The enrichment path reads it to decide whether the record
	// can be handed out as a citable insight reference: only an applied insight
	// is organization knowledge every identified caller may dereference.
	InsightStatus string `json:"insight_status,omitempty"`
}

// InsightStatusOf returns a record's explicit review marker, or empty when it
// carries none. insight_status is authoritative and legacy_status (migration
// 000031) is its fallback, matching the precedence the entity-lookup gate
// applies in SQL. A record with neither marker is deliberately NOT resolved to
// the insight lens's "pending" default here: callers of this function ask what
// the record explicitly claims about itself, not what a lens would assume.
func InsightStatusOf(r Record) string {
	for _, key := range []string{MetaKeyInsightStatus, MetaKeyLegacyStatus} {
		if s, ok := r.Metadata[key].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// MiddlewareAdapter implements memory recall for the cross-enrichment middleware.
type MiddlewareAdapter struct {
	store Store
}

// NewMiddlewareAdapter creates a new adapter wrapping a memory store.
func NewMiddlewareAdapter(store Store) *MiddlewareAdapter {
	return &MiddlewareAdapter{store: store}
}

// RecallForEntities returns memory snippets linked to the given DataHub URNs.
func (a *MiddlewareAdapter) RecallForEntities(ctx context.Context, urns []string, persona string, limit int) ([]Snippet, error) {
	if len(urns) == 0 {
		return nil, nil
	}

	if limit <= 0 {
		limit = defaultRecallLimit
	}

	seen := make(map[string]bool)
	var snippets []Snippet

	for _, urn := range urns {
		records, err := a.store.EntityLookup(ctx, urn, persona, "")
		if err != nil {
			return nil, fmt.Errorf("entity lookup for %s: %w", urn, err)
		}

		for _, r := range records {
			if seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			snippets = append(snippets, Snippet{
				ID:            r.ID,
				Content:       r.Content,
				Dimension:     r.Dimension,
				Category:      r.Category,
				Confidence:    r.Confidence,
				CreatedAt:     r.CreatedAt,
				EntityURNs:    r.EntityURNs,
				InsightStatus: InsightStatusOf(r),
			})
			if len(snippets) >= limit {
				return snippets, nil
			}
		}
	}

	return snippets, nil
}
