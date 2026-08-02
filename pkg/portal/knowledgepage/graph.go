package knowledgepage

import (
	"context"
	"fmt"

	"github.com/lib/pq"
)

// GraphReader is the corpus-wide reference read behind the knowledge-graph view
// (#1162). It is an optional Store capability (the postgres store implements it,
// like Searcher) so the graph endpoint registers only on a backend that can serve
// it, rather than every Store implementation growing a method it cannot answer.
type GraphReader interface {
	// ListEntityRefsForPages returns the references of every given page in one
	// query, so a corpus-wide graph is one round trip rather than N+1.
	ListEntityRefsForPages(ctx context.Context, pageIDs []string) ([]EntityRef, error)
}

// Compile-time check: the postgres store serves the graph read.
var _ GraphReader = (*postgresStore)(nil)

// ListEntityRefsForPages returns the references of the given pages, oldest
// first within the whole set. An empty pageIDs returns no references without
// querying. References of a soft-deleted page are excluded, so a graph built from
// a live page listing never grows an edge out of a page the listing omitted.
func (s *postgresStore) ListEntityRefsForPages(ctx context.Context, pageIDs []string) ([]EntityRef, error) { //nolint:revive // interface impl
	if len(pageIDs) == 0 {
		return nil, nil
	}
	query := `SELECT ` + prefixedEntityRefColumns + `
		FROM knowledge_page_entity_refs r
		JOIN portal_knowledge_pages p ON p.id = r.page_id
		WHERE r.page_id = ANY($1) AND p.deleted_at IS NULL
		ORDER BY r.created_at, r.id`
	rows, err := s.db.QueryContext(ctx, query, pq.Array(pageIDs))
	if err != nil {
		return nil, fmt.Errorf("querying entity refs for pages: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var refs []EntityRef
	for rows.Next() {
		ref, scanErr := scanEntityRef(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating entity ref rows: %w", err)
	}
	return refs, nil
}
