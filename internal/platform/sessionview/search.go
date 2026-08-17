package sessionview

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// A session is found by what its caller said they were doing. The purposes the
// calls stated (#1317) are the only words a session has of its own; the names
// of the assets it saved are the words it left behind. Together they are the
// document a session is ranked against, so "what did I do about churn last
// week" reaches the session rather than only the individual calls inside it.
//
// The match is lexical. A session is derived from the audit log rather than
// stored (see the package comment), so there is no row to carry an embedding
// and no index to keep in step with one; the two arms of a hybrid search would
// have nothing to fuse. What the search does use is the two indexes that do
// exist: the purpose full-text index (migration 000108) and the asset search
// index (migration 000063), each matched with the expression its index was
// built on.

// SearchQuery is one search over the caller's own sessions.
type SearchQuery struct {
	// Text is the natural-language query, matched against the purposes the
	// session's calls stated and the names of the assets it saved.
	Text string
	// UserID scopes the search to the sessions that caller ran. A search with
	// no caller returns nothing rather than everyone's sessions.
	UserID string
	Limit  int
}

// Match is one session found by relevance: enough of it to rank and to render
// a result, without loading the timeline a fetch would.
//
// It is deliberately not a Summary. A summary states who ran the session, under
// which persona, and which tools and connections it touched; a match knows none
// of that, and returning a Summary with those fields blank would say the
// session had no caller rather than that the search did not read one.
type Match struct {
	SessionID    string
	Kind         Kind
	StartedAt    time.Time
	LastActiveAt time.Time
	CallCount    int
	FailureCount int
	// Purposes are the distinct reasons the session's calls stated. Empty on a
	// session whose calls were all ungated, which is then reachable only
	// through the names of what it produced.
	Purposes []string
	// AssetNames are the names of the assets the session saved, oldest first.
	AssetNames []string
	// Score is the lexical relevance of the session's document to the query.
	Score float64
}

// defaultSearchLimit bounds a search that states no limit.
const defaultSearchLimit = 10

// effectiveLimit returns the bounded page size for a search.
func (q SearchQuery) effectiveLimit() int {
	if q.Limit <= 0 || q.Limit > MaxPerPage {
		return defaultSearchLimit
	}
	return q.Limit
}

// searchQuery ranks the caller's sessions against one query.
//
// The two arms that can use an index run first, in candidates: a purpose
// matched through the audit full-text index, and an asset matched through
// portal_asset_fts, which is the expression migration 000063 indexed and so
// must be called with exactly these arguments. Only the sessions they name are
// rolled up, so the aggregate never runs over the caller's whole audit history.
//
// The query is spelled inline rather than computed once in a CTE: a matching
// expression is what lets the planner use the two GIN indexes, and a value
// carried out of a materialized CTE is not that expression. What the roll-up
// then costs is set by how many of the caller's own sessions stated a matching
// purpose, and each of them costs two session-keyed index lookups.
//
// The roll-up re-states the caller, which is what makes an asset-matched
// candidate safe: a session id reached through an asset still has to be one
// this caller made calls in, or it groups to no rows and disappears. The
// scoping is the predicate itself, exactly as it is in Get and Timeline, so
// another caller's session is indistinguishable from one that never existed.
const searchQuery = `
WITH candidates AS (
    SELECT DISTINCT a.session_id
      FROM audit_logs a
     WHERE a.user_id = $1
       AND a.session_id <> ''
       AND a.purpose IS NOT NULL
       AND a.purpose <> ''
       AND to_tsvector('english', a.purpose) @@ plainto_tsquery('english', $2)
    UNION
    SELECT DISTINCT p.session_id
      FROM portal_assets p
     WHERE p.owner_id = $1
       AND p.session_id <> ''
       AND p.deleted_at IS NULL
       AND portal_asset_fts(p.name, p.description, p.tags) @@ plainto_tsquery('english', $2)
),
rolled AS (
    SELECT a.session_id,
           MIN(a.timestamp) AS started_at,
           MAX(a.timestamp) AS last_active_at,
           COUNT(*) AS call_count,
           COUNT(*) FILTER (WHERE NOT a.success) AS failure_count,
           -- The purposes in the order the session first stated them, which is
           -- the order the work happened in and so the order it reads back in.
           -- array_agg(DISTINCT ...) can only be ordered by the value itself,
           -- which would alphabetize a story, so the distinct set is built by
           -- first occurrence in a subquery over the one grouped session.
           COALESCE((
               SELECT array_agg(d.purpose ORDER BY d.first_at)
                 FROM (SELECT a2.purpose, MIN(a2.timestamp) AS first_at
                         FROM audit_logs a2
                        WHERE a2.session_id = a.session_id
                          AND a2.user_id = $1
                          AND a2.purpose IS NOT NULL
                          AND a2.purpose <> ''
                        GROUP BY a2.purpose) d
           ), '{}'::text[]) AS purposes
      FROM audit_logs a
      JOIN candidates c ON c.session_id = a.session_id
     WHERE a.user_id = $1
     GROUP BY a.session_id
),
produced AS (
    SELECT p.session_id,
           array_agg(COALESCE(p.name, '') ORDER BY p.created_at) AS asset_names
      FROM portal_assets p
      JOIN candidates c ON c.session_id = p.session_id
     WHERE p.deleted_at IS NULL
     GROUP BY p.session_id
)
SELECT r.session_id, r.started_at, r.last_active_at, r.call_count, r.failure_count,
       r.purposes,
       COALESCE(pr.asset_names, '{}'::text[]) AS asset_names,
       ts_rank_cd(
           to_tsvector('english',
               array_to_string(r.purposes, ' ') || ' ' ||
               array_to_string(COALESCE(pr.asset_names, '{}'::text[]), ' ')),
           plainto_tsquery('english', $2)) AS score
  FROM rolled r
  LEFT JOIN produced pr ON pr.session_id = r.session_id
 ORDER BY score DESC, r.last_active_at DESC
 LIMIT $3`

// Search returns the caller's sessions ranked against the query text, most
// relevant first. It returns nothing for an empty caller or an empty query
// rather than falling back to an unscoped or unranked listing.
func (s *PostgresStore) Search(ctx context.Context, q SearchQuery) ([]Match, error) {
	if q.UserID == "" || strings.TrimSpace(q.Text) == "" {
		return nil, nil
	}
	limit := q.effectiveLimit()

	rows, err := s.db.QueryContext(ctx, searchQuery, q.UserID, q.Text, limit)
	if err != nil {
		return nil, fmt.Errorf("searching sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	matches := make([]Match, 0, listCapacity(limit))
	for rows.Next() {
		m, err := scanMatch(rows)
		if err != nil {
			return nil, err
		}
		matches = append(matches, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating session search: %w", err)
	}
	return matches, nil
}

// scanMatch reads one ranked session.
func scanMatch(rows *sql.Rows) (Match, error) {
	var (
		m                    Match
		purposes, assetNames pq.StringArray
	)
	if err := rows.Scan(
		&m.SessionID, &m.StartedAt, &m.LastActiveAt, &m.CallCount, &m.FailureCount,
		&purposes, &assetNames, &m.Score,
	); err != nil {
		return m, fmt.Errorf("scanning session match: %w", err)
	}
	m.Kind = KindOf(m.SessionID)
	m.Purposes = []string(purposes)
	m.AssetNames = []string(assetNames)
	return m, nil
}
