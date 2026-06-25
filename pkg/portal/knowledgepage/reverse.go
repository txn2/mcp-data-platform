package knowledgepage

import (
	"context"
	"database/sql"
	"fmt"
)

// PageRef identifies a knowledge page that references an entity. It is the result
// of the reverse lookup (the pages that reference a target), the counterpart of
// ListEntityRefs (a page's references).
type PageRef struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

// ReverseLookup is the reverse-lookup capability PagesForURNs needs (the pages that
// reference a target), satisfied by Store and by lighter adapters in callers.
type ReverseLookup interface {
	ListPagesReferencing(ctx context.Context, ref EntityRef) ([]PageRef, error)
}

// PagesForURNs returns the distinct pages that reference any of the given entity
// URNs, in first-seen order, capped at limit (limit <= 0 means no cap). A URN that
// does not parse as a reference is skipped; a lookup error is returned. It backs the
// cross-enrichment that surfaces the knowledge about the entities a tool returns.
func PagesForURNs(ctx context.Context, store ReverseLookup, urns []string, limit int) ([]PageRef, error) {
	seen := make(map[string]bool)
	out := make([]PageRef, 0, len(urns))
	for _, urn := range urns {
		ref, err := ParseEntityRef(urn)
		if err != nil {
			continue
		}
		pages, err := store.ListPagesReferencing(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("listing pages referencing %q: %w", urn, err)
		}
		for _, pg := range pages {
			if seen[pg.ID] {
				continue
			}
			seen[pg.ID] = true
			out = append(out, pg)
			if limit > 0 && len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}

// reverseLookupFilter returns the WHERE clause and arguments that select the
// references pointing at the given target, or ("", nil) for an unknown type. The
// clause is a fixed literal per type; the target values are returned as args and
// bound as query parameters, never concatenated into the SQL.
func reverseLookupFilter(ref EntityRef) (where string, args []any) {
	switch ref.TargetType {
	case RefTargetAsset:
		return "r.asset_id = $1", []any{ref.AssetID}
	case RefTargetPrompt:
		return "r.prompt_id = $1", []any{ref.PromptID}
	case RefTargetCollection:
		return "r.collection_id = $1", []any{ref.CollectionID}
	case RefTargetKnowledgePage:
		return "r.ref_page_id = $1", []any{ref.RefPageID}
	case RefTargetConnection:
		return "r.connection_kind = $1 AND r.connection_name = $2", connArgs(ref)
	case RefTargetDataHub:
		return "r.entity_urn = $1", []any{ref.EntityURN}
	default:
		return "", nil
	}
}

// connArgs builds the two bound parameters for a connection reverse lookup. The
// fixed-capacity make plus append avoids a slice-literal-with-field pattern that
// the unbounded-make-slice-capacity rule false-positives on.
func connArgs(ref EntityRef) []any {
	args := make([]any, 0, 2)
	return append(args, ref.ConnectionKind, ref.ConnectionName)
}

// ListPagesReferencing returns the live knowledge pages that reference the given
// target entity, the reverse of ListEntityRefs. The query is indexed per target
// type (migration 000074).
func (s *postgresStore) ListPagesReferencing(ctx context.Context, ref EntityRef) ([]PageRef, error) { //nolint:revive // interface impl
	where, args := reverseLookupFilter(ref)
	if where == "" {
		return nil, nil
	}
	// #nosec G202 -- `where` is a fixed literal clause from reverseLookupFilter;
	// the target values are bound as query parameters via args, not concatenated.
	query := `SELECT DISTINCT p.id, p.slug, p.title
		FROM knowledge_page_entity_refs r
		JOIN portal_knowledge_pages p ON p.id = r.page_id
		WHERE ` + where + ` AND p.deleted_at IS NULL
		ORDER BY p.title`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying referencing pages: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var pages []PageRef
	for rows.Next() {
		var p PageRef
		var slug sql.NullString
		if err := rows.Scan(&p.ID, &slug, &p.Title); err != nil {
			return nil, fmt.Errorf("scanning referencing page: %w", err)
		}
		p.Slug = slug.String
		pages = append(pages, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating referencing pages: %w", err)
	}
	return pages, nil
}
