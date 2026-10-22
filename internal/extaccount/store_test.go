package extaccount

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresStore(db), mock
}

var accountCols = []string{"id", "email", "name", "persona", "expires_at", "suspended_at", "created_by", "created_at", "last_sign_in_at", "converted_at"}

func TestPostgresStoreReadsAndWritesAccounts(t *testing.T) {
	s, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now()

	mock.ExpectExec("INSERT INTO external_accounts").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Insert(ctx, Account{ID: "a1", Email: "c@example.org", Persona: "client"}))
	mock.ExpectExec("INSERT INTO external_accounts").WillReturnError(errors.New(`duplicate key value violates unique constraint "external_accounts_email_idx"`))
	assert.ErrorIs(t, s.Insert(ctx, Account{ID: "a2", Email: "c@example.org"}), ErrExists)
	mock.ExpectExec("INSERT INTO external_accounts").WillReturnError(errors.New("down"))
	assert.Error(t, s.Insert(ctx, Account{ID: "a3"}))

	mock.ExpectQuery("SELECT .* FROM external_accounts WHERE id").
		WillReturnRows(sqlmock.NewRows(accountCols).AddRow("a1", "c@example.org", "C", "client", now, nil, "admin", now, now, nil))
	got, err := s.Get(ctx, "a1")
	require.NoError(t, err)
	assert.NotNil(t, got.ExpiresAt)
	assert.Nil(t, got.SuspendedAt)
	mock.ExpectQuery("SELECT .* FROM external_accounts WHERE lower").WillReturnRows(sqlmock.NewRows(accountCols))
	_, err = s.GetByEmail(ctx, "x@example.org")
	assert.ErrorIs(t, err, ErrNotFound)

	mock.ExpectQuery("SELECT .* FROM external_accounts ORDER BY").
		WillReturnRows(sqlmock.NewRows(accountCols).AddRow("a1", "c@example.org", "C", "client", nil, nil, "admin", now, nil, nil))
	list, err := s.List(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 1)
	mock.ExpectQuery("SELECT .* FROM external_accounts ORDER BY").WillReturnError(errors.New("down"))
	_, err = s.List(ctx)
	assert.Error(t, err)

	for _, call := range []func() error{
		func() error { return s.SetExpiry(ctx, "a1", nil) },
		func() error { return s.SetSuspended(ctx, "a1", &now) },
		func() error { return s.TouchSignIn(ctx, "a1", now) },
		func() error { return s.Delete(ctx, "a1") },
	} {
		mock.ExpectExec("external_accounts").WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, call())
	}
	mock.ExpectExec("DELETE FROM external_accounts").WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, s.Delete(ctx, "gone"), ErrNotFound)
	mock.ExpectExec("UPDATE external_accounts").WillReturnError(errors.New("down"))
	assert.Error(t, s.SetExpiry(ctx, "a1", nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStoreLinksConversionAndShares(t *testing.T) {
	s, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now()

	mock.ExpectExec("INSERT INTO external_signin_links").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.InsertLink(ctx, "h", "a1", now))
	mock.ExpectQuery("UPDATE external_signin_links").WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow("a1"))
	id, ok, err := s.ClaimLink(ctx, "h", now)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "a1", id)
	mock.ExpectQuery("UPDATE external_signin_links").WillReturnRows(sqlmock.NewRows([]string{"account_id"}))
	_, ok, err = s.ClaimLink(ctx, "h", now)
	require.NoError(t, err)
	assert.False(t, ok)
	mock.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	n, err := s.CountLinksSince(ctx, "a1", now)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	mock.ExpectQuery("UPDATE external_accounts SET converted_at").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("a1"))
	mock.ExpectExec("DELETE FROM external_signin_links").WillReturnResult(sqlmock.NewResult(0, 1))
	converted, err := s.Convert(ctx, "c@example.org", now)
	require.NoError(t, err)
	assert.True(t, converted)
	mock.ExpectQuery("UPDATE external_accounts SET converted_at").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	converted, err = s.Convert(ctx, "c@example.org", now)
	require.NoError(t, err)
	assert.False(t, converted)

	mock.ExpectExec("UPDATE portal_shares SET revoked").WillReturnResult(sqlmock.NewResult(0, 3))
	require.NoError(t, s.RevokeSharesTo(ctx, "c@example.org"))
	mock.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	n, err = s.CountSharesTo(ctx, "c@example.org")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	mock.ExpectExec("UPDATE portal_shares SET revoked").WillReturnError(errors.New("down"))
	assert.Error(t, s.RevokeSharesTo(ctx, "c@example.org"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// Every store write and count reports the failure it hit, and a conversion
// whose link cleanup fails still reports the account converted.
func TestPostgresStoreReportsFailures(t *testing.T) {
	s, mock := newMockStore(t)
	ctx := context.Background()
	down := errors.New("down")

	mock.ExpectQuery("UPDATE external_accounts SET converted_at").WillReturnError(down)
	_, err := s.Convert(ctx, "c@example.org", time.Now())
	assert.ErrorIs(t, err, down)
	mock.ExpectQuery("UPDATE external_accounts SET converted_at").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("a1"))
	mock.ExpectExec("DELETE FROM external_signin_links").WillReturnError(down)
	converted, err := s.Convert(ctx, "c@example.org", time.Now())
	assert.True(t, converted)
	assert.ErrorIs(t, err, down)

	mock.ExpectExec("INSERT INTO external_signin_links").WillReturnError(down)
	assert.ErrorIs(t, s.InsertLink(ctx, "h", "a1", time.Now()), down)
	mock.ExpectQuery("UPDATE external_signin_links").WillReturnError(down)
	_, _, err = s.ClaimLink(ctx, "h", time.Now())
	assert.ErrorIs(t, err, down)
	mock.ExpectQuery("SELECT count").WillReturnError(down)
	_, err = s.CountLinksSince(ctx, "a1", time.Now())
	assert.ErrorIs(t, err, down)
	mock.ExpectExec("UPDATE portal_shares").WillReturnError(down)
	assert.ErrorIs(t, s.RevokeSharesTo(ctx, "c@example.org"), down)
	mock.ExpectQuery("SELECT count").WillReturnError(down)
	_, err = s.CountSharesTo(ctx, "c@example.org")
	assert.ErrorIs(t, err, down)
	assert.NoError(t, mock.ExpectationsWereMet())
}
