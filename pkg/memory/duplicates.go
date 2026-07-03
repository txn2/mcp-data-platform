package memory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// SimilarPair is a pair of active records owned by the same user whose
// embeddings are highly similar — a likely duplicate the capture-time recall
// gate missed (#762). Older/Newer are ordered by creation time so the default
// consolidation (keep the newer restatement, supersede the older) is explicit.
type SimilarPair struct {
	Older Record  `json:"older"`
	Newer Record  `json:"newer"`
	Score float64 `json:"score"`
}

// DuplicateFinder is the optional store capability behind the memory_manage
// review_duplicates backstop: it lists high-similarity active pairs for human
// consolidation. Only the postgres store implements it (the search needs
// pgvector); callers type-assert the wired Store against this and degrade
// gracefully when it is absent, mirroring knowledge.InsightSearcher.
type DuplicateFinder interface {
	// SimilarActivePairs returns the createdBy owner's pairs of active,
	// embedded records with cosine similarity at or above minScore, highest
	// first, at most limit pairs. createdBy is required — memory content is
	// per-user, so an unscoped listing would expose other users' records.
	// Each record contributes its nearest neighbors only, so the listing is a
	// best-effort review queue, not an exhaustive join.
	SimilarActivePairs(ctx context.Context, createdBy string, minScore float64, limit int) ([]SimilarPair, error)
}

// pairNeighborK is how many nearest neighbors each record contributes to the
// pair scan. Duplicates are near-identical, so the counterpart is virtually
// always the first neighbor; a small k keeps the lateral scan cheap while
// tolerating a couple of interleaved near-matches.
const pairNeighborK = 3

// SimilarActivePairs implements DuplicateFinder over pgvector: for every
// active embedded record of the owner, its k nearest active neighbors (same
// owner) are scored, and pairs clearing minScore are deduplicated (each pair
// is seen from both sides) and returned highest-similarity first.
func (s *postgresStore) SimilarActivePairs(ctx context.Context, createdBy string, minScore float64, limit int) ([]SimilarPair, error) {
	if createdBy == "" {
		return nil, fmt.Errorf("similar-pair search requires an owner scope (createdBy)")
	}
	limit = clampStoreLimit(limit)

	// Raw SQL for the same reason as VectorSearch: the vector expressions use
	// positional parameters squirrel would misnumber. Each side of the pair is
	// an alias-qualified copy of the standard record projection; the lateral
	// fetches ordered neighbors so the ANN index drives the scan. The SQL LIMIT
	// is 2x the requested pairs because every pair can appear once per side.
	activeEmbedded := func(alias string) string {
		return alias + ".status = '" + StatusActive + "' AND " + alias + ".embedding IS NOT NULL"
	}
	sqlStr := fmt.Sprintf( // #nosec G201 -- tableName/cols are constants, limit is a sanitized int, minScore/createdBy bind as $1/$2
		`SELECT %s, %s, 1 - (a.embedding <=> b.embedding) AS score
FROM %s a
JOIN LATERAL (
    SELECT * FROM %s m
    WHERE %s AND m.created_by = a.created_by AND m.id <> a.id
    ORDER BY m.embedding <=> a.embedding
    LIMIT %d
) b ON 1 - (a.embedding <=> b.embedding) >= $1
WHERE %s AND a.created_by = $2
ORDER BY score DESC
LIMIT %d`,
		qualifiedRecordCols("a"), qualifiedRecordCols("b"),
		tableName, tableName, activeEmbedded("m"), pairNeighborK, activeEmbedded("a"), 2*limit,
	)

	rows, err := s.db.QueryContext(ctx, sqlStr, minScore, createdBy)
	if err != nil {
		return nil, fmt.Errorf("executing similar-pair search: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	pairs, err := collectPairRows(rows)
	if err != nil {
		return nil, err
	}
	return dedupePairs(pairs, limit), nil
}

// qualifiedRecordCols returns the standard record projection with every column
// prefixed by the table alias, for queries joining the table to itself.
func qualifiedRecordCols(alias string) string {
	cols := recordColumns()
	for i, c := range cols {
		cols[i] = alias + "." + c
	}
	return strings.Join(cols, ", ")
}

// collectPairRows scans (record, record, score) rows.
func collectPairRows(rows *sql.Rows) ([]SimilarPair, error) {
	var out []SimilarPair
	for rows.Next() {
		var a, b recordScanBuf
		var score float64
		dest := append(a.dest(), b.dest()...)
		dest = append(dest, &score)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scanning similar-pair row: %w", err)
		}
		ra, err := a.finish()
		if err != nil {
			return nil, err
		}
		rb, err := b.finish()
		if err != nil {
			return nil, err
		}
		out = append(out, orderPair(*ra, *rb, score))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating similar-pair rows: %w", err)
	}
	return out, nil
}

// orderPair arranges a pair as (older, newer) by creation time, falling back
// to id order for identical timestamps so deduplication is deterministic.
func orderPair(a, b Record, score float64) SimilarPair {
	if a.CreatedAt.After(b.CreatedAt) || (a.CreatedAt.Equal(b.CreatedAt) && a.ID > b.ID) {
		a, b = b, a
	}
	return SimilarPair{Older: a, Newer: b, Score: score}
}

// dedupePairs drops the mirror-image duplicates (each pair is found from both
// sides of the lateral join) and trims to limit. Rows arrive already sorted
// highest-score-first (the SQL orders by score DESC and collectPairRows
// preserves row order), so plain iteration keeps that order.
func dedupePairs(pairs []SimilarPair, limit int) []SimilarPair {
	seen := make(map[string]struct{}, len(pairs))
	out := make([]SimilarPair, 0, len(pairs))
	for _, p := range pairs {
		key := p.Older.ID + "\x00" + p.Newer.ID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, p)
		if len(out) == limit {
			break
		}
	}
	return out
}
