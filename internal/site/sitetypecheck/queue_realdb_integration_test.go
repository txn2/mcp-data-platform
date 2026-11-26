//go:build integration

package sitetypecheck

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestQueue_RealDB_ClaimRunAndRecord is #2793's queue against the real schema:
// a queued check reports how many run before it, is claimed oldest first,
// runs, and records its result and the environment's state; a running check
// whose replica stopped is claimed again; and the summaries name the newest
// check of each release.
func TestQueue_RealDB_ClaimRunAndRecord(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	_, err := db.ExecContext(ctx, `INSERT INTO sites (id, name, owner_email, created_by) VALUES ('site_1', 'docs-2793', 'dana@example.com', 'dana@example.com')`)
	require.NoError(t, err)
	checker := NewChecker(Config{Dir: t.TempDir()}, &fakeGetter{}, Cache{})
	var asked []int
	q := NewQueue(db, checker, func(_ context.Context, _ string, release int) (fs.FS, error) {
		asked = append(asked, release)
		// No tsconfig: the check ends unsupported without the compiler.
		return fstest.MapFS{"src/a.ts": {Data: []byte("x")}}, nil
	})

	var first int64
	first, err = q.Enqueue(ctx, Request{SiteID: "site_1", Release: 1, Environment: "dev", Tsconfig: "tsconfig.json", By: "dana@example.com"})
	require.NoError(t, err)
	_, err = q.Enqueue(ctx, Request{SiteID: "site_1", Release: 2, Environment: "dev", Tsconfig: "tsconfig.json", By: "dana@example.com"})
	require.NoError(t, err)
	second, err := q.Latest(ctx, "site_1", 2)
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, StatusQueued, second.Status)
	assert.Equal(t, 1, second.Ahead, "one check runs before it")

	require.NoError(t, q.runQueued(ctx))
	assert.Equal(t, []int{1, 2}, asked, "oldest first")
	done, err := q.Latest(ctx, "site_1", 1)
	require.NoError(t, err)
	assert.Equal(t, first, done.ID)
	assert.Equal(t, StatusUnsupported, done.Status)
	require.NotNil(t, done.Result)
	assert.Contains(t, done.Result.Message, "does not have")
	assert.NotNil(t, done.FinishedAt)

	// A check left running by a stopped replica is claimed again.
	_, err = db.ExecContext(ctx, `INSERT INTO site_typechecks (site_id, release_number, environment, tsconfig, status, claimed_until)
		VALUES ('site_1', 3, 'dev', 'tsconfig.json', 'running', NOW() - interval '1 minute')`)
	require.NoError(t, err)
	require.NoError(t, q.runQueued(ctx))
	third, err := q.Latest(ctx, "site_1", 3)
	require.NoError(t, err)
	assert.Equal(t, StatusUnsupported, third.Status)

	// The environment's state is kept by finish.
	require.NoError(t, q.finish(ctx, done, Result{Status: StatusClean}, []byte("tsbuildinfo")))
	var state []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT tsbuildinfo FROM site_typecheck_state WHERE site_id = 'site_1' AND environment = 'dev'`).Scan(&state))
	assert.Equal(t, "tsbuildinfo", string(state))

	sums, err := q.Summaries(ctx, "site_1")
	require.NoError(t, err)
	assert.Equal(t, StatusClean, sums[1].Status)
	assert.Equal(t, StatusUnsupported, sums[2].Status)
	none, err := q.Latest(ctx, "site_1", 99)
	require.NoError(t, err)
	assert.Nil(t, none)

	// Deleting the site deletes its checks and its state (#2828).
	_, err = db.ExecContext(ctx, `DELETE FROM sites WHERE id = 'site_1'`)
	require.NoError(t, err)
	var left int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM site_typechecks WHERE site_id = 'site_1') +
		(SELECT count(*) FROM site_typecheck_state WHERE site_id = 'site_1')`).Scan(&left))
	assert.Zero(t, left, "a deleted site's type checks and state are left behind")
}
