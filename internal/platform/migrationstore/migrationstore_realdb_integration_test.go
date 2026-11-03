//go:build integration

package migrationstore_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/migrationstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

func TestStoreRoundTrip_RealDB(t *testing.T) {
	ctx := context.Background()
	s := migrationstore.New(testdb.New(t))

	m := postgres.Migration{
		Connection: "orders", Schema: "public", Version: 2, Name: "items",
		Up: "CREATE TABLE items (id int)", Down: "DROP TABLE items", Transactional: true,
		Checksum: postgres.ChecksumOf("CREATE TABLE items (id int)", "DROP TABLE items", true), Author: "a@example.com",
	}
	replaced, err := s.Put(ctx, m)
	require.NoError(t, err)
	assert.False(t, replaced)
	first := m
	first.Version, first.Name = 1, "orders"
	_, err = s.Put(ctx, first)
	require.NoError(t, err)
	_, err = s.Put(ctx, postgres.Migration{Connection: "orders", Schema: "other", Version: 1, Name: "x", Up: "SELECT 1", Checksum: "c"})
	require.NoError(t, err)

	m.Up, m.Checksum = "CREATE TABLE items (id bigint)", "changed"
	replaced, err = s.Put(ctx, m)
	require.NoError(t, err)
	assert.True(t, replaced, "a second put of a version replaces it")

	got, err := s.List(ctx, "orders", "public")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 1, got[0].Version, "version order")
	assert.Equal(t, "CREATE TABLE items (id bigint)", got[1].Up)
	assert.Equal(t, "changed", got[1].Checksum)
	assert.True(t, got[1].Transactional)
	assert.False(t, got[1].CreatedAt.IsZero())

	empty, err := s.List(ctx, "nothing", "public")
	require.NoError(t, err)
	assert.NotNil(t, empty, "an empty list is [], never null")

	for _, e := range []postgres.MigrationEvent{
		{Connection: "orders", Schema: "public", Version: 1, Event: postgres.EventAdded, Actor: "a@example.com"},
		{Connection: "orders", Schema: "public", Version: 1, Event: postgres.EventApplied, Actor: "a@example.com", DurationMS: 12, Checksum: "c"},
		{Connection: "orders", Schema: "public", Version: 2, Event: postgres.EventFailed, Detail: "applied: migration 2: SQLSTATE 42P07"},
	} {
		require.NoError(t, s.Record(ctx, e))
	}
	events, err := s.Events(ctx, "orders", "public", 2)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, postgres.EventFailed, events[0].Event, "newest first")
	assert.Equal(t, int64(12), events[1].DurationMS)
	assert.False(t, events[0].At.IsZero())

	require.Error(t, s.Record(ctx, postgres.MigrationEvent{Connection: "orders", Schema: "public", Version: 1, Event: "renamed"}),
		"the table holds only the events it knows")
}
