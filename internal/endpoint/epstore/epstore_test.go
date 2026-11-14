package epstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
)

var rowTime = time.Unix(1700000000, 0).UTC()

var bindingColumns = []string{
	"id", "script_id", "name", "methods", "path", "auth_mode", "callers", "bot_check", "checked_methods", "timeout_seconds",
	"max_memory_bytes", "max_body_bytes", "allowed_origins", "rate_limit", "caller_rate_limit", "enabled",
	"created_by", "updated_by", "created_at", "updated_at", "response_mode", "page_cdns",
	"page_frame_origins", "page_connect_origins", "page_form_origins", "ip_rate_limit",
}

func bindingRow() []driver.Value {
	return []driver.Value{
		"e1", "s1", "orders", "{GET,POST}", "/{id}", epbinding.AuthAuthenticated, "{persona:analyst}", "", nil, 10,
		int64(64 << 20), 1 << 20, "{}", 600, 120, true, "a@example.com", "a@example.com", rowTime, rowTime, "api", "{}",
		"{https://js.stripe.com}", "{}", nil, 30,
	}
}

func newMock(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	return New(db), mock
}

func sample() *epbinding.Binding {
	return &epbinding.Binding{
		ScriptID: "s1", Name: "orders", Methods: []string{"GET"}, AuthMode: epbinding.AuthAuthenticated,
		Callers: []string{"*"}, TimeoutSeconds: 10, MaxMemoryBytes: 64 << 20, MaxBodyBytes: 1 << 20, RateLimit: 600,
		CallerRateLimit: 120, Enabled: true, UpdatedBy: "a@example.com",
	}
}

func TestSet_StoresAndReadsBackTheIdentity(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(`^\s*INSERT INTO script_endpoints [^;]*RETURNING id, created_by, created_at, updated_at$`).
		WithArgs("s1", "orders", sqlmock.AnyArg(), "", epbinding.AuthAuthenticated, sqlmock.AnyArg(), "", sqlmock.AnyArg(),
			10, int64(64<<20), 1<<20, sqlmock.AnyArg(), 600, 120, true, "a@example.com", "", sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_by", "created_at", "updated_at"}).
			AddRow("e1", "first@example.com", rowTime, rowTime))
	b := sample()
	require.NoError(t, s.Set(context.Background(), b))
	assert.Equal(t, "e1", b.ID)
	assert.Equal(t, "first@example.com", b.CreatedBy)
}

// TestSet_ReplacesTheBindingWithItsID pins the replace half of the write
// (#2441): a binding with an id updates that row, held to its own script, so
// an id read from another script's binding changes nothing and reads as
// ErrNotFound; the creator and creation stamp the row had are kept.
func TestSet_ReplacesTheBindingWithItsID(t *testing.T) {
	update := regexp.QuoteMeta("WHERE id = $23 AND script_id = $1")
	created := rowTime.Add(-time.Hour)

	s, mock := newMock(t)
	mock.ExpectQuery(update).
		WithArgs("s1", "orders", sqlmock.AnyArg(), "", epbinding.AuthAuthenticated, sqlmock.AnyArg(), "", sqlmock.AnyArg(),
			10, int64(64<<20), 1<<20, sqlmock.AnyArg(), 600, 120, true, "a@example.com", "", sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), 0, "e1").
		WillReturnRows(sqlmock.NewRows([]string{"created_by", "created_at", "updated_at"}).
			AddRow("first@example.com", created, rowTime))
	b := sample()
	b.ID = "e1"
	require.NoError(t, s.Set(context.Background(), b))
	assert.Equal(t, "e1", b.ID, "the binding keeps its id")
	assert.Equal(t, "first@example.com", b.CreatedBy, "the creator of the binding replaced is kept")
	assert.Equal(t, created, b.CreatedAt)

	mock.ExpectQuery(update).WillReturnRows(sqlmock.NewRows([]string{"created_by", "created_at", "updated_at"}))
	other := sample()
	other.ID = "e-of-another-script"
	require.ErrorIs(t, s.Set(context.Background(), other), epbinding.ErrNotFound)

	mock.ExpectQuery(update).WillReturnError(&pq.Error{Code: "23505", Constraint: nameConstraint})
	renamed := sample()
	renamed.ID = "e1"
	require.ErrorIs(t, s.Set(context.Background(), renamed), epbinding.ErrNameTaken)
}

func TestSet_ANameAnotherScriptHasIsErrNameTaken(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_endpoints")).
		WillReturnError(&pq.Error{Code: "23505", Constraint: nameConstraint})
	require.ErrorIs(t, s.Set(context.Background(), sample()), epbinding.ErrNameTaken)

	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_endpoints")).
		WillReturnError(&pq.Error{Code: "23503", Constraint: checkConstraint})
	checked := sample()
	checked.AuthMode, checked.Callers, checked.BotCheck = epbinding.AuthBotCheck, nil, "gone"
	err := s.Set(context.Background(), checked)
	require.ErrorIs(t, err, epbinding.ErrNoSuchCheck)
	require.ErrorContains(t, err, `"gone"`)

	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO script_endpoints")).WillReturnError(errors.New("boom"))
	err = s.Set(context.Background(), sample())
	require.ErrorContains(t, err, "set endpoint binding")
	assert.NotErrorIs(t, err, epbinding.ErrNameTaken)
}

func TestGetByIDAndGetByName(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("WHERE id = $1")).WithArgs("e1").
		WillReturnRows(sqlmock.NewRows(bindingColumns).AddRow(bindingRow()...))
	b, err := s.GetByID(context.Background(), "e1")
	require.NoError(t, err)
	assert.Equal(t, "e1", b.ID)
	assert.Equal(t, []string{"GET", "POST"}, b.Methods)
	assert.Equal(t, []string{}, b.AllowedOrigins)
	assert.Equal(t, []string{"https://js.stripe.com"}, b.PageFrameOrigins, "#2554's lists are read back")
	assert.Equal(t, []string{}, b.PageFormOrigins, "a NULL list reads as empty")
	assert.Equal(t, 30, b.IPRateLimit, "#2556's limit is read back")

	mock.ExpectQuery(regexp.QuoteMeta("WHERE id = $1")).WithArgs("nope").WillReturnError(sql.ErrNoRows)
	_, err = s.GetByID(context.Background(), "nope")
	require.ErrorIs(t, err, epbinding.ErrNotFound)

	mock.ExpectQuery(regexp.QuoteMeta("WHERE name = $1")).WithArgs("nope").WillReturnError(sql.ErrNoRows)
	_, err = s.GetByName(context.Background(), "nope")
	require.ErrorIs(t, err, epbinding.ErrNotFound)

	mock.ExpectQuery(regexp.QuoteMeta("WHERE name = $1")).WithArgs("x").WillReturnError(errors.New("boom"))
	_, err = s.GetByName(context.Background(), "x")
	require.ErrorContains(t, err, "get endpoint binding")
}

func TestList_PagesFiltersAndCounts(t *testing.T) {
	s, mock := newMock(t)
	withCount := append(append([]string{}, bindingColumns...), "count")
	mock.ExpectQuery(regexp.QuoteMeta("ORDER BY name LIMIT $1 OFFSET $5")).
		WithArgs(MaxListLimit, nil, "", sql.NullBool{}, 0).
		WillReturnRows(sqlmock.NewRows(withCount).AddRow(append(bindingRow(), int64(226))...))
	all, total, err := s.List(context.Background(), epbinding.Filter{Limit: 5000})
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, 226, total, "the total counts every match, not the page")

	on := true
	mock.ExpectQuery(regexp.QuoteMeta("ORDER BY name LIMIT $1 OFFSET $5")).
		WithArgs(5, sqlmock.AnyArg(), "public", sql.NullBool{Bool: true, Valid: true}, 10).
		WillReturnRows(sqlmock.NewRows(withCount))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM script_endpoints")).
		WithArgs(sqlmock.AnyArg(), "public", sql.NullBool{Bool: true, Valid: true}).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))
	none, total, err := s.List(context.Background(), epbinding.Filter{ScriptIDs: []string{}, AuthMode: "public", Enabled: &on, Limit: 5, Offset: 10})
	require.NoError(t, err)
	assert.Equal(t, []epbinding.Binding{}, none, "an empty listing is [] and never null")
	assert.Equal(t, 7, total, "a page past the last still reports the total")

	mock.ExpectQuery(regexp.QuoteMeta("ORDER BY name")).WillReturnRows(sqlmock.NewRows(withCount))
	_, total, err = s.List(context.Background(), epbinding.Filter{Offset: -3})
	require.NoError(t, err)
	assert.Zero(t, total, "the first page of nothing counts nothing without a second read")

	mock.ExpectQuery(regexp.QuoteMeta("ORDER BY name")).WillReturnRows(sqlmock.NewRows(withCount))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).WillReturnError(errors.New("boom"))
	_, _, err = s.List(context.Background(), epbinding.Filter{Offset: 1})
	require.ErrorContains(t, err, "count endpoint bindings")

	mock.ExpectQuery(regexp.QuoteMeta("ORDER BY name")).WillReturnError(errors.New("boom"))
	_, _, err = s.List(context.Background(), epbinding.Filter{})
	require.ErrorContains(t, err, "list endpoint bindings")

	mock.ExpectQuery(regexp.QuoteMeta("ORDER BY name")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("e1"))
	_, _, err = s.List(context.Background(), epbinding.Filter{})
	require.ErrorContains(t, err, "scan endpoint binding")

	mock.ExpectQuery(regexp.QuoteMeta("ORDER BY name")).
		WillReturnRows(sqlmock.NewRows(withCount).AddRow(append(bindingRow(), int64(1))...).RowError(0, errors.New("boom")))
	_, _, err = s.List(context.Background(), epbinding.Filter{})
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnable(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE script_endpoints SET enabled")).WithArgs("e1", false, "a@example.com").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Enable(context.Background(), "e1", false, "a@example.com"))

	mock.ExpectExec(regexp.QuoteMeta("WHERE id = $1")).WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.Enable(context.Background(), "e2", true, ""), epbinding.ErrNotFound)

	mock.ExpectExec(regexp.QuoteMeta("UPDATE script_endpoints SET enabled")).WillReturnError(errors.New("boom"))
	require.ErrorContains(t, s.Enable(context.Background(), "e2", true, ""), "set endpoint binding enabled")
}

// TestByScript reads every binding a script holds (#2441), by name, and an
// empty list rather than null for a script with none.
func TestByScript(t *testing.T) {
	s, mock := newMock(t)
	second := bindingRow()
	second[0], second[2] = "e2", "zeta"
	mock.ExpectQuery(regexp.QuoteMeta("WHERE script_id = $1 ORDER BY name")).WithArgs("s1").
		WillReturnRows(sqlmock.NewRows(bindingColumns).AddRow(bindingRow()...).AddRow(second...))
	held, err := s.ByScript(context.Background(), "s1")
	require.NoError(t, err)
	require.Len(t, held, 2)
	assert.Equal(t, []string{"e1", "e2"}, []string{held[0].ID, held[1].ID})
	assert.Equal(t, "zeta", held[1].Name)

	mock.ExpectQuery(regexp.QuoteMeta("WHERE script_id = $1")).WithArgs("s2").
		WillReturnRows(sqlmock.NewRows(bindingColumns))
	none, err := s.ByScript(context.Background(), "s2")
	require.NoError(t, err)
	assert.Equal(t, []epbinding.Binding{}, none, "a script with no binding is [] and never null")

	mock.ExpectQuery(regexp.QuoteMeta("WHERE script_id = $1")).WillReturnError(errors.New("boom"))
	_, err = s.ByScript(context.Background(), "s1")
	require.ErrorContains(t, err, "list a script's endpoint bindings")

	mock.ExpectQuery(regexp.QuoteMeta("WHERE script_id = $1")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("e1"))
	_, err = s.ByScript(context.Background(), "s1")
	require.ErrorContains(t, err, "scan endpoint binding")

	mock.ExpectQuery(regexp.QuoteMeta("WHERE script_id = $1")).
		WillReturnRows(sqlmock.NewRows(bindingColumns).AddRow(bindingRow()...).RowError(0, errors.New("boom")))
	_, err = s.ByScript(context.Background(), "s1")
	require.ErrorContains(t, err, "iterate endpoint bindings")
}

// TestDelete removes one binding by its id; an id no binding has is
// ErrNotFound, and a failed statement is not mistaken for one.
func TestDelete(t *testing.T) {
	s, mock := newMock(t)
	del := regexp.QuoteMeta("DELETE FROM script_endpoints WHERE id = $1")
	mock.ExpectExec(del).WithArgs("e1").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Delete(context.Background(), "e1"))

	mock.ExpectExec(del).WithArgs("gone").WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.Delete(context.Background(), "gone"), epbinding.ErrNotFound)

	mock.ExpectExec(del).WillReturnError(errors.New("boom"))
	err := s.Delete(context.Background(), "e1")
	require.ErrorContains(t, err, "delete endpoint binding")
	assert.NotErrorIs(t, err, epbinding.ErrNotFound)
}

func TestFailures(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_endpoint_failures")).
		WithArgs("e1", 3, "POST", 504, "timed out", int64(10000)).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.RecordFailure(context.Background(), epbinding.Failure{
		EndpointID: "e1", Version: 3, Method: "POST", Status: 504, Error: "timed out", DurationMS: 10000,
	}))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_endpoint_failures")).WillReturnError(errors.New("boom"))
	require.ErrorContains(t, s.RecordFailure(context.Background(), epbinding.Failure{}), "record endpoint failure")

	mock.ExpectQuery(regexp.QuoteMeta("FROM script_endpoint_failures")).WithArgs("e1", defaultFailureLimit).
		WillReturnRows(sqlmock.NewRows([]string{"id", "endpoint_id", "version", "method", "status", "error", "duration_ms", "created_at"}).
			AddRow("f1", "e1", 3, "POST", 504, "timed out", 10000, rowTime))
	got, err := s.Failures(context.Background(), "e1", 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 504, got[0].Status)

	mock.ExpectQuery(regexp.QuoteMeta("FROM script_endpoint_failures")).WillReturnError(errors.New("boom"))
	_, err = s.Failures(context.Background(), "e1", 5)
	require.ErrorContains(t, err, "list endpoint failures")

	mock.ExpectQuery(regexp.QuoteMeta("FROM script_endpoint_failures")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("f1"))
	_, err = s.Failures(context.Background(), "e1", 5)
	require.ErrorContains(t, err, "scan endpoint failure")

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM script_endpoint_failures")).WithArgs(rowTime).
		WillReturnResult(sqlmock.NewResult(0, 4))
	n, err := s.SweepFailures(context.Background(), rowTime)
	require.NoError(t, err)
	assert.Equal(t, int64(4), n)
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM script_endpoint_failures")).WillReturnError(errors.New("boom"))
	_, err = s.SweepFailures(context.Background(), rowTime)
	require.ErrorContains(t, err, "sweep endpoint failures")
}

func TestStamp(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_endpoints e JOIN scripts s")).WithArgs("orders").
		WillReturnRows(sqlmock.NewRows([]string{"e", "s", "a", "app", "conn"}).AddRow(rowTime, rowTime.Add(time.Second), 2, "app_1", "shop"))
	st, err := s.Stamp(context.Background(), "orders")
	require.NoError(t, err)
	assert.Equal(t, epbinding.Stamp{
		Binding: rowTime, Script: rowTime.Add(time.Second), ApprovedVersion: 2, Application: "app_1", AppConnection: "shop",
	}, st)

	mock.ExpectQuery(regexp.QuoteMeta("FROM script_endpoints e JOIN scripts s")).WillReturnError(sql.ErrNoRows)
	_, err = s.Stamp(context.Background(), "nope")
	require.ErrorIs(t, err, epbinding.ErrNotFound)
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_endpoints e JOIN scripts s")).WillReturnError(errors.New("boom"))
	_, err = s.Stamp(context.Background(), "x")
	require.ErrorContains(t, err, "read endpoint stamp")
}
