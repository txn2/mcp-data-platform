package resourcedownload

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPostgresUses: the first download of a single-download link is recorded,
// a second is told apart, rows a day past their link are swept first, and a
// database that fails is an error.
func TestPostgresUses(t *testing.T) {
	assert.Nil(t, NewUses(nil))
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	u := NewUses(db)
	ctx := context.Background()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	exp := now.Add(time.Hour)

	sweep := `DELETE FROM resource_download_uses WHERE expires_at < \$1`
	insert := `INSERT INTO resource_download_uses \(link_id, resource_id, used_at, expires_at\) VALUES \(\$1, \$2, \$3, \$4\)\s+ON CONFLICT \(link_id\) DO NOTHING`
	mock.ExpectExec(sweep).WithArgs(now.Add(-24 * time.Hour)).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(insert).WithArgs("n1", "r1", now, exp).WillReturnResult(sqlmock.NewResult(0, 1))
	fresh, err := u.claim(ctx, "n1", "r1", exp, now)
	require.NoError(t, err)
	assert.True(t, fresh)

	mock.ExpectExec(sweep).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(insert).WillReturnResult(sqlmock.NewResult(0, 0))
	fresh, err = u.claim(ctx, "n1", "r1", exp, now)
	require.NoError(t, err)
	assert.False(t, fresh, "a second download")

	mock.ExpectExec(sweep).WillReturnError(errors.New("db down"))
	_, err = u.claim(ctx, "n2", "r1", exp, now)
	require.Error(t, err)
	mock.ExpectExec(sweep).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(insert).WillReturnError(errors.New("db down"))
	_, err = u.claim(ctx, "n2", "r1", exp, now)
	require.Error(t, err)
	mock.ExpectExec(sweep).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(insert).WillReturnResult(sqlmock.NewErrorResult(errors.New("no count")))
	_, err = u.claim(ctx, "n2", "r1", exp, now)
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
