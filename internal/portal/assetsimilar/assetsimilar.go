// Package assetsimilar finds assets that look like the same work (#2508,
// #2509): the live assets whose stored embedding sits within NearDuplicate of
// a vector, among the assets a person can open, and the groups of one owner's
// assets that sit that close to each other.
//
// Closeness is read from the embedding each asset is already indexed with for
// search (portal_assets.embedding, the vector of portaldomain.AssetIndexText:
// name, description and tags), so nothing new is embedded or stored to answer
// it. A save asks Nearest with the vector of the asset it just wrote; the
// cleanup page asks Groups. Both answer with what the caller can open and
// nothing else: Nearest applies the reach in SQL (internal/portal/reachstore),
// and Groups reads one owner's rows, or every owner's for an administrator.
package assetsimilar

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	sq "github.com/Masterminds/squirrel"
	"github.com/pgvector/pgvector-go"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/reachstore"
)

// NearDuplicate is the cosine similarity at or above which two assets are
// treated as the same work: save_asset's similar_assets (#2508) and the
// Possible duplicates groups (#2509) share it, so the two can never disagree
// about what "very close" means.
//
// It is set against the embedding model the platform ships with
// (nomic-embed-text, 768 dimensions), the model opranking.ScoreFloor was
// measured on: unrelated text sits near a 0.44 cosine (0.72 normalized) and
// text on one topic in different words between 0.70 and 0.85, while the same
// name, description and tags with a word or two changed stay above 0.95.
// Measured on the dev stack's nomic-embed-text over asset index texts (#2508,
// #2509): an identical copy 1.00, a "(copy)" with one tag dropped 0.951, the
// same weekly report saved for another date 0.917, Q3 against Q4 of one report
// 0.84, unrelated subjects 0.48 to 0.59. 0.93 sits between the copies and the
// recurring report a schedule writes each period, which must not be flagged.
const NearDuplicate = 0.93

// DefaultLimit is how many similar assets a save reports.
const DefaultLimit = 3

// maxLimit bounds a Nearest call.
const maxLimit = 20

// defaultMaxAssets bounds the assets a Groups call compares pairwise, newest
// first: pairwise comparison is quadratic, and a library past this size is
// read in the slices its newest assets give.
const defaultMaxAssets = 2000

// maxPairs bounds the pairs a Groups call reads.
const maxPairs = 5000

var psq = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// Store answers closeness questions over portal_assets.
type Store struct{ db *sql.DB }

// New builds a Store over db. A nil db answers nothing.
func New(db *sql.DB) *Store { return &Store{db: db} }

// NearestQuery is what Nearest is asked.
type NearestQuery struct {
	Vector  []float32
	Reach   *portaldomain.Reach
	Exclude []string
	Limit   int
}

// BuildNearest renders the Nearest statement: the live assets in reach with a
// stored embedding at least NearDuplicate similar to the vector, closest
// first. The distance is exact, over the reach's rows: an HNSW scan filters
// after its nearest-neighbor step, and a narrow reach would keep none.
func BuildNearest(q NearestQuery) (query string, args []any, err error) {
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, maxLimit)
	vec := pgvector.NewVector(q.Vector)
	qb := psq.Select("id", "name", "owner_email").
		Column(sq.Expr("1 - (embedding <=> ?) AS similarity", vec)).
		From("portal_assets").
		Where("deleted_at IS NULL").
		Where("embedding IS NOT NULL").
		Where(reachstore.AssetPredicate(q.Reach)).
		Where(sq.Expr("(embedding <=> ?) <= ?", vec, 1-NearDuplicate)).
		OrderBy("similarity DESC", "id").
		Limit(uint64(limit)) // #nosec G115 -- limit is clamped to [1, maxLimit]
	if len(q.Exclude) > 0 {
		qb = qb.Where(sq.NotEq{"id": q.Exclude})
	}
	query, args, err = qb.ToSql()
	if err != nil {
		return "", nil, fmt.Errorf("building nearest statement: %w", err)
	}
	return query, args, nil
}

// Nearest returns up to q.Limit live assets the reach covers whose stored
// embedding is at least NearDuplicate similar to q.Vector, closest first. An
// empty vector or an unidentified reach answers nothing.
func (s *Store) Nearest(ctx context.Context, q NearestQuery) ([]Match, error) {
	out := []Match{}
	if s == nil || s.db == nil || len(q.Vector) == 0 || !q.Reach.Identified() {
		return out, nil
	}
	query, args, err := BuildNearest(q)
	if err != nil {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return out, fmt.Errorf("finding similar assets: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query
	for rows.Next() {
		var m Match
		if err := rows.Scan(&m.AssetID, &m.Name, &m.OwnerEmail, &m.Similarity); err != nil {
			return out, fmt.Errorf("scanning similar asset: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("iterating similar assets: %w", err)
	}
	return out, nil
}

// GroupsQuery is what Groups is asked. A nil Owner compares every owner's
// assets, each owner's among themselves: the administrator's view.
type GroupsQuery struct {
	Owner     *portaldomain.AssetOwner
	MaxAssets int
}

// ownerKey is the one column two rows of one owner agree on: the user id
// where it is recorded, the lowercased address where it is not.
const ownerKey = "COALESCE(NULLIF(owner_id, ''), LOWER(owner_email))"

// BuildPairs renders the Groups statement: every pair of one owner's live,
// embedded assets at least NearDuplicate similar, over the newest MaxAssets.
func BuildPairs(q GroupsQuery) (query string, args []any, err error) {
	maxAssets := q.MaxAssets
	if maxAssets <= 0 {
		maxAssets = defaultMaxAssets
	}
	cand := psq.Select("id", "name", "owner_id", "owner_email", "current_version", "updated_at", "embedding",
		ownerKey+" AS owner_key").
		From("portal_assets").
		Where("deleted_at IS NULL").
		Where("embedding IS NOT NULL").
		OrderBy("updated_at DESC", "id").
		Limit(uint64(maxAssets)) // #nosec G115 -- maxAssets is positive
	if q.Owner != nil {
		if !q.Owner.Identified() {
			cand = cand.Where("FALSE")
		} else {
			cand = cand.Where(reachstore.OwnerPredicate(*q.Owner))
		}
	}
	candSQL, candArgs, err := cand.ToSql()
	if err != nil {
		return "", nil, fmt.Errorf("building candidate statement: %w", err)
	}
	pairs := fmt.Sprintf(
		"WITH c AS MATERIALIZED (%s) "+
			"SELECT a.id, a.name, a.owner_id, a.owner_email, a.current_version, a.updated_at, "+
			"b.id, b.name, b.owner_id, b.owner_email, b.current_version, b.updated_at, "+
			"1 - (a.embedding <=> b.embedding) AS similarity "+
			"FROM c a JOIN c b ON a.id < b.id AND a.owner_key = b.owner_key "+
			"WHERE (a.embedding <=> b.embedding) <= $%d "+
			"ORDER BY similarity DESC, a.id, b.id LIMIT %d",
		candSQL, len(candArgs)+1, maxPairs)
	return pairs, append(candArgs, 1-NearDuplicate), nil
}

// pair is one close pair read from the statement.
type pair struct {
	a, b       Member
	similarity float64
}

// Groups returns the groups of near-identical assets, closest first. Each
// group's members are one owner's.
func (s *Store) Groups(ctx context.Context, q GroupsQuery) ([]Group, error) {
	if s == nil || s.db == nil {
		return []Group{}, nil
	}
	query, args, err := BuildPairs(q)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("finding duplicate assets: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.a.AssetID, &p.a.Name, &p.a.OwnerID, &p.a.OwnerEmail, &p.a.CurrentVersion, &p.a.UpdatedAt,
			&p.b.AssetID, &p.b.Name, &p.b.OwnerID, &p.b.OwnerEmail, &p.b.CurrentVersion, &p.b.UpdatedAt,
			&p.similarity); err != nil {
			return nil, fmt.Errorf("scanning duplicate pair: %w", err)
		}
		pairs = append(pairs, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating duplicate pairs: %w", err)
	}
	return groupPairs(pairs), nil
}

// groupPairs joins close pairs into groups: two assets are in one group when
// a chain of close pairs links them. Groups come back closest first, members
// newest first.
func groupPairs(pairs []pair) []Group {
	sets := unionFind{}
	members := map[string]Member{}
	for _, p := range pairs {
		members[p.a.AssetID] = p.a
		members[p.b.AssetID] = p.b
		sets.union(p.a.AssetID, p.b.AssetID)
	}
	byRoot := map[string]*Group{}
	for _, p := range pairs {
		root := sets.find(p.a.AssetID)
		g := byRoot[root]
		if g == nil {
			g = &Group{}
			byRoot[root] = g
		}
		g.Similarity = max(g.Similarity, p.similarity)
	}
	for id, m := range members {
		g := byRoot[sets.find(id)]
		g.Members = append(g.Members, m)
	}
	out := make([]Group, 0, len(byRoot))
	for _, g := range byRoot {
		sort.Slice(g.Members, newestFirst(g.Members))
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Similarity != out[j].Similarity {
			return out[i].Similarity > out[j].Similarity
		}
		return out[i].Members[0].AssetID < out[j].Members[0].AssetID
	})
	return out
}

// newestFirst orders a group's members by when they last changed, newest
// first, ties by id.
func newestFirst(m []Member) func(i, j int) bool {
	return func(i, j int) bool {
		if !m[i].UpdatedAt.Equal(m[j].UpdatedAt) {
			return m[i].UpdatedAt.After(m[j].UpdatedAt)
		}
		return m[i].AssetID < m[j].AssetID
	}
}

// unionFind is the disjoint sets groupPairs links assets into.
type unionFind map[string]string

func (u unionFind) find(id string) string {
	if _, ok := u[id]; !ok {
		u[id] = id
	}
	for u[id] != id {
		u[id] = u[u[id]]
		id = u[id]
	}
	return id
}

func (u unionFind) union(a, b string) {
	if ra, rb := u.find(a), u.find(b); ra != rb {
		u[rb] = ra
	}
}
