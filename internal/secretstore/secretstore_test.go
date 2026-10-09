package secretstore

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// prefixEncryptor marks what it encrypted, so a test sees the value was
// encrypted before it was written and decrypted after it was read.
type prefixEncryptor struct{ fail bool }

func (p prefixEncryptor) Encrypt(s string) (string, error) {
	if p.fail {
		return "", errors.New("no key")
	}
	return "enc:" + s, nil
}

func (p prefixEncryptor) Decrypt(s string) (string, error) {
	if p.fail {
		return "", errors.New("no key")
	}
	return strings.TrimPrefix(s, "enc:"), nil
}

func TestValidate(t *testing.T) {
	good := Write{Name: "portal_password", Value: new("hunter22"), AllowConnections: []string{"grid"}}
	require.NoError(t, Validate(good, true))
	update := good
	update.Value = nil
	require.NoError(t, Validate(update, false), "an update may keep the stored value")

	cases := map[string]struct {
		w        Write
		creating bool
		want     string
	}{
		"bad name":         {Write{Name: "Bad Name", Value: new("hunter22"), AllowConnections: []string{"g"}}, true, "lower case"},
		"no value":         {Write{Name: "a", AllowConnections: []string{"g"}}, true, "value is required"},
		"short value":      {Write{Name: "a", Value: new("abc"), AllowConnections: []string{"g"}}, true, "at least 6"},
		"long value":       {Write{Name: "a", Value: new(strings.Repeat("x", MaxValueBytes+1)), AllowConnections: []string{"g"}}, true, "longer than"},
		"long description": {Write{Name: "a", Value: new("hunter22"), Description: strings.Repeat("x", MaxDescriptionBytes+1), AllowConnections: []string{"g"}}, true, "description"},
		"no connections":   {Write{Name: "a", Value: new("hunter22")}, true, "at least one connection"},
		"empty connection": {Write{Name: "a", Value: new("hunter22"), AllowConnections: []string{" "}}, true, "empty name"},
		"empty persona":    {Write{Name: "a", Value: new("hunter22"), AllowConnections: []string{"g"}, AllowPersonas: []string{""}}, true, "empty name"},
	}
	for name, c := range cases {
		err := Validate(c.w, c.creating)
		require.ErrorIs(t, err, ErrInvalid, name)
		assert.Contains(t, err.Error(), c.want, name)
	}
}

func TestAllowed(t *testing.T) {
	sec := Secret{Name: "pw", AllowConnections: []string{"grid", "vendor"}}
	require.NoError(t, Allowed(sec, "grid", ""))
	assert.ErrorContains(t, Allowed(sec, "other", ""), `may not be sent through connection "other"; it is allowed on grid, vendor`)
	sec.AllowPersonas = []string{"admin"}
	require.NoError(t, Allowed(sec, "grid", "admin"))
	assert.ErrorContains(t, Allowed(sec, "grid", "analyst"), "may not be used by persona analyst; it is allowed for admin")
	assert.ErrorContains(t, Allowed(sec, "grid", ""), "persona (none)")
}

var metaCols = []string{
	"name", "description", "allow_connections", "allow_personas", "created_by", "updated_by", "created_at", "updated_at",
	"kind", "totp_algorithm", "totp_digits", "totp_period",
}

func metaRow(now time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(metaCols).AddRow("pw", "portal login", "{grid}", "{}", "admin@example.com", "admin@example.com", now, now, "value", "", 0, 0)
}

func newMock(t *testing.T, enc Encryptor) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db, enc), mock
}

func TestListAndGet(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, mock := newMock(t, nil)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT ` + metaColumns + ` FROM gateway_secrets ORDER BY name`)).WillReturnRows(metaRow(now))
	list, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, []string{"grid"}, list[0].AllowConnections)
	assert.Equal(t, []string{}, list[0].AllowPersonas, "an empty list is [] on the wire, never null")

	mock.ExpectQuery(`FROM gateway_secrets WHERE name`).WithArgs("pw").WillReturnRows(metaRow(now))
	got, err := s.Get(ctx, "pw")
	require.NoError(t, err)
	assert.Equal(t, "portal login", got.Description)

	mock.ExpectQuery(`FROM gateway_secrets WHERE name`).WithArgs("none").WillReturnError(sql.ErrNoRows)
	_, err = s.Get(ctx, "none")
	require.ErrorIs(t, err, ErrNotFound)

	mock.ExpectQuery(`FROM gateway_secrets WHERE name`).WithArgs("x").WillReturnError(errors.New("down"))
	_, err = s.Get(ctx, "x")
	require.ErrorContains(t, err, "down")

	mock.ExpectQuery(`ORDER BY name`).WillReturnError(errors.New("down"))
	_, err = s.List(ctx)
	require.ErrorContains(t, err, "listing secrets")

	mock.ExpectQuery(`ORDER BY name`).WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("x"))
	_, err = s.List(ctx)
	require.ErrorContains(t, err, "reading a secret")

	mock.ExpectQuery(`ORDER BY name`).WillReturnRows(metaRow(now).RowError(0, errors.New("cut")))
	_, err = s.List(ctx)
	require.ErrorContains(t, err, "iterating secrets")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPutCreatesEncryptedAndUpdatesKeepingValue(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, mock := newMock(t, prefixEncryptor{})

	mock.ExpectQuery(`FROM gateway_secrets WHERE name`).WithArgs("pw").WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(`INSERT INTO gateway_secrets`).
		WithArgs("pw", "portal login", "enc:hunter22", `{"grid"}`, "{}", "admin@example.com", "value", "", 0, 0).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`FROM gateway_secrets WHERE name`).WithArgs("pw").WillReturnRows(metaRow(now))
	sec, created, err := s.Put(ctx, Write{Name: "pw", Description: "portal login", Value: new("hunter22"), AllowConnections: []string{"grid"}, Actor: "admin@example.com"})
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, "pw", sec.Name)

	mock.ExpectQuery(`FROM gateway_secrets WHERE name`).WithArgs("pw").WillReturnRows(metaRow(now))
	mock.ExpectExec(`UPDATE gateway_secrets SET`).
		WithArgs("pw", "rescoped", sql.NullString{}, `{"grid","vendor"}`, `{"admin"}`, "other@example.com", "value", "", 0, 0).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`FROM gateway_secrets WHERE name`).WithArgs("pw").WillReturnRows(metaRow(now))
	_, created, err = s.Put(ctx, Write{Name: "pw", Description: "rescoped", AllowConnections: []string{"grid", "vendor"}, AllowPersonas: []string{"admin"}, Actor: "other@example.com"})
	require.NoError(t, err)
	assert.False(t, created)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPutRefusals(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t, prefixEncryptor{fail: true})
	mock.ExpectQuery(`FROM gateway_secrets WHERE name`).WillReturnError(errors.New("down"))
	_, _, err := s.Put(ctx, Write{Name: "pw"})
	require.ErrorContains(t, err, "down")

	mock.ExpectQuery(`FROM gateway_secrets WHERE name`).WillReturnError(sql.ErrNoRows)
	_, _, err = s.Put(ctx, Write{Name: "pw", AllowConnections: []string{"g"}})
	require.ErrorIs(t, err, ErrInvalid)

	mock.ExpectQuery(`FROM gateway_secrets WHERE name`).WillReturnError(sql.ErrNoRows)
	_, _, err = s.Put(ctx, Write{Name: "pw", Value: new("hunter22"), AllowConnections: []string{"g"}})
	require.ErrorContains(t, err, "encrypting secret value")

	plain, mock2 := newMock(t, nil)
	mock2.ExpectQuery(`FROM gateway_secrets WHERE name`).WillReturnError(sql.ErrNoRows)
	mock2.ExpectExec(`INSERT INTO gateway_secrets`).WithArgs("pw", "", "hunter22", `{"g"}`, "{}", "", "value", "", 0, 0).WillReturnError(errors.New("down"))
	_, _, err = plain.Put(ctx, Write{Name: "pw", Value: new("hunter22"), AllowConnections: []string{"g"}})
	require.ErrorContains(t, err, "writing secret")
	require.NoError(t, mock.ExpectationsWereMet())
	require.NoError(t, mock2.ExpectationsWereMet())
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	s, mock := newMock(t, nil)
	mock.ExpectExec(`DELETE FROM gateway_secrets`).WithArgs("pw").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Delete(ctx, "pw"))
	mock.ExpectExec(`DELETE FROM gateway_secrets`).WithArgs("none").WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.Delete(ctx, "none"), ErrNotFound)
	mock.ExpectExec(`DELETE FROM gateway_secrets`).WithArgs("x").WillReturnError(errors.New("down"))
	require.ErrorContains(t, s.Delete(ctx, "x"), "deleting secret")
	require.NoError(t, mock.ExpectationsWereMet())
}

func valueRow(now time.Time, value, personas string) *sqlmock.Rows {
	return sqlmock.NewRows(append(append([]string(nil), metaCols...), "value")).
		AddRow("pw", "", "{grid}", personas, "", "", now, now, "value", "", 0, 0, value)
}

func TestLookup(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, mock := newMock(t, prefixEncryptor{})
	lookup := s.Lookup(ctx, "grid", "analyst")

	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("pw").WillReturnRows(valueRow(now, "enc:hunter22", "{}"))
	v, err := lookup("pw")
	require.NoError(t, err)
	assert.Equal(t, "hunter22", v)
	v, err = lookup("pw")
	require.NoError(t, err, "a second placeholder of one call is answered without a read")
	assert.Equal(t, "hunter22", v)

	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("nope").WillReturnError(sql.ErrNoRows)
	_, err = lookup("nope")
	require.ErrorContains(t, err, `secret "nope" does not exist`)

	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("down").WillReturnError(errors.New("down"))
	_, err = lookup("down")
	require.ErrorContains(t, err, `reading secret "down"`)

	other := s.Lookup(ctx, "elsewhere", "analyst")
	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("pw").WillReturnRows(valueRow(now, "enc:hunter22", "{}"))
	_, err = other("pw")
	require.ErrorContains(t, err, `may not be sent through connection "elsewhere"`)

	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("pw").WillReturnRows(valueRow(now, "enc:hunter22", "{finance}"))
	_, err = s.Lookup(ctx, "grid", "analyst")("pw")
	require.ErrorContains(t, err, "may not be used by persona analyst")

	s.WithAdmin("admin")
	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("pw").WillReturnRows(valueRow(now, "enc:hunter22", "{finance}"))
	v, err = s.Lookup(ctx, "grid", "admin")("pw")
	require.NoError(t, err, "the administrator persona is not limited by allow_personas")
	assert.Equal(t, "hunter22", v)
	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("pw").WillReturnRows(valueRow(now, "enc:hunter22", "{finance}"))
	_, err = s.Lookup(ctx, "vendor", "admin")("pw")
	require.ErrorContains(t, err, "may not be sent through connection", "allow_connections holds for the administrator too")

	broken, mock2 := newMock(t, prefixEncryptor{fail: true})
	mock2.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("pw").WillReturnRows(valueRow(now, "enc:hunter22", "{}"))
	_, err = broken.Lookup(ctx, "grid", "")("pw")
	require.ErrorContains(t, err, "decrypting secret")

	plain, mock3 := newMock(t, nil)
	mock3.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("pw").WillReturnRows(valueRow(now, "hunter22", "{}"))
	v, err = plain.Lookup(ctx, "grid", "")("pw")
	require.NoError(t, err)
	assert.Equal(t, "hunter22", v)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestConnectionValue is the read a Google key secret goes through (#2061):
// scoped to the connection that names it, and refused when it names personas,
// since the one token it mints serves every persona on the connection.
func TestConnectionValue(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, mock := newMock(t, prefixEncryptor{})

	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("pw").WillReturnRows(valueRow(now, "enc:keyfile", "{}"))
	v, err := s.ConnectionValue(ctx, "pw", "grid")
	require.NoError(t, err)
	assert.Equal(t, "keyfile", v)

	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("pw").WillReturnRows(valueRow(now, "enc:keyfile", "{}"))
	_, err = s.ConnectionValue(ctx, "pw", "elsewhere")
	require.ErrorContains(t, err, `may not be used by connection "elsewhere"; it is allowed on grid`)

	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("pw").WillReturnRows(valueRow(now, "enc:keyfile", "{finance}"))
	_, err = s.ConnectionValue(ctx, "pw", "grid")
	require.ErrorContains(t, err, "clear its allow_personas")

	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("nope").WillReturnError(sql.ErrNoRows)
	_, err = s.ConnectionValue(ctx, "nope", "grid")
	require.ErrorContains(t, err, `secret "nope" does not exist`)

	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("down").WillReturnError(errors.New("down"))
	_, err = s.ConnectionValue(ctx, "down", "grid")
	require.ErrorContains(t, err, `reading secret "down"`)
	require.NoError(t, mock.ExpectationsWereMet())
}
