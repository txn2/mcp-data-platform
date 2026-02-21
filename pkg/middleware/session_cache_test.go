package middleware

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants for session cache tests.
const (
	cacheTestSession1      = "session-1"
	cacheTestTable         = "catalog.schema.table"
	cacheTestTable1        = "table1"
	cacheTestGoroutines    = 10
	cacheTestLoadedSession = "loaded-session"
	cacheTestMergeSession  = "merge-session"
	cacheTestSessionA      = "session-A"
	cacheTestSessionB      = "session-B"
	cacheTestUsersTable    = "catalog.schema.users"
	cacheTestOrdersTable   = "catalog.schema.orders"
	cacheTestSess1         = "sess-1"
	cacheTestTokenCount    = 250
)

func TestSessionEnrichmentCache_MarkAndCheck(t *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)

	// Initially not sent
	assert.False(t, cache.WasSentRecently(cacheTestSession1, cacheTestTable))

	// Mark as sent
	cache.MarkSent(cacheTestSession1, cacheTestTable, cacheTestTokenCount)

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

	cache.MarkSent(cacheTestSession1, cacheTestTable, 0)
	assert.True(t, cache.WasSentRecently(cacheTestSession1, cacheTestTable))

	// Wait for TTL to expire
	time.Sleep(60 * time.Millisecond)

	// Should no longer be "recently" sent
	assert.False(t, cache.WasSentRecently(cacheTestSession1, cacheTestTable))
}

func TestSessionEnrichmentCache_SessionIsolation(t *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)

	cache.MarkSent(cacheTestSessionA, cacheTestUsersTable, 100)
	cache.MarkSent(cacheTestSessionB, cacheTestOrdersTable, 200)

	// Each session only knows about its own tables
	assert.True(t, cache.WasSentRecently(cacheTestSessionA, cacheTestUsersTable))
	assert.False(t, cache.WasSentRecently(cacheTestSessionA, cacheTestOrdersTable))
	assert.False(t, cache.WasSentRecently(cacheTestSessionB, cacheTestUsersTable))
	assert.True(t, cache.WasSentRecently(cacheTestSessionB, cacheTestOrdersTable))
}

func TestSessionEnrichmentCache_SessionCount(t *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)

	assert.Equal(t, 0, cache.SessionCount())

	cache.MarkSent(cacheTestSession1, cacheTestTable1, 0)
	assert.Equal(t, 1, cache.SessionCount())

	cache.MarkSent("session-2", cacheTestTable1, 0)
	assert.Equal(t, 2, cache.SessionCount())

	// Same session again doesn't create new entry
	cache.MarkSent(cacheTestSession1, "table2", 0)
	assert.Equal(t, 2, cache.SessionCount())
}

func TestSessionEnrichmentCache_IdleSessionCleanup(t *testing.T) {
	// Very short timeouts for testing
	cache := NewSessionEnrichmentCache(50*time.Millisecond, 100*time.Millisecond)

	cache.MarkSent(cacheTestSession1, cacheTestTable1, 0)
	cache.MarkSent("session-2", cacheTestTable1, 0)
	require.Equal(t, 2, cache.SessionCount())

	// Wait for session timeout
	time.Sleep(150 * time.Millisecond)

	// Trigger cleanup manually
	cache.cleanup()

	assert.Equal(t, 0, cache.SessionCount())
}

func TestSessionEnrichmentCache_CleanupRemovesExpiredEntries(t *testing.T) {
	cache := NewSessionEnrichmentCache(50*time.Millisecond, 10*time.Second)

	cache.MarkSent(cacheTestSession1, cacheTestTable1, 0)
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

	cache.MarkSent(cacheTestSession1, cacheTestTable1, 0)
	require.Equal(t, 1, cache.SessionCount())

	// Start background cleanup with short interval
	cache.StartCleanup(50 * time.Millisecond)

	// Wait for session timeout + cleanup cycle
	time.Sleep(200 * time.Millisecond)

	assert.Equal(t, 0, cache.SessionCount())

	// Stop should not panic
	cache.Stop()
}

func TestSessionEnrichmentCache_LoadSession(t *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)

	now := time.Now()
	sentTables := map[string]SentTableEntry{
		"catalog.schema.table1": {SentAt: now.Add(-1 * time.Minute), TokenCount: 100},
		"catalog.schema.table2": {SentAt: now.Add(-2 * time.Minute), TokenCount: 200},
	}

	cache.LoadSession(cacheTestLoadedSession, sentTables)

	assert.True(t, cache.WasSentRecently(cacheTestLoadedSession, "catalog.schema.table1"))
	assert.True(t, cache.WasSentRecently(cacheTestLoadedSession, "catalog.schema.table2"))
	assert.False(t, cache.WasSentRecently(cacheTestLoadedSession, "catalog.schema.table3"))
	assert.Equal(t, 1, cache.SessionCount())
}

func TestSessionEnrichmentCache_LoadSessionMerges(t *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)

	// Pre-populate
	cache.MarkSent(cacheTestMergeSession, "catalog.schema.existing", 0)

	// Load additional tables
	now := time.Now()
	cache.LoadSession(cacheTestMergeSession, map[string]SentTableEntry{
		"catalog.schema.loaded": {SentAt: now, TokenCount: 150},
	})

	assert.True(t, cache.WasSentRecently(cacheTestMergeSession, "catalog.schema.existing"))
	assert.True(t, cache.WasSentRecently(cacheTestMergeSession, "catalog.schema.loaded"))
}

func TestSessionEnrichmentCache_ExportSessions(t *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)

	cache.MarkSent(cacheTestSessionA, "table1", 100)
	cache.MarkSent(cacheTestSessionA, "table2", 200)
	cache.MarkSent(cacheTestSessionB, "table3", 300)

	exported := cache.ExportSessions()
	require.Len(t, exported, 2)
	assert.Len(t, exported[cacheTestSessionA], 2)
	assert.Len(t, exported[cacheTestSessionB], 1)
	assert.Contains(t, exported[cacheTestSessionA], "table1")
	assert.Contains(t, exported[cacheTestSessionA], "table2")
	assert.Contains(t, exported[cacheTestSessionB], "table3")
	assert.Equal(t, 100, exported[cacheTestSessionA]["table1"].TokenCount)
	assert.Equal(t, 300, exported[cacheTestSessionB]["table3"].TokenCount)
}

func TestSessionEnrichmentCache_ExportSessionsEmpty(t *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)

	exported := cache.ExportSessions()
	assert.Empty(t, exported)
}

func TestSessionEnrichmentCache_LoadExportRoundTrip(t *testing.T) {
	// Create cache, populate, export
	cache1 := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)
	cache1.MarkSent(cacheTestSess1, cacheTestUsersTable, 100)
	cache1.MarkSent(cacheTestSess1, cacheTestOrdersTable, 200)
	cache1.MarkSent("sess-2", "catalog.schema.products", 300)

	exported := cache1.ExportSessions()

	// Create new cache, load from export
	cache2 := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)
	for sessionID, tables := range exported {
		cache2.LoadSession(sessionID, tables)
	}

	// Verify state matches
	assert.True(t, cache2.WasSentRecently(cacheTestSess1, cacheTestUsersTable))
	assert.True(t, cache2.WasSentRecently(cacheTestSess1, cacheTestOrdersTable))
	assert.True(t, cache2.WasSentRecently("sess-2", "catalog.schema.products"))
	assert.False(t, cache2.WasSentRecently("sess-2", cacheTestUsersTable))
	assert.Equal(t, 2, cache2.SessionCount())

	// Verify token counts survived
	assert.Equal(t, 100, cache2.GetTokenCount(cacheTestSess1, cacheTestUsersTable))
	assert.Equal(t, 200, cache2.GetTokenCount(cacheTestSess1, cacheTestOrdersTable))
	assert.Equal(t, 300, cache2.GetTokenCount("sess-2", "catalog.schema.products"))
}

func TestSessionEnrichmentCache_GetTokenCount(t *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)

	// Not found cases
	assert.Equal(t, 0, cache.GetTokenCount("no-session", "table"))
	cache.MarkSent(cacheTestSession1, cacheTestTable, cacheTestTokenCount)
	assert.Equal(t, 0, cache.GetTokenCount(cacheTestSession1, "other-table"))

	// Found case
	assert.Equal(t, cacheTestTokenCount, cache.GetTokenCount(cacheTestSession1, cacheTestTable))
}

func TestSessionEnrichmentCache_TokenCounters(t *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)

	assert.Equal(t, int64(0), cache.TokensFull())
	assert.Equal(t, int64(0), cache.TokensDeduped())

	cache.AddTokensFull(500)
	cache.AddTokensFull(300)
	assert.Equal(t, int64(800), cache.TokensFull())

	cache.AddTokensDeduped(50)
	cache.AddTokensDeduped(25)
	assert.Equal(t, int64(75), cache.TokensDeduped())
}

func TestSessionEnrichmentCache_ConcurrentAccess(_ *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)

	done := make(chan struct{})
	for i := range cacheTestGoroutines {
		go func(_ int) {
			defer func() { done <- struct{}{} }()
			sessionID := "session"
			for range 100 {
				cache.MarkSent(sessionID, "table", 100)
				cache.WasSentRecently(sessionID, "table")
				cache.GetTokenCount(sessionID, "table")
				cache.SessionCount()
			}
		}(i)
	}

	for range cacheTestGoroutines {
		<-done
	}
}
