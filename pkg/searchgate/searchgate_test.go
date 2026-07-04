package searchgate

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStore_MarkAndHas(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore(30 * time.Minute)

	ok, err := s.HasDiscovered(ctx, "sess")
	require.NoError(t, err)
	assert.False(t, ok, "unknown session has not discovered")

	require.NoError(t, s.MarkDiscovered(ctx, "sess"))

	ok, err = s.HasDiscovered(ctx, "sess")
	require.NoError(t, err)
	assert.True(t, ok, "marked session has discovered")

	ok, err = s.HasDiscovered(ctx, "other")
	require.NoError(t, err)
	assert.False(t, ok, "sessions are isolated")
}

func TestMemoryStore_Expiry(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore(20 * time.Millisecond)

	require.NoError(t, s.MarkDiscovered(ctx, "sess"))
	ok, _ := s.HasDiscovered(ctx, "sess")
	require.True(t, ok)

	time.Sleep(40 * time.Millisecond)

	ok, err := s.HasDiscovered(ctx, "sess")
	require.NoError(t, err)
	assert.False(t, ok, "discovery record expires after the TTL")
}

func TestMemoryStore_Cleanup(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore(20 * time.Millisecond)

	require.NoError(t, s.MarkDiscovered(ctx, "a"))
	require.NoError(t, s.MarkDiscovered(ctx, "b"))
	time.Sleep(40 * time.Millisecond)

	require.NoError(t, s.Cleanup(ctx))

	s.mu.RLock()
	remaining := len(s.m)
	s.mu.RUnlock()
	assert.Equal(t, 0, remaining, "cleanup evicts expired entries")
}

func TestMemoryStore_Close(t *testing.T) {
	assert.NoError(t, NewMemoryStore(time.Minute).Close())
}
