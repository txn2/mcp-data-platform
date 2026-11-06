package approval

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var at = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func mockStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresStore(db), mock
}

var columns = []string{
	"id", "subject_kind", "purpose", "script_id", "name", "version", "source_sha256",
	"requester_email", "requester_persona", "reason", "changes", "status", "decided_by", "decided_at",
	"decision_comment", "self_approved", "created_at",
}

func row(id, status string, decided any) *sqlmock.Rows {
	return sqlmock.NewRows(columns).AddRow(id, SubjectScriptVersion, PurposeFormPublic, "s1", "intake", 2, "abc",
		"jane@example.com", "analyst", "launch", []byte(`{"approved_version":1,"added":3,"removed":1}`), status, "", decided,
		"", false, at)
}

func TestStoreInsert(t *testing.T) {
	s, mock := mockStore(t)
	r := &Request{ID: "apr_1", SubjectKind: SubjectScriptVersion, Purpose: PurposeFormPublic, ScriptID: "s1", Version: 2}
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO approval_requests")).
		WillReturnRows(sqlmock.NewRows([]string{"status", "created_at"}).AddRow(StatusPending, at))
	require.NoError(t, s.Insert(context.Background(), r))
	assert.Equal(t, StatusPending, r.Status)
	assert.Equal(t, at, r.CreatedAt)

	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO approval_requests")).WillReturnError(&pq.Error{Code: uniqueViolation})
	require.ErrorIs(t, s.Insert(context.Background(), r), ErrPending)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO approval_requests")).WillReturnError(errors.New("down"))
	require.ErrorContains(t, s.Insert(context.Background(), r), "storing the request")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreGetAndList(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()
	mock.ExpectQuery(regexp.QuoteMeta("WHERE r.id = $1")).WithArgs("apr_1").WillReturnRows(row("apr_1", StatusApproved, at))
	r, err := s.Get(ctx, "apr_1")
	require.NoError(t, err)
	assert.Equal(t, "intake", r.ScriptName)
	assert.Equal(t, Changes{ApprovedVersion: 1, Added: 3, Removed: 1}, r.Changes)
	require.NotNil(t, r.DecidedAt)

	mock.ExpectQuery(regexp.QuoteMeta("WHERE r.id = $1")).WillReturnError(sql.ErrNoRows)
	_, err = s.Get(ctx, "none")
	require.ErrorIs(t, err, ErrNotFound)

	mock.ExpectQuery(regexp.QuoteMeta("WHERE r.status = $1 AND r.requester_email = $2 AND r.script_id::text = $3")).
		WithArgs(StatusPending, "jane@example.com", "s1").WillReturnRows(row("apr_2", StatusPending, nil))
	rs, err := s.List(ctx, Filter{Status: StatusPending, Requester: "jane@example.com", ScriptID: "s1", Limit: 10})
	require.NoError(t, err)
	require.Len(t, rs, 1)
	assert.Nil(t, rs[0].DecidedAt)

	mock.ExpectQuery(regexp.QuoteMeta("ORDER BY r.created_at DESC, r.id LIMIT 100")).WillReturnRows(sqlmock.NewRows(columns))
	rs, err = s.List(ctx, Filter{Limit: 9999})
	require.NoError(t, err)
	assert.Equal(t, []Request{}, rs, "no request is [] and never null")

	mock.ExpectQuery("SELECT").WillReturnError(errors.New("down"))
	_, err = s.List(ctx, Filter{})
	require.ErrorContains(t, err, "listing requests")
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("x"))
	_, err = s.List(ctx, Filter{})
	require.Error(t, err, "a row that does not scan")
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(columns).AddRow("apr_3", SubjectScriptVersion,
		PurposeFormPublic, "s1", "intake", 2, "abc", "j", "", "", []byte(`{`), StatusPending, "", nil, "", false, at))
	_, err = s.List(ctx, Filter{})
	require.ErrorContains(t, err, "decoding")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreDecide(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE approval_requests")).WithArgs("apr_1", StatusApproved, "admin@example.com", "ok", true, at).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("WHERE r.id = $1")).WillReturnRows(row("apr_1", StatusApproved, at))
	r, err := s.Decide(ctx, "apr_1", Decision{Status: StatusApproved, By: "admin@example.com", Comment: "ok", SelfApproved: true, At: at})
	require.NoError(t, err)
	assert.Equal(t, StatusApproved, r.Status)

	mock.ExpectExec(regexp.QuoteMeta("UPDATE approval_requests")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("WHERE r.id = $1")).WillReturnRows(row("apr_1", StatusApproved, at))
	_, err = s.Decide(ctx, "apr_1", Decision{Status: StatusDenied, By: "a", At: at})
	require.ErrorIs(t, err, ErrDecided)

	mock.ExpectExec(regexp.QuoteMeta("UPDATE approval_requests")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("WHERE r.id = $1")).WillReturnError(sql.ErrNoRows)
	_, err = s.Decide(ctx, "none", Decision{Status: StatusDenied, By: "a", At: at})
	require.ErrorIs(t, err, ErrNotFound)

	mock.ExpectExec(regexp.QuoteMeta("UPDATE approval_requests")).WillReturnError(errors.New("down"))
	_, err = s.Decide(ctx, "apr_1", Decision{Status: StatusDenied, By: "a", At: at})
	require.ErrorContains(t, err, "deciding")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreLatestApproved(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()
	mock.ExpectQuery(regexp.QuoteMeta("r.status = 'approved'")).WithArgs("s1", PurposeFormPublic).
		WillReturnRows(row("apr_1", StatusApproved, at))
	r, err := s.LatestApproved(ctx, "s1", PurposeFormPublic)
	require.NoError(t, err)
	assert.Equal(t, 2, r.Version)
	mock.ExpectQuery(regexp.QuoteMeta("r.status = 'approved'")).WillReturnError(sql.ErrNoRows)
	_, err = s.LatestApproved(ctx, "s1", PurposeFormPublic)
	require.ErrorIs(t, err, ErrNotFound)
	mock.ExpectQuery(regexp.QuoteMeta("r.status = 'approved'")).WillReturnError(errors.New("down"))
	_, err = s.LatestApproved(ctx, "s1", PurposeFormPublic)
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
