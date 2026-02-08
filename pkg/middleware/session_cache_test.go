package middleware

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants for session cache tests.
const (
	cacheTestSession1   = "session-1"
	cacheTestTable      = "catalog.schema.table"
	cacheTestTable1     = "table1"
	cacheTestGoroutines = 10
)

func TestSessionEnrichmentCache_MarkAndCheck(t *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)

	// Initially not sent
	assert.False(t, cache.WasSentRecently(cacheTestSession1, cacheTestTable))

	// Mark as sent
	cache.MarkSent(cacheTestSession1, cacheTestTable)

	// Now should be true
	assert.True(t, cache.WasSentRecently(cacheTestSession1, cacheTestTable))

	// Different table in same session
	assert.False(t, cache.WasSentRecently(cacheTestSession1, "catalog.schema.other"))

	// Different session, same table
	assert.False(t, cache.WasSentRecently("session-2", cacheTestTable))
}

func TestSessionEnrichmentCache_TTLExpiry(t *testing.T) {
	// Use very short TTL for testing
	cache := NewSessionEnrichmentCache(50*time.Millisecond, 30*time.Minute)

	cache.MarkSent(cacheTestSession1, cacheTestTable)
	assert.True(t, cache.WasSentRecently(cacheTestSession1, cacheTestTable))

	// Wait for TTL to expire
	time.Sleep(60 * time.Millisecond)

	// Should no longer be "recently" sent
	assert.False(t, cache.WasSentRecently(cacheTestSession1, cacheTestTable))
}

func TestSessionEnrichmentCache_SessionIsolation(t *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)

	cache.MarkSent("session-A", "catalog.schema.users")
	cache.MarkSent("session-B", "catalog.schema.orders")

	// Each session only knows about its own tables
	assert.True(t, cache.WasSentRecently("session-A", "catalog.schema.users"))
	assert.False(t, cache.WasSentRecently("session-A", "catalog.schema.orders"))
	assert.False(t, cache.WasSentRecently("session-B", "catalog.schema.users"))
	assert.True(t, cache.WasSentRecently("session-B", "catalog.schema.orders"))
}

func TestSessionEnrichmentCache_SessionCount(t *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)

	assert.Equal(t, 0, cache.SessionCount())

	cache.MarkSent(cacheTestSession1, cacheTestTable1)
	assert.Equal(t, 1, cache.SessionCount())

	cache.MarkSent("session-2", cacheTestTable1)
	assert.Equal(t, 2, cache.SessionCount())

	// Same session again doesn't create new entry
	cache.MarkSent(cacheTestSession1, "table2")
	assert.Equal(t, 2, cache.SessionCount())
}

func TestSessionEnrichmentCache_IdleSessionCleanup(t *testing.T) {
	// Very short timeouts for testing
	cache := NewSessionEnrichmentCache(50*time.Millisecond, 100*time.Millisecond)

	cache.MarkSent(cacheTestSession1, cacheTestTable1)
	cache.MarkSent("session-2", cacheTestTable1)
	require.Equal(t, 2, cache.SessionCount())

	// Wait for session timeout
	time.Sleep(150 * time.Millisecond)

	// Trigger cleanup manually
	cache.cleanup()

	assert.Equal(t, 0, cache.SessionCount())
}

func TestSessionEnrichmentCache_CleanupRemovesExpiredEntries(t *testing.T) {
	cache := NewSessionEnrichmentCache(50*time.Millisecond, 10*time.Second)

	cache.MarkSent(cacheTestSession1, cacheTestTable1)
	assert.True(t, cache.WasSentRecently(cacheTestSession1, cacheTestTable1))

	// Wait for entry TTL to expire but not session timeout
	time.Sleep(60 * time.Millisecond)

	cache.cleanup()

	// Session still exists (not timed out) but entry is removed
	assert.Equal(t, 1, cache.SessionCount())
	assert.False(t, cache.WasSentRecently(cacheTestSession1, cacheTestTable1))
}

func TestSessionEnrichmentCache_StartAndStopCleanup(t *testing.T) {
	cache := NewSessionEnrichmentCache(50*time.Millisecond, 100*time.Millisecond)

	cache.MarkSent(cacheTestSession1, cacheTestTable1)
	require.Equal(t, 1, cache.SessionCount())

	// Start background cleanup with short interval
	cache.StartCleanup(50 * time.Millisecond)

	// Wait for session timeout + cleanup cycle
	time.Sleep(200 * time.Millisecond)

	assert.Equal(t, 0, cache.SessionCount())

	// Stop should not panic
	cache.Stop()
}

func TestSessionEnrichmentCache_ConcurrentAccess(_ *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)

	done := make(chan struct{})
	for i := range cacheTestGoroutines {
		go func(_ int) {
			defer func() { done <- struct{}{} }()
			sessionID := "session"
			for range 100 {
				cache.MarkSent(sessionID, "table")
				cache.WasSentRecently(sessionID, "table")
				cache.SessionCount()
			}
		}(i)
	}

	for range cacheTestGoroutines {
		<-done
	}
}
