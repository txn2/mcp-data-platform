package callrecord

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// psq builds statements with PostgreSQL's dollar placeholders. Only the
// outermost builder carries it: squirrel rewrites an embedded sub-select's
// placeholders to positional form.
var psq = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// Config is what a deployment chooses about the catalog: how long a call that
// came to nothing is kept. Everything else about a record is derived.
type Config struct {
	// RetentionDays bounds how long an unused record is kept. Zero or
	// negative takes the default.
	RetentionDays int
}

// PostgresStore is the call catalog over PostgreSQL. It also owns the sweep
// that keeps the catalog from growing without bound; see retention.go.
type PostgresStore struct {
	db            *sql.DB
	retentionDays int

	// cancel and done are the sweeper's lifecycle, nil until it is started.
	cancel context.CancelFunc
	done   chan struct{}
}

// NewPostgresStore returns a call catalog over db.
func NewPostgresStore(db *sql.DB, cfg Config) *PostgresStore {
	return &PostgresStore{db: db, retentionDays: RetentionDays(cfg.RetentionDays)}
}

// callReferencePrefix is the mcp:call: prefix the satisfaction rule matches a
// capture's sources against. It is bound as a parameter rather than spelled in
// SQL: the mcp: reference grammar has one owner (pkg/portal/knowledgepage).
func callReferencePrefix() string { return knowledgepage.CallReferencePrefix }

// storedColumns are the record's own columns, in the order every scan reads
// them. The derived three follow, then the outcome computed from them.
var storedColumns = []string{
	"r.id", "r.event_id", "r.kind", "r.tool_name", "r.connection",
	"r.statement", "r.method", "r.path", "r.operation_id", "r.targets",
	"r.purpose", "r.user_id", "r.user_email", "r.session_id", "r.persona",
	"r.success", "r.error_message", "r.duration_ms", "r.response_chars",
	"r.promoted_urn", "r.promoted_at", "r.promoted_by",
	"r.rejected_at", "r.rejected_by", "r.rejection_note", "r.created_at",
}

// satisfiedByCase answers how, if at all, this call was used. It is written
// once, as a function of the placeholder its caller binds the reference prefix
// with, because three statements ask the same question in three placeholder
// dialects: the derived read (squirrel's `?`), the artifact lookup, and the
// retention sweep (positional `$n`). A second spelling of this rule is a second
// definition of what "satisfied" means.
//
// Both halves read the artifact's own record of its sources rather than a link
// table maintained beside it, so a record's outcome cannot disagree with the
// asset or the insight that gives it meaning. The capture route wins when both
// apply: an agent that wrote a description of what the query answers has said
// more than a save that happened to include it.
//
// The asset half distinguishes an export from a save by the tool that took the
// capture, which is the platform's own naming convention for stream-to-asset
// tools (#1320 records it on every capture).
func satisfiedByCase(prefixPlaceholder string) string {
	return `CASE
	WHEN EXISTS (
		SELECT 1 FROM memory_records m
		WHERE m.metadata @> jsonb_build_object('sources', jsonb_build_array(` + prefixPlaceholder + ` || r.event_id))
	) THEN 'capture'
	ELSE (
		SELECT CASE
			WHEN bool_or(right(cap->>'tool', 7) = '_export') THEN 'export'
			WHEN COUNT(*) > 0 THEN 'asset'
		END
		FROM portal_assets a
		CROSS JOIN LATERAL jsonb_array_elements(COALESCE(a.provenance->'captures', '[]'::jsonb)) cap
		WHERE a.deleted_at IS NULL
		  AND a.provenance @> jsonb_build_object('captures', jsonb_build_array(jsonb_build_object('event_ids', jsonb_build_array(r.event_id))))
		  AND cap->'event_ids' @> to_jsonb(r.event_id)
		  AND ` + namedSourceExpr("r.event_id") + `
	)
END`
}

// namedSourceExpr answers whether the capture in scope NAMED this call as a
// source, as opposed to having swept it up in the session's default window.
//
// A capture holds every data-access call the session made since the previous
// one, which is a useful record of the work and is not evidence that any given
// call answered anything: a session that read a notification history, looked up
// a user and ran a security probe before saving an asset had all three
// captured, and all three read `satisfied` (#1353). Naming is the evidence. A
// caller's `sources` argument names calls, and so does the capturing call's own
// record of itself, which is how an export cites the statement it streamed.
//
// The capture-level flag is read as well as the per-call one so a capture
// written before the per-call flag existed still says what it knew: an
// explicit capture named everything in it, by construction.
func namedSourceExpr(eventID string) string {
	return `(
		(cap->>'explicit')::boolean IS TRUE
		OR EXISTS (
			SELECT 1 FROM jsonb_array_elements(COALESCE(cap->'calls', '[]'::jsonb)) c
			WHERE c->>'event_id' = ` + eventID + `
			  AND (c->>'cited')::boolean IS TRUE
		)
	)`
}

// satisfiedByExpr is the derived read's column: the rule above, named.
var satisfiedByExpr = satisfiedByCase("?") + " AS satisfied_by"

// supersededExpr answers whether the same session later ran a better version of
// this call: same connection, same kind, same targets, and it succeeded.
//
// It requires targets, so a call whose targets could not be determined is never
// declared superseded — "we could not tell what either query read" is not
// evidence that one replaced the other. The comparison is a tuple so two calls
// recorded in the same millisecond still have a defined order.
//
// Both calls must also be reads. Supersession is a read-shaped idea: a later
// read of the same thing is a better answer to the same question, while a
// mutation is not a better version of an earlier mutation, even against the
// same resource (#1352). Approving a script twice is two decisions, and
// inserting a row twice is two rows.
var supersededExpr = fmt.Sprintf(`EXISTS (
	SELECT 1 FROM call_records l
	WHERE r.session_id <> ''
	  AND l.session_id = r.session_id
	  AND l.id <> r.id
	  AND l.success
	  AND l.kind = r.kind
	  AND l.connection = r.connection
	  AND l.targets = r.targets
	  AND jsonb_array_length(r.targets) > 0
	  AND %s
	  AND %s
	  AND (l.created_at, l.event_id) > (r.created_at, r.event_id)
) AS superseded`, readShapedExpr("r"), readShapedExpr("l"))

// readStatementPattern matches a statement that opens with a reading verb. It is
// anchored at the start, past any opening parentheses a wrapped select carries,
// and closed with a word boundary so the match is a whole keyword rather than
// the prefix of a longer word. A statement the audit policy withheld matches
// nothing and is therefore not read-shaped, which is the same conservative
// answer its absent targets already give.
//
// The verbs are the ones trino_query accepts, which is the tool that enforces
// read-only. It classifies by the opening verb and not by what the engine went
// on to do, so `EXPLAIN ANALYZE <a mutation>` through trino_execute would read
// as a read; the cost of that is two identical such statements in one session
// comparing as draft and correction, which is why the verb list is not widened
// past what a read-only tool would accept.
const readStatementPattern = `^[\s(]*(with|select|show|describe|desc|explain|table|values)\y`

// readShapedExpr reports whether one record only read. An API call is a read
// when its method is one, and a query when its statement begins with a reading
// verb; the two kinds carry different evidence and neither is guessed from the
// other.
func readShapedExpr(alias string) string {
	return fmt.Sprintf(`(CASE WHEN %[1]s.kind = '%[2]s' THEN %[1]s.method IN ('GET', 'HEAD')
	    ELSE %[1]s.statement ~* '%[3]s' END)`, alias, KindAPI, readStatementPattern)
}

// reuseCountExpr counts the sessions that fetched this record and then ran what
// it holds. Counting the credit rows rather than keeping a counter is what
// makes the number impossible to double-count: the credit table's primary key
// is (record, session).
const reuseCountExpr = `(
	SELECT COUNT(*) FROM call_record_reuse u WHERE u.call_record_id = r.id
) AS reuse_count`

// outcomeExpr decides the record's fate from the three derived facts, in the
// order the rule is stated: a failure is a failure whatever came after, a call
// something was built from is satisfied even if a better one followed, and a
// call replaced by a later one over the same targets is a draft.
var outcomeExpr = fmt.Sprintf(`CASE
	WHEN NOT d.success THEN '%s'
	WHEN d.satisfied_by IS NOT NULL THEN '%s'
	WHEN d.superseded THEN '%s'
	ELSE '%s'
END AS outcome`, OutcomeFailed, OutcomeSatisfied, OutcomeSuperseded, OutcomeRan)

// derived builds the record rows with their derived columns, before the outcome
// is named or filtered. The capture reference prefix is bound rather than
// spliced: the mcp: reference grammar has one owner (pkg/portal/knowledgepage)
// and this reads it rather than restating it in SQL.
func derived(f Filter) sq.SelectBuilder {
	qb := sq.Select(storedColumns...).
		Column(satisfiedByExpr, callReferencePrefix()).
		Column(supersededExpr).
		Column(reuseCountExpr).
		From("call_records r")
	return applyFilters(qb, f)
}

// withOutcome names the outcome over the derived rows.
func withOutcome(f Filter) sq.SelectBuilder { return outcomeOver(derived(f)) }

// outcomeOver names the outcome over any already-built derived select, so the
// list, the single read and the search all decide a record's fate with the one
// expression.
func outcomeOver(inner sq.SelectBuilder) sq.SelectBuilder {
	return sq.Select("d.*", outcomeExpr).FromSelect(inner, "d")
}

// applyFilters narrows the catalog by the facets a caller may state. The caller
// scope is one of them, and it is a predicate here rather than a check after
// the read, so another caller's record is not found rather than refused.
func applyFilters(qb sq.SelectBuilder, f Filter) sq.SelectBuilder {
	if f.UserID != "" {
		qb = qb.Where(sq.Eq{"r.user_id": f.UserID})
	}
	if f.Kind != "" {
		qb = qb.Where(sq.Eq{"r.kind": f.Kind})
	}
	if f.Connection != "" {
		qb = qb.Where(sq.Eq{"r.connection": f.Connection})
	}
	if f.SessionID != "" {
		qb = qb.Where(sq.Eq{"r.session_id": f.SessionID})
	}
	if len(f.EventIDs) > 0 {
		qb = qb.Where(sq.Eq{"r.event_id": f.EventIDs})
	}
	if f.Target != "" {
		qb = qb.Where(sq.Expr("r.targets @> to_jsonb(?::text)", f.Target))
	}
	if f.Search != "" {
		like := "%" + f.Search + "%"
		qb = qb.Where(sq.Or{
			sq.Expr("r.purpose ILIKE ?", like),
			sq.Expr("r.statement ILIKE ?", like),
			sq.Expr("r.path ILIKE ?", like),
			sq.Expr("r.operation_id ILIKE ?", like),
		})
	}
	if f.PromotableOnly {
		qb = qb.Where(sq.Eq{"r.promoted_urn": "", "r.rejected_at": nil})
	}
	return qb
}

// outcomeFilter applies the derived-outcome facets over the named rows. The
// review queue is a predicate on the outcome, so it belongs here rather than
// beside the stored-column filters.
func outcomeFilter(qb sq.SelectBuilder, f Filter) sq.SelectBuilder {
	if f.PromotableOnly {
		return qb.Where(sq.Eq{"o.outcome": OutcomeSatisfied})
	}
	if f.Outcome != "" && ValidOutcome(f.Outcome) {
		return qb.Where(sq.Eq{"o.outcome": f.Outcome})
	}
	return qb
}

// listQuery assembles the full statement: derived columns, named outcome,
// outcome facet, ordering and paging.
func listQuery(f Filter) sq.SelectBuilder {
	qb := psq.Select("o.*").FromSelect(withOutcome(f), "o")
	qb = outcomeFilter(qb, f)
	// The review queue orders by reuse first: a query a stranger re-ran is
	// better evidence than one its own author vouched for.
	if f.PromotableOnly {
		return qb.OrderBy("o.reuse_count DESC", "o.created_at DESC")
	}
	return qb.OrderBy("o.created_at DESC", "o.event_id DESC")
}

// List returns records matching the filter, newest first.
func (s *PostgresStore) List(ctx context.Context, f Filter) ([]Record, error) {
	qb := listQuery(f)
	if f.Limit > 0 {
		qb = qb.Limit(uint64(f.Limit))
	}
	if f.Offset > 0 {
		qb = qb.Offset(uint64(f.Offset))
	}
	query, args, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("building call record list query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying call records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	records := make([]Record, 0, listCapacity(f.Limit))
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating call records: %w", err)
	}
	return records, nil
}

// Count returns how many records match the filter, ignoring its paging.
func (s *PostgresStore) Count(ctx context.Context, f Filter) (int, error) {
	f.Limit, f.Offset = 0, 0
	inner := sq.Select("o.outcome").FromSelect(withOutcome(f), "o")
	inner = outcomeFilter(inner, f)

	query, args, err := psq.Select("COUNT(*)").FromSelect(inner, "c").ToSql()
	if err != nil {
		return 0, fmt.Errorf("building call record count query: %w", err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting call records: %w", err)
	}
	return count, nil
}

// defaultTargetLimit bounds the records a table's enrichment carries when the
// caller states no limit. It is small on purpose: a nudge toward reuse, not a
// query history.
const defaultTargetLimit = 5

const (
	// maxListCapacity caps slice preallocation so a hostile page size cannot
	// make the process allocate before a row is read.
	maxListCapacity = 1000
	// fallbackListCapacity is the preallocation for an unstated page size.
	fallbackListCapacity = 50
)

func listCapacity(limit int) int {
	if limit <= 0 || limit > maxListCapacity {
		return fallbackListCapacity
	}
	return limit
}

// rowAux holds the columns a record row carries that its Go shape does not: the
// nullable ones, and the two derived booleans the outcome is named from.
type rowAux struct {
	targets     []byte
	satisfiedBy sql.NullString
	superseded  bool
	promotedAt  sql.NullTime
	rejectedAt  sql.NullTime
	outcome     string
}

// recordDest returns the scan targets for one record row, in the order every
// statement selects: the stored columns, the three derived ones, and the
// outcome named from them. Written once so a column added to storedColumns and
// a field added to the scan cannot drift apart.
func recordDest(r *Record, aux *rowAux) []any {
	return []any{
		&r.ID, &r.EventID, &r.Kind, &r.ToolName, &r.Connection,
		&r.Statement, &r.Method, &r.Path, &r.OperationID, &aux.targets,
		&r.Purpose, &r.UserID, &r.UserEmail, &r.SessionID, &r.Persona,
		&r.Success, &r.ErrorMessage, &r.DurationMS, &r.ResponseChars,
		&r.PromotedURN, &aux.promotedAt, &r.PromotedBy,
		&aux.rejectedAt, &r.RejectedBy, &r.RejectionNote, &r.CreatedAt,
		&aux.satisfiedBy, &aux.superseded, &r.ReuseCount, &aux.outcome,
	}
}

// finishRecord fills in what the auxiliary columns say about the record.
func finishRecord(r *Record, aux *rowAux) {
	r.Outcome = aux.outcome
	r.SatisfiedBy = aux.satisfiedBy.String
	r.Targets = decodeTargets(aux.targets)
	if aux.promotedAt.Valid {
		t := aux.promotedAt.Time.UTC()
		r.PromotedAt = &t
	}
	if aux.rejectedAt.Valid {
		t := aux.rejectedAt.Time.UTC()
		r.RejectedAt = &t
	}
	r.Reference = knowledgepage.CallRef(r.EventID)
}

// scanRecord reads one row of the list statement.
func scanRecord(rows *sql.Rows) (Record, error) {
	var (
		r   Record
		aux rowAux
	)
	if err := rows.Scan(recordDest(&r, &aux)...); err != nil {
		return r, fmt.Errorf("scanning call record: %w", err)
	}
	finishRecord(&r, &aux)
	return r, nil
}

// scanRecordWithScore reads one row of a search statement, which carries a
// relevance column the other statements do not. The score is selected in the
// derived select and so arrives before the outcome named over it, which is why the
// destination is spliced rather than appended.
func scanRecordWithScore(rows *sql.Rows) (Record, float64, error) {
	var (
		r     Record
		aux   rowAux
		score float64
	)
	dest := recordDest(&r, &aux)
	dest = append(dest[:len(dest)-1:len(dest)-1], &score, &aux.outcome)
	if err := rows.Scan(dest...); err != nil {
		return r, 0, fmt.Errorf("scanning call record search row: %w", err)
	}
	finishRecord(&r, &aux)
	return r, score, nil
}

// decodeTargets reads the stored target array, yielding an empty slice for a
// row with none so a client never has to model both null and [].
func decodeTargets(raw []byte) []string {
	targets := []string{}
	if len(raw) == 0 {
		return targets
	}
	if err := json.Unmarshal(raw, &targets); err != nil {
		return []string{}
	}
	return targets
}

// Get returns one record with its artifacts, or ErrNotFound when the scope
// admits none.
func (s *PostgresStore) Get(ctx context.Context, scope Scope) (*Record, error) {
	if scope.ID == "" {
		return nil, ErrNotFound
	}
	return s.one(ctx, sq.Eq{"r.id": scope.ID}, scope.UserID)
}

// GetByEventID resolves an mcp:call:<event_id> reference, scoped the same way
// Get is.
func (s *PostgresStore) GetByEventID(ctx context.Context, eventID, userID string) (*Record, error) {
	if eventID == "" {
		return nil, ErrNotFound
	}
	return s.one(ctx, sq.Eq{"r.event_id": eventID}, userID)
}

// one reads a single record under an identifying predicate and the caller
// scope, then loads what was built from it.
func (s *PostgresStore) one(ctx context.Context, id sq.Sqlizer, userID string) (*Record, error) {
	f := Filter{UserID: userID}
	inner := derived(f).Where(id)
	query, args, err := psq.Select("o.*").
		FromSelect(sq.Select("d.*", outcomeExpr).FromSelect(inner, "d"), "o").
		Limit(1).ToSql()
	if err != nil {
		return nil, fmt.Errorf("building call record query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying call record: %w", err)
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("querying call record: %w", err)
		}
		return nil, ErrNotFound
	}
	rec, err := scanRecord(rows)
	if err != nil {
		return nil, err
	}
	artifacts, err := s.artifacts(ctx, rec.EventID)
	if err != nil {
		return nil, err
	}
	rec.Artifacts = artifacts
	return &rec, nil
}

// artifactQuery lists what cites one call: the assets and exports whose
// provenance names it, then the captured insights that named its reference.
// Both halves read the artifact's own record of its sources, which is the same
// evidence the outcome is derived from — including the naming rule, so a record
// can never list an artifact that did not put it there and read `ran` at the
// same time.
//
// #nosec G202 -- the only thing concatenated is this package's own naming rule;
// every value the statement compares is bound as a parameter.
var artifactQuery = `
	SELECT kind, id, name FROM (
		SELECT CASE WHEN right(cap->>'tool', 7) = '_export' THEN 'export' ELSE 'asset' END AS kind,
		       a.id AS id, a.name AS name, a.created_at AS created_at
		FROM portal_assets a
		CROSS JOIN LATERAL jsonb_array_elements(COALESCE(a.provenance->'captures', '[]'::jsonb)) cap
		WHERE a.deleted_at IS NULL
		  AND a.provenance @> jsonb_build_object('captures', jsonb_build_array(jsonb_build_object('event_ids', jsonb_build_array($1::text))))
		  AND cap->'event_ids' @> to_jsonb($1::text)
		  AND ` + namedSourceExpr("$1::text") + `
		UNION
		SELECT 'capture' AS kind, m.id AS id, left(m.content, 120) AS name, m.created_at AS created_at
		FROM memory_records m
		WHERE m.metadata @> jsonb_build_object('sources', jsonb_build_array($2::text || $1::text))
	) x
	ORDER BY created_at ASC
	LIMIT 50`

// artifacts returns what was built from one call.
func (s *PostgresStore) artifacts(ctx context.Context, eventID string) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx, artifactQuery, eventID, callReferencePrefix())
	if err != nil {
		return nil, fmt.Errorf("querying call record artifacts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	artifacts := []Artifact{}
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.Kind, &a.ID, &a.Name); err != nil {
			return nil, fmt.Errorf("scanning call record artifact: %w", err)
		}
		artifacts = append(artifacts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating call record artifacts: %w", err)
	}
	return artifacts, nil
}

// insertQuery records one call. It is idempotent on the event id: the audit
// pipeline can deliver the same event twice and the catalog holds one record.
const insertQuery = `
	INSERT INTO call_records (
		event_id, kind, tool_name, connection, statement, statement_norm,
		method, path, operation_id, targets, purpose,
		user_id, user_email, session_id, persona,
		success, error_message, duration_ms, response_chars, created_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
	ON CONFLICT (event_id) DO NOTHING`

// Insert records one call.
func (s *PostgresStore) Insert(ctx context.Context, r Record) error {
	targets, err := json.Marshal(normalizeTargets(r.Targets))
	if err != nil {
		return fmt.Errorf("encoding call record targets: %w", err)
	}
	created := r.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	if _, err := s.db.ExecContext(ctx, insertQuery,
		r.EventID, r.Kind, r.ToolName, r.Connection, r.Statement, NormalizeStatement(r.Statement),
		r.Method, r.Path, r.OperationID, targets, r.Purpose,
		r.UserID, r.UserEmail, r.SessionID, r.Persona,
		r.Success, r.ErrorMessage, r.DurationMS, r.ResponseChars, created,
	); err != nil {
		return fmt.Errorf("inserting call record: %w", err)
	}
	return nil
}

// recordFetchQuery notes that a session dereferenced a record. The first fetch
// is the one kept: what reuse asks is whether the session had seen the record
// before it ran the query, and the earliest sighting is the honest answer.
//
// The moment is stated by the application rather than taken from the database's
// NOW(). Reuse compares this against a record's created_at, which is the audit
// event's timestamp and therefore the application's clock; a database clock
// running milliseconds ahead (measured at up to 14ms against a containerized
// Postgres) would put a sighting after the call it preceded and silently drop
// the credit. One clock, one comparison.
const recordFetchQuery = `
	INSERT INTO call_record_fetches (call_record_id, session_id, user_id, fetched_at)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (call_record_id, session_id) DO NOTHING`

// RecordFetch notes that a session dereferenced this record.
func (s *PostgresStore) RecordFetch(ctx context.Context, recordID string, by Fetcher) error {
	if recordID == "" || by.SessionID == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, recordFetchQuery,
		recordID, by.SessionID, by.UserID, time.Now().UTC()); err != nil {
		return fmt.Errorf("recording call record fetch: %w", err)
	}
	return nil
}

// creditReuseQuery credits every record this call re-ran.
//
// The three conditions are the whole definition of reuse: the session had
// fetched the record before making this call, the record came out of a
// different session, and what ran is the same statement (or the same API
// resource) over the same connection. A session re-running its own query is
// not reuse, and neither is an identical query written independently: without
// the fetch, nothing says the record was what led to it.
//
// The API arm compares resolved targets rather than the operation id, for the
// reason supersession does: an operation id names an endpoint, and a session
// that read one record and then invoked the same endpoint against a different
// resource re-ran nothing (#1352). A call with no target that distinguishes it
// credits nothing.
//
// #nosec G101 -- the reuse-credit INSERT, not a credential: the scanner is
// matching on the column names (user_id, session_id) the statement selects.
const creditReuseQuery = `
	INSERT INTO call_record_reuse (call_record_id, session_id, user_id, reused_event_id)
	SELECT p.id, $1, $2, $3
	FROM call_record_fetches f
	JOIN call_records p ON p.id = f.call_record_id
	WHERE f.session_id = $1
	  AND f.fetched_at <= $4
	  AND p.session_id <> $1
	  AND p.kind = $5
	  AND p.connection = $6
	  AND (
	        ($5 = 'sql' AND $7 <> '' AND p.statement_norm = $7)
	     OR ($5 = 'api' AND jsonb_array_length($8::jsonb) > 0 AND p.targets = $8::jsonb)
	      )
	ON CONFLICT (call_record_id, session_id) DO NOTHING`

// CreditReuse credits the records this call re-ran, and reports how many.
func (s *PostgresStore) CreditReuse(ctx context.Context, r Record) (int, error) {
	if !r.Success || r.SessionID == "" {
		return 0, nil
	}
	created := r.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	targets, err := json.Marshal(normalizeTargets(r.Targets))
	if err != nil {
		return 0, fmt.Errorf("encoding call record targets: %w", err)
	}
	res, err := s.db.ExecContext(ctx, creditReuseQuery,
		r.SessionID, r.UserID, r.EventID, created,
		r.Kind, r.Connection, NormalizeStatement(r.Statement), targets,
	)
	if err != nil {
		return 0, fmt.Errorf("crediting call record reuse: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil //nolint:nilerr // a driver that cannot count is not a failed credit
	}
	return int(n), nil
}

// ForTargets returns satisfied records addressing any of the given datasets,
// most reused first.
func (s *PostgresStore) ForTargets(ctx context.Context, urns []string, userID string, limit int) ([]Record, error) {
	if len(urns) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultTargetLimit
	}
	targeted := sq.Or{}
	for _, urn := range urns {
		targeted = append(targeted, sq.Expr("r.targets @> to_jsonb(?::text)", urn))
	}
	inner := derived(Filter{UserID: userID}).Where(targeted)
	qb := psq.Select("o.*").
		FromSelect(sq.Select("d.*", outcomeExpr).FromSelect(inner, "d"), "o").
		Where(sq.Eq{"o.outcome": OutcomeSatisfied}).
		OrderBy("o.reuse_count DESC", "o.created_at DESC")

	query, args, err := paged(qb, limit).ToSql()
	if err != nil {
		return nil, fmt.Errorf("building call records for targets query: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying call records for targets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	records := []Record{}
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating call records for targets: %w", err)
	}
	return records, nil
}

// Promote stores what the record became.
func (s *PostgresStore) Promote(ctx context.Context, id string, p Promotion) error {
	const query = `
		UPDATE call_records
		SET promoted_urn = $2, promoted_by = $3, promoted_at = NOW(),
		    rejected_at = NULL, rejected_by = '', rejection_note = '',
		    updated_at = NOW()
		WHERE id = $1`
	return s.exec(ctx, query, "promoting call record", id, p.URN, p.Actor)
}

// Reject records that the record was reviewed and declined.
func (s *PostgresStore) Reject(ctx context.Context, id string, r Rejection) error {
	const query = `
		UPDATE call_records
		SET rejected_at = NOW(), rejected_by = $2, rejection_note = $3, updated_at = NOW()
		WHERE id = $1`
	return s.exec(ctx, query, "rejecting call record", id, r.Actor, r.Note)
}

// exec runs a single-row update and reports ErrNotFound when it matched none.
// what names the action, for the wrapped error.
func (s *PostgresStore) exec(ctx context.Context, query, what string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("updating call record (%s): %w", what, err)
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// normalizeTargets sorts and deduplicates a target set so two calls over the
// same tables compare equal, which is what supersession asks.
func normalizeTargets(targets []string) []string {
	out := make([]string, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, t := range targets {
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

// Verify interface compliance.
var _ Store = (*PostgresStore)(nil)
