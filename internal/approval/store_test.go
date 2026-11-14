package approval

import (
	"context"
	"database/sql"
	"database/sql/driver"
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
	"decision_comment", "self_approved", "created_at", "site_release_id", "target", "site_name", "site_id",
	"decided_roles", "call_tool", "call_connection", "call_operation", "call_arguments", "call_rendering", "call_handlers",
	"approvers", "calls_asked", "calls_approved", "calls_used", "allowance_until",
}

// noCall is the call columns of a request that is not a call.
var noCall = []driver.Value{"{}", "", "", "", nil, "", []byte(`[]`), "{}", 0, 0, 0, nil}

func row(id, status string, decided any) *sqlmock.Rows {
	return sqlmock.NewRows(columns).AddRow(append([]driver.Value{
		id, SubjectScriptVersion, PurposeFormPublic, "s1", "intake", 2, "abc",
		"jane@example.com", "analyst", "launch", []byte(`{"approved_version":1,"added":3,"removed":1}`), status, "", decided,
		"", false, at, "", "", "", "",
	}, noCall...)...)
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
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(columns).AddRow(append([]driver.Value{
		"apr_3", SubjectScriptVersion,
		PurposeFormPublic, "s1", "intake", 2, "abc", "j", "", "", []byte(`{`), StatusPending, "", nil, "", false, at, "", "", "", "",
	}, noCall...)...))
	_, err = s.List(ctx, Filter{})
	require.ErrorContains(t, err, "decoding")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreDecide(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE approval_requests")).WithArgs("apr_1", StatusApproved, "admin@example.com", "ok", true, at, sqlmock.AnyArg(), 0, sqlmock.AnyArg()).
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

func TestStoreReadsASiteRelease(t *testing.T) {
	s, mock := mockStore(t)
	mock.ExpectQuery(regexp.QuoteMeta("LEFT JOIN sites st ON st.id = COALESCE(sr.site_id, r.site_id)")).WithArgs("apr_9").
		WillReturnRows(sqlmock.NewRows(columns).AddRow(append([]driver.Value{
			"apr_9", SubjectSiteRelease, PurposeSitePromote, "", "", 3, "digest",
			"jane@example.com", "", "launch", []byte(`{"approved_version":0}`), StatusPending, "", nil, "", false, at,
			"srel_1", "prod", "marketing", "",
		}, noCall...)...))
	r, err := s.Get(context.Background(), "apr_9")
	require.NoError(t, err)
	assert.Equal(t, SubjectSiteRelease, r.SubjectKind)
	assert.Empty(t, r.ScriptID, "a site release names no script")
	assert.Equal(t, "srel_1", r.SiteReleaseID)
	assert.Equal(t, "prod", r.Target)
	assert.Equal(t, "marketing", r.SiteName)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreInsertCarriesTheRelease(t *testing.T) {
	s, mock := mockStore(t)
	r := &Request{
		ID: "apr_1", SubjectKind: SubjectSiteRelease, Purpose: PurposeSitePromote, Version: 3, SourceSHA256: "digest",
		RequesterEmail: "jane@example.com", SiteReleaseID: "srel_1", Target: "prod",
	}
	mock.ExpectQuery(regexp.QuoteMeta("NULLIF($11, '')")).
		WithArgs("apr_1", SubjectSiteRelease, PurposeSitePromote, "", 3, "digest", "jane@example.com", "", "", sqlmock.AnyArg(), "srel_1", "prod", "",
			"", "", "", sqlmock.AnyArg(), "", sqlmock.AnyArg(), sqlmock.AnyArg(), 0).
		WillReturnRows(sqlmock.NewRows([]string{"status", "created_at"}).AddRow(StatusPending, at))
	require.NoError(t, s.Insert(context.Background(), r))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreReleaseApproved(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()
	expect := func() *sqlmock.ExpectedQuery {
		return mock.ExpectQuery(regexp.QuoteMeta("site_release_id = $1 AND target = $2 AND source_sha256 = $3 AND status = 'approved'")).
			WithArgs("srel_1", "prod", "digest")
	}

	expect().WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	ok, err := s.ReleaseApproved(ctx, "srel_1", "prod", "digest")
	require.NoError(t, err)
	assert.True(t, ok)

	expect().WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	ok, err = s.ReleaseApproved(ctx, "srel_1", "prod", "digest")
	require.NoError(t, err)
	assert.False(t, ok)

	expect().WillReturnError(errors.New("down"))
	ok, err = s.ReleaseApproved(ctx, "srel_1", "prod", "digest")
	require.ErrorContains(t, err, "reading the release's approval")
	assert.False(t, ok)
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

// A request to make a site environment public names the site by id and the
// environment, carries no release, and is read back with the site's name
// (#2604).
func TestStoreSiteEnvironmentRequest(t *testing.T) {
	s, mock := mockStore(t)
	r := &Request{
		ID: "apr_2", SubjectKind: SubjectSiteEnvironment, Purpose: PurposeSitePublic, SourceSHA256: "public:t0",
		RequesterEmail: "jane@example.com", SiteID: "site_1", Target: "prod",
	}
	mock.ExpectQuery(regexp.QuoteMeta("NULLIF($13, '')")).
		WithArgs("apr_2", SubjectSiteEnvironment, PurposeSitePublic, "", 0, "public:t0", "jane@example.com", "", "", sqlmock.AnyArg(), "", "prod", "site_1",
			"", "", "", sqlmock.AnyArg(), "", sqlmock.AnyArg(), sqlmock.AnyArg(), 0).
		WillReturnRows(sqlmock.NewRows([]string{"status", "created_at"}).AddRow(StatusPending, at))
	require.NoError(t, s.Insert(context.Background(), r))

	mock.ExpectQuery(regexp.QuoteMeta("WHERE r.id = $1")).WithArgs("apr_2").
		WillReturnRows(sqlmock.NewRows(columns).AddRow(append([]driver.Value{
			"apr_2", SubjectSiteEnvironment, PurposeSitePublic, "", "", 0, "public:t0",
			"jane@example.com", "", "", []byte(`{"approved_version":0}`), StatusPending, "", nil, "", false, at,
			"", "prod", "docs", "site_1",
		}, noCall...)...))
	got, err := s.Get(context.Background(), "apr_2")
	require.NoError(t, err)
	assert.Equal(t, "site_1", got.SiteID)
	assert.Equal(t, "docs", got.SiteName)
	assert.Empty(t, got.SiteReleaseID)

	mock.ExpectQuery(regexp.QuoteMeta("WHERE r.status = $1 AND r.site_id = $2")).WithArgs(StatusPending, "site_1").
		WillReturnRows(sqlmock.NewRows(columns))
	_, err = s.List(context.Background(), Filter{Status: StatusPending, SiteID: "site_1"})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreEnvironmentApproved(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()
	expect := func() *sqlmock.ExpectedQuery {
		return mock.ExpectQuery(regexp.QuoteMeta("subject_kind = 'site_environment' AND site_id = $1 AND target = $2")).
			WithArgs("site_1", "prod", "public:t0")
	}
	expect().WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	ok, err := s.EnvironmentApproved(ctx, "site_1", "prod", "public:t0")
	require.NoError(t, err)
	assert.True(t, ok)

	expect().WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	ok, err = s.EnvironmentApproved(ctx, "site_1", "prod", "public:t0")
	require.NoError(t, err)
	assert.False(t, ok, "an approval for another access stamp is not this one")

	expect().WillReturnError(errors.New("down"))
	ok, err = s.EnvironmentApproved(ctx, "site_1", "prod", "public:t0")
	require.ErrorContains(t, err, "reading the environment's approval")
	assert.False(t, ok)
	require.NoError(t, mock.ExpectationsWereMet())
}
