package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	memTestTTL          = 5 * time.Minute
	memTestShortTTL     = 50 * time.Millisecond
	memTestGoroutines   = 10
	memTestIterations   = 100
	memTestCleanupSleep = 150 * time.Millisecond
	memTestSess1        = "sess-1"
)

func newTestSession(id string, ttl time.Duration) *Session {
	now := time.Now()
	return &Session{
		ID:           id,
		UserID:       "user-" + id,
		CreatedAt:    now,
		LastActiveAt: now,
		ExpiresAt:    now.Add(ttl),
		State:        make(map[string]any),
	}
}

func TestMemoryStore_CreateAndGet(t *testing.T) {
	store := NewMemoryStore(memTestTTL)
	ctx := context.Background()

	sess := newTestSession(memTestSess1, memTestTTL)
	require.NoError(t, store.Create(ctx, sess))

	got, err := store.Get(ctx, memTestSess1)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, memTestSess1, got.ID)
	assert.Equal(t, "user-sess-1", got.UserID)
}

func TestMemoryStore_GetNotFound(t *testing.T) {
	store := NewMemoryStore(memTestTTL)
	ctx := context.Background()

	got, err := store.Get(ctx, "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestMemoryStore_GetExpired(t *testing.T) {
	store := NewMemoryStore(memTestShortTTL)
	ctx := context.Background()

	sess := newTestSession(memTestSess1, memTestShortTTL)
	require.NoError(t, store.Create(ctx, sess))

	time.Sleep(2 * memTestShortTTL)

	got, err := store.Get(ctx, memTestSess1)
	require.NoError(t, err)
	assert.Nil(t, got, "expired session should return nil")
}

func TestMemoryStore_Touch(t *testing.T) {
	store := NewMemoryStore(memTestTTL)
	ctx := context.Background()

	sess := newTestSession(memTestSess1, memTestTTL)
	originalExpiry := sess.ExpiresAt
	require.NoError(t, store.Create(ctx, sess))

	time.Sleep(10 * time.Millisecond)
	require.NoError(t, store.Touch(ctx, memTestSess1))

	got, err := store.Get(ctx, memTestSess1)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.True(t, got.ExpiresAt.After(originalExpiry), "Touch should extend ExpiresAt")
	assert.True(t, got.LastActiveAt.After(sess.CreatedAt), "Touch should update LastActiveAt")
}

func TestMemoryStore_TouchNonexistent(t *testing.T) {
	store := NewMemoryStore(memTestTTL)
	ctx := context.Background()

	err := store.Touch(ctx, "nonexistent")
	assert.NoError(t, err, "Touch on nonexistent session should not error")
}

func TestMemoryStore_Delete(t *testing.T) {
	store := NewMemoryStore(memTestTTL)
	ctx := context.Background()

	sess := newTestSession(memTestSess1, memTestTTL)
	require.NoError(t, store.Create(ctx, sess))

	require.NoError(t, store.Delete(ctx, memTestSess1))

	got, err := store.Get(ctx, memTestSess1)
	require.NoError(t, err)
	assert.Nil(t, got, "deleted session should return nil")
}

func TestMemoryStore_List(t *testing.T) {
	store := NewMemoryStore(memTestTTL)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, newTestSession(memTestSess1, memTestTTL)))
	require.NoError(t, store.Create(ctx, newTestSession("sess-2", memTestTTL)))
	require.NoError(t, store.Create(ctx, newTestSession("expired", -time.Second)))

	sessions, err := store.List(ctx)
	require.NoError(t, err)
	assert.Len(t, sessions, 2, "List should exclude expired sessions")
}

func TestMemoryStore_UpdateState(t *testing.T) {
	store := NewMemoryStore(memTestTTL)
	ctx := context.Background()

	sess := newTestSession(memTestSess1, memTestTTL)
	require.NoError(t, store.Create(ctx, sess))

	require.NoError(t, store.UpdateState(ctx, memTestSess1, map[string]any{
		"key1": "value1",
	}))

	got, err := store.Get(ctx, memTestSess1)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "value1", got.State["key1"])

	// Merge additional state
	require.NoError(t, store.UpdateState(ctx, memTestSess1, map[string]any{
		"key2": "value2",
	}))

	got, err = store.Get(ctx, memTestSess1)
	require.NoError(t, err)
	assert.Equal(t, "value1", got.State["key1"], "existing key should remain")
	assert.Equal(t, "value2", got.State["key2"], "new key should be merged")
}

func TestMemoryStore_UpdateStateNonexistent(t *testing.T) {
	store := NewMemoryStore(memTestTTL)
	ctx := context.Background()

	err := store.UpdateState(ctx, "nonexistent", map[string]any{"k": "v"})
	assert.NoError(t, err)
}

func TestMemoryStore_UpdateStateNilState(t *testing.T) {
	store := NewMemoryStore(memTestTTL)
	ctx := context.Background()

	sess := newTestSession(memTestSess1, memTestTTL)
	sess.State = nil
	require.NoError(t, store.Create(ctx, sess))

	require.NoError(t, store.UpdateState(ctx, memTestSess1, map[string]any{"k": "v"}))

	got, err := store.Get(ctx, memTestSess1)
	require.NoError(t, err)
	assert.Equal(t, "v", got.State["k"])
}

func TestMemoryStore_Cleanup(t *testing.T) {
	store := NewMemoryStore(memTestTTL)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, newTestSession("active", memTestTTL)))
	require.NoError(t, store.Create(ctx, newTestSession("expired", -time.Second)))

	require.NoError(t, store.Cleanup(ctx))

	// Active session should remain
	got, err := store.Get(ctx, "active")
	require.NoError(t, err)
	assert.NotNil(t, got)

	// Expired session should be removed
	got, err = store.Get(ctx, "expired")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestMemoryStore_CleanupRoutineLifecycle(t *testing.T) {
	store := NewMemoryStore(memTestShortTTL)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, newTestSession(memTestSess1, memTestShortTTL)))

	store.StartCleanupRoutine(20 * time.Millisecond)

	time.Sleep(memTestCleanupSleep)

	sessions, err := store.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, sessions, "cleanup should have removed expired session")

	assert.NoError(t, store.Close())
}

func TestMemoryStore_CloseWithoutStart(t *testing.T) {
	store := NewMemoryStore(memTestTTL)
	assert.NoError(t, store.Close(), "Close without StartCleanupRoutine should not panic")
}

func TestMemoryStore_ConcurrentAccess(_ *testing.T) {
	store := NewMemoryStore(memTestTTL)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := range memTestGoroutines {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for range memTestIterations {
				sess := newTestSession("sess-concurrent", memTestTTL)
				_ = store.Create(ctx, sess)
				_, _ = store.Get(ctx, "sess-concurrent")
				_ = store.Touch(ctx, "sess-concurrent")
				_ = store.UpdateState(ctx, "sess-concurrent", map[string]any{"n": n})
				_, _ = store.List(ctx)
				_ = store.Cleanup(ctx)
			}
		}(i)
	}
	wg.Wait()
}
