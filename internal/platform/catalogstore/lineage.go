package catalogstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

const markRemoved = `
UPDATE catalog_entities SET removed_at = NOW(), updated_at = NOW()
WHERE kind = 'dataset' AND connection_kind = $1 AND connection_name = $2
  AND dataset_type = ANY($3) AND removed_at IS NULL AND NOT (reference = ANY($4))
  AND NOT (path = ANY($5) OR path LIKE ANY($6))`

// MarkRemoved marks the datasets of the given types on a connection that a
// scan did not see, leaving every dataset under a path prefix it could not
// read.
func (s *Store) MarkRemoved(ctx context.Context, r catalog.Removal) (int, error) {
	typeNames := make([]string, len(r.Types))
	for i, t := range r.Types {
		typeNames[i] = string(t)
	}
	keep := nonNil(r.Keep)
	unread := nonNil(r.Unread)
	under := make([]string, len(unread))
	for i, p := range unread {
		under[i] = likeEscaper.Replace(p) + ".%"
	}
	res, err := s.db.ExecContext(ctx, markRemoved, r.ConnectionKind, r.Connection, pq.Array(typeNames), pq.Array(keep),
		pq.Array(unread), pq.Array(under))
	if err != nil {
		return 0, fmt.Errorf("marking removed datasets: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting removed datasets: %w", err)
	}
	return int(n), nil
}

// nonNil returns an empty list for nil, so the database is handed an empty
// array rather than NULL, which ANY would never match.
func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

const markDatasetRemoved = `
UPDATE catalog_entities SET removed_at = NOW(), updated_at = NOW()
WHERE kind = 'dataset' AND reference = $1 AND removed_at IS NULL`

// MarkDatasetRemoved marks one dataset removed.
func (s *Store) MarkDatasetRemoved(ctx context.Context, ref catalog.Ref) error {
	if _, err := s.db.ExecContext(ctx, markDatasetRemoved, ref.String()); err != nil {
		return fmt.Errorf("marking %s removed: %w", ref, err)
	}
	return nil
}

const insertEdge = `
INSERT INTO catalog_lineage (source_ref, target_ref, writer_kind, writer_ref, written_by, written_at)
VALUES ($1, $2, $3, $4, $5, NOW())
ON CONFLICT (source_ref, target_ref, writer_kind, writer_ref) DO UPDATE SET
    written_by = EXCLUDED.written_by, written_at = EXCLUDED.written_at`

// AddEdges records lineage edges in one transaction. An edge from a dataset
// to itself says nothing and is skipped.
func (s *Store) AddEdges(ctx context.Context, edges []catalog.Edge) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, e := range edges {
			if e.Source == e.Target {
				continue
			}
			if _, err := tx.ExecContext(ctx, insertEdge,
				e.Source, e.Target, string(e.WriterKind), e.WriterRef, e.WrittenBy); err != nil {
				return fmt.Errorf("recording edge %s -> %s: %w", e.Source, e.Target, err)
			}
		}
		return nil
	})
}

// walkUpstream follows edges from targets to their sources. The path column
// stops a cycle from being walked again, and each edge is reported at the
// shallowest depth it was reached.
const walkUpstream = `
WITH RECURSIVE walk (source_ref, target_ref, writer_kind, writer_ref, written_by, written_at, depth, seen) AS (
    SELECT source_ref, target_ref, writer_kind, writer_ref, written_by, written_at, 1,
           ARRAY[target_ref, source_ref]
    FROM catalog_lineage WHERE target_ref = $1
  UNION ALL
    SELECT l.source_ref, l.target_ref, l.writer_kind, l.writer_ref, l.written_by, l.written_at, w.depth + 1,
           w.seen || l.source_ref
    FROM catalog_lineage l JOIN walk w ON l.target_ref = w.source_ref
    WHERE w.depth < $2 AND NOT (l.source_ref = ANY(w.seen))
)
SELECT DISTINCT ON (source_ref, target_ref, writer_kind, writer_ref)
       source_ref, target_ref, writer_kind, writer_ref, written_by, written_at, depth
FROM walk ORDER BY source_ref, target_ref, writer_kind, writer_ref, depth`

// walkDownstream follows edges from sources to their targets.
const walkDownstream = `
WITH RECURSIVE walk (source_ref, target_ref, writer_kind, writer_ref, written_by, written_at, depth, seen) AS (
    SELECT source_ref, target_ref, writer_kind, writer_ref, written_by, written_at, 1,
           ARRAY[source_ref, target_ref]
    FROM catalog_lineage WHERE source_ref = $1
  UNION ALL
    SELECT l.source_ref, l.target_ref, l.writer_kind, l.writer_ref, l.written_by, l.written_at, w.depth + 1,
           w.seen || l.target_ref
    FROM catalog_lineage l JOIN walk w ON l.source_ref = w.target_ref
    WHERE w.depth < $2 AND NOT (l.target_ref = ANY(w.seen))
)
SELECT DISTINCT ON (source_ref, target_ref, writer_kind, writer_ref)
       source_ref, target_ref, writer_kind, writer_ref, written_by, written_at, depth
FROM walk ORDER BY source_ref, target_ref, writer_kind, writer_ref, depth`

// Lineage walks the edges from ref in one direction.
func (s *Store) Lineage(ctx context.Context, ref string, direction catalog.Direction, maxDepth int) ([]catalog.Hop, error) {
	query := walkUpstream
	switch direction {
	case catalog.Upstream:
	case catalog.Downstream:
		query = walkDownstream
	default:
		return nil, fmt.Errorf("unknown lineage direction %q", direction)
	}
	if maxDepth < 1 {
		maxDepth = 1
	}
	rows, err := s.db.QueryContext(ctx, query, ref, maxDepth)
	if err != nil {
		return nil, fmt.Errorf("walking lineage: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]catalog.Hop, 0)
	for rows.Next() {
		var h catalog.Hop
		var kind string
		if err := rows.Scan(&h.Source, &h.Target, &kind, &h.WriterRef, &h.WrittenBy, &h.WrittenAt, &h.Depth); err != nil {
			return nil, fmt.Errorf("scanning lineage: %w", err)
		}
		h.WriterKind = catalog.WriterKind(kind)
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("walking lineage: %w", err)
	}
	return out, nil
}
