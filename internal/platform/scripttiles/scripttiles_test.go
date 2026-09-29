package scripttiles

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgres(db), mock
}

var errBoom = errors.New("boom")

func TestKeys(t *testing.T) {
	assert.Equal(t, "p/scripts/s1/tile.png", Key("p", "s1", VariantLight))
	assert.Equal(t, "p/scripts/s1/tile-dark.png", Key("p", "s1", VariantDark))
	assert.Equal(t, "scripts/s1/tile.png", Key("", "s1", VariantLight))
	assert.Equal(t, "p/scripts/s1/tile-dark.png", DarkKey("p/scripts/s1/tile.png"))
}

func TestClaim(t *testing.T) {
	s, mock := newStore(t)
	mock.ExpectQuery(regexp.QuoteMeta("WITH owed AS")).WithArgs(2, float64(60), 5).
		WillReturnRows(sqlmock.NewRows([]string{"script_id"}).AddRow("s1"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT s.id, s.name")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "version", "source_code", "library", "s3_key", "attempts"}).
			AddRow("s1", "daily", 3, "x = 1", false, "", 1).
			AddRow("l1", "date-windows", 2, "def f():\n    return 1\n", true, "", 1))
	work, err := s.Claim(context.Background(), 2, time.Minute, 5)
	require.NoError(t, err)
	assert.Equal(t, []Work{
		{ScriptID: "s1", Name: "daily", Version: 3, Source: "x = 1", Attempts: 1},
		{ScriptID: "l1", Name: "date-windows", Version: 2, Source: "def f():\n    return 1\n", Library: true, Attempts: 1},
	}, work)

	mock.ExpectQuery(regexp.QuoteMeta("WITH owed AS")).WillReturnRows(sqlmock.NewRows([]string{"script_id"}))
	work, err = s.Claim(context.Background(), 2, time.Minute, 5)
	require.NoError(t, err)
	assert.Equal(t, []Work{}, work, "nothing owed is an empty list")

	mock.ExpectQuery(regexp.QuoteMeta("WITH owed AS")).WillReturnError(errBoom)
	_, err = s.Claim(context.Background(), 2, time.Minute, 5)
	assert.ErrorIs(t, err, errBoom)

	mock.ExpectQuery(regexp.QuoteMeta("WITH owed AS")).WillReturnRows(sqlmock.NewRows([]string{"script_id"}).AddRow("s1"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT s.id, s.name")).WillReturnError(errBoom)
	_, err = s.Claim(context.Background(), 2, time.Minute, 5)
	assert.ErrorIs(t, err, errBoom)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWrites(t *testing.T) {
	s, mock := newStore(t)
	ctx := context.Background()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE script_tiles\n   SET version = $2")).WithArgs("s1", 3, "k", 2).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Record(ctx, "s1", 3, "k", 2))
	mock.ExpectExec(regexp.QuoteMeta("SET failure = $3")).WithArgs("s1", 3, "why").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.RecordFailure(ctx, "s1", 3, "why"))
	mock.ExpectExec(regexp.QuoteMeta("SET claimed_until = NOW()")).WithArgs("s1", float64(30), 2).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Hold(ctx, "s1", 30*time.Second, 2))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM script_tiles")).WithArgs("s1").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, s.Forget(ctx, "s1"))

	for _, run := range []func() error{
		func() error { return s.Record(ctx, "s1", 3, "k", 2) },
		func() error { return s.RecordFailure(ctx, "s1", 3, "why") },
		func() error { return s.Hold(ctx, "s1", time.Second, 1) },
		func() error { return s.Forget(ctx, "s1") },
	} {
		mock.ExpectExec(".").WillReturnError(errBoom)
		assert.ErrorIs(t, run(), errBoom)
	}
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOrphans(t *testing.T) {
	s, mock := newStore(t)
	mock.ExpectQuery(regexp.QuoteMeta("NOT EXISTS")).WithArgs(10).
		WillReturnRows(sqlmock.NewRows([]string{"script_id", "s3_key"}).AddRow("gone", "k"))
	got, err := s.Orphans(context.Background(), 10)
	require.NoError(t, err)
	assert.Equal(t, []Orphan{{ScriptID: "gone", Key: "k"}}, got)
	mock.ExpectQuery(regexp.QuoteMeta("NOT EXISTS")).WillReturnError(errBoom)
	_, err = s.Orphans(context.Background(), 10)
	assert.ErrorIs(t, err, errBoom)
}

type fakeObjects struct {
	key string
	err error
}

func (f *fakeObjects) GetObject(_ context.Context, _, key string) (body []byte, contentType string, err error) {
	f.key = key
	return []byte("png"), "image/png", f.err
}

func TestReader(t *testing.T) {
	s, mock := newStore(t)
	objects := &fakeObjects{}
	r := NewReader(s, objects, "b")
	ctx := context.Background()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT t.s3_key, t.version = s.version")).WithArgs("s1").
		WillReturnRows(sqlmock.NewRows([]string{"s3_key", "current"}).AddRow("p/scripts/s1/tile.png", true))
	data, current, err := r.Tile(ctx, "s1", VariantDark)
	require.NoError(t, err)
	assert.Equal(t, []byte("png"), data)
	assert.True(t, current)
	assert.Equal(t, "p/scripts/s1/tile-dark.png", objects.key)

	// A version saved since the tile was drawn: the older tile, not current.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT t.s3_key")).
		WillReturnRows(sqlmock.NewRows([]string{"s3_key", "current"}).AddRow("p/scripts/s1/tile.png", false))
	_, current, err = r.Tile(ctx, "s1", VariantLight)
	require.NoError(t, err)
	assert.False(t, current)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT t.s3_key")).WillReturnRows(sqlmock.NewRows([]string{"s3_key", "current"}))
	_, _, err = r.Tile(ctx, "s1", VariantLight)
	assert.ErrorIs(t, err, ErrNoTile)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT t.s3_key")).WillReturnError(errBoom)
	_, _, err = r.Tile(ctx, "s1", VariantLight)
	assert.ErrorIs(t, err, errBoom)

	objects.err = errBoom
	mock.ExpectQuery(regexp.QuoteMeta("SELECT t.s3_key")).WillReturnRows(sqlmock.NewRows([]string{"s3_key", "current"}).AddRow("k", true))
	_, _, err = r.Tile(ctx, "s1", VariantLight)
	assert.ErrorIs(t, err, errBoom)
}
