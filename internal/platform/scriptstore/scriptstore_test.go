package scriptstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// testAuthor is the author every write in these tests is attributed to. It
// carries roles because a version's author roles are the ceiling on what
// approving that version can grant.
var testAuthor = script.Author{Email: "jane@example.com", Roles: []string{"analyst"}}

// scriptSelectColumns is the result-set shape a SELECT mock must return, in
// scriptColumns order.
var scriptSelectColumns = []string{
	"id", "name", "display_name", "description", "category", "source_code", "params",
	"scope", "personas", "owner_email", "tags", "enabled", "status",
	"superseded_by", "deprecated_at", "version", "approved_version_id",
	"created_at", "updated_at",
}

var rowTime = time.Unix(1700000000, 0).UTC()

// rowSpec describes the script row a SELECT mock should return.
type rowSpec struct {
	id              string
	name            string
	scope           string
	owner           string
	paramsJSON      []byte
	approvedVersion string
	source          string
	category        string
}

// scriptRow returns one full result row in scriptColumns order.
func scriptRow(spec rowSpec) []driver.Value {
	source := spec.source
	if source == "" {
		source = "print(1)"
	}
	return []driver.Value{
		spec.id, spec.name, "Daily", "A daily report", spec.category, source, spec.paramsJSON,
		spec.scope, pq.Array([]string{}), spec.owner, pq.Array([]string{}), true, "draft",
		"", nil, 1, spec.approvedVersion, rowTime, rowTime,
	}
}

func emptyParams(t *testing.T) []byte {
	t.Helper()
	data, err := json.Marshal([]script.Param{})
	require.NoError(t, err)
	return data
}

func newMock(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return New(db), mock
}

// expectLiveRowUpdate stands in for updateTx's write of the live script row.
// It is a QueryRow rather than an Exec because the statement reports, through
// RETURNING, whether the write moved the text the scripts index is built from;
// indexChanged is that answer.
func expectLiveRowUpdate(mock sqlmock.Sqlmock, indexChanged bool) {
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE scripts")).
		WillReturnRows(sqlmock.NewRows([]string{"changed"}).AddRow(indexChanged))
}

// expectLiveRowUpdateMissing stands in for updateTx against a script id that no
// longer exists: no row is returned, which is how the statement reports it.
func expectLiveRowUpdateMissing(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE scripts")).
		WillReturnRows(sqlmock.NewRows([]string{"changed"}))
}

func TestGet_ResolvesSharedByName(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM scripts WHERE name = $1 AND scope <> 'personal'")).
		WithArgs("daily").
		WillReturnRows(sqlmock.NewRows(scriptSelectColumns).
			AddRow(scriptRow(rowSpec{id: "script_1", name: "daily", scope: "global", owner: "jane@example.com", paramsJSON: emptyParams(t), approvedVersion: ""})...))

	got, err := s.Get(context.Background(), "daily")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "daily", got.Name)
	assert.Equal(t, []script.Param{}, got.Params, "a nil params slice is normalized so JSON carries [] not null")
	assert.Equal(t, []string{}, got.Personas)
	assert.False(t, got.Executable())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPersonal_NeedsAnOwner(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("WHERE name = $1 AND owner_email = $2 AND scope = 'personal'")).
		WithArgs("daily", "jane@example.com").
		WillReturnRows(sqlmock.NewRows(scriptSelectColumns).
			AddRow(scriptRow(rowSpec{id: "script_1", name: "daily", scope: "personal", owner: "jane@example.com", paramsJSON: emptyParams(t), approvedVersion: "sver_9"})...))

	got, err := s.GetPersonal(context.Background(), "jane@example.com", "daily")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.True(t, got.Executable(), "the approved-version pointer is read back as the execution gate")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGet_NotFoundIsNilNil pins the store contract every caller branches on.
func TestGet_NotFoundIsNilNil(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WillReturnRows(sqlmock.NewRows(scriptSelectColumns))

	got, err := s.Get(context.Background(), "missing")
	require.NoError(t, err)
	assert.Nil(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGet_QueryErrorIsWrapped(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").WillReturnError(errors.New("boom"))

	_, err := s.GetByID(context.Background(), "script_1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get script")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGet_MalformedParamsAreReported(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").
		WillReturnRows(sqlmock.NewRows(scriptSelectColumns).
			AddRow(scriptRow(rowSpec{id: "script_1", name: "daily", scope: "global", owner: "j@example.com", paramsJSON: []byte("{not json"), approvedVersion: ""})...))

	_, err := s.Get(context.Background(), "daily")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal script params")
}

// TestCreate_WritesTheRowAndItsFirstSnapshot pins the invariant that a script
// never exists without the version history that explains it.
func TestCreate_WritesTheRowAndItsFirstSnapshot(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO scripts")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow("script_1", rowTime, rowTime))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_versions")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	sc := &script.Script{Name: "daily", Scope: script.ScopePersonal, Source: "print(1)", OwnerEmail: "j@example.com"}
	require.NoError(t, s.Create(context.Background(), sc, testAuthor))
	assert.Equal(t, "script_1", sc.ID)
	assert.Equal(t, 1, sc.Version)
	assert.Equal(t, script.StatusDraft, sc.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreate_RollsBackWhenTheSnapshotFails(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO scripts")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow("script_1", rowTime, rowTime))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_versions")).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()

	err := s.Create(context.Background(), &script.Script{Name: "daily", Scope: script.ScopePersonal}, testAuthor)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert script version")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdate_MissingRowIsAnError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	expectLiveRowUpdateMissing(mock)
	mock.ExpectRollback()

	err := s.Update(context.Background(), &script.Script{ID: "script_1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDelete(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM scripts")).WithArgs("script_1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Delete(context.Background(), "script_1"))

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM scripts")).WithArgs("gone").
		WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorContains(t, s.Delete(context.Background(), "gone"), "not found")

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM scripts")).WithArgs("bad").
		WillReturnError(errors.New("boom"))
	assert.ErrorContains(t, s.Delete(context.Background(), "bad"), "delete script")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestBuildListQuery covers the filter assembly directly, including the
// three-column search clause whose placeholder index is repeated.
func TestBuildListQuery(t *testing.T) {
	enabled := true
	query, args := buildListQuery(script.ListFilter{
		Scope: "personal", Personas: []string{"analyst"}, OwnerEmail: "j@example.com",
		Enabled: &enabled, Status: "draft", Search: "sales", Limit: 10,
	})
	assert.Contains(t, query, "scope = $1")
	assert.Contains(t, query, "personas && $2")
	assert.Contains(t, query, "owner_email = $3")
	assert.Contains(t, query, "enabled = $4")
	assert.Contains(t, query, "status = $5")
	assert.Contains(t, query, "(name ILIKE $6 OR display_name ILIKE $6 OR description ILIKE $6)")
	assert.Contains(t, query, "LIMIT $7")
	assert.NotContains(t, query, "MISSING", "every placeholder must be numbered")
	require.Len(t, args, 7)
	assert.Equal(t, "%sales%", args[5])
	assert.Equal(t, 10, args[6])

	query, args = buildListQuery(script.ListFilter{})
	assert.NotContains(t, query, "WHERE")
	assert.Equal(t, []any{defaultListLimit}, args)

	// The visibility predicate is one clause naming both bound values, which is
	// what keeps the listing and the read path answering the same question.
	query, args = buildListQuery(script.ListFilter{VisibleTo: "j@example.com", VisiblePersona: "analyst"})
	assert.Contains(t, query,
		"(scope = 'global' OR (scope = 'persona' AND personas && $2) OR (scope = 'personal' AND owner_email = $1))")
	require.Len(t, args, 3)
	assert.Equal(t, "j@example.com", args[0])

	// An over-large limit is clamped rather than honored.
	_, args = buildListQuery(script.ListFilter{Limit: 100000})
	assert.Equal(t, defaultListLimit, args[0])
}

// TestBuildListQuery_FacetAxes covers the two axes the listing gained with the
// category (#1369). The tag arm is an OVERLAP rather than a containment: naming
// two tags asks for the scripts carrying either, which is the union of two
// shelves and not their intersection.
func TestBuildListQuery_FacetAxes(t *testing.T) {
	query, args := buildListQuery(script.ListFilter{Category: "reporting"})
	assert.Contains(t, query, "category = $1")
	require.Len(t, args, 2)
	assert.Equal(t, "reporting", args[0])

	query, _ = buildListQuery(script.ListFilter{Tags: []string{"sales", "weekly"}})
	assert.Contains(t, query, "tags && $1")

	// Both axes together are a conjunction: a reader who pressed a category and
	// a tag asked for the scripts that are both.
	query, _ = buildListQuery(script.ListFilter{Category: "reporting", Tags: []string{"sales"}})
	assert.Contains(t, query, "category = $1 AND tags && $2")

	// An empty tag list is not a filter for "no tags", which would answer an
	// unfiltered listing with nothing.
	query, _ = buildListQuery(script.ListFilter{Tags: []string{}})
	assert.NotContains(t, query, "tags &&")
}

// TestScanScript_ReadsTheCategory proves the column reaches the record. The
// scan order is positional, so a column added to scriptColumns without a
// matching destination silently shifts every field after it.
func TestScanScript_ReadsTheCategory(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("FROM scripts WHERE id = $1")).
		WillReturnRows(sqlmock.NewRows(scriptSelectColumns).AddRow(scriptRow(rowSpec{
			id: "script_1", name: "daily", scope: "personal",
			owner: "jane@example.com", paramsJSON: emptyParams(t), category: "reporting",
		})...))

	got, err := s.GetByID(context.Background(), "script_1")

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "reporting", got.Category)
	assert.Equal(t, "daily", got.Name, "the fields after the new column still land where they belong")
	assert.Equal(t, "A daily report", got.Description)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestList(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").
		WillReturnRows(sqlmock.NewRows(scriptSelectColumns).
			AddRow(scriptRow(rowSpec{id: "script_1", name: "a", scope: "personal", owner: "j@example.com", paramsJSON: emptyParams(t), approvedVersion: ""})...).
			AddRow(scriptRow(rowSpec{id: "script_2", name: "b", scope: "personal", owner: "j@example.com", paramsJSON: emptyParams(t), approvedVersion: ""})...))

	got, err := s.List(context.Background(), script.ListFilter{})
	require.NoError(t, err)
	assert.Len(t, got, 2)
	require.NoError(t, mock.ExpectationsWereMet())

	mock.ExpectQuery("FROM scripts").WillReturnError(errors.New("boom"))
	_, err = s.List(context.Background(), script.ListFilter{})
	assert.ErrorContains(t, err, "list scripts")
}

func TestList_ScanErrorPropagates(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("FROM scripts").
		WillReturnRows(sqlmock.NewRows(scriptSelectColumns).
			AddRow(scriptRow(rowSpec{id: "script_1", name: "a", scope: "personal", owner: "j@example.com", paramsJSON: []byte("{bad"), approvedVersion: ""})...))

	_, err := s.List(context.Background(), script.ListFilter{})
	assert.ErrorContains(t, err, "unmarshal script params")
}

// TestScriptColumns_MatchTheScanOrder is the cheap guard against the classic
// store bug: adding a column to the SELECT list without adding a destination to
// scanScript, which fails at runtime with a scan-arity error nobody sees until
// the query runs. Commas inside a function call (COALESCE) do not separate
// columns, so the split has to respect nesting.
func TestScriptColumns_MatchTheScanOrder(t *testing.T) {
	assert.Len(t, splitTopLevel(scriptColumns), len(scriptSelectColumns),
		"scriptColumns and the scan destinations in scanScript must have the same arity")
	assert.Len(t, splitTopLevel(versionColumns), len(versionSelectColumns),
		"versionColumns and the scan destinations in scanVersion must have the same arity")
}

// splitTopLevel splits a SQL column list on the commas that separate columns,
// ignoring commas nested inside parentheses.
func splitTopLevel(list string) []string {
	var (
		out   []string
		depth int
		start int
	)
	for i, r := range list {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(list[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(list[start:]))
}

func TestWithTx_BeginAndCommitErrors(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin().WillReturnError(errors.New("boom"))
	err := s.withTx(context.Background(), "op", func(*sql.Tx) error { return nil })
	assert.ErrorContains(t, err, "begin op")

	mock.ExpectBegin()
	mock.ExpectCommit().WillReturnError(errors.New("boom"))
	err = s.withTx(context.Background(), "op", func(*sql.Tx) error { return nil })
	assert.ErrorContains(t, err, "commit op")
	require.NoError(t, mock.ExpectationsWereMet())
}
