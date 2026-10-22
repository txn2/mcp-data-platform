//go:build integration

package accessreq

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestStoreRealDB holds the SQL to what the route tests' memory store keeps:
// one pending request per requester and item, MaxPerDay a day, an answer
// recorded once, an owner's list of live requests.
func TestStoreRealDB(t *testing.T) {
	ctx := context.Background()
	s := NewStore(testdb.New(t))
	base := Request{
		TargetType: TargetAsset, TargetID: "a1", RequesterID: "u1", RequesterEmail: "u1@example.com",
		OwnerEmail: "Owner@Example.com", ExpiresAt: time.Now().Add(time.Hour),
	}

	first := base
	first.ID = "r1"
	got, created, err := s.Open(ctx, first)
	require.NoError(t, err)
	require.True(t, created)
	second := base
	second.ID = "r2"
	again, created, err := s.Open(ctx, second)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, got.ID, again.ID)

	list, err := s.ListForOwner(ctx, "owner@example.com", "", "", false)
	require.NoError(t, err)
	require.Len(t, list, 1)
	list, err = s.ListForOwner(ctx, "someone@example.com", TargetAsset, "a1", false)
	require.NoError(t, err)
	assert.Empty(t, list)
	list, err = s.ListForOwner(ctx, "", TargetAsset, "a1", true)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	require.NoError(t, s.Decide(ctx, "r1", Decision{Status: StatusDeclined, DecidedBy: "owner@example.com"}))
	assert.ErrorIs(t, s.Decide(ctx, "r1", Decision{Status: StatusApproved, Permission: "viewer", DecidedBy: "owner@example.com", ShareID: "s1"}), ErrDecided)
	read, err := s.Get(ctx, "r1")
	require.NoError(t, err)
	assert.Equal(t, StatusDeclined, read.Status)
	assert.NotNil(t, read.DecidedAt)

	for i := range MaxPerDay - 1 {
		r := base
		r.ID = "r" + string(rune('3'+i))
		_, created, err := s.Open(ctx, r)
		require.NoError(t, err)
		require.True(t, created)
		require.NoError(t, s.Decide(ctx, r.ID, Decision{Status: StatusDeclined, DecidedBy: "o"}))
	}
	more := base
	more.ID = "r9"
	_, _, err = s.Open(ctx, more)
	assert.ErrorIs(t, err, ErrTooMany)
	_, err = s.Pending(ctx, TargetAsset, "a1", "u1")
	assert.ErrorIs(t, err, ErrNotFound)
}
