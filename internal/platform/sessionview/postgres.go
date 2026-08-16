package sessionview

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// psq builds statements with PostgreSQL's dollar placeholders. Only the
// outermost builder carries it: squirrel rewrites a sub-select's placeholders
// to positional form when it is embedded.
var psq = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

const (
	tableAudit     = "audit_logs"
	colSessionID   = "session_id"
	colTimestamp   = "timestamp"
	colUserID      = "user_id"
	colToolkitKind = "toolkit_kind"

	// defaultTimelineLimit bounds a timeline page when the caller states no
	// limit, so a long-running agent session cannot be read in one response.
	defaultTimelineLimit = 100

	// assetsOfSession is the correlated source for "the assets this session
	// saved", written as a bare FROM..WHERE so both the count and the
	// EXISTS filter are built from the one predicate. Deleted assets are
	// excluded: a session's output is what it left behind, and a deleted
	// asset is no longer that.
	assetsOfSession = "FROM portal_assets pa WHERE pa.session_id = g.session_id AND pa.deleted_at IS NULL"
)

// insightsOfSession is the correlated source for "the insights this session
// captured". An insight is a knowledge-dimension memory record carrying the
// capturing session in its metadata — migration 000031 folded the old
// knowledge_insights table into memory_records — so the join is on the same
// expression the Insights read uses.
var insightsOfSession = "FROM memory_records mr" +
	" WHERE mr.dimension = '" + memory.DimensionKnowledge + "'" +
	" AND mr.metadata->>'" + memory.MetaKeySessionID + "' = g.session_id"

// PostgresStore reads sessions from the audit log and the two tables that
// record what a session produced. It writes nothing.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore returns a session read model over db.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// groupedSessions rolls the audit log up into one row per session id, applying
// every filter that is a predicate on the session's own events. The filters
// that are predicates on what the session produced are applied by the caller,
// outside the grouping.
func groupedSessions(filter Filter) sq.SelectBuilder {
	qb := sq.Select(
		colSessionID,
		"MIN(timestamp) AS started_at",
		"MAX(timestamp) AS last_active_at",
		"COUNT(*) AS call_count",
		"COUNT(*) FILTER (WHERE NOT success) AS failure_count",
		// The caller and persona of the session's first event: a session
		// belongs to one caller, and every one of its rows repeats it.
		"(array_agg(user_id ORDER BY timestamp) FILTER (WHERE user_id <> ''))[1] AS user_id",
		"(array_agg(user_email ORDER BY timestamp) FILTER (WHERE user_email <> ''))[1] AS user_email",
		"(array_agg(persona ORDER BY timestamp) FILTER (WHERE persona <> ''))[1] AS persona",
		"array_remove(array_agg(DISTINCT tool_name ORDER BY tool_name), '') AS tools",
		// connection is nullable, and one NULL element fails the scan of
		// the whole page rather than of the row that carries it, so it is
		// coalesced to the empty string the aggregate already drops.
		"array_remove(array_agg(DISTINCT COALESCE(connection, '') ORDER BY COALESCE(connection, '')), '') AS connections",
	).From(tableAudit).
		Where(sq.NotEq{colSessionID: ""}).
		GroupBy(colSessionID)

	qb = applyEventFilters(qb, filter)
	if filter.HasFailures {
		qb = qb.Having("COUNT(*) FILTER (WHERE NOT success) > 0")
	}
	return qb
}

// applyEventFilters narrows the audit rows the rollup reads. A time range
// bounds the events, so a session matches when any of its calls falls inside
// the window rather than only when the whole session does.
func applyEventFilters(qb sq.SelectBuilder, filter Filter) sq.SelectBuilder {
	if filter.SessionID != "" {
		qb = qb.Where(sq.Eq{colSessionID: filter.SessionID})
	}
	if filter.UserID != "" {
		qb = qb.Where(sq.Eq{colUserID: filter.UserID})
	}
	if filter.StartTime != nil {
		qb = qb.Where(sq.GtOrEq{colTimestamp: *filter.StartTime})
	}
	if filter.EndTime != nil {
		qb = qb.Where(sq.LtOrEq{colTimestamp: *filter.EndTime})
	}
	return applyKindFilter(qb, filter.Kind)
}

// applyKindFilter restricts the rollup to one id origin. A prefixed kind is
// matched by its prefix; KindTransport is defined by carrying none of them and
// so is matched by excluding all three. starts_with is used rather than LIKE
// because every prefix ends in an underscore, which LIKE reads as a wildcard.
func applyKindFilter(qb sq.SelectBuilder, kind Kind) sq.SelectBuilder {
	if kind == "" {
		return qb
	}
	if prefix, ok := prefixForKind(kind); ok {
		return qb.Where(sq.Expr("starts_with("+colSessionID+", ?)", prefix))
	}
	if kind != KindTransport {
		// An unknown kind matches nothing rather than everything: a
		// typo in the query string must not widen the result set.
		return qb.Where(sq.Expr("false"))
	}
	for _, k := range []Kind{KindAgent, KindPortal, KindScript} {
		prefix, _ := prefixForKind(k)
		qb = qb.Where(sq.Expr("NOT starts_with("+colSessionID+", ?)", prefix))
	}
	return qb
}

// sessionRows selects the session rows themselves: the rollup, the persona
// overlay from the live session row, and the counts of what the session
// produced.
func sessionRows(filter Filter) sq.SelectBuilder {
	qb := psq.Select(
		"g.session_id", "g.started_at", "g.last_active_at",
		"g.call_count", "g.failure_count",
		"g.user_id", "g.user_email",
		// The live session row holds the persona the handle was minted
		// under; it outranks the events, which carry the persona each
		// call resolved to. Once the row expires the events are all
		// that is left.
		"COALESCE(NULLIF(sess.state->>'persona', ''), g.persona) AS persona",
		"g.tools", "g.connections",
		"(SELECT COUNT(*) "+assetsOfSession+") AS asset_count",
		"(SELECT COUNT(*) "+insightsOfSession+") AS insight_count",
	).FromSelect(groupedSessions(filter), "g").
		LeftJoin("sessions sess ON sess.id = g.session_id")

	if filter.HasAssets {
		qb = qb.Where("EXISTS (SELECT 1 " + assetsOfSession + ")")
	}
	return qb
}

// List returns sessions matching the filter, most recently active first.
func (s *PostgresStore) List(ctx context.Context, filter Filter) ([]Summary, error) {
	qb := sessionRows(filter).OrderBy("g.last_active_at DESC")
	if filter.Limit > 0 {
		qb = qb.Limit(uint64(filter.Limit))
	}
	if filter.Offset > 0 {
		qb = qb.Offset(uint64(filter.Offset))
	}

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("building session list query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	summaries := make([]Summary, 0, listCapacity(filter.Limit))
	for rows.Next() {
		summary, err := scanSummary(rows)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating sessions: %w", err)
	}
	return summaries, nil
}

const (
	// maxListCapacity caps the slice preallocation so a hostile per_page
	// cannot make the process allocate a large slice before a single row is
	// read.
	maxListCapacity = 1000
	// fallbackListCapacity is the preallocation for an unstated or
	// out-of-range page size.
	fallbackListCapacity = 50
)

// listCapacity returns the slice capacity to preallocate for a page.
func listCapacity(limit int) int {
	if limit <= 0 || limit > maxListCapacity {
		return fallbackListCapacity
	}
	return limit
}

// scanSummary reads one session row. The caller, persona, and the two arrays
// are nullable: a rollup over rows that carry none of them yields NULL.
func scanSummary(rows *sql.Rows) (Summary, error) {
	var (
		s                          Summary
		userID, userEmail, persona sql.NullString
		tools, connections         pq.StringArray
	)
	if err := rows.Scan(
		&s.SessionID, &s.StartedAt, &s.LastActiveAt,
		&s.CallCount, &s.FailureCount,
		&userID, &userEmail, &persona,
		&tools, &connections,
		&s.AssetCount, &s.InsightCount,
	); err != nil {
		return s, fmt.Errorf("scanning session row: %w", err)
	}
	s.Kind = KindOf(s.SessionID)
	s.UserID = userID.String
	s.UserEmail = userEmail.String
	s.Persona = persona.String
	s.Tools = []string(tools)
	s.Connections = []string(connections)
	if s.Tools == nil {
		s.Tools = []string{}
	}
	if s.Connections == nil {
		s.Connections = []string{}
	}
	return s, nil
}

// Count returns how many sessions match the filter. It counts the rolled-up
// rows rather than the events behind them, and ignores limit and offset.
func (s *PostgresStore) Count(ctx context.Context, filter Filter) (int, error) {
	filter.Limit, filter.Offset = 0, 0
	inner := sq.Select("g.session_id").FromSelect(groupedSessions(filter), "g")
	if filter.HasAssets {
		inner = inner.Where("EXISTS (SELECT 1 " + assetsOfSession + ")")
	}

	query, args, err := psq.Select("COUNT(*)").FromSelect(inner, "c").ToSql()
	if err != nil {
		return 0, fmt.Errorf("building session count query: %w", err)
	}

	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting sessions: %w", err)
	}
	return count, nil
}

// Get returns one session, or ErrNotFound when the audit log holds no call for
// that id. A session with no calls does not exist: the calls are what make it
// one.
func (s *PostgresStore) Get(ctx context.Context, sessionID string) (*Summary, error) {
	found, err := s.List(ctx, Filter{SessionID: sessionID, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, ErrNotFound
	}
	return &found[0], nil
}

// Timeline returns the session's calls oldest first, with the session's total
// call count so a caller can page without a second query for the total.
func (s *PostgresStore) Timeline(ctx context.Context, sessionID string, limit, offset int) ([]TimelineEntry, int, error) {
	if limit <= 0 {
		limit = defaultTimelineLimit
	}
	qb := psq.Select(
		"id", colTimestamp, "tool_name", "purpose", colToolkitKind,
		"connection", "success", "error_message", "duration_ms",
	).From(tableAudit).
		Where(sq.Eq{colSessionID: sessionID}).
		OrderBy(colTimestamp+" ASC", "id ASC").
		Limit(uint64(limit))
	if offset > 0 {
		qb = qb.Offset(uint64(offset))
	}

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("building session timeline query: %w", err)
	}

	entries, err := s.queryTimeline(ctx, query, args, limit)
	if err != nil {
		return nil, 0, err
	}

	total, err := s.countEvents(ctx, sessionID)
	if err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}

// queryTimeline runs the timeline statement and scans its rows.
func (s *PostgresStore) queryTimeline(ctx context.Context, query string, args []any, limit int) ([]TimelineEntry, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying session timeline: %w", err)
	}
	defer func() { _ = rows.Close() }()

	entries := make([]TimelineEntry, 0, listCapacity(limit))
	for rows.Next() {
		var (
			e       TimelineEntry
			purpose sql.NullString
		)
		if err := rows.Scan(
			&e.EventID, &e.Timestamp, &e.ToolName, &purpose, &e.ToolkitKind,
			&e.Connection, &e.Success, &e.ErrorMessage, &e.DurationMS,
		); err != nil {
			return nil, fmt.Errorf("scanning session timeline row: %w", err)
		}
		e.Purpose = purpose.String
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating session timeline: %w", err)
	}
	return entries, nil
}

// countEvents returns how many calls the session made.
func (s *PostgresStore) countEvents(ctx context.Context, sessionID string) (int, error) {
	query, args, err := psq.Select("COUNT(*)").From(tableAudit).
		Where(sq.Eq{colSessionID: sessionID}).ToSql()
	if err != nil {
		return 0, fmt.Errorf("building session event count query: %w", err)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("counting session events: %w", err)
	}
	return total, nil
}

// Assets returns the assets the session saved, oldest first.
func (s *PostgresStore) Assets(ctx context.Context, sessionID string) ([]AssetRef, error) {
	const query = `
		SELECT id, name, content_type, created_at
		FROM portal_assets
		WHERE session_id = $1 AND deleted_at IS NULL
		ORDER BY created_at ASC`

	rows, err := s.db.QueryContext(ctx, query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("querying session assets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	assets := []AssetRef{}
	for rows.Next() {
		var a AssetRef
		if err := rows.Scan(&a.ID, &a.Name, &a.ContentType, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning session asset: %w", err)
		}
		assets = append(assets, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating session assets: %w", err)
	}
	return assets, nil
}

// insightQuery reads the knowledge-dimension memory records a session
// captured. Insights are not a table of their own: migration 000031 folded
// knowledge_insights into memory_records, where an insight is a
// knowledge-dimension record whose metadata carries the capturing session and
// the review-status overlay. The status expression mirrors
// resolveInsightStatus in pkg/toolkits/knowledge, which owns that vocabulary:
// the explicit overlay first, then the status a migrated row carried in, then
// the lifecycle column mapped onto review names.
//
// Every one of those vocabulary values is bound rather than spliced, so the
// statement is a constant and the constants that define the convention stay
// where they are defined (see insightArgs).
const insightQuery = `
	SELECT id, category, content,
	       COALESCE(
	           NULLIF(metadata->>$2, ''),
	           NULLIF(metadata->>$3, ''),
	           CASE status
	               WHEN $4 THEN $5
	               WHEN $6 THEN $7
	               ELSE status
	           END) AS status,
	       created_at
	FROM memory_records
	WHERE dimension = $8
	  AND metadata->>$9 = $1
	ORDER BY created_at ASC`

// insightArgs binds insightQuery's placeholders: the session, then the
// metadata keys and status values the insight convention is defined by.
func insightArgs(sessionID string) []any {
	return []any{
		sessionID,
		memory.MetaKeyInsightStatus, memory.MetaKeyLegacyStatus,
		memory.StatusActive, memory.InsightStatusPending,
		memory.StatusArchived, knowledge.StatusRejected,
		memory.DimensionKnowledge, memory.MetaKeySessionID,
	}
}

// Insights returns the insights the session captured, oldest first.
func (s *PostgresStore) Insights(ctx context.Context, sessionID string) ([]InsightRef, error) {
	rows, err := s.db.QueryContext(ctx, insightQuery, insightArgs(sessionID)...)
	if err != nil {
		return nil, fmt.Errorf("querying session insights: %w", err)
	}
	defer func() { _ = rows.Close() }()

	insights := []InsightRef{}
	for rows.Next() {
		var i InsightRef
		if err := rows.Scan(&i.ID, &i.Category, &i.Text, &i.Status, &i.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning session insight: %w", err)
		}
		insights = append(insights, i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating session insights: %w", err)
	}
	return insights, nil
}

// ErrNotFound reports a session id the audit log holds no call for. Returned by
// Get, and wrapped by Load, so a caller distinguishes an unknown session from a
// read failure without inspecting a nil summary.
var ErrNotFound = errors.New("session not found")

// Load assembles one session in full: its summary, what it produced, and a
// page of its timeline. It is the single call the detail surface makes, so
// the four reads stay together rather than being re-sequenced per caller.
func Load(ctx context.Context, store Store, sessionID string, limit, offset int) (*Detail, error) {
	summary, err := store.Get(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("reading session: %w", err)
	}

	timeline, total, err := store.Timeline(ctx, sessionID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("reading session timeline: %w", err)
	}
	assets, err := store.Assets(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("reading session assets: %w", err)
	}
	insights, err := store.Insights(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("reading session insights: %w", err)
	}

	return &Detail{
		Summary:       *summary,
		Assets:        assets,
		Insights:      insights,
		Timeline:      timeline,
		TimelineTotal: total,
	}, nil
}

// Verify interface compliance.
var _ Store = (*PostgresStore)(nil)
