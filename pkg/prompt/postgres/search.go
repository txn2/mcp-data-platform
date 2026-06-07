package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// Compile-time check: the PostgreSQL store provides ranked search.
var _ prompt.Searcher = (*Store)(nil)

// promptFTSExpr is the full-text expression the lexical arm matches and ranks
// against. It calls the prompt_fts() function from migration 000062 with the
// same argument order, so the planner uses idx_prompts_search_fts (the GIN index
// built on that same call). A function, not an inline expression, is required
// because the composition folds in array_to_string(tags), which is only STABLE
// while a GIN index expression demands IMMUTABLE; prompt_fts composes the same
// corpus as prompt.IndexText (title + description + body + tags, the title
// falling back from display_name to name).
const promptFTSExpr = `prompt_fts(display_name, name, description, content, tags)`

// promptFTSQuery is the parameterized tsquery the lexical predicate compares
// against. $2 is the query text in the hybrid arms; searchLexical rebinds it to
// $1 (it has no vector parameter).
const (
	promptFTSQueryHybrid  = "plainto_tsquery('english', $2)"
	promptFTSQueryLexical = "plainto_tsquery('english', $1)"
)

// Visibility-filter starting parameter indices. The hybrid arms bind $1=vector
// and $2=query, so their visibility predicate starts at $3; the lexical-only
// path binds only $1=query, so it starts at $2.
const (
	hybridVisibilityStart  = 3
	lexicalVisibilityStart = 2
)

// hybridSemanticWeight is the alpha blending the semantic and lexical signals:
// score = alpha*semantic + (1-alpha)*lexical. It deliberately matches the
// memory and api-gateway rankers (0.6) so every surface ranks hybrid results on
// the same curve; keep them in step if any is tuned.
const hybridSemanticWeight = 0.6

// lexical component values before blending, named to keep the magic 0.0/1.0 out
// of the formula (matches pkg/memory/ranking.go).
const (
	lexicalMatchPresent = 1.0
	lexicalMatchAbsent  = 0.0
)

// Search ranks approved prompts by relevance to the query within the caller's
// visibility. A non-nil q.Embedding selects hybrid (semantic + lexical)
// ranking; a nil embedding selects the lexical-only fallback used when no
// embedding provider is configured. Visibility is applied in SQL before
// ranking, so a prompt the caller cannot read is never returned.
func (s *Store) Search(ctx context.Context, q prompt.SearchQuery) ([]prompt.ScoredPrompt, error) {
	if len(q.Embedding) > 0 {
		return s.searchHybrid(ctx, q)
	}
	return s.searchLexical(ctx, q)
}

// searchHybrid runs two index-backed arms and fuses in Go rather than ordering
// by a blended SQL expression, mirroring memory.HybridSearch: the hnsw ANN
// index only accelerates a pure `ORDER BY embedding <=> $1 LIMIT k` and the GIN
// index only accelerates the tsquery match, so a single blended ORDER BY would
// forfeit both. The vector arm returns the cosine top-k; the lexical arm
// returns the full-text top-k (including NULL-embedding rows the vector arm
// cannot see). Their union is deduped by id (keeping the higher fused score)
// and sorted.
func (s *Store) searchHybrid(ctx context.Context, q prompt.SearchQuery) ([]prompt.ScoredPrompt, error) {
	limit := q.EffectiveLimit()
	vis, visArgs, _ := promptVisibilityClause(q, hybridVisibilityStart)
	base := "status = 'approved' AND enabled = true" + vis

	args := make([]any, 0, 2+len(visArgs))
	args = append(args, pgvector.NewVector(q.Embedding), q.QueryText)
	args = append(args, visArgs...)

	// #nosec G201 -- promptColumns/promptFTSExpr/base are constants or built from
	// parameterized placeholders (promptVisibilityClause); limit is a sanitized
	// int. No user input is concatenated into the SQL.
	vecArm := fmt.Sprintf(
		"SELECT %s, 1 - (embedding <=> $1) AS vec_score, (%s @@ %s) AS lex_match "+
			"FROM prompts WHERE embedding IS NOT NULL AND %s "+
			"ORDER BY embedding <=> $1 LIMIT %d",
		promptColumns, promptFTSExpr, promptFTSQueryHybrid, base, limit)
	lexArm := fmt.Sprintf(
		"SELECT %s, CASE WHEN embedding IS NOT NULL THEN 1 - (embedding <=> $1) ELSE 0 END AS vec_score, TRUE AS lex_match "+
			"FROM prompts WHERE %s @@ %s AND %s "+
			"ORDER BY ts_rank_cd(%s, %s) DESC LIMIT %d",
		promptColumns, promptFTSExpr, promptFTSQueryHybrid, base, promptFTSExpr, promptFTSQueryHybrid, limit)
	// #nosec G202 -- both arms are assembled from constant column/expression
	// strings with parameterized placeholders; no user input is concatenated.
	sqlStr := "(" + vecArm + ") UNION ALL (" + lexArm + ")"

	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("search prompts (hybrid): %w", err)
	}
	defer func() { _ = rows.Close() }()

	fused, err := collectHybridScored(rows)
	if err != nil {
		return nil, err
	}
	return truncate(fused, limit), nil
}

// collectHybridScored scans both arms' rows, fuses each into a single score, and
// dedups by prompt id keeping the higher score (a row matched by both arms
// appears twice). The result is sorted by descending score.
func collectHybridScored(rows *sql.Rows) ([]prompt.ScoredPrompt, error) {
	byID := make(map[string]prompt.ScoredPrompt)
	for rows.Next() {
		p := &prompt.Prompt{}
		var argsJSON []byte
		var vecScore float64
		var lexMatch bool
		dest := append(promptScanDest(p, &argsJSON), &vecScore, &lexMatch)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan hybrid prompt: %w", err)
		}
		if err := finishPrompt(p, argsJSON); err != nil {
			return nil, err
		}
		score := fuseHybridScore(vecScore, lexMatch)
		if prev, ok := byID[p.ID]; !ok || score > prev.Score {
			byID[p.ID] = prompt.ScoredPrompt{Prompt: *p, Score: score}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate hybrid prompts: %w", err)
	}
	scored := make([]prompt.ScoredPrompt, 0, len(byID))
	for _, sp := range byID {
		scored = append(scored, sp)
	}
	sortByScoreDesc(scored)
	return scored, nil
}

// searchLexical ranks visible approved prompts by full-text relevance only. It
// is the graceful-degradation path used when no embedding provider is available:
// it has no vector parameter, surfaces NULL-embedding rows, and orders by
// ts_rank_cd in SQL (the same ranker the hybrid lexical arm uses).
func (s *Store) searchLexical(ctx context.Context, q prompt.SearchQuery) ([]prompt.ScoredPrompt, error) {
	vis, args, _ := promptVisibilityClause(q, lexicalVisibilityStart)
	// #nosec G201 -- promptColumns/promptFTSExpr are constants; vis is built from
	// parameterized placeholders only (see promptVisibilityClause).
	query := fmt.Sprintf(
		"SELECT %s, ts_rank_cd(%s, %s) AS lex_rank "+
			"FROM prompts WHERE status = 'approved' AND enabled = true "+
			"AND %s @@ %s%s ORDER BY lex_rank DESC LIMIT %d",
		promptColumns, promptFTSExpr, promptFTSQueryLexical,
		promptFTSExpr, promptFTSQueryLexical, vis, q.EffectiveLimit())

	params := append([]any{q.QueryText}, args...)
	rows, err := s.db.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, fmt.Errorf("search prompts (lexical): %w", err)
	}
	defer func() { _ = rows.Close() }()

	var scored []prompt.ScoredPrompt
	for rows.Next() {
		p := &prompt.Prompt{}
		var argsJSON []byte
		var lexRank float64
		dest := append(promptScanDest(p, &argsJSON), &lexRank)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan lexical prompt: %w", err)
		}
		if err := finishPrompt(p, argsJSON); err != nil {
			return nil, err
		}
		scored = append(scored, prompt.ScoredPrompt{Prompt: *p, Score: lexRank})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lexical prompts: %w", err)
	}
	return scored, nil
}

// fuseHybridScore blends a row's cosine similarity (mapped from [-1,1] to [0,1])
// with a binary lexical-match flag into a rank score in [0,1]. The binary blend
// gives an exact-term match a decisive boost over a merely semantically-near
// prompt, matching the memory/api-gateway rankers.
func fuseHybridScore(cosineSim float64, lexMatch bool) float64 {
	semantic := (cosineSim + 1) / 2
	lex := lexicalMatchAbsent
	if lexMatch {
		lex = lexicalMatchPresent
	}
	return hybridSemanticWeight*semantic + (1-hybridSemanticWeight)*lex
}

// promptVisibilityClause builds the SQL fragment (starting with " AND ...") and
// parameters that restrict ranking to prompts the caller may read, beginning at
// parameter index startIdx. An admin sees every approved prompt; a non-admin
// sees global prompts, persona prompts matching q.Persona, and their own
// personal prompts. An explicit q.Scope narrows the set further for either.
func promptVisibilityClause(q prompt.SearchQuery, startIdx int) (clause string, args []any, nextIdx int) {
	idx := startIdx
	var conds []string

	if q.Scope != "" {
		conds = append(conds, fmt.Sprintf("scope = $%d", idx))
		args = append(args, q.Scope)
		idx++
	}

	if !q.IsAdmin {
		or := []string{"scope = 'global'"}
		or = append(or, fmt.Sprintf("(scope = 'personal' AND owner_email = $%d)", idx))
		args = append(args, q.OwnerEmail)
		idx++
		if q.Persona != "" {
			or = append(or, fmt.Sprintf("(scope = 'persona' AND $%d = ANY(personas))", idx))
			args = append(args, q.Persona)
			idx++
		}
		conds = append(conds, "("+strings.Join(or, " OR ")+")")
	}

	if len(conds) == 0 {
		return "", nil, idx
	}
	return " AND " + strings.Join(conds, " AND "), args, idx
}

// sortByScoreDesc orders scored prompts by descending score, breaking ties by
// name for a stable, deterministic ordering.
func sortByScoreDesc(scored []prompt.ScoredPrompt) {
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Prompt.Name < scored[j].Prompt.Name
	})
}

// truncate returns at most n elements.
func truncate(scored []prompt.ScoredPrompt, n int) []prompt.ScoredPrompt {
	if len(scored) > n {
		return scored[:n]
	}
	return scored
}
