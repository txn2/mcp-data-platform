package shareguest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockStore(t *testing.T) (*PostgresLinkStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresLinkStore(db), mock
}

func TestPostgresLinkStoreInsert(t *testing.T) {
	store, mock := newMockStore(t)
	now := time.Now()
	l := Link{ID: "l1", ShareID: "sh1", TokenHash: "hash", CreatedAt: now, ExpiresAt: now.Add(LinkTTL)}

	mock.ExpectExec(`DELETE FROM portal_share_guest_links`).
		WithArgs(l.CreatedAt.Add(-purgeAge)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO portal_share_guest_links`).
		WithArgs(l.ID, l.ShareID, l.TokenHash, l.CreatedAt, l.ExpiresAt).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.Insert(context.Background(), l))

	mock.ExpectExec(`DELETE FROM portal_share_guest_links`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO portal_share_guest_links`).
		WillReturnError(errors.New("db down"))
	assert.Error(t, store.Insert(context.Background(), l))

	mock.ExpectExec(`DELETE FROM portal_share_guest_links`).
		WillReturnError(errors.New("db down"))
	assert.Error(t, store.Insert(context.Background(), l), "a failed purge surfaces rather than masking table growth")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresLinkStoreClaim(t *testing.T) {
	store, mock := newMockStore(t)
	now := time.Now()

	// A winning claim returns the row id.
	mock.ExpectQuery(`UPDATE portal_share_guest_links`).
		WithArgs("hash", "sh1", now).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("l1"))
	ok, err := store.Claim(context.Background(), "hash", "sh1", now)
	require.NoError(t, err)
	assert.True(t, ok)

	// No matching row (used, expired, or foreign share): ok=false, no error.
	mock.ExpectQuery(`UPDATE portal_share_guest_links`).
		WithArgs("hash", "sh1", now).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	ok, err = store.Claim(context.Background(), "hash", "sh1", now)
	require.NoError(t, err)
	assert.False(t, ok)

	mock.ExpectQuery(`UPDATE portal_share_guest_links`).
		WillReturnError(errors.New("db down"))
	_, err = store.Claim(context.Background(), "hash", "sh1", now)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresLinkStoreCountSince(t *testing.T) {
	store, mock := newMockStore(t)
	since := time.Now().Add(-time.Hour)

	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM portal_share_guest_links`).
		WithArgs("sh1", since).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	n, err := store.CountSince(context.Background(), "sh1", since)
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM portal_share_guest_links`).
		WillReturnError(errors.New("db down"))
	_, err = store.CountSince(context.Background(), "sh1", since)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
