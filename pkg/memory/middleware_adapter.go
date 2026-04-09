package memory

import (
	"context"
	"fmt"
	"time"
)

// defaultRecallLimit is the default number of memory snippets returned per entity recall.
const defaultRecallLimit = 5

// Snippet is a lightweight memory representation for cross-injection.
type Snippet struct {
	ID         string    `json:"id"`
	Content    string    `json:"content"`
	Dimension  string    `json:"dimension"`
	Category   string    `json:"category"`
	Confidence string    `json:"confidence"`
	CreatedAt  time.Time `json:"created_at"`
}

// MiddlewareAdapter implements memory recall for the cross-injection middleware.
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
		records, err := a.store.EntityLookup(ctx, urn, persona)
		if err != nil {
			return nil, fmt.Errorf("entity lookup for %s: %w", urn, err)
		}

		for _, r := range records {
			if seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			snippets = append(snippets, Snippet{
				ID:         r.ID,
				Content:    r.Content,
				Dimension:  r.Dimension,
				Category:   r.Category,
				Confidence: r.Confidence,
				CreatedAt:  r.CreatedAt,
			})
			if len(snippets) >= limit {
				return snippets, nil
			}
		}
	}

	return snippets, nil
}
