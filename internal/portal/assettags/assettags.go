// Package assettags is the tag list over assets (#2508): the tags a person can
// see with how many of their assets carry each, the tags an administrator
// manages across the deployment with rename and merge, and the rule that says
// which existing tags a new tag is a variant of.
//
// Tags live on each asset (portal_assets.tags, a JSON array); there is no tag
// table. Tags compare without case for listing and merging: Finance and
// finance are one row, shown in the spelling most assets use, with the
// spellings it covers.
package assettags

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/reachstore"
)

// Limits on a listing.
const (
	DefaultLimit = 50
	MaxLimit     = 500
	// MaxTagLength is the longest tag an asset may carry
	// (portaldomain's tag rule).
	MaxTagLength = 100
)

var psq = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// Query selects tags: those beginning with Prefix (without case), a page of
// them, over the assets a Reach covers, or every live asset when Reach is nil.
type Query struct {
	Reach  *portaldomain.Reach
	Prefix string
	Limit  int
	Offset int
}

// likeEscaper escapes LIKE's wildcards so a prefix matches literally.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// BuildList renders the listing statement: tags grouped without case, the
// most used spelling first, most carried first.
func BuildList(q Query) (query string, args []any, err error) {
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	offset := max(q.Offset, 0)
	inner := psq.Select("DISTINCT portal_assets.id AS asset_id", "t.v AS tag").
		From("portal_assets CROSS JOIN LATERAL jsonb_array_elements_text(portal_assets.tags) AS t(v)").
		Where("portal_assets.deleted_at IS NULL").
		Where("jsonb_typeof(portal_assets.tags) = 'array'")
	if q.Reach != nil {
		inner = inner.Where(reachstore.AssetPredicate(q.Reach))
	}
	if p := strings.TrimSpace(q.Prefix); p != "" {
		inner = inner.Where(sq.Expr(`LOWER(t.v) LIKE ? ESCAPE '\'`, strings.ToLower(likeEscaper.Replace(p))+"%"))
	}
	innerSQL, innerArgs, err := inner.ToSql()
	if err != nil {
		return "", nil, fmt.Errorf("building tag candidates: %w", err)
	}
	// #nosec G201 -- the inner statement is builder-rendered with bound
	// placeholders; limit and offset are clamped ints.
	query = fmt.Sprintf(
		"WITH e AS (%s), s AS (SELECT LOWER(tag) AS k, tag, COUNT(*) AS n FROM e GROUP BY LOWER(tag), tag) "+
			"SELECT (array_agg(s.tag ORDER BY s.n DESC, s.tag))[1] AS tag, "+
			"(SELECT COUNT(DISTINCT e.asset_id) FROM e WHERE LOWER(e.tag) = s.k) AS n, "+
			"array_agg(s.tag ORDER BY s.n DESC, s.tag) AS spellings, array_agg(s.n ORDER BY s.n DESC, s.tag) AS counts, "+
			"COUNT(*) OVER () AS total "+
			"FROM s GROUP BY s.k ORDER BY n DESC, s.k LIMIT %d OFFSET %d",
		innerSQL, limit, offset)
	return query, innerArgs, nil
}

// Store reads and rewrites tags over portal_assets.
type Store struct{ db *sql.DB }

// New builds a Store over db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// List returns a page of tags for q. An unidentified reach sees none.
func (s *Store) List(ctx context.Context, q Query) (Page, error) {
	out := Page{Tags: []Tag{}}
	if s == nil || s.db == nil || (q.Reach != nil && !q.Reach.Identified()) {
		return out, nil
	}
	query, args, err := BuildList(q)
	if err != nil {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return out, fmt.Errorf("listing tags: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query
	for rows.Next() {
		var (
			t         Tag
			spellings pq.StringArray
			counts    pq.Int64Array
		)
		if err := rows.Scan(&t.Tag, &t.Count, &spellings, &counts, &out.Total); err != nil {
			return out, fmt.Errorf("scanning tag: %w", err)
		}
		t.Spellings = spellingsOf(spellings, counts)
		out.Tags = append(out.Tags, t)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("iterating tags: %w", err)
	}
	return out, nil
}

// spellingsOf pairs each spelling of a tag with its count.
func spellingsOf(spellings pq.StringArray, counts pq.Int64Array) []Spelling {
	out := make([]Spelling, len(spellings))
	for i, sp := range spellings {
		out[i] = Spelling{Tag: sp}
		if i < len(counts) {
			out[i].Count = int(counts[i])
		}
	}
	return out
}

// Retag is one rename or merge: every asset carrying any spelling of a source
// tag, or a spelling of the target other than the target itself, carries the
// target instead, once.
type Retag struct {
	Sources []string
	Target  string
}

// Normalize trims the request and checks it: a target that is a valid tag,
// and at least one source.
func (r Retag) Normalize() (Retag, error) {
	r.Target = strings.TrimSpace(r.Target)
	if r.Target == "" {
		return r, errors.New("the new tag is required")
	}
	if len([]rune(r.Target)) > MaxTagLength {
		return r, fmt.Errorf("a tag is at most %d characters", MaxTagLength)
	}
	sources := make([]string, 0, len(r.Sources))
	for _, src := range r.Sources {
		if src = strings.TrimSpace(src); src != "" {
			sources = append(sources, src)
		}
	}
	if len(sources) == 0 {
		return r, errors.New("name the tag to rename or merge")
	}
	r.Sources = sources
	return r, nil
}

// matchSet is every lowercased spelling the retag rewrites: the sources and
// the target's other spellings.
func (r Retag) matchSet() []string {
	set := []string{strings.ToLower(r.Target)}
	for _, src := range r.Sources {
		if l := strings.ToLower(src); !slices.Contains(set, l) {
			set = append(set, l)
		}
	}
	return set
}

// retagSQL rewrites each affected asset's tags in place, keeping their order
// and carrying the target once. Deleted assets are rewritten too, so a
// restored asset does not bring a merged spelling back. The embedding is
// dropped because the indexed text (name, description, tags) changed; the
// reconciler embeds it again off the request path.
const retagSQL = `UPDATE portal_assets SET tags = (
	SELECT COALESCE(jsonb_agg(d.v ORDER BY d.ord), '[]'::jsonb) FROM (
		SELECT r.v, MIN(r.ord) AS ord FROM (
			SELECT CASE WHEN LOWER(e.v) = ANY($1) THEN $2 ELSE e.v END AS v, e.ord
			FROM jsonb_array_elements_text(portal_assets.tags) WITH ORDINALITY AS e(v, ord)
		) r GROUP BY r.v
	) d
), embedding = NULL, embedding_model = '', embedding_text_hash = NULL, updated_at = $3
WHERE jsonb_typeof(portal_assets.tags) = 'array' AND EXISTS (
	SELECT 1 FROM jsonb_array_elements_text(portal_assets.tags) AS x(v)
	WHERE LOWER(x.v) = ANY($1) AND x.v <> $2
)`

// Apply runs one rename or merge and returns how many assets it moved.
func (s *Store) Apply(ctx context.Context, r Retag, now time.Time) (int64, error) {
	r, err := r.Normalize()
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, retagSQL, pq.Array(r.matchSet()), r.Target, now)
	if err != nil {
		return 0, fmt.Errorf("rewriting tags: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting rewritten assets: %w", err)
	}
	return n, nil
}
