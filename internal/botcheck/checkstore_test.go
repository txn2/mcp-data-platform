package botcheck

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var checkCols = []string{
	"name", "provider", "site_key", "secret_name", "difficulty", "max_difficulty", "rate_threshold",
	"challenge_ttl_seconds", "created_by", "updated_by", "created_at", "updated_at",
}

func checkRow(name string) []driver.Value {
	at := time.Unix(1_800_000_000, 0).UTC()
	return []driver.Value{name, ProviderPoW, "", "", 16, 22, 30, 300, "a@example.com", "a@example.com", at, at}
}

func mockStore(t *testing.T) (*PostgresChecks, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	return NewStore(db), mock
}

func TestPostgresChecks_ListAndGet(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()
	mock.ExpectQuery(regexp.QuoteMeta("FROM bot_checks ORDER BY name")).
		WillReturnRows(sqlmock.NewRows(checkCols).AddRow(checkRow("default")...).AddRow(checkRow("forms")...))
	list, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "forms", list[1].Name)

	mock.ExpectQuery("FROM bot_checks ORDER BY name").WillReturnError(errors.New("down"))
	_, err = s.List(ctx)
	require.ErrorContains(t, err, "listing bot checks")
	mock.ExpectQuery("FROM bot_checks ORDER BY name").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("x"))
	_, err = s.List(ctx)
	require.ErrorContains(t, err, "reading a bot check")

	mock.ExpectQuery("WHERE name = \\$1").WithArgs("default").WillReturnRows(sqlmock.NewRows(checkCols).AddRow(checkRow("default")...))
	c, err := s.Get(ctx, "default")
	require.NoError(t, err)
	assert.Equal(t, 16, c.Difficulty)
	mock.ExpectQuery("WHERE name = \\$1").WithArgs("gone").WillReturnRows(sqlmock.NewRows(checkCols))
	_, err = s.Get(ctx, "gone")
	require.ErrorIs(t, err, ErrNotFound)
	mock.ExpectQuery("WHERE name = \\$1").WillReturnError(errors.New("down"))
	_, err = s.Get(ctx, "x")
	require.ErrorContains(t, err, `reading bot check "x"`)
}

func TestPostgresChecks_Put(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO bot_checks")).
		WithArgs("forms", ProviderPoW, "", "", 16, 22, 30, 300, "admin@example.com").
		WillReturnRows(sqlmock.NewRows(checkCols).AddRow(checkRow("forms")...))
	got, err := s.Put(ctx, New("forms"), "admin@example.com")
	require.NoError(t, err)
	assert.Equal(t, "forms", got.Name)
	mock.ExpectQuery("INSERT INTO bot_checks").WillReturnError(errors.New("down"))
	_, err = s.Put(ctx, New("forms"), "a")
	require.ErrorContains(t, err, `writing bot check "forms"`)
}

// A delete the endpoints' foreign key refuses names the endpoints.
func TestPostgresChecks_DeleteAndUsedBy(t *testing.T) {
	s, mock := mockStore(t)
	ctx := context.Background()
	mock.ExpectExec("DELETE FROM bot_checks").WithArgs("forms").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Delete(ctx, "forms"))
	mock.ExpectExec("DELETE FROM bot_checks").WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.Delete(ctx, "gone"), ErrNotFound)

	mock.ExpectExec("DELETE FROM bot_checks").WillReturnError(&pq.Error{Code: foreignKeyViolation})
	mock.ExpectQuery("FROM script_endpoints WHERE bot_check IS NOT NULL").
		WillReturnRows(sqlmock.NewRows([]string{"bot_check", "name"}).AddRow("default", "orders").AddRow("default", "contact"))
	err := s.Delete(ctx, "default")
	var inUse *InUseError
	require.ErrorAs(t, err, &inUse)
	assert.Equal(t, []string{"contact", "orders"}, inUse.Endpoints, "sorted")

	mock.ExpectExec("DELETE FROM bot_checks").WillReturnError(&pq.Error{Code: foreignKeyViolation})
	mock.ExpectQuery("FROM script_endpoints").WillReturnError(errors.New("down"))
	require.ErrorContains(t, s.Delete(ctx, "default"), "endpoints each bot check is used by")

	mock.ExpectExec("DELETE FROM bot_checks").WillReturnError(errors.New("down"))
	require.ErrorContains(t, s.Delete(ctx, "x"), `deleting bot check "x"`)

	mock.ExpectQuery("FROM script_endpoints").WillReturnRows(sqlmock.NewRows([]string{"bot_check"}).AddRow("x"))
	_, err = s.UsedBy(ctx)
	require.ErrorContains(t, err, "reading an endpoint's bot check")
}
