package whsource

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// prefixEncryptor marks what it encrypted, so a test can see a secret was
// encrypted before it was written and decrypted after it was read.
type prefixEncryptor struct{ fail bool }

func (e prefixEncryptor) Encrypt(p string) (string, error) {
	if e.fail {
		return "", errors.New("no key")
	}
	return "enc:" + p, nil
}

func (e prefixEncryptor) Decrypt(c string) (string, error) {
	if e.fail {
		return "", errors.New("no key")
	}
	return strings.TrimPrefix(c, "enc:"), nil
}

func mockStore(t *testing.T, enc Encryptor) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db, enc), mock
}

var at = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func sourceRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"name", "enabled", "auth", "config", "connection_name", "created_by", "created_at", "updated_at"})
}

func TestStoreCreateEncryptsSecrets(t *testing.T) {
	st, mock := mockStore(t, prefixEncryptor{})
	src := validHMAC()
	src.Auth.PreviousSecret = "older"
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO webhook_sources`)).
		WithArgs("esp-events", true, sqlmock.AnyArg(), sqlmock.AnyArg(), "scratch", "").
		WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, st.Create(context.Background(), src))

	auth, _, err := st.encode(src)
	require.NoError(t, err)
	assert.Contains(t, string(auth), `"secret":"enc:s3cret"`)
	assert.Contains(t, string(auth), `"previous_secret":"enc:older"`)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreCreateDuplicate(t *testing.T) {
	st, mock := mockStore(t, nil)
	mock.ExpectExec(`INSERT INTO webhook_sources`).WillReturnError(&pq.Error{Code: uniqueViolation})
	assert.ErrorIs(t, st.Create(context.Background(), validHMAC()), ErrExists)

	mock.ExpectExec(`INSERT INTO webhook_sources`).WillReturnError(errors.New("down"))
	err := st.Create(context.Background(), validHMAC())
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrExists)
}

func TestStoreEncryptFailure(t *testing.T) {
	st, _ := mockStore(t, prefixEncryptor{fail: true})
	assert.Error(t, st.Create(context.Background(), validHMAC()))
	assert.Error(t, st.Update(context.Background(), validHMAC()))
}

func TestStoreGetDecrypts(t *testing.T) {
	st, mock := mockStore(t, prefixEncryptor{})
	mock.ExpectQuery(`SELECT .* FROM webhook_sources WHERE name`).WithArgs("esp-events").
		WillReturnRows(sourceRows().AddRow("esp-events", true,
			[]byte(`{"mode":"hmac","secret":"enc:s3cret","previous_secret":"enc:older"}`),
			[]byte(`{"buffer_limit":100}`), "scratch", "admin@example.com", at, at))
	src, err := st.Get(context.Background(), "esp-events")
	require.NoError(t, err)
	assert.Equal(t, "s3cret", src.Auth.Secret)
	assert.Equal(t, "older", src.Auth.PreviousSecret)
	assert.Equal(t, 100, src.Config.BufferLimit)
	assert.Equal(t, "admin@example.com", src.CreatedBy)

	mock.ExpectQuery(`SELECT .* FROM webhook_sources WHERE name`).WillReturnError(sql.ErrNoRows)
	_, err = st.Get(context.Background(), "nope")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestStoreGetBadRows(t *testing.T) {
	cases := map[string]*sqlmock.Rows{
		"bad auth":   sourceRows().AddRow("a", true, []byte(`{`), []byte(`{}`), "c", "", at, at),
		"bad config": sourceRows().AddRow("a", true, []byte(`{}`), []byte(`[`), "c", "", at, at),
		"bad column": sqlmock.NewRows([]string{"name"}).AddRow("a"),
	}
	for name, rows := range cases {
		t.Run(name, func(t *testing.T) {
			st, mock := mockStore(t, nil)
			mock.ExpectQuery(`SELECT`).WillReturnRows(rows)
			_, err := st.Get(context.Background(), "a")
			assert.Error(t, err)
		})
	}
	t.Run("decrypt fails", func(t *testing.T) {
		st, mock := mockStore(t, prefixEncryptor{fail: true})
		mock.ExpectQuery(`SELECT`).WillReturnRows(
			sourceRows().AddRow("a", true, []byte(`{"secret":"x"}`), []byte(`{}`), "c", "", at, at))
		_, err := st.Get(context.Background(), "a")
		assert.Error(t, err)
	})
	t.Run("previous decrypt fails", func(t *testing.T) {
		st, mock := mockStore(t, prefixEncryptor{fail: true})
		mock.ExpectQuery(`SELECT`).WillReturnRows(
			sourceRows().AddRow("a", true, []byte(`{"previous_secret":"x"}`), []byte(`{}`), "c", "", at, at))
		_, err := st.Get(context.Background(), "a")
		assert.Error(t, err)
	})
}

func TestStoreList(t *testing.T) {
	st, mock := mockStore(t, nil)
	mock.ExpectQuery(`SELECT .* FROM webhook_sources ORDER BY name`).WillReturnRows(sourceRows())
	got, err := st.List(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, got, "no sources is an empty list, never null")
	assert.Empty(t, got)

	mock.ExpectQuery(`SELECT`).WillReturnRows(sourceRows().
		AddRow("a", true, []byte(`{}`), []byte(`{}`), "c", "", at, at).
		AddRow("b", false, []byte(`{}`), []byte(`{}`), "c", "", at, at))
	got, err = st.List(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.False(t, got[1].Enabled)

	mock.ExpectQuery(`SELECT`).WillReturnError(errors.New("down"))
	_, err = st.List(context.Background())
	assert.Error(t, err)

	mock.ExpectQuery(`SELECT`).WillReturnRows(sourceRows().AddRow("a", true, []byte(`{`), []byte(`{}`), "c", "", at, at))
	_, err = st.List(context.Background())
	assert.Error(t, err)

	mock.ExpectQuery(`SELECT`).WillReturnRows(sourceRows().
		AddRow("a", true, []byte(`{}`), []byte(`{}`), "c", "", at, at).RowError(0, errors.New("broken")))
	_, err = st.List(context.Background())
	assert.Error(t, err)
}

func TestStoreUpdateDelete(t *testing.T) {
	st, mock := mockStore(t, nil)
	mock.ExpectExec(`UPDATE webhook_sources`).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.Update(context.Background(), validHMAC()))
	mock.ExpectExec(`UPDATE webhook_sources`).WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, st.Update(context.Background(), validHMAC()), ErrNotFound)
	mock.ExpectExec(`UPDATE webhook_sources`).WillReturnError(errors.New("down"))
	assert.Error(t, st.Update(context.Background(), validHMAC()))

	mock.ExpectExec(`DELETE FROM webhook_sources`).WithArgs("a").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.Delete(context.Background(), "a"))
	mock.ExpectExec(`DELETE FROM webhook_sources`).WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, st.Delete(context.Background(), "a"), ErrNotFound)
	mock.ExpectExec(`DELETE FROM webhook_sources`).WillReturnError(errors.New("down"))
	assert.Error(t, st.Delete(context.Background(), "a"))
	mock.ExpectExec(`DELETE FROM webhook_sources`).WillReturnResult(sqlmock.NewErrorResult(errors.New("x")))
	assert.Error(t, st.Delete(context.Background(), "a"))
	require.NoError(t, mock.ExpectationsWereMet())
}
