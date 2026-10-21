//go:build integration

package advisorylock_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/connlimit"
	"github.com/txn2/mcp-data-platform/internal/dbobs"
	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
	"github.com/txn2/mcp-data-platform/internal/platform/storagecap"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/internal/webhook/whalert"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// TestRealDB_TheLoopsShareOneConnection holds, all at once and on a pool of
// the platform's minimum, the locks of the two loops v1.147.0 added (storage
// capacity and webhook alerts, through levelalert.Locked), two connlimit
// slots and a connoauth refresh lock, and serves a request while they are
// held: between them they hold one connection (#2169).
func TestRealDB_TheLoopsShareOneConnection(t *testing.T) {
	db := testdb.New(t)
	db.SetMaxOpenConns(dbobs.MinOpenConns)
	baseline := db.Stats().InUse
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	release := make(chan struct{})
	var held, done sync.WaitGroup
	for _, key := range []int64{storagecap.LockKey, whalert.LockKey} {
		held.Add(1)
		done.Go(func() {
			err := levelalert.Locked(ctx, db, key, func(ctx context.Context) error {
				held.Done()
				<-release
				var n int
				return db.QueryRowContext(ctx, "SELECT count(*) FROM alert_conditions").Scan(&n)
			})
			assert.NoError(t, err, "key %d", key)
		})
	}
	limiter := connlimit.New(db, nil)
	var slots []*connlimit.Slot
	for range 2 {
		slot, _, err := limiter.Acquire(ctx, "api", "crm", 2)
		require.NoError(t, err)
		slots = append(slots, slot)
	}
	unlock, ok, err := connoauth.NewPostgresLocker(db).TryLock(ctx, connoauth.Key{Kind: connoauth.KindAPI, Name: "crm"})
	require.NoError(t, err)
	require.True(t, ok)
	held.Wait()

	assert.Equal(t, 1, db.Stats().InUse-baseline, "two loop locks, two slots and a refresh lock on one connection")
	var answer int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT 1").Scan(&answer), "a request while they are held")

	close(release)
	done.Wait()
	for _, s := range slots {
		s.Release()
	}
	unlock()
	require.NoError(t, ctx.Err())
	assert.Equal(t, 0, db.Stats().InUse-baseline, "the session went back to the pool")
}
