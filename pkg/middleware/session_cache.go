package middleware

import (
	"sync"
	"time"
)

// DedupMode controls what content is sent for previously-enriched tables.
type DedupMode string

const (
	// DedupModeReference sends a minimal reference (table name, description, note).
	DedupModeReference DedupMode = "reference"

	// DedupModeSummary sends table-level semantic_context without column_context.
	DedupModeSummary DedupMode = "summary"

	// DedupModeNone sends no enrichment at all for repeat queries.
	DedupModeNone DedupMode = "none"
)

// SessionEnrichmentCache tracks which tables have been enriched per client
// session, enabling deduplication of large semantic metadata blocks.
type SessionEnrichmentCache struct {
	mu             sync.RWMutex
	sessions       map[string]*sessionState
	entryTTL       time.Duration
	sessionTimeout time.Duration
	done           chan struct{}
}

// sessionState tracks enrichment state for a single session.
type sessionState struct {
	sentTables map[string]time.Time
	lastAccess time.Time
}

// NewSessionEnrichmentCache creates a new session enrichment cache.
func NewSessionEnrichmentCache(entryTTL, sessionTimeout time.Duration) *SessionEnrichmentCache {
	return &SessionEnrichmentCache{
		sessions:       make(map[string]*sessionState),
		entryTTL:       entryTTL,
		sessionTimeout: sessionTimeout,
		done:           make(chan struct{}),
	}
}

// MarkSent records that full enrichment was sent for the given table in this session.
func (c *SessionEnrichmentCache) MarkSent(sessionID, tableKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := c.getOrCreateSession(sessionID)
	state.sentTables[tableKey] = time.Now()
	state.lastAccess = time.Now()
}

// WasSentRecently returns true if the table was enriched within the entry TTL
// for this session.
func (c *SessionEnrichmentCache) WasSentRecently(sessionID, tableKey string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	state, ok := c.sessions[sessionID]
	if !ok {
		return false
	}

	sentAt, ok := state.sentTables[tableKey]
	if !ok {
		return false
	}

	return time.Since(sentAt) < c.entryTTL
}

// StartCleanup starts a background goroutine that evicts idle sessions.
func (c *SessionEnrichmentCache) StartCleanup(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-c.done:
				return
			case <-ticker.C:
				c.cleanup()
			}
		}
	}()
}

// Stop stops the background cleanup goroutine.
func (c *SessionEnrichmentCache) Stop() {
	close(c.done)
}

// SessionCount returns the number of tracked sessions.
func (c *SessionEnrichmentCache) SessionCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.sessions)
}

// getOrCreateSession returns the session state, creating it if needed.
// Caller must hold the write lock.
func (c *SessionEnrichmentCache) getOrCreateSession(sessionID string) *sessionState {
	state, ok := c.sessions[sessionID]
	if !ok {
		state = &sessionState{
			sentTables: make(map[string]time.Time),
			lastAccess: time.Now(),
		}
		c.sessions[sessionID] = state
	}
	return state
}

// cleanup evicts sessions that have been idle longer than sessionTimeout,
// and removes expired entries from active sessions.
func (c *SessionEnrichmentCache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for id, state := range c.sessions {
		if now.Sub(state.lastAccess) > c.sessionTimeout {
			delete(c.sessions, id)
			continue
		}
		// Remove expired entries from active sessions
		for table, sentAt := range state.sentTables {
			if now.Sub(sentAt) > c.entryTTL {
				delete(state.sentTables, table)
			}
		}
	}
}
