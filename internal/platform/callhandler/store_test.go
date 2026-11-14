package callhandler

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var at = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

var cols = []string{
	"id", "script_id", "kind", "connection", "operations", "personas", "users", "conditions", "args",
	"timeout_ms", "on_failure", "items_path", "writes", "protected", "ord", "enabled", "created_by", "updated_by", "created_at", "updated_at",
}

func row(id string) []driver.Value {
	return []driver.Value{
		id, "s1", KindPolicy, "billing-api", `{addSubscribers}`, `{marketing}`, `{}`, []byte(`[{"in":"path","name":"id","equals":"7"}]`),
		[]byte(`{"limit":3}`), 2000, FailHold, "subscribers", []byte(`{"x":true}`), `{}`, 0, true, "admin@example.com", "admin@example.com", at, at,
	}
}

func mockStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db), mock
}

func TestNewStore(t *testing.T) { assert.Nil(t, NewStore(nil)) }

// TestStoreWritesAndReads a handler, by id, by script, all and enabled.
func TestStoreWritesAndReads(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()
	h := handler(KindPolicy)
	h.ScriptID, h.CreatedBy = "s1", "admin@example.com"
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO call_handlers")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow("h1", at, at))
	require.NoError(t, s.Insert(ctx, h))
	assert.Equal(t, "h1", h.ID)

	mock.ExpectQuery(regexp.QuoteMeta("UPDATE call_handlers SET connection")).WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(at))
	require.NoError(t, s.Update(ctx, h))
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE call_handlers SET connection")).WillReturnRows(sqlmock.NewRows([]string{"updated_at"}))
	require.ErrorIs(t, s.Update(ctx, h), ErrNotFound)

	mock.ExpectQuery(regexp.QuoteMeta("WHERE id = $1")).WithArgs("h1").WillReturnRows(sqlmock.NewRows(cols).AddRow(row("h1")...))
	got, err := s.Get(ctx, "h1")
	require.NoError(t, err)
	assert.Equal(t, []string{"addSubscribers"}, got.Operations)
	assert.Equal(t, []Condition{{In: InPath, Name: "id", Equals: "7"}}, got.Conditions)
	assert.Equal(t, map[string]any{"limit": float64(3)}, got.Args)
	assert.Equal(t, map[string]bool{"x": true}, got.Writes)

	mock.ExpectQuery(regexp.QuoteMeta("WHERE id = $1")).WillReturnRows(sqlmock.NewRows(cols))
	_, err = s.Get(ctx, "none")
	require.ErrorIs(t, err, ErrNotFound)

	for _, read := range []func() ([]Handler, error){
		func() ([]Handler, error) { return s.ByScript(ctx, "s1") },
		func() ([]Handler, error) { return s.All(ctx) },
		func() ([]Handler, error) { return s.Enabled(ctx) },
	} {
		mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(cols).AddRow(row("h1")...).AddRow(row("h2")...))
		list, err := read()
		require.NoError(t, err)
		assert.Len(t, list, 2)
	}
	mock.ExpectQuery("SELECT").WillReturnError(errors.New("down"))
	_, err = s.All(ctx)
	require.Error(t, err)
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("x"))
	_, err = s.All(ctx)
	require.Error(t, err, "a row that does not scan")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestStoreStateAndCounts turns handlers on, off and away, and counts.
func TestStoreStateAndCounts(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE call_handlers SET enabled")).WithArgs("h1", false, "a").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.SetEnabled(ctx, "h1", false, "a"))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE call_handlers SET enabled")).WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.SetEnabled(ctx, "h9", true, "a"), ErrNotFound)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE call_handlers SET enabled")).WillReturnError(errors.New("down"))
	require.Error(t, s.SetEnabled(ctx, "h1", true, "a"))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM call_handlers")).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Delete(ctx, "h1"))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM call_handlers")).WillReturnError(errors.New("down"))
	require.Error(t, s.Delete(ctx, "h1"))

	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO call_handler_counts")).WithArgs("h1", "k", "c1", 2).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COALESCE(SUM(amount), 0)")).WillReturnRows(sqlmock.NewRows([]string{"sum"}).AddRow(5))
	mock.ExpectCommit()
	n, err := s.Count(ctx, Tally{HandlerID: "h1", Key: "k", CallID: "c1", Amount: 2, Window: time.Hour})
	require.NoError(t, err)
	assert.Equal(t, 5, n)

	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COALESCE(SUM(amount), 0)")).WillReturnRows(sqlmock.NewRows([]string{"sum"}).AddRow(3))
	mock.ExpectCommit()
	n, err = s.Count(ctx, Tally{HandlerID: "h1", Key: "k", CallID: "c1", Amount: 0, Window: time.Hour})
	require.NoError(t, err)
	assert.Equal(t, 3, n, "a zero amount reads the count and adds nothing")

	mock.ExpectBegin().WillReturnError(errors.New("down"))
	_, err = s.Count(ctx, Tally{HandlerID: "h1", Key: "k", CallID: "c1", Amount: 1, Window: time.Hour})
	require.Error(t, err)

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM call_handler_counts WHERE call_id")).WithArgs("c1").WillReturnResult(sqlmock.NewResult(0, 2))
	require.NoError(t, s.Release(ctx, "c1"))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM call_handler_counts WHERE call_id")).WillReturnError(errors.New("down"))
	require.Error(t, s.Release(ctx, "c1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestStoreFailuresStatsOrderAndSweep are a handler's failure record, a
// connection's counts, the order transforms run in and the sweep.
func TestStoreFailuresStatsOrderAndSweep(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO call_handler_failures")).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.RecordFailure(ctx, Failure{HandlerID: "h1", Call: map[string]any{"tool": "t"}, Error: "boom"}))
	mock.ExpectQuery(regexp.QuoteMeta("FROM call_handler_failures")).WillReturnRows(sqlmock.NewRows(
		[]string{"id", "handler_id", "version", "user_email", "call", "prints", "error", "outcome", "duration_ms", "at"}).
		AddRow("f1", "h1", 2, "jo@example.com", []byte(`{"tool":"t"}`), "printed", "boom", FailHold, 12, at))
	list, err := s.Failures(ctx, "h1", 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, map[string]any{"tool": "t"}, list[0].Call)

	mock.ExpectQuery(regexp.QuoteMeta("FROM audit_logs a, jsonb_array_elements(a.handlers) h")).WithArgs("billing-api").
		WillReturnRows(sqlmock.NewRows([]string{"id", "m", "d", "h", "e", "q", "p50", "p99"}).AddRow("h1", 10, 1, 2, 3, 0, 4.0, 9.0))
	stats, err := s.Stats(ctx, "billing-api")
	require.NoError(t, err)
	assert.Equal(t, Stat{Matched: 10, Denied: 1, Held: 2, Errors: 3, P50MS: 4, P99MS: 9}, stats["h1"])

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE call_handlers SET ord")).WithArgs("h2", 1, "a").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE call_handlers SET ord")).WithArgs("h1", 2, "a").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, s.SetOrder(ctx, []string{"h2", "h1"}, "a"))

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM call_handler_counts WHERE at")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM call_handler_failures WHERE at")).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Sweep(ctx, at))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM call_handler_counts WHERE at")).WillReturnError(errors.New("down"))
	require.Error(t, s.Sweep(ctx, at))
	require.NoError(t, mock.ExpectationsWereMet())
}

// reader is an index's store in memory. An index reads it in the
// background, so what it holds is changed through set.
type reader struct {
	mu    sync.Mutex
	list  []Handler
	err   error
	reads atomic.Int32
}

func (r *reader) Enabled(context.Context) ([]Handler, error) {
	r.reads.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.list, r.err
}

func (r *reader) set(list []Handler, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list, r.err = list, err
}

// TestIndex answers from memory, reads a heard change before the next call,
// and keeps the last read when a read fails.
func TestIndex(t *testing.T) {
	ctx := context.Background()
	p := handler(KindPolicy)
	p.ID = "p1"
	w := handler(KindWatch)
	w.ID, w.Connection = "w1", "crm"
	r := &reader{list: []Handler{*p, *w}}
	x := &Index{Reader: r}
	assert.Nil(t, x.Match(call(), KindPolicy), "nothing loaded matches nothing")
	assert.False(t, x.Any("billing-api"))
	require.NoError(t, x.Load(ctx))
	assert.True(t, x.Any("billing-api"))
	assert.False(t, x.Any("other"))
	got := x.Match(call(), KindPolicy)
	require.Len(t, got, 1)
	assert.Equal(t, "p1", got[0].ID)
	assert.Empty(t, x.Match(call(), KindWatch), "a watch on another connection")

	r.set(nil, nil)
	x.Notify()
	assert.Empty(t, x.Match(call(), KindPolicy), "a heard change is read before the next call")

	r.set(nil, errors.New("down"))
	x.Notify()
	assert.Empty(t, x.Match(call(), KindPolicy), "a failed read keeps the last one")

	failing := &Index{Reader: &reader{err: errors.New("down")}}
	require.Error(t, failing.Load(ctx))
}

// TestIndexRefreshesAnAgedSnapshotInTheBackground: a snapshot past MaxAge
// answers while a fresh one is read.
func TestIndexRefreshesAnAgedSnapshotInTheBackground(t *testing.T) {
	r := &reader{list: []Handler{*handler(KindPolicy)}}
	now := at
	x := &Index{Reader: r, MaxAge: time.Second, now: func() time.Time { return now }}
	require.NoError(t, x.Load(context.Background()))
	now = now.Add(2 * time.Second)
	assert.Len(t, x.Match(call(), KindPolicy), 1, "the aged snapshot answers")
	require.Eventually(t, func() bool { return r.reads.Load() >= 2 }, 5*time.Second, 10*time.Millisecond)
}

// TestStoreFailuresAreReported: every read and write names what failed when
// the database does, and a value that cannot be encoded is refused before
// it is sent.
func TestStoreFailuresAreReported(t *testing.T) {
	ctx := context.Background()
	s, mock := mockStore(t)
	down := errors.New("down")
	h := handler(KindPolicy)

	mock.ExpectQuery("INSERT INTO call_handlers").WillReturnError(down)
	require.ErrorContains(t, s.Insert(ctx, h), "inserting call handler")
	mock.ExpectQuery("UPDATE call_handlers SET connection").WillReturnError(down)
	require.ErrorContains(t, s.Update(ctx, h), "updating call handler")
	mock.ExpectQuery("WHERE id = ").WillReturnError(down)
	_, err := s.Get(ctx, "h1")
	require.ErrorContains(t, err, "reading call handler")

	bad := handler(KindPolicy)
	bad.Args = map[string]any{"x": make(chan int)}
	require.ErrorContains(t, s.Insert(ctx, bad), "encoding args")
	require.ErrorContains(t, s.Update(ctx, bad), "encoding args")
	_, err = encodeJSON(&Handler{Conditions: []Condition{{Equals: string([]byte{0xff})}}})
	require.NoError(t, err, "a string is always encodable")

	mock.ExpectBegin().WillReturnError(down)
	_, err = s.Count(ctx, Tally{HandlerID: "h1", Key: "k", CallID: "c1", Amount: 1, Window: time.Hour})
	require.ErrorContains(t, err, "counting")
	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").WillReturnError(down)
	mock.ExpectRollback()
	_, err = s.Count(ctx, Tally{HandlerID: "h1", Key: "k", CallID: "c1", Amount: 1, Window: time.Hour})
	require.ErrorContains(t, err, "counting")
	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO call_handler_counts").WillReturnError(down)
	mock.ExpectRollback()
	_, err = s.Count(ctx, Tally{HandlerID: "h1", Key: "k", CallID: "c1", Amount: 1, Window: time.Hour})
	require.ErrorContains(t, err, "counting")
	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT COALESCE").WillReturnError(down)
	mock.ExpectRollback()
	_, err = s.Count(ctx, Tally{HandlerID: "h1", Key: "k", CallID: "c1", Amount: 0, Window: time.Hour})
	require.ErrorContains(t, err, "counting")
	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(1))
	mock.ExpectCommit().WillReturnError(down)
	_, err = s.Count(ctx, Tally{HandlerID: "h1", Key: "k", CallID: "c1", Amount: 0, Window: time.Hour})
	require.ErrorContains(t, err, "counting")

	mock.ExpectExec("DELETE FROM call_handler_counts WHERE call_id").WillReturnError(down)
	require.ErrorContains(t, s.Release(ctx, "c1"), "releasing counts")
	mock.ExpectExec("INSERT INTO call_handler_failures").WillReturnError(down)
	require.ErrorContains(t, s.RecordFailure(ctx, Failure{HandlerID: "h1"}), "recording")
	require.ErrorContains(t, s.RecordFailure(ctx, Failure{Call: map[string]any{"x": make(chan int)}}), "encoding the failed call")
	mock.ExpectQuery("FROM call_handler_failures").WillReturnError(down)
	_, err = s.Failures(ctx, "h1", 5)
	require.ErrorContains(t, err, "reading handler failures")
	mock.ExpectQuery("FROM call_handler_failures").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("x"))
	_, err = s.Failures(ctx, "h1", 5)
	require.ErrorContains(t, err, "reading handler failures")

	mock.ExpectExec("DELETE FROM call_handler_counts WHERE at").WillReturnError(down)
	require.ErrorContains(t, s.Sweep(ctx, time.Now()), "sweeping handler counts")
	mock.ExpectExec("DELETE FROM call_handler_counts WHERE at").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM call_handler_failures WHERE at").WillReturnError(down)
	require.ErrorContains(t, s.Sweep(ctx, time.Now()), "sweeping handler failures")

	mock.ExpectQuery("jsonb_array_elements").WillReturnError(down)
	_, err = s.Stats(ctx, "c")
	require.ErrorContains(t, err, "reading handler counts")
	mock.ExpectQuery("jsonb_array_elements").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("x"))
	_, err = s.Stats(ctx, "c")
	require.ErrorContains(t, err, "reading handler counts")

	mock.ExpectBegin().WillReturnError(down)
	require.ErrorContains(t, s.SetOrder(ctx, []string{"a"}, "x"), "ordering transforms")
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE call_handlers SET ord").WillReturnError(down)
	mock.ExpectRollback()
	require.ErrorContains(t, s.SetOrder(ctx, []string{"a"}, "x"), "ordering transforms")
	mock.ExpectBegin()
	mock.ExpectCommit().WillReturnError(down)
	require.ErrorContains(t, s.SetOrder(ctx, nil, "x"), "ordering transforms")
	require.NoError(t, mock.ExpectationsWereMet())
}
