package sitetile

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKey(t *testing.T) {
	assert.Equal(t, "tiles/sites/s1/qa%20one/3.png", Key("tiles/", "s1", "qa one", 3))
}

func TestStore(t *testing.T) {
	assert.Nil(t, NewStore(nil))
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	st := NewStore(db)
	ctx := context.Background()

	mock.ExpectQuery(`WITH due AS`).WithArgs(Renderer, float64(60), 5, 3).
		WillReturnRows(sqlmock.NewRows([]string{"site_id", "environment", "attempt_release", "attempts"}).AddRow("s1", "prod", 4, 1))
	work, err := st.Claim(ctx, Renderer, time.Minute, 5, 3)
	require.NoError(t, err)
	assert.Equal(t, []Work{{SiteID: "s1", Environment: "prod", Release: 4, Attempts: 1}}, work)
	mock.ExpectQuery(`WITH due AS`).WillReturnError(errors.New("db down"))
	_, err = st.Claim(ctx, Renderer, time.Minute, 5, 3)
	require.Error(t, err)

	mock.ExpectExec(`UPDATE site_tiles SET drawn_release = \$3, tile_key = \$4`).WithArgs("s1", "prod", 4, "k", Renderer).WillReturnResult(sqlmock.NewResult(0, 1))
	one := Work{SiteID: "s1", Environment: "prod", Release: 4}
	require.NoError(t, st.Record(ctx, one, "k", Renderer))
	mock.ExpectExec(`failure = \$4`).WithArgs("s1", "prod", 4, "no page", Renderer).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.RecordFailure(ctx, one, "no page", Renderer))
	mock.ExpectExec(`SET lease_until = now\(\)`).WithArgs("s1", "prod", float64(30)).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.Hold(ctx, "s1", "prod", 30*time.Second))

	mock.ExpectExec(`SET drawn_release = NULL, tile_key = '', attempt_release = NULL, attempts = 0 WHERE site_id = \$1 AND environment = \$2 AND tile_key = \$3`).
		WithArgs("s1", "prod", "k").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, st.Forget(ctx, "s1", "prod", "k"))

	mock.ExpectQuery(`SELECT drawn_release, tile_key, failure FROM site_tiles`).WithArgs("s1", "prod").
		WillReturnRows(sqlmock.NewRows([]string{"r", "k", "f"}).AddRow(4, "k", ""))
	got, err := st.Get(ctx, "s1", "prod")
	require.NoError(t, err)
	assert.Equal(t, &Tile{Release: 4, Key: "k"}, got)
	mock.ExpectQuery(`SELECT drawn_release`).WillReturnRows(sqlmock.NewRows([]string{"r", "k", "f"}))
	_, err = st.Get(ctx, "s1", "dev")
	require.ErrorIs(t, err, ErrNoTile)

	for _, fail := range []func() error{
		func() error { return st.Record(ctx, one, "k", 1) },
		func() error { return st.RecordFailure(ctx, one, "r", 1) },
		func() error { return st.Hold(ctx, "s", "e", time.Second) },
		func() error { return st.Forget(ctx, "s", "e", "k") },
	} {
		mock.ExpectExec(`UPDATE site_tiles`).WillReturnError(errors.New("db down"))
		require.Error(t, fail())
	}
	mock.ExpectQuery(`SELECT drawn_release`).WillReturnError(errors.New("db down"))
	_, err = st.Get(ctx, "s", "e")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
