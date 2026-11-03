//go:build integration

package whpgstate

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// A listener's state round-trips through webhook_pg_listeners, a replica's
// stop leaves another replica's claim alone, and a write for a source that
// was deleted changes nothing.
func TestStates_RealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO webhook_sources (name, connection_name) VALUES ('orders-outbox', 'scratch')`)
	require.NoError(t, err)
	s := New(db)
	assert.Nil(t, New(nil))

	st, err := s.Get(ctx, "orders-outbox")
	require.NoError(t, err)
	assert.Equal(t, State{}, st, "no replica has listened")

	since := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	require.NoError(t, s.Failed(ctx, "orders-outbox", "connection refused", since.Add(-time.Minute)))
	require.NoError(t, s.Listening(ctx, "orders-outbox", "r1", since))
	require.NoError(t, s.Filed(ctx, "orders-outbox", 3, 1, since.Add(time.Second)))
	require.NoError(t, s.Filed(ctx, "orders-outbox", 0, 2, since.Add(2*time.Second)))
	st, err = s.Get(ctx, "orders-outbox")
	require.NoError(t, err)
	assert.Equal(t, "r1", st.Replica)
	assert.True(t, since.Equal(*st.ListeningSince))
	assert.True(t, since.Add(time.Second).Equal(*st.LastEventAt), "a delivery that filed nothing leaves the last event")
	assert.Equal(t, int64(3), st.Delivered)
	assert.Equal(t, int64(3), st.Rejected)
	assert.Empty(t, st.LastError, "listening clears the last failure")

	require.NoError(t, s.Stopped(ctx, "orders-outbox", "r2"))
	st, _ = s.Get(ctx, "orders-outbox")
	assert.Equal(t, "r1", st.Replica, "another replica's stop leaves the claim")
	require.NoError(t, s.Stopped(ctx, "orders-outbox", "r1"))
	st, _ = s.Get(ctx, "orders-outbox")
	assert.Empty(t, st.Replica)
	assert.Nil(t, st.ListeningSince)

	_, err = db.ExecContext(ctx, `DELETE FROM webhook_sources WHERE name = 'orders-outbox'`)
	require.NoError(t, err)
	require.NoError(t, s.Failed(ctx, "orders-outbox", "gone", since), "a deleted source's state write is dropped")
	require.NoError(t, s.Listening(ctx, "orders-outbox", "r1", since))
	st, err = s.Get(ctx, "orders-outbox")
	require.NoError(t, err)
	assert.Equal(t, State{}, st)
}
