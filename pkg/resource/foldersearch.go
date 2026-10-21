package resource

import (
	"context"
	"fmt"
	"sort"

	"github.com/pgvector/pgvector-go"
)

// ScoredFolder is one folder's search entry ranked against a query (#2111): a
// folder whose files are not indexed one by one, found as one thing.
type ScoredFolder struct {
	Library     ScopeFilter
	Path        string
	Description string
	// Summary is the text the folder was embedded on: its path, description,
	// how many files it holds and of what kinds, their dates and a few names.
	Summary string
	Score   float64
}

// folderFTS is the full-text expression a folder entry is matched on.
const folderFTS = "to_tsvector('english', fts_name_words(path) || ' ' || description || ' ' || summary_text)"

// SearchFolders ranks the folder entries in the caller's visible libraries.
// Only a folder that has an entry -- one holding files that are not indexed
// themselves, embedded at least once -- is a candidate. Like Search it fails
// closed on an empty scope set and takes the lexical path without an
// embedding.
func (s *postgresStore) SearchFolders(ctx context.Context, q SearchQuery) ([]ScoredFolder, error) {
	if len(q.Scopes) == 0 {
		return nil, nil
	}
	query, args := buildFolderSearch(q)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search resource folders: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ScoredFolder
	seen := map[string]int{}
	for rows.Next() {
		var (
			f        ScoredFolder
			vecScore float64
			lexMatch bool
		)
		if err := rows.Scan(&f.Library.Scope, &f.Library.ScopeID, &f.Path, &f.Description, &f.Summary,
			&vecScore, &lexMatch); err != nil {
			return nil, fmt.Errorf("scanning resource folder row: %w", err)
		}
		f.Score = fuseHybridScore(vecScore, lexMatch)
		key := FolderKey(f.Library, f.Path)
		if i, dup := seen[key]; dup {
			out[i].Score = max(out[i].Score, f.Score)
			continue
		}
		seen[key] = len(out)
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating resource folder rows: %w", err)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

// folderScopeArg is the first placeholder the scope predicate takes in the
// vector statement, after $1 the vector and $2 the text.
const folderScopeArg = 3

// buildFolderSearch renders the folder statement: with an embedding, the
// vector top-k with a lexical match flag unioned with the lexical top-k, as
// file search does, so a folder named by the words of its description is
// found though it is not among the nearest vectors; without one, the lexical
// top-k. A folder in both arms is one hit (SearchFolders keeps its best
// score). A function so the statement gate can prepare it (#1512).
func buildFolderSearch(q SearchQuery) (query string, args []any) {
	limit := q.EffectiveLimit()
	if len(q.Embedding) > 0 {
		scopeWhere, scopeArgs, _ := scopeVisibilityWhere(q.Scopes, folderScopeArg)
		args = append([]any{pgvector.NewVector(q.Embedding), q.QueryText}, scopeArgs...)
		// #nosec G201 -- constant expressions, a parameterized scope predicate, a sanitized limit.
		vecArm := fmt.Sprintf(
			"SELECT scope, COALESCE(scope_id, ''), path, description, summary_text, "+
				"1 - (embedding <=> $1) AS vec_score, (%s @@ plainto_tsquery('english', $2)) AS lex_match "+
				"FROM resource_folders WHERE embedding IS NOT NULL AND %s "+
				"ORDER BY embedding <=> $1 LIMIT %d",
			folderFTS, scopeWhere, limit)
		// #nosec G201 -- constant expressions, a parameterized scope predicate, a sanitized limit.
		lexArm := fmt.Sprintf(
			"SELECT scope, COALESCE(scope_id, ''), path, description, summary_text, "+
				"1 - (embedding <=> $1) AS vec_score, TRUE AS lex_match "+
				"FROM resource_folders WHERE embedding IS NOT NULL AND %s @@ plainto_tsquery('english', $2) AND %s "+
				"ORDER BY ts_rank_cd(%s, plainto_tsquery('english', $2)) DESC LIMIT %d",
			folderFTS, scopeWhere, folderFTS, limit)
		return "(" + vecArm + ") UNION ALL (" + lexArm + ")", args
	}
	scopeWhere, scopeArgs, _ := scopeVisibilityWhere(q.Scopes, 2) //nolint:mnd // after $1 text
	args = append([]any{q.QueryText}, scopeArgs...)
	// #nosec G201 -- constant expressions, a parameterized scope predicate, a sanitized limit.
	return fmt.Sprintf(
		"SELECT scope, COALESCE(scope_id, ''), path, description, summary_text, "+
			"0::float8 AS vec_score, TRUE AS lex_match "+
			"FROM resource_folders WHERE embedding IS NOT NULL AND %s @@ plainto_tsquery('english', $1) AND %s "+
			"ORDER BY ts_rank_cd(%s, plainto_tsquery('english', $1)) DESC LIMIT %d",
		folderFTS, scopeWhere, folderFTS, limit), args
}
