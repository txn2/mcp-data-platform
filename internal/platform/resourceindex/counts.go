package resourceindex

import (
	"context"
	"fmt"
)

// PolicyCounts is how many resources are under each index policy, and how
// many of them a writer chose.
type PolicyCounts struct {
	Full     int `json:"full"`
	Metadata int `json:"metadata"`
	None     int `json:"none"`
	Explicit int `json:"explicit"`
	// ContentBytes is the extracted text the library keeps for search.
	ContentBytes int64 `json:"content_bytes"`
	// Folders is the folders searchable as one entry.
	Folders int `json:"folders"`
}

// Counts reports the library's policies (#2111).
func (s *Store) Counts(ctx context.Context) (PolicyCounts, error) {
	var c PolicyCounts
	err := s.db.QueryRowContext(ctx, `SELECT
		COUNT(*) FILTER (WHERE index_policy = 'full'),
		COUNT(*) FILTER (WHERE index_policy = 'metadata'),
		COUNT(*) FILTER (WHERE index_policy = 'none'),
		COUNT(*) FILTER (WHERE index_policy_explicit),
		COALESCE(SUM(octet_length(content_text)), 0),
		(SELECT COUNT(*) FROM resource_folders WHERE embedding IS NOT NULL)
		FROM resources`).Scan(&c.Full, &c.Metadata, &c.None, &c.Explicit, &c.ContentBytes, &c.Folders)
	if err != nil {
		return c, fmt.Errorf("resourceindex: policy counts: %w", err)
	}
	return c, nil
}
