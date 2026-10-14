package catalogstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// Compile-time interface verification.
var _ catalog.Lister = (*Store)(nil)

// definitionKinds are every kind ListDefinitions lists when a query names none.
var definitionKinds = []string{
	string(catalog.KindGlossaryTerm), string(catalog.KindGlossaryNode), string(catalog.KindTag),
	string(catalog.KindDomain), string(catalog.KindDataProduct),
}

// listDefinitions pages through definitions. $1 the kinds, $2 a parent
// reference (” any), $3 roots only, $4 the text (” any), $5 limit, $6 offset.
const listDefinitions = `
SELECT e.reference, e.kind, e.slug, e.name, COALESCE(d.value #>> '{}', ''), COALESCE(p.reference, ''),
       (SELECT count(*) FROM catalog_entities c
        WHERE c.parent_id = e.id AND c.kind IN ('glossary_term', 'glossary_node'))
FROM catalog_entities e
LEFT JOIN catalog_entities p ON p.id = e.parent_id
LEFT JOIN catalog_aspects d ON d.entity_id = e.id AND d.aspect = 'description'
WHERE e.kind = ANY($1::text[])
  AND ($2 = '' OR p.reference = $2)
  AND (NOT $3 OR e.parent_id IS NULL)
  AND ($4 = '' OR e.name ILIKE '%' || $4 || '%' OR e.slug ILIKE '%' || $4 || '%'
       OR (d.value #>> '{}') ILIKE '%' || $4 || '%')
ORDER BY lower(e.name), e.reference
LIMIT $5 OFFSET $6`

// definitionKindsOf is the kinds a listing names, every definition kind when
// it names none; a kind that is not a definition's is refused.
func definitionKindsOf(named []catalog.Kind) ([]string, error) {
	if len(named) == 0 {
		return definitionKinds, nil
	}
	kinds := make([]string, 0, len(named))
	for _, k := range named {
		if !isDefinitionKind(k) {
			return nil, fmt.Errorf("kind %s is not a definition kind: %w", k, catalog.ErrInvalidRef)
		}
		kinds = append(kinds, string(k))
	}
	return kinds, nil
}

// ListDefinitions returns a page of definitions, ordered by name.
func (s *Store) ListDefinitions(ctx context.Context, q catalog.DefinitionQuery) ([]catalog.DefinitionSummary, error) {
	kinds, err := definitionKindsOf(q.Kinds)
	if err != nil {
		return nil, err
	}
	parent, err := s.parentReference(ctx, q.Parent)
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	rows, err := s.db.QueryContext(ctx, listDefinitions, pq.Array(kinds), parent, q.Roots && parent == "",
		likeEscaper.Replace(q.Text), limit, max(q.Offset, 0))
	if err != nil {
		return nil, fmt.Errorf("listing definitions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]catalog.DefinitionSummary, 0)
	for rows.Next() {
		var d catalog.DefinitionSummary
		var kind string
		if err := rows.Scan(&d.Reference, &kind, &d.Slug, &d.Name, &d.Description, &d.Parent, &d.Children); err != nil {
			return nil, fmt.Errorf("scanning definition: %w", err)
		}
		d.Kind = catalog.Kind(kind)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing definitions: %w", err)
	}
	return out, nil
}

// parentReference resolves a glossary node reference, whose key may be its
// slug, to the canonical reference stored as a parent. Empty stays empty.
func (s *Store) parentReference(ctx context.Context, parent string) (string, error) {
	if parent == "" {
		return "", nil
	}
	ref, err := catalog.ParseRef(parent)
	if err != nil {
		return "", fmt.Errorf("parent: %w", err)
	}
	if ref.Kind != catalog.KindGlossaryNode {
		return "", fmt.Errorf("parent %q is not a glossary node: %w", parent, catalog.ErrInvalidRef)
	}
	e, err := resolve(ctx, s.db, ref)
	if err != nil {
		return "", err
	}
	return e.reference, nil
}

const countChildren = `
SELECT count(*) FROM catalog_entities WHERE parent_id = $1 AND kind IN ('glossary_term', 'glossary_node')`

const countUses = `
SELECT count(*) FROM catalog_aspects a JOIN catalog_entities e ON e.id = a.entity_id
WHERE a.aspect = $1 AND e.kind IN ('dataset', 'column') AND a.value @> $2::jsonb`

const deleteEntity = `DELETE FROM catalog_entities WHERE id = $1`

// DeleteDefinition deletes a definition no glossary entry sits under and no
// dataset or column carries.
func (s *Store) DeleteDefinition(ctx context.Context, ref catalog.Ref) error {
	if !isDefinitionKind(ref.Kind) {
		return fmt.Errorf("kind %s is not a definition kind: %w", ref.Kind, catalog.ErrInvalidRef)
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		e, err := resolve(ctx, tx, ref)
		if err != nil {
			return err
		}
		if err := refuseInUse(ctx, tx, e, ref); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, deleteEntity, e.id); err != nil {
			return fmt.Errorf("deleting %s: %w", e.reference, err)
		}
		return nil
	})
}

// refuseInUse refuses the delete of a definition a glossary entry sits under
// or a dataset or column carries, naming how many.
func refuseInUse(ctx context.Context, tx *sql.Tx, e entity, ref catalog.Ref) error {
	var children int
	if err := tx.QueryRowContext(ctx, countChildren, e.id).Scan(&children); err != nil {
		return fmt.Errorf("counting the entries under %s: %w", e.reference, err)
	}
	if children > 0 {
		return &catalog.InUseError{Reference: e.reference, Children: children}
	}
	aspect, needle, ok := useNeedle(e, ref)
	if !ok {
		return nil
	}
	var uses int
	if err := tx.QueryRowContext(ctx, countUses, aspect, needle).Scan(&uses); err != nil {
		return fmt.Errorf("counting the uses of %s: %w", e.reference, err)
	}
	if uses > 0 {
		return &catalog.InUseError{Reference: e.reference, Entities: uses}
	}
	return nil
}

// useNeedle is the aspect a definition is carried in and the JSON a carrying
// value contains: a tag's name in the tags list, a term's reference in the
// glossary terms list, a domain's reference as the domain. A glossary node
// and a data product are carried by nothing.
func useNeedle(e entity, ref catalog.Ref) (aspect string, needle []byte, ok bool) {
	var v any
	switch e.kind {
	case catalog.KindTag:
		aspect, v = string(catalog.AspectTags), []string{ref.Key}
	case catalog.KindGlossaryTerm:
		aspect, v = string(catalog.AspectGlossaryTerms), []string{e.reference}
	case catalog.KindDomain:
		aspect, v = string(catalog.AspectDomain), e.reference
	default:
		return "", nil, false
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", nil, false
	}
	return aspect, b, true
}

const selectUses = `
SELECT e.reference, COALESCE(p.reference, e.reference), COALESCE(p.name, e.name), e.column_name, e.connection_name
FROM catalog_aspects a JOIN catalog_entities e ON e.id = a.entity_id
LEFT JOIN catalog_entities p ON p.id = e.parent_id AND e.kind = 'column'
WHERE a.aspect = $1 AND e.kind IN ('dataset', 'column') AND a.value @> $2::jsonb
ORDER BY e.reference
LIMIT $3`

// Uses returns the datasets and columns carrying a glossary term, a tag or a
// domain.
func (s *Store) Uses(ctx context.Context, ref catalog.Ref, limit int) ([]catalog.Use, error) {
	e, err := resolve(ctx, s.db, ref)
	if err != nil {
		return nil, err
	}
	aspect, needle, ok := useNeedle(e, ref)
	if !ok {
		return []catalog.Use{}, nil
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	rows, err := s.db.QueryContext(ctx, selectUses, aspect, needle, limit)
	if err != nil {
		return nil, fmt.Errorf("listing the uses of %s: %w", e.reference, err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]catalog.Use, 0)
	for rows.Next() {
		var u catalog.Use
		if err := rows.Scan(&u.Reference, &u.Dataset, &u.Name, &u.Column, &u.Connection); err != nil {
			return nil, fmt.Errorf("scanning a use: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing the uses of %s: %w", e.reference, err)
	}
	return out, nil
}

// selectBrowse lists one level of a connection's datasets that are not
// removed, under a path prefix ($3, a LIKE pattern; ” the whole connection),
// as one row per next path segment: the segment as stored (quoted where the
// reference form quotes it), whether it is a dataset directly on the level,
// how many datasets the row stands for, and for a dataset its reference and
// type. $4 is where the path under the prefix starts. The database does the
// grouping, so browsing the top of a connection with many datasets reads one
// row per catalog rather than one per dataset.
const selectBrowse = `
WITH under AS (
    SELECT substr(path, $4) AS rest, reference, dataset_type FROM catalog_entities
    WHERE kind = 'dataset' AND removed_at IS NULL AND connection_kind = $1 AND connection_name = $2
      AND ($3 = '' OR path LIKE $3)
), seg AS (
    SELECT (regexp_match(rest, '^("(?:[^"]|"")*"|[^."]+)'))[1] AS segment, rest, reference, dataset_type FROM under
)
SELECT segment, rest = segment AS leaf, count(*), min(reference), min(dataset_type)
FROM seg WHERE segment IS NOT NULL
GROUP BY segment, rest = segment
ORDER BY leaf, segment`

// Browse lists one level of a connection's datasets: each path segment holding
// datasets further down, then each dataset directly under the prefix.
func (s *Store) Browse(ctx context.Context, q catalog.BrowseQuery) ([]catalog.BrowseEntry, error) {
	if q.ConnectionKind == "" || q.Connection == "" {
		return nil, fmt.Errorf("browsing needs a connection kind and a connection: %w", catalog.ErrInvalidRef)
	}
	pattern, start := "", 1
	if len(q.Prefix) > 0 {
		prefix := catalog.DatasetRef(q.ConnectionKind, q.Connection, q.Prefix...).PathString()
		pattern = likeEscaper.Replace(prefix) + ".%"
		start = len(prefix) + 2
	}
	rows, err := s.db.QueryContext(ctx, selectBrowse, q.ConnectionKind, q.Connection, pattern, start)
	if err != nil {
		return nil, fmt.Errorf("browsing %s: %w", q.Connection, err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]catalog.BrowseEntry, 0)
	for rows.Next() {
		e, err := scanBrowseRow(rows, q)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("browsing %s: %w", q.Connection, err)
	}
	return out, nil
}

// scanBrowseRow reads one level row: a dataset directly on the level, or a
// group whose reference is a dataset reference for the path through it, which
// fetching browses in turn.
func scanBrowseRow(rows *sql.Rows, q catalog.BrowseQuery) (catalog.BrowseEntry, error) {
	var segment, ref, typ string
	var leaf bool
	var count int
	if err := rows.Scan(&segment, &leaf, &count, &ref, &typ); err != nil {
		return catalog.BrowseEntry{}, fmt.Errorf("scanning a browse row: %w", err)
	}
	name, err := catalog.ParsePath(segment)
	if err != nil || len(name) != 1 {
		return catalog.BrowseEntry{}, fmt.Errorf("stored path segment %q: %w", segment, catalog.ErrInvalidRef)
	}
	if leaf {
		return catalog.BrowseEntry{Name: name[0], Reference: ref, Type: catalog.DatasetType(typ)}, nil
	}
	path := append(slices.Clone(q.Prefix), name[0])
	return catalog.BrowseEntry{
		Name:      name[0],
		Reference: catalog.DatasetRef(q.ConnectionKind, q.Connection, path...).String(),
		Datasets:  count,
	}, nil
}
