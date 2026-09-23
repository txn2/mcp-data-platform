package scriptgrant

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

const scriptID = "3f2b6c1e-8d4a-4b8e-9f1a-2c3d4e5f6a7b"

func TestGrantValidate(t *testing.T) {
	require.NoError(t, Grant{Kind: KindAPIKey, Principal: "reporting-app"}.Validate())
	require.NoError(t, Grant{Kind: KindPersona, Principal: "analyst"}.Validate())
	require.NoError(t, Grant{Kind: KindRole, Principal: "dp_reports"}.Validate())
	assert.ErrorContains(t, Grant{Kind: "user", Principal: "x"}.Validate(), "persona, role or api_key")
	assert.ErrorContains(t, Grant{Kind: KindRole, Principal: " "}.Validate(), "1 to 200")
}

// A caller-bound parameter takes the claim, refuses a body value, and refuses
// a caller who does not carry the claim (#1846's second criterion).
func TestBindCaller(t *testing.T) {
	defs := []script.Param{
		{Name: "tenant", Type: script.ParamTypeString, Bind: "caller.tenant"},
		{Name: "org", Type: script.ParamTypeInt, Bind: "caller.org.id"},
		{Name: "region", Type: script.ParamTypeString, Default: "west"},
	}
	claims := map[string]any{"tenant": "acme", "org": map[string]any{"id": float64(7)}}

	got, err := BindCaller(defs, map[string]any{"region": "east"}, claims)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"tenant": "acme", "org": int64(7), "region": "east"}, got)

	got, err = BindCaller(defs, nil, claims)
	require.NoError(t, err)
	assert.Equal(t, "west", got["region"])

	_, err = BindCaller(defs, map[string]any{"tenant": "globex"}, claims)
	require.ErrorIs(t, err, ErrBoundInRequest)

	for name, c := range map[string]map[string]any{
		"absent":    {"org": map[string]any{"id": float64(7)}},
		"empty":     {"tenant": "", "org": map[string]any{"id": float64(7)}},
		"an object": {"tenant": map[string]any{"a": "b"}, "org": map[string]any{"id": float64(7)}},
		"not a map": {"tenant": "acme", "org": "7"},
		"no claims": nil,
	} {
		_, err = BindCaller(defs, nil, c)
		assert.ErrorIs(t, err, ErrClaimMissing, name)
	}
}

func TestCallerAPIKeyName(t *testing.T) {
	assert.Equal(t, "reporting-app", Caller{UserID: "apikey:reporting-app"}.apiKeyName())
	assert.Empty(t, Caller{UserID: "jane@example.com"}.apiKeyName())
}

func newStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgres(db), mock
}

func TestPostgresList(t *testing.T) {
	s, mock := newStore(t)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta("FROM script_grants WHERE script_id = $1")).WithArgs(scriptID).
		WillReturnRows(sqlmock.NewRows([]string{"script_id", "principal_kind", "principal", "granted_by", "created_at"}).
			AddRow(scriptID, KindAPIKey, "reporting-app", "jane@example.com", now))
	got, err := s.List(context.Background(), scriptID)
	require.NoError(t, err)
	assert.Equal(t, []Grant{{ScriptID: scriptID, Kind: KindAPIKey, Principal: "reporting-app", GrantedBy: "jane@example.com", CreatedAt: now}}, got)

	mock.ExpectQuery("FROM script_grants").WillReturnRows(sqlmock.NewRows([]string{"script_id"}))
	got, err = s.List(context.Background(), scriptID)
	require.NoError(t, err)
	assert.NotNil(t, got, "an empty listing is [], never null")
	assert.Empty(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAddRemove(t *testing.T) {
	s, mock := newStore(t)
	g := Grant{ScriptID: scriptID, Kind: KindRole, Principal: "dp_reports", GrantedBy: "jane@example.com"}
	mock.ExpectExec(regexp.QuoteMeta("ON CONFLICT (script_id, principal_kind, principal) DO NOTHING")).
		WithArgs(scriptID, KindRole, "dp_reports", "jane@example.com").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Add(context.Background(), g))

	mock.ExpectExec("DELETE FROM script_grants").WithArgs(scriptID, KindRole, "dp_reports").
		WillReturnResult(sqlmock.NewResult(0, 1))
	removed, err := s.Remove(context.Background(), g)
	require.NoError(t, err)
	assert.True(t, removed)

	mock.ExpectExec("DELETE FROM script_grants").WillReturnResult(sqlmock.NewResult(0, 0))
	removed, err = s.Remove(context.Background(), g)
	require.NoError(t, err)
	assert.False(t, removed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresAllowsAndGranted(t *testing.T) {
	s, mock := newStore(t)
	c := Caller{UserID: "apikey:reporting-app", Persona: "analyst", Roles: []string{"dp_reports"}}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS")).
		WithArgs(scriptID, "analyst", pq.Array([]string{"dp_reports"}), "reporting-app").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	ok, err := s.Allows(context.Background(), scriptID, c)
	require.NoError(t, err)
	assert.True(t, ok)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT script_id::text")).
		WithArgs("analyst", pq.Array([]string{"dp_reports"}), "reporting-app", maxGrantedScripts).
		WillReturnRows(sqlmock.NewRows([]string{"script_id"}).AddRow(scriptID))
	ids, err := s.GrantedScriptIDs(context.Background(), c)
	require.NoError(t, err)
	assert.Equal(t, []string{scriptID}, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresFailures(t *testing.T) {
	s, mock := newStore(t)
	boom := errors.New("boom")
	ctx := context.Background()
	g := Grant{ScriptID: scriptID, Kind: KindRole, Principal: "r"}

	mock.ExpectQuery("FROM script_grants").WillReturnError(boom)
	_, err := s.List(ctx, scriptID)
	require.ErrorIs(t, err, boom)

	mock.ExpectQuery("FROM script_grants").WillReturnRows(
		sqlmock.NewRows([]string{"script_id", "principal_kind", "principal", "granted_by", "created_at"}).
			AddRow(scriptID, KindRole, "r", "", "not a time"))
	_, err = s.List(ctx, scriptID)
	require.Error(t, err)

	mock.ExpectExec("INSERT INTO script_grants").WillReturnError(boom)
	require.ErrorIs(t, s.Add(ctx, g), boom)

	mock.ExpectExec("DELETE FROM script_grants").WillReturnError(boom)
	_, err = s.Remove(ctx, g)
	require.ErrorIs(t, err, boom)

	mock.ExpectExec("DELETE FROM script_grants").WillReturnResult(sqlmock.NewErrorResult(boom))
	_, err = s.Remove(ctx, g)
	require.ErrorIs(t, err, boom)

	mock.ExpectQuery("SELECT EXISTS").WillReturnError(boom)
	_, err = s.Allows(ctx, scriptID, Caller{})
	require.ErrorIs(t, err, boom)

	mock.ExpectQuery("SELECT DISTINCT").WillReturnError(boom)
	_, err = s.GrantedScriptIDs(ctx, Caller{})
	require.ErrorIs(t, err, boom)

	mock.ExpectQuery("SELECT DISTINCT").WillReturnRows(sqlmock.NewRows([]string{"script_id", "extra"}).AddRow(scriptID, 1))
	_, err = s.GrantedScriptIDs(ctx, Caller{})
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
