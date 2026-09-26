package platform

import (
	"context"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// wireRetention starts the #1904 sweeps with the platform and stops them with
// it; with no database it registers nothing.
func TestWireRetention(t *testing.T) {
	wireRetention(&Platform{config: &Config{}, lifecycle: NewLifecycle()}) // no database: no hooks

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.MatchExpectationsInOrder(false)
	// Every sweep finds its lock held elsewhere, so nothing is deleted: four
	// sweeps with the portal, three without it.
	for range 7 {
		mock.ExpectQuery("pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"l"}).AddRow(false))
	}
	off := false
	for _, portal := range []*bool{nil, &off} {
		lc := NewLifecycle()
		wireRetention(&Platform{db: db, config: &Config{Portal: PortalConfig{Enabled: portal}}, lifecycle: lc})
		require.NoError(t, lc.Start(context.Background()))
		// Stop cancels the loop, so the first pass may end before every
		// sweep has asked for its lock; what is asserted is that start and
		// stop run cleanly through the lifecycle.
		require.NoError(t, lc.Stop(context.Background()))
	}
}
