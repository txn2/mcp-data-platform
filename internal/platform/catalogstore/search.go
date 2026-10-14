package catalogstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

const selectByPath = `
SELECT reference FROM catalog_entities
WHERE kind = 'dataset' AND connection_kind = $1 AND path = $2
ORDER BY connection_name`

// DatasetsByPath returns the datasets one kind of connection names by path.
func (s *Store) DatasetsByPath(ctx context.Context, connectionKind, path string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, selectByPath, connectionKind, path)
	if err != nil {
		return nil, fmt.Errorf("finding datasets by path: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]string, 0)
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, fmt.Errorf("scanning dataset reference: %w", err)
		}
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("finding datasets by path: %w", err)
	}
	return out, nil
}

// searchDatasets pages through the datasets that are not removed. $1 is the
// text to match (” matches everything), $2 a connection kind (” any), $3
// tags every match must carry (empty: no requirement), $6 a connection (”
// any), and $7 whether only the "kind/name" connections in $8 are searched.
const searchDatasets = `
SELECT e.reference, e.connection_kind, e.connection_name, e.path, e.name, e.dataset_type,
       COALESCE(d.value #>> '{}', ''), COALESCE(t.value, '[]'::jsonb), COALESCE(m.value #>> '{}', '')
FROM catalog_entities e
LEFT JOIN catalog_aspects d ON d.entity_id = e.id AND d.aspect = 'description'
LEFT JOIN catalog_aspects t ON t.entity_id = e.id AND t.aspect = 'tags'
LEFT JOIN catalog_aspects m ON m.entity_id = e.id AND m.aspect = 'domain'
WHERE e.kind = 'dataset' AND e.removed_at IS NULL
  AND ($1 = '' OR e.name ILIKE '%' || $1 || '%' OR e.path ILIKE '%' || $1 || '%'
       OR (d.value #>> '{}') ILIKE '%' || $1 || '%')
  AND ($2 = '' OR e.connection_kind = $2)
  AND (cardinality($3::text[]) = 0 OR COALESCE(t.value, '[]'::jsonb) @> to_jsonb($3::text[]))
  AND ($6 = '' OR e.connection_name = $6)
  AND (NOT $7 OR (e.connection_kind || '/' || e.connection_name) = ANY($8::text[]))
ORDER BY e.reference
LIMIT $4 OFFSET $5`

// likeEscaper escapes the characters LIKE reads as wildcards, under its
// default escape character, so a name is matched as written.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// defaultSearchLimit is the page a search returns when it names none.
const defaultSearchLimit = 50

// SearchDatasets returns a page of matching datasets.
func (s *Store) SearchDatasets(ctx context.Context, q catalog.DatasetQuery) ([]catalog.DatasetSummary, error) {
	text := q.Text
	if text == "*" {
		text = ""
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	tags := q.Tags
	if tags == nil {
		tags = []string{}
	}
	if q.Scoped && len(q.Within) == 0 {
		return []catalog.DatasetSummary{}, nil
	}
	within := q.Within
	if within == nil {
		within = []string{}
	}
	rows, err := s.db.QueryContext(ctx, searchDatasets, likeEscaper.Replace(text), q.ConnectionKind, pq.Array(tags),
		limit, max(q.Offset, 0), q.Connection, q.Scoped, pq.Array(within))
	if err != nil {
		return nil, fmt.Errorf("searching datasets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]catalog.DatasetSummary, 0)
	for rows.Next() {
		var d catalog.DatasetSummary
		var typ string
		var tagsJSON []byte
		if err := rows.Scan(&d.Reference, &d.ConnectionKind, &d.Connection, &d.Path, &d.Name, &typ,
			&d.Description, &tagsJSON, &d.Domain); err != nil {
			return nil, fmt.Errorf("scanning dataset: %w", err)
		}
		d.Type = catalog.DatasetType(typ)
		if err := json.Unmarshal(tagsJSON, &d.Tags); err != nil {
			return nil, fmt.Errorf("decoding dataset tags: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("searching datasets: %w", err)
	}
	return out, nil
}
