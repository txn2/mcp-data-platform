package apichange

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// warned records what a Warnings sent.
type warned struct {
	mu   sync.Mutex
	list []Warning
}

func (w *warned) Warned(_ context.Context, wr Warning) {
	w.mu.Lock()
	w.list = append(w.list, wr)
	w.mu.Unlock()
}

// settle waits for the recorder's writes in flight.
func settle(w *Warnings) {
	for range maxWrites {
		w.slots <- struct{}{}
	}
	for range maxWrites {
		<-w.slots
	}
}

func TestWarnings_AHeaderWarningIsRecordedAndSentOnce(t *testing.T) {
	db, mock := mockDB(t)
	sentTo := &warned{}
	w := NewWarnings(db, sentTo)

	mock.ExpectExec(regexp.QuoteMeta(successSQL)).WithArgs("vendor", "listPets").WillReturnResult(sqlmock.NewResult(0, 1))
	w.Observe(t.Context(), "vendor", "listPets", http.StatusOK, http.Header{})
	settle(w)

	mock.MatchExpectationsInOrder(false)
	mock.ExpectExec(regexp.QuoteMeta(successSQL)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(warnSQL)).WithArgs("vendor", "getPet", WarningSunset, "Wed, 01 Jan 2027 00:00:00 GMT").
		WillReturnRows(sqlmock.NewRows([]string{"created"}).AddRow(true))
	mock.ExpectExec(regexp.QuoteMeta(notifiedSQL)).WithArgs("vendor", "getPet", WarningSunset).WillReturnResult(sqlmock.NewResult(0, 1))
	w.Observe(t.Context(), "vendor", "getPet", http.StatusOK, http.Header{"Sunset": {"Wed, 01 Jan 2027 00:00:00 GMT"}})
	settle(w)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Len(t, sentTo.list, 1)
	assert.Equal(t, Warning{
		Connection: "vendor", Operation: "getPet", Kind: WarningSunset,
		Detail: "Wed, 01 Jan 2027 00:00:00 GMT", FirstSeenAt: sentTo.list[0].FirstSeenAt,
		LastSeenAt: sentTo.list[0].LastSeenAt, SeenCount: 1,
	}, sentTo.list[0])

	// Within the throttle window the same answer writes nothing.
	w.Observe(t.Context(), "vendor", "getPet", http.StatusOK, http.Header{"Sunset": {"x"}})
	settle(w)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWarnings_AnUpdateOfAKnownWarningDoesNotNotify(t *testing.T) {
	db, mock := mockDB(t)
	sentTo := &warned{}
	w := NewWarnings(db, sentTo)
	mock.ExpectQuery(regexp.QuoteMeta(warnSQL)).WillReturnRows(sqlmock.NewRows([]string{"created"}).AddRow(false))
	w.Observe(t.Context(), "vendor", "getPet", http.StatusBadRequest, http.Header{"Deprecation": {"@1767225600"}})
	settle(w)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Empty(t, sentTo.list, "another replica, or an earlier answer, made the row")
}

// #2486: a 404 or 410 is a removal only for an operation that answered 2xx
// before, which the statement itself checks.
func TestWarnings_ARemoval(t *testing.T) {
	db, mock := mockDB(t)
	sentTo := &warned{}
	w := NewWarnings(db, sentTo)
	mock.ExpectQuery(regexp.QuoteMeta(removalSQL)).WithArgs("vendor", "deletePet", "answered 410").
		WillReturnRows(sqlmock.NewRows([]string{"created"}).AddRow(true))
	mock.ExpectExec(regexp.QuoteMeta(notifiedSQL)).WithArgs("vendor", "deletePet", WarningRemoved).WillReturnResult(sqlmock.NewResult(0, 1))
	w.Observe(t.Context(), "vendor", "deletePet", http.StatusGone, http.Header{})
	settle(w)
	require.Len(t, sentTo.list, 1)
	assert.Equal(t, WarningRemoved, sentTo.list[0].Kind)

	mock.ExpectQuery(regexp.QuoteMeta(removalSQL)).WillReturnRows(sqlmock.NewRows([]string{"created"}))
	w.Observe(t.Context(), "vendor", "neverWorked", http.StatusNotFound, http.Header{})
	settle(w)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Len(t, sentTo.list, 1, "an operation that never answered 2xx is a bad request, not a removal")
}

func TestWarnings_FailuresAreLoggedNotRaised(t *testing.T) {
	db, mock := mockDB(t)
	w := NewWarnings(db, nil)
	mock.ExpectExec(regexp.QuoteMeta(successSQL)).WillReturnError(errDown)
	mock.ExpectQuery(regexp.QuoteMeta(warnSQL)).WillReturnError(errDown)
	mock.MatchExpectationsInOrder(false)
	w.Observe(t.Context(), "vendor", "op", http.StatusOK, http.Header{"Deprecation": {"true"}})
	settle(w)
	require.NoError(t, mock.ExpectationsWereMet())

	mock.ExpectQuery(regexp.QuoteMeta(warnSQL)).WillReturnRows(sqlmock.NewRows([]string{"created"}).AddRow(true))
	mock.ExpectExec(regexp.QuoteMeta(notifiedSQL)).WillReturnError(errDown)
	w.Observe(t.Context(), "vendor", "op2", http.StatusTeapot, http.Header{"Deprecation": {"true"}})
	settle(w)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWarnings_ABurstPastTheSlotsIsPassedOver(t *testing.T) {
	db, _ := mockDB(t)
	w := NewWarnings(db, nil)
	for range maxWrites {
		w.slots <- struct{}{}
	}
	w.Observe(t.Context(), "vendor", "op", http.StatusOK, http.Header{})
	for range maxWrites {
		<-w.slots
	}
}

func TestWarnings_ThrottleIsPerKey(t *testing.T) {
	now := time.Now()
	w := &Warnings{last: map[string]time.Time{}, now: func() time.Time { return now }}
	assert.True(t, w.due("a", time.Minute))
	assert.False(t, w.due("a", time.Minute))
	assert.True(t, w.due("b", time.Minute))
	now = now.Add(time.Minute)
	assert.True(t, w.due("a", time.Minute))
}

// The throttle map is swept of expired keys once it reaches its bound, so a
// replica's per-run keys do not accumulate.
func TestWarnings_TheThrottleMapIsBounded(t *testing.T) {
	now := time.Now()
	w := &Warnings{last: map[string]time.Time{}, now: func() time.Time { return now }}
	for i := range maxThrottleKeys {
		w.last[fmt.Sprint(i)] = now.Add(-2 * successEvery)
	}
	w.last["fresh"] = now
	assert.True(t, w.due("new", time.Minute))
	assert.Len(t, w.last, 2, "the expired keys were swept, the fresh one kept")
}

func TestCleanDetail(t *testing.T) {
	assert.Len(t, cleanDetail(string(make([]byte, 0))+stringOf(300)), maxDetail)
	assert.Equal(t, "ab", cleanDetail("a\u200bb"))
}

func stringOf(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}

var warningCols = []string{"connection", "operation", "kind", "detail", "first_seen_at", "last_seen_at", "seen_count", "notified_at"}

func TestListWarnings(t *testing.T) {
	db, mock := mockDB(t)
	now := time.Now()
	mock.ExpectQuery("FROM api_operation_warnings").WithArgs("vendor").WillReturnRows(sqlmock.NewRows(warningCols).
		AddRow("vendor", "getPet", "sunset", "x", now, now, 3, now).
		AddRow("vendor", "op", "removed", "", now, now, 1, nil))
	list, err := ListWarnings(t.Context(), db, "vendor")
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.NotNil(t, list[0].NotifiedAt)
	assert.Nil(t, list[1].NotifiedAt)
	mock.ExpectQuery("FROM api_operation_warnings").WillReturnRows(sqlmock.NewRows(warningCols))
	list, err = ListWarnings(t.Context(), db, "none")
	require.NoError(t, err)
	assert.Equal(t, []Warning{}, list)
	mock.ExpectQuery("FROM api_operation_warnings").WillReturnError(errDown)
	_, err = ListWarnings(t.Context(), db, "vendor")
	require.ErrorIs(t, err, errDown)
}

// innerLog records what the decorator passes on.
type innerLog struct{ events []audit.Event }

func (l *innerLog) Log(_ context.Context, e audit.Event) error {
	l.events = append(l.events, e)
	return nil
}

func (*innerLog) Query(context.Context, audit.QueryFilter) ([]audit.Event, error) { return nil, nil }
func (*innerLog) Close() error                                                    { return nil }

// #2485: a script run's API call records the operation its script uses, from
// the run its session names; nothing of the call's arguments is kept.
func TestCallers_RecordsAScriptRunsOperation(t *testing.T) {
	db, mock := mockDB(t)
	inner := &innerLog{}
	keyer := func(_ context.Context, _, method, path, opID string) string {
		if opID != "" {
			return opID
		}
		return method + " " + path
	}
	c := NewCallers(inner, db, keyer)
	mock.ExpectExec(regexp.QuoteMeta(callerSQL)).WithArgs("vendor", "listPets", "dpx_run1").WillReturnResult(sqlmock.NewResult(0, 1))
	event := audit.Event{
		ToolName: "api_invoke_endpoint", UserID: "script:nightly", Connection: "vendor", SessionID: "dpx_run1",
		Parameters: map[string]any{"operation_id": "listPets", "query_params": map[string]any{"token": "sekret"}},
	}
	require.NoError(t, c.Log(t.Context(), event))
	settle(c.w)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Len(t, inner.events, 1)

	// The same run calling it again within the window writes nothing; a
	// person's call, another tool, and a call with no session write nothing.
	require.NoError(t, c.Log(t.Context(), event))
	for _, e := range []audit.Event{
		{ToolName: "api_invoke_endpoint", UserID: "jane@example.com", Connection: "vendor", SessionID: "s"},
		{ToolName: "trino_query", UserID: "script:nightly", Connection: "vendor", SessionID: "s"},
		{ToolName: "api_export", UserID: "script:nightly", Connection: "vendor"},
	} {
		require.NoError(t, c.Log(t.Context(), e))
	}
	settle(c.w)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Len(t, inner.events, 5, "every event reaches the audit store")

	none := NewCallers(inner, db, func(context.Context, string, string, string, string) string { return "" })
	require.NoError(t, none.Log(t.Context(), event), "an operation the catalog does not declare is not recorded")
	mock.ExpectExec(regexp.QuoteMeta(callerSQL)).WillReturnError(errDown)
	failing := NewCallers(inner, db, keyer)
	require.NoError(t, failing.Log(t.Context(), event), "a failed record never fails the audit write")
	settle(failing.w)

	_, err := c.Query(t.Context(), audit.QueryFilter{})
	require.NoError(t, err)
	require.NoError(t, c.Close())
}

type keyedToolkit struct {
	registry.Toolkit
	op string
}

func (k keyedToolkit) OperationKey(context.Context, string, string, string, string) string {
	return k.op
}

func TestOperationKeyOf(t *testing.T) {
	assert.Nil(t, OperationKeyOf(nil))
	key := OperationKeyOf(func() []registry.Toolkit {
		return []registry.Toolkit{nil, keyedToolkit{op: ""}, keyedToolkit{op: "listPets"}}
	})
	assert.Equal(t, "listPets", key(t.Context(), "vendor", "GET", "/pets", ""))
	none := OperationKeyOf(func() []registry.Toolkit { return nil })
	assert.Empty(t, none(t.Context(), "vendor", "GET", "/pets", ""))
}

func TestPruneCallers(t *testing.T) {
	db, mock := mockDB(t)
	mock.ExpectExec("DELETE FROM api_operation_callers").WillReturnError(errDown)
	require.ErrorIs(t, PruneCallers(t.Context(), db), errDown)
}
