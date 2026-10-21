//go:build integration

package advisorylock

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// The production keys of the loops that held a lock at once on the replica
// #2169 reported, and the others that take one, more locks than the pool has
// connections.
var loopKeys = []int64{
	4713210201, // catalog scan
	4713210301, // maps
	4713210402, // storage capacity alerts
	4713210403, // webhook receiver alerts
	4713210401, // capacity listing
	4713210001, // call catalog sweep
}

// TestRealDB_LockedPassesFinishOnASmallPool is #2169: a pool of five, six
// passes each holding its lock while it runs its statements, and a request on
// the side. With a session per lock, five locks held every connection, the
// sixth waited for one, and no pass could run a statement; on the shared
// session every pass finishes and the request is served while they run.
func TestRealDB_LockedPassesFinishOnASmallPool(t *testing.T) {
	db := testdb.New(t)
	db.SetMaxOpenConns(5)
	s := New(db)
	baseline := db.Stats().InUse // what the harness itself holds
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		holding sync.WaitGroup
		done    sync.WaitGroup
	)
	allHeld := make(chan struct{})
	holding.Add(len(loopKeys))
	for _, key := range loopKeys {
		done.Go(func() {
			release, ok, err := s.Try(ctx, ID(key))
			holding.Done()
			if !assert.NoError(t, err) || !assert.True(t, ok, "key %d", key) {
				return
			}
			defer release()
			<-allHeld
			assert.NoError(t, pass(ctx, db), "key %d", key)
		})
	}
	holding.Wait()
	assert.Equal(t, 1, db.Stats().InUse-baseline, "six locks held on one connection")
	close(allHeld)

	var answer int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT 1").Scan(&answer), "a request while every pass runs")
	done.Wait()
	require.NoError(t, ctx.Err(), "the passes finished")
	assert.Equal(t, 0, db.Stats().InUse-baseline, "the session went back to the pool with the last lock")
}

// pass runs a few statements one after another, each on a connection from
// the pool.
func pass(ctx context.Context, db *sql.DB) error {
	for range 3 {
		if _, err := db.ExecContext(ctx, "SELECT pg_sleep(0.05)"); err != nil {
			return err
		}
	}
	return nil
}
