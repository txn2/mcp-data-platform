package callrecord

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordColumns lists the record SELECT columns in scan order: the stored
// columns, the three derived, and the outcome named over them.
var recordColumns = []string{
	"id", "event_id", "kind", "tool_name", "connection",
	"statement", "method", "path", "operation_id", "targets",
	"purpose", "user_id", "user_email", "session_id", "persona",
	"success", "error_message", "duration_ms", "response_chars",
	"promoted_urn", "promoted_at", "promoted_by",
	"rejected_at", "rejected_by", "rejection_note", "created_at",
	"satisfied_by", "superseded", "reuse_count", "outcome",
}

func testTime() time.Time {
	return time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC) //nolint:revive // test fixture date
}

// newMock returns a catalog over a mocked database plus the mock controller.
func newMock(t *testing.T) (*PostgresStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		assert.NoError(t, mock.ExpectationsWereMet())
	})
	return NewPostgresStore(db, Config{}), mock
}

// recordRow builds one row of the record projection, with the derived columns
// a caller can vary.
func recordRow(outcome, satisfiedBy string, reuse int, extra ...any) *sqlmock.Rows {
	values := [][]driver.Value{{
		"call-1", "evt-1", KindSQL, "trino_query", "acme-warehouse",
		"SELECT 1", "", "", "", []byte(`["urn:li:dataset:(urn:li:dataPlatform:trino,sales.orders,PROD)"]`),
		"Sizing revenue.", "u1", "analyst@example.com", "dps_abc", "data-engineer",
		true, "", int64(143), 2450,
		"", nil, "",
		nil, "", "", testTime(),
		nullable(satisfiedBy), false, reuse, outcome,
	}}
	cols := recordColumns
	if len(extra) > 0 {
		// A search row carries its relevance before the outcome.
		cols = append(append([]string{}, recordColumns[:len(recordColumns)-1]...), "score", "outcome")
		row := values[0]
		values[0] = append(append([]driver.Value{}, row[:len(row)-1]...),
			driver.Value(extra[0]), row[len(row)-1])
	}
	rows := sqlmock.NewRows(cols)
	for _, v := range values {
		rows.AddRow(v...)
	}
	return rows
}

// nullable renders an empty derived string as SQL NULL, which is what the
// satisfied-by expression yields for a record nothing cites.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// expectJITOff queues the read-only transaction a list or a count runs in,
// with JIT off (#2231).
func expectJITOff(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL jit = off").WillReturnResult(sqlmock.NewResult(0, 0))
}

func TestListProjectsARecord(t *testing.T) {
	store, mock := newMock(t)
	expectJITOff(mock)
	mock.ExpectQuery("FROM call_records").
		WillReturnRows(recordRow(OutcomeSatisfied, SatisfiedByCapture, 2))
	mock.ExpectCommit()

	got, err := store.List(context.Background(), Filter{UserID: "u1", Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 1)

	assert.Equal(t, OutcomeSatisfied, got[0].Outcome)
	assert.Equal(t, SatisfiedByCapture, got[0].SatisfiedBy)
	assert.Equal(t, 2, got[0].ReuseCount)
	assert.Equal(t, "mcp:call:evt-1", got[0].Reference,
		"a record carries the reference an agent cites it by")
	assert.Len(t, got[0].Targets, 1)
}

func TestListCarriesADecidedRecordsTimestamps(t *testing.T) {
	store, mock := newMock(t)
	promoted := testTime().Add(time.Hour)
	rejected := testTime().Add(2 * time.Hour)
	row := sqlmock.NewRows(recordColumns).AddRow(
		"call-1", "evt-1", KindSQL, "trino_query", "acme-warehouse",
		"SELECT 1", "", "", "", []byte(`[]`),
		"Sizing revenue.", "u1", "analyst@example.com", "dps_abc", "data-engineer",
		true, "", int64(143), 2450,
		"urn:li:query:abc", promoted, "reviewer@example.com",
		rejected, "reviewer@example.com", "Superseded.", testTime(),
		"asset", false, 0, OutcomeSatisfied,
	)

	expectJITOff(mock)
	mock.ExpectQuery("FROM call_records").WillReturnRows(row)
	mock.ExpectCommit()
	got, err := store.List(context.Background(), Filter{})
	require.NoError(t, err)
	require.Len(t, got, 1)

	require.NotNil(t, got[0].PromotedAt)
	require.NotNil(t, got[0].RejectedAt)
	assert.Equal(t, promoted.UTC(), *got[0].PromotedAt)
	assert.Equal(t, rejected.UTC(), *got[0].RejectedAt)
	assert.False(t, got[0].Promotable(), "a decided record is no longer offered")
}

func TestListScopesToTheCallerInSQL(t *testing.T) {
	store, mock := newMock(t)
	// The caller is a predicate inside the query, not a check after the read:
	// another caller's record must never be selected in the first place.
	expectJITOff(mock)
	mock.ExpectQuery("FROM call_records").
		WithArgs(sqlmock.AnyArg(), "u1").
		WillReturnRows(sqlmock.NewRows(recordColumns))
	mock.ExpectCommit()

	_, err := store.List(context.Background(), Filter{UserID: "u1"})
	require.NoError(t, err)
}

func TestListAppliesEveryFacet(t *testing.T) {
	store, mock := newMock(t)
	expectJITOff(mock)
	mock.ExpectQuery("FROM call_records").
		WillReturnRows(sqlmock.NewRows(recordColumns))
	mock.ExpectCommit()

	_, err := store.List(context.Background(), Filter{
		UserID: "u1", Kind: KindAPI, Connection: "acme", Outcome: OutcomeRan,
		Target: "api:acme:listOrders", SessionID: "dps_abc", Search: "orders",
		Limit: 5, Offset: 5,
	})
	require.NoError(t, err)
}

func TestListReviewQueueOrdersByReuse(t *testing.T) {
	store, mock := newMock(t)
	// A query a stranger re-ran is better evidence than one its own author
	// vouched for, so the queue leads with reuse.
	expectJITOff(mock)
	mock.ExpectQuery("ORDER BY o.reuse_count DESC").
		WillReturnRows(recordRow(OutcomeSatisfied, SatisfiedByAsset, 3))
	mock.ExpectCommit()

	got, err := store.List(context.Background(), Filter{PromotableOnly: true})
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestListReportsAQueryFailure(t *testing.T) {
	store, mock := newMock(t)
	expectJITOff(mock)
	mock.ExpectQuery("FROM call_records").WillReturnError(errors.New("db down"))
	mock.ExpectRollback()

	_, err := store.List(context.Background(), Filter{})
	require.Error(t, err)
}

func TestCountIgnoresPaging(t *testing.T) {
	store, mock := newMock(t)
	expectJITOff(mock)
	mock.ExpectQuery("COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))
	mock.ExpectCommit()

	got, err := store.Count(context.Background(), Filter{UserID: "u1", Limit: 5, Offset: 10})
	require.NoError(t, err)
	assert.Equal(t, 7, got)
}

func TestGetLoadsWhatWasBuiltFromTheCall(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM call_records").
		WillReturnRows(recordRow(OutcomeSatisfied, SatisfiedByAsset, 0))
	mock.ExpectQuery("FROM portal_assets").
		WillReturnRows(sqlmock.NewRows([]string{"kind", "id", "name"}).
			AddRow("asset", "ast-1", "Q4 Revenue"))

	got, err := store.Get(context.Background(), Scope{ID: "call-1", UserID: "u1"})
	require.NoError(t, err)
	require.Len(t, got.Artifacts, 1)
	assert.Equal(t, "ast-1", got.Artifacts[0].ID)
}

func TestGetAnotherCallersRecordIsNotFound(t *testing.T) {
	store, mock := newMock(t)
	// Scoped in SQL, so another caller's id returns no row at all: the same
	// answer an id that was never used gets.
	mock.ExpectQuery("FROM call_records").WillReturnRows(sqlmock.NewRows(recordColumns))

	_, err := store.Get(context.Background(), Scope{ID: "call-1", UserID: "someone-else"})
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestGetWithoutAnIDIsNotFound(t *testing.T) {
	store, _ := newMock(t)

	_, err := store.Get(context.Background(), Scope{})
	assert.ErrorIs(t, err, ErrNotFound)

	_, err = store.GetByEventID(context.Background(), "", "u1")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestGetByEventIDResolvesAReference(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM call_records").WillReturnRows(recordRow(OutcomeRan, "", 0))
	mock.ExpectQuery("FROM portal_assets").
		WillReturnRows(sqlmock.NewRows([]string{"kind", "id", "name"}))

	got, err := store.GetByEventID(context.Background(), "evt-1", "u1")
	require.NoError(t, err)
	assert.Equal(t, "evt-1", got.EventID)
	assert.Empty(t, got.Artifacts)
}

func TestInsertIsIdempotentOnTheEventID(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectExec("INSERT INTO call_records").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := store.Insert(context.Background(), Record{
		EventID: "evt-1", Kind: KindSQL, ToolName: "trino_query",
		Statement: "SELECT 1", Targets: []string{"b", "a", "a", ""},
	})
	require.NoError(t, err)
}

func TestRecordFetchNeedsBothSides(t *testing.T) {
	store, mock := newMock(t)
	// A fetch with no session credits nothing later, so there is nothing to
	// record and no reason to touch the database.
	require.NoError(t, store.RecordFetch(context.Background(), "call-1", Fetcher{}))
	require.NoError(t, store.RecordFetch(context.Background(), "", Fetcher{SessionID: "dps_x"}))

	// The moment is the application's, not the database's: reuse compares it
	// against a record's created_at, which comes from the same clock.
	mock.ExpectExec("INSERT INTO call_record_fetches").
		WithArgs("call-1", "dps_x", "u2", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.RecordFetch(context.Background(), "call-1", Fetcher{SessionID: "dps_x", UserID: "u2"}))
}

func TestCreditReuseOnlyForASuccessfulSessionCall(t *testing.T) {
	store, mock := newMock(t)

	n, err := store.CreditReuse(context.Background(), Record{Success: false, SessionID: "dps_x"})
	require.NoError(t, err)
	assert.Zero(t, n, "a failed call re-ran nothing")

	n, err = store.CreditReuse(context.Background(), Record{Success: true})
	require.NoError(t, err)
	assert.Zero(t, n, "a call with no session cannot be credited to one")

	mock.ExpectExec("INSERT INTO call_record_reuse").
		WillReturnResult(sqlmock.NewResult(0, 2))
	n, err = store.CreditReuse(context.Background(), Record{
		Success: true, SessionID: "dps_x", Kind: KindSQL, Statement: "SELECT 1",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}

// TestCreditReuseComparesResolvedTargets proves the API arm is bound the
// record's normalized targets rather than its operation id, which is what stops
// a session that read one resource's record and then addressed another from
// being credited with re-running it (#1352).
func TestCreditReuseComparesResolvedTargets(t *testing.T) {
	store, mock := newMock(t)

	mock.ExpectExec("INSERT INTO call_record_reuse").
		WithArgs("dps_x", "u1", "evt-9", testTime(), KindAPI, "platform-admin", "",
			[]byte(`["api:platform-admin:POST /admin/scripts/script-a/approve"]`)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	n, err := store.CreditReuse(context.Background(), Record{
		Success: true, SessionID: "dps_x", UserID: "u1", EventID: "evt-9",
		Kind: KindAPI, Connection: "platform-admin", OperationID: "approveScript",
		Targets:   []string{"api:platform-admin:POST /admin/scripts/script-a/approve"},
		CreatedAt: testTime(),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

func TestForTargetsKeepsOnlySatisfiedRecords(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("FROM call_records").
		WillReturnRows(recordRow(OutcomeSatisfied, SatisfiedByAsset, 1))

	got, err := store.ForTargets(context.Background(),
		[]string{"urn:li:dataset:(urn:li:dataPlatform:trino,sales.orders,PROD)"}, "u1", 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, OutcomeSatisfied, got[0].Outcome)
}

func TestForTargetsWithoutATargetAsksNothing(t *testing.T) {
	store, _ := newMock(t)

	got, err := store.ForTargets(context.Background(), nil, "u1", 3)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestPromoteAndRejectReportAMissingRecord(t *testing.T) {
	store, mock := newMock(t)

	mock.ExpectExec("UPDATE call_records").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.Promote(context.Background(), "call-1", Promotion{URN: "urn:li:query:x", Actor: "a"}))

	mock.ExpectExec("UPDATE call_records").WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, store.Reject(context.Background(), "gone", Rejection{Actor: "a"}), ErrNotFound)
}

func TestSearchNeedsACaller(t *testing.T) {
	store, _ := newMock(t)

	// A search with no caller returns nothing rather than everyone's calls.
	got, err := store.Search(context.Background(), SearchQuery{Text: "revenue"})
	require.NoError(t, err)
	assert.Empty(t, got)

	// Lexical ranking with nothing to match on asks the database nothing.
	got, err = store.Search(context.Background(), SearchQuery{UserID: "u1"})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSearchRanksLexically(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("ts_rank_cd").WillReturnRows(recordRow(OutcomeSatisfied, SatisfiedByCapture, 4, 0.42))

	got, err := store.Search(context.Background(), SearchQuery{Text: "revenue", UserID: "u1"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.InDelta(t, 0.42, got[0].Score, 0.001)
	assert.Equal(t, OutcomeSatisfied, got[0].Record.Outcome,
		"a hit carries its outcome so an agent can prefer a query that answered something")
}

func TestSearchFusesBothArms(t *testing.T) {
	store, mock := newMock(t)
	// The vector arm and the lexical arm run against the index each can use;
	// each hit carries its cosine, and a record whose words match gains the
	// lexical term.
	mock.ExpectQuery("embedding").WillReturnRows(recordRow(OutcomeRan, "", 0, 0.8))
	mock.ExpectQuery("ts_rank_cd").WillReturnRows(recordRow(OutcomeRan, "", 0, 0.8))

	got, err := store.Search(context.Background(), SearchQuery{
		Text: "revenue", UserID: "u1", Embedding: []float32{0.1, 0.2},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.InDelta(t, semanticWeight*0.9+(1-semanticWeight), got[0].Score, 0.001)
}

func TestFuseBlendsOnOneCurve(t *testing.T) {
	t.Parallel()

	// A near neighbor in meaning, a record matched by its words and embedded,
	// and a fresh record matched by an exact token and not embedded yet.
	vector := []Scored{
		{Record: Record{ID: "neighbor", ReuseCount: 3}, Score: 0.95},
		{Record: Record{ID: "both"}, Score: 0.4},
	}
	lexical := []Scored{
		{Record: Record{ID: "both"}, Score: 0.4},
		{Record: Record{ID: "fresh"}, Score: 0},
	}
	got := fuse(vector, lexical, 10)

	require.Len(t, got, 3)
	assert.Equal(t, "both", got[0].Record.ID)
	assert.Equal(t, "fresh", got[1].Record.ID,
		"an exact token match not embedded yet outranks a neighbor that matches none of the words (#2251)")
	assert.Equal(t, "neighbor", got[2].Record.ID)
	assert.InDelta(t, semanticWeight*0.5+(1-semanticWeight), got[1].Score, 0.001)
	assert.InDelta(t, semanticWeight*0.975, got[2].Score, 0.001)
	assert.Len(t, fuse(vector, lexical, 1), 1, "the limit bounds the fused set")
}

func TestDecodeTargetsNeverYieldsNull(t *testing.T) {
	t.Parallel()

	// A client should never have to model both null and [].
	assert.Equal(t, []string{}, decodeTargets(nil))
	assert.Equal(t, []string{}, decodeTargets([]byte("not json")))
	assert.Equal(t, []string{"a"}, decodeTargets([]byte(`["a"]`)))
}

func TestListCapacityIsBounded(t *testing.T) {
	t.Parallel()

	// A hostile page size must not make the process allocate before a row is
	// read.
	assert.Equal(t, fallbackListCapacity, listCapacity(0))
	assert.Equal(t, fallbackListCapacity, listCapacity(maxListCapacity+1))
	assert.Equal(t, 25, listCapacity(25))
}

func TestNormalizeTargetsSortsAndDeduplicates(t *testing.T) {
	t.Parallel()

	// Supersession compares target sets, so the same tables in another order
	// must compare equal.
	assert.Equal(t, []string{"a", "b"}, normalizeTargets([]string{"b", "a", "b", ""}))
}

// Verify the store satisfies the sql.DB-backed contract the wiring passes it as.
var _ Store = (*PostgresStore)(nil)

// TestCountByOutcomeStatesTheCasesBranch: a count filtered by outcome counts
// the records meeting the condition the outcome CASE reaches it by, over the
// cited join, and asks no outcome of an unfiltered count (#2231).
func TestCountByOutcomeStatesTheCasesBranch(t *testing.T) {
	cases := map[string][]string{
		OutcomeFailed:     {"NOT r.success", "r.success = $"},
		OutcomeSatisfied:  {"cited.event_id IS NOT NULL THEN", ") IS NOT NULL"},
		OutcomeSuperseded: {") IS NULL", "COALESCE(sup.later, FALSE)", "PARTITION BY w.session_id"},
		OutcomeRan:        {") IS NULL", "NOT COALESCE(sup.later, FALSE)", "PARTITION BY w.session_id"},
	}
	for outcome, fragments := range cases {
		query, _, err := countQuery(Filter{Outcome: outcome}).ToSql()
		require.NoError(t, err)
		assert.Contains(t, query, "LEFT JOIN (SELECT substr(src.v", outcome)
		for _, fragment := range fragments {
			assert.Contains(t, query, fragment, outcome)
		}
		assert.NotContains(t, query, "AS outcome", "a count names no outcome")
	}
	plain, _, err := countQuery(Filter{UserID: "u1"}).ToSql()
	require.NoError(t, err)
	assert.NotContains(t, plain, "cited.event_id", "an unfiltered count joins nothing")
	queue, _, err := countQuery(Filter{PromotableOnly: true}).ToSql()
	require.NoError(t, err)
	assert.Contains(t, queue, ") IS NOT NULL", "the review queue counts satisfied records")
}

// TestListOfSatisfiedJoinsTheCitedSet: a page of satisfied records reads the
// records an artifact names, and every other page asks the rule per record so
// it can stop at a page of matches (#2231).
func TestListOfSatisfiedJoinsTheCitedSet(t *testing.T) {
	satisfied, _, err := listQuery(Filter{Outcome: OutcomeSatisfied}).ToSql()
	require.NoError(t, err)
	assert.Contains(t, satisfied, "JOIN (SELECT substr(src.v")
	assert.Contains(t, satisfied, "r.success = $")
	ran, _, err := listQuery(Filter{Outcome: OutcomeRan}).ToSql()
	require.NoError(t, err)
	assert.NotContains(t, ran, "cited.event_id")
}

// TestCatalogReadFailures: each step of the read's transaction that fails is
// reported.
func TestCatalogReadFailures(t *testing.T) {
	boom := errors.New("db down")
	steps := map[string]func(sqlmock.Sqlmock){
		"begin": func(m sqlmock.Sqlmock) { m.ExpectBegin().WillReturnError(boom) },
		"settings": func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectExec("SET LOCAL jit").WillReturnError(boom)
			m.ExpectRollback()
		},
		"count": func(m sqlmock.Sqlmock) {
			expectJITOff(m)
			m.ExpectQuery("COUNT").WillReturnError(boom)
			m.ExpectRollback()
		},
		"commit": func(m sqlmock.Sqlmock) {
			expectJITOff(m)
			m.ExpectQuery("COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			m.ExpectCommit().WillReturnError(boom)
		},
	}
	for name, expect := range steps {
		t.Run(name, func(t *testing.T) {
			store, mock := newMock(t)
			expect(mock)
			_, err := store.Count(context.Background(), Filter{UserID: "u1"})
			require.ErrorIs(t, err, boom)
		})
	}
	t.Run("scan", func(t *testing.T) {
		store, mock := newMock(t)
		expectJITOff(mock)
		mock.ExpectQuery("FROM call_records").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("x"))
		mock.ExpectRollback()
		_, err := store.List(context.Background(), Filter{})
		require.Error(t, err)
	})
}
