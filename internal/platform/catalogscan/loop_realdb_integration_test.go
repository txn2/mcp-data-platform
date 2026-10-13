//go:build integration

package catalogscan

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestLoopHonorsARequestMadeDuringAScan_RealDB is the regression for a scan
// request lost while a scan was running: connections are scanned one after
// another, so a request made mid-scan is older than the last connection's
// start and was read as already served. The whole scan's start is what a
// request is compared with.
func TestLoopHonorsARequestMadeDuringAScan_RealDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	settings := NewStore(db)
	l := NewLoop(New(&fakeWriter{}, settings, settings), settings, db)

	ran, err := l.lockedScan(ctx)
	require.NoError(t, err)
	assert.True(t, ran, "a deployment never scanned is due")

	ran, err = l.lockedScan(ctx)
	require.NoError(t, err)
	assert.False(t, ran, "a scan that just started is not due again")

	// A request made after that scan started, as one made during it would be.
	require.NoError(t, l.ScanNow(ctx, "admin@example.com"))
	ran, err = l.lockedScan(ctx)
	require.NoError(t, err)
	assert.True(t, ran, "a request newer than the scan's start owes a scan")

	ran, err = l.lockedScan(ctx)
	require.NoError(t, err)
	assert.False(t, ran, "the request was served")
}
