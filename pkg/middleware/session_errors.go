package middleware

import (
	"sync"
	"time"
)

// defaultMaxFailuresPerSession caps the pending failures retained per session so
// a session that errors repeatedly without ever succeeding cannot grow the
// tracker without bound. When the cap is exceeded the oldest failure is dropped.
const defaultMaxFailuresPerSession = 32

// minPairingSimilarity is the Jaccard threshold over the two queries' meaningful
// identifiers (columns/tables/functions, SQL keywords stripped) at which a
// successful query is treated as the fix for a prior failure. Below it the
// success is unrelated (e.g. a different query over the same table) and no
// correction is minted. Pairing on identifier overlap rather than a shared
// physical table lets a table/schema-rename correction pair (the fixed query
// references a different table) while rejecting an unrelated success.
const minPairingSimilarity = 0.5

// FailedQuery is a query error awaiting a later fix in the same session. The
// reflexive-capture middleware records one on a worth-capturing query error and
// consumes it when a later, related, same-connection success arrives, pairing
// the misconception with the fix (#635).
type FailedQuery struct {
	// NormalizedSQL is the whitespace-collapsed, lowercased SQL used to tell a
	// genuine fix (different SQL) from a transient error that merely succeeded on
	// retry of the identical statement.
	NormalizedSQL string
	// RawSQL is the original failing statement, preserved for the capture body.
	RawSQL string
	// Idents is the set of meaningful identifiers in the failing query (SQL
	// keywords stripped), used to score how related a later success is.
	Idents map[string]struct{}
	// Connection is the connection the failing query ran against; a fix must run
	// against the same connection (a same-named table on another connection is a
	// physically different dataset, not a fix).
	Connection   string
	ErrorMessage string
	FailedAt     time.Time
}

// sessionErrors holds one session's pending failures.
type sessionErrors struct {
	failures   []FailedQuery
	lastAccess time.Time
}

// SessionErrorTracker holds per-session query failures so a later fix can be
// paired with the misconception that preceded it (#635). It is safe for
// concurrent use and mirrors SessionEnrichmentCache's lifecycle (TTL eviction
// via a background cleanup goroutine plus an idempotent Stop).
type SessionErrorTracker struct {
	mu             sync.Mutex
	sessions       map[string]*sessionErrors
	entryTTL       time.Duration
	sessionTimeout time.Duration
	maxPerSession  int
	done           chan struct{}
	stopOnce       sync.Once
}

// NewSessionErrorTracker creates a tracker. entryTTL bounds how long a failure
// stays eligible to pair with a later fix; sessionTimeout bounds how long an
// idle session's state is retained.
func NewSessionErrorTracker(entryTTL, sessionTimeout time.Duration) *SessionErrorTracker {
	return &SessionErrorTracker{
		sessions:       make(map[string]*sessionErrors),
		entryTTL:       entryTTL,
		sessionTimeout: sessionTimeout,
		maxPerSession:  defaultMaxFailuresPerSession,
		done:           make(chan struct{}),
	}
}

// RecordFailure appends a failed query to the session's pending set. A blank
// session ID is ignored (failures cannot be paired without one). When the
// per-session cap is exceeded the oldest failure is dropped.
func (t *SessionErrorTracker) RecordFailure(sessionID string, f FailedQuery) {
	if sessionID == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	state := t.getOrCreate(sessionID)
	state.failures = append(state.failures, f)
	if len(state.failures) > t.maxPerSession {
		state.failures = state.failures[len(state.failures)-t.maxPerSession:]
	}
	state.lastAccess = time.Now()
}

// TakeResolved returns and removes the single best-matching prior failure a
// successful query resolves: same connection, not expired, different SQL, and
// identifier-set similarity at or above minPairingSimilarity. Only that one
// failure is consumed; unrelated failures remain for their own later fixes.
// Expired failures are pruned. Returns nil when nothing qualifies.
func (t *SessionErrorTracker) TakeResolved(sessionID, connection string, successIdents map[string]struct{}, successNormalizedSQL string) *FailedQuery {
	if sessionID == "" || len(successIdents) == 0 {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	state, ok := t.sessions[sessionID]
	if !ok {
		return nil
	}

	live := t.livingFailures(state.failures)
	state.lastAccess = time.Now()

	idx := bestMatch(live, connection, successIdents, successNormalizedSQL)
	if idx < 0 {
		state.failures = live
		return nil
	}

	best := live[idx]
	kept := make([]FailedQuery, 0, len(live)-1)
	kept = append(kept, live[:idx]...)
	kept = append(kept, live[idx+1:]...)
	state.failures = kept
	return &best
}

// livingFailures returns the session's non-expired failures.
func (t *SessionErrorTracker) livingFailures(failures []FailedQuery) []FailedQuery {
	now := time.Now()
	live := make([]FailedQuery, 0, len(failures))
	for _, f := range failures {
		if now.Sub(f.FailedAt) <= t.entryTTL {
			live = append(live, f)
		}
	}
	return live
}

// bestMatch returns the index of the failure a success best resolves, or -1 when
// none qualifies: same connection, different SQL, the success introduces a novel
// (corrected) identifier, and identifier similarity is at or above the
// threshold. The novel-identifier check rejects a bare sub-query (e.g.
// SELECT count(*) FROM t after a column error on t) that overlaps but is not a
// fix. Ties prefer the most recent failure (live is in insertion order).
func bestMatch(live []FailedQuery, connection string, successIdents map[string]struct{}, successNormalizedSQL string) int {
	bestIdx := -1
	bestSim := minPairingSimilarity
	for i, f := range live {
		if f.Connection != connection || f.NormalizedSQL == successNormalizedSQL {
			continue
		}
		if !hasNovelIdent(successIdents, f.Idents) {
			continue
		}
		if sim := jaccardSimilarity(f.Idents, successIdents); sim >= bestSim {
			bestSim = sim
			bestIdx = i
		}
	}
	return bestIdx
}

// hasNovelIdent reports whether success contains at least one identifier not in
// failed (the corrected token a genuine fix introduces).
func hasNovelIdent(success, failed map[string]struct{}) bool {
	for k := range success {
		if _, ok := failed[k]; !ok {
			return true
		}
	}
	return false
}

// jaccardSimilarity returns |a ∩ b| / |a ∪ b| over two identifier sets, 0 when
// either is empty.
func jaccardSimilarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// StartCleanup starts a background goroutine that evicts idle sessions and
// expired failures on the given interval.
func (t *SessionErrorTracker) StartCleanup(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-t.done:
				return
			case <-ticker.C:
				t.cleanup()
			}
		}
	}()
}

// Stop halts the cleanup goroutine. It is idempotent.
func (t *SessionErrorTracker) Stop() {
	t.stopOnce.Do(func() { close(t.done) })
}

// SessionCount returns the number of tracked sessions (diagnostics/tests).
func (t *SessionErrorTracker) SessionCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.sessions)
}

// getOrCreate returns the session state, creating it if needed.
// Caller must hold the lock.
func (t *SessionErrorTracker) getOrCreate(sessionID string) *sessionErrors {
	state, ok := t.sessions[sessionID]
	if !ok {
		state = &sessionErrors{lastAccess: time.Now()}
		t.sessions[sessionID] = state
	}
	return state
}

// cleanup evicts idle sessions and prunes expired failures from active ones.
func (t *SessionErrorTracker) cleanup() {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	for id, state := range t.sessions {
		if now.Sub(state.lastAccess) > t.sessionTimeout {
			delete(t.sessions, id)
			continue
		}
		kept := state.failures[:0]
		for _, f := range state.failures {
			if now.Sub(f.FailedAt) <= t.entryTTL {
				kept = append(kept, f)
			}
		}
		state.failures = kept
	}
}
