package middleware

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/searchgate"
)

// DefaultDiscoveryTools lists the tool names that satisfy the search-first gate.
// search is the universal discovery front door and the tool every description
// override and discovery note steers agents toward; the datahub_* tools are
// structured catalog navigation that also genuinely discovers an entity's
// business context, so they satisfy the gate too. Keeping the datahub_* tools in
// the default set is important now that the gate is a hard block: the shipped
// personas (and the documented examples) grant datahub_* without search, so a
// narrower set would deadlock any such persona out of query tools. Teams can
// override with workflow.discovery_tools.
var DefaultDiscoveryTools = []string{
	toolNameSearch,
	toolNameDatahubGetEntity,
	"datahub_get_schema",
	"datahub_get_lineage",
	"datahub_get_queries",
	"datahub_browse",
	"datahub_get_glossary_term",
	"datahub_get_data_product",
}

// DefaultQueryTools lists the tool names that are gated by discovery.
var DefaultQueryTools = []string{
	toolNameTrinoQuery,
	toolNameTrinoExecute,
}

// defaultWorkflowSessionTimeout guards the constructor against a non-positive
// timeout (the platform always supplies a positive one; a direct caller might
// not). It bounds the discovery record's lifetime and the slide throttle.
const defaultWorkflowSessionTimeout = 30 * time.Minute

// SessionWorkflowTracker tracks whether agents perform discovery before
// querying, per session. The shared searchgate.Store is the single source of
// truth so the signal is consistent across replicas (#789): the tracker never
// holds a replica-local "discovered" bit that could diverge from it. Discovery
// reads consult the store; an active session's record is slid forward on any
// tool activity (store writes throttled per slideEvery) so a long session is not
// re-gated mid-workflow. It is safe for concurrent use.
//
// Two costs are inherent to backing the signal with a shared store rather than a
// per-process map, and are accepted deliberately:
//   - The gate's HasPerformedDiscovery and the enrichment discovery-note both do
//     a store read (a primary-key EXISTS lookup) per call; this is negligible
//     next to the Trino query or enrichment work on the same path.
//   - The slide throttle (lastSlide) is per replica, so an active session
//     load-balanced across R replicas may refresh the shared record up to R
//     times per window instead of once. The refresh is an idempotent upsert.
type SessionWorkflowTracker struct {
	discoverySet map[string]bool
	querySet     map[string]bool

	store      searchgate.Store
	slideEvery time.Duration

	mu        sync.RWMutex
	lastSlide map[string]time.Time // per-session throttle for store writes

	done     chan struct{}
	stopOnce sync.Once
}

// NewSessionWorkflowTracker creates a new tracker. If discoveryTools or
// queryTools are nil/empty, the respective defaults are used. If store is nil,
// an in-memory store is used (correct for single-replica / no-database
// deployments only).
func NewSessionWorkflowTracker(discoveryTools, queryTools []string, store searchgate.Store, sessionTimeout time.Duration) *SessionWorkflowTracker {
	if len(discoveryTools) == 0 {
		discoveryTools = DefaultDiscoveryTools
	}
	if len(queryTools) == 0 {
		queryTools = DefaultQueryTools
	}
	if sessionTimeout <= 0 {
		sessionTimeout = defaultWorkflowSessionTimeout
	}
	if store == nil {
		store = searchgate.NewMemoryStore(sessionTimeout)
	}

	dSet := make(map[string]bool, len(discoveryTools))
	for _, t := range discoveryTools {
		dSet[t] = true
	}
	qSet := make(map[string]bool, len(queryTools))
	for _, t := range queryTools {
		qSet[t] = true
	}

	return &SessionWorkflowTracker{
		discoverySet: dSet,
		querySet:     qSet,
		store:        store,
		// Refresh the shared record at least twice per session-timeout window so
		// an active session's record never lapses before the next slide.
		slideEvery: sessionTimeout / 2,
		lastSlide:  make(map[string]time.Time),
		done:       make(chan struct{}),
	}
}

// RecordToolCall records a tool invocation for the session. A discovery tool
// records/refreshes discovery in the shared store. Any other tool call by a
// session this replica already knows has discovered slides the record forward
// (throttled), so continuous activity of any kind keeps the gate open for the
// life of the session rather than only query activity.
func (t *SessionWorkflowTracker) RecordToolCall(ctx context.Context, sessionID, toolName string) {
	switch {
	case t.discoverySet[toolName]:
		t.mark(ctx, sessionID, true) // a discovery call always (re)persists
	case t.locallyKnownDiscovered(sessionID):
		t.mark(ctx, sessionID, false) // other activity slides, throttled
	}
}

// HasPerformedDiscovery returns true if a discovery tool has been called in the
// session. The shared store is authoritative. On a positive result the record
// is slid forward (throttled) so continuous query activity keeps the gate open.
// On a store error it deliberately fails open (returns true): the gate is a
// workflow quality guard, not a security boundary, so a database outage should
// not wall off every query. The error is logged. (Note: a fail-open true also
// suppresses the soft discovery note in appendDiscoveryNoteIfNeeded for the
// outage; the nudge is non-essential and returns once the store recovers.)
func (t *SessionWorkflowTracker) HasPerformedDiscovery(ctx context.Context, sessionID string) bool {
	ok, err := t.store.HasDiscovered(ctx, sessionID)
	if err != nil {
		slog.Warn("search gate: discovery check failed; allowing the call (fail-open)",
			"error", err, "session_id", sessionID)
		return true
	}
	if ok {
		t.mark(ctx, sessionID, false) // slide the shared TTL on active use (throttled)
	}
	return ok
}

// locallyKnownDiscovered reports whether this replica has already recorded or
// confirmed discovery for the session (a cheap read used to decide whether a
// non-discovery tool call should slide the shared record).
func (t *SessionWorkflowTracker) locallyKnownDiscovered(sessionID string) bool {
	t.mu.RLock()
	_, ok := t.lastSlide[sessionID]
	t.mu.RUnlock()
	return ok
}

// mark records or extends the session's discovery in the shared store. A
// force=true call (a discovery tool, or a retry after a gated query) always
// writes, so a failed discovery write is re-attempted the next time the agent
// calls search — a failed write therefore degrades to at most one repeated
// SEARCH_REQUIRED, never a permanent or cross-replica-inconsistent block.
// force=false calls (activity slides) are throttled to at most once per
// slideEvery, and a failed slide is NOT retried until the next window: this
// keeps the gate from hammering an unhealthy database with a write on every
// query when writes fail but reads still succeed.
func (t *SessionWorkflowTracker) mark(ctx context.Context, sessionID string, force bool) {
	now := time.Now()

	t.mu.Lock()
	last, seen := t.lastSlide[sessionID]
	due := force || !seen || now.Sub(last) >= t.slideEvery
	if due {
		t.lastSlide[sessionID] = now
	}
	t.mu.Unlock()

	if !due {
		return
	}

	if err := t.store.MarkDiscovered(ctx, sessionID); err != nil {
		slog.Warn("search gate: failed to persist discovery",
			"error", err, "session_id", sessionID, "forced", force)
	}
}

// IsQueryTool returns true if the given tool name is in the query tool set.
func (t *SessionWorkflowTracker) IsQueryTool(toolName string) bool {
	return t.querySet[toolName]
}

// DiscoveryToolNames returns the configured discovery tool names.
func (t *SessionWorkflowTracker) DiscoveryToolNames() []string {
	names := make([]string, 0, len(t.discoverySet))
	for name := range t.discoverySet {
		names = append(names, name)
	}
	return names
}

// QueryToolNames returns the configured query tool names.
func (t *SessionWorkflowTracker) QueryToolNames() []string {
	names := make([]string, 0, len(t.querySet))
	for name := range t.querySet {
		names = append(names, name)
	}
	return names
}

// StartCleanup starts a background goroutine that evicts expired store entries
// and stale throttle stamps.
func (t *SessionWorkflowTracker) StartCleanup(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-t.done:
				return
			case <-ticker.C:
				if err := t.store.Cleanup(context.Background()); err != nil {
					slog.Warn("search gate: store cleanup failed", "error", err)
				}
				t.evictStaleThrottle()
			}
		}
	}()
}

// Stop stops the background cleanup goroutine and closes the store. It is
// idempotent: calling it more than once (e.g. Close called twice) is safe.
func (t *SessionWorkflowTracker) Stop() {
	t.stopOnce.Do(func() {
		close(t.done)
		if err := t.store.Close(); err != nil {
			slog.Warn("search gate: store close failed", "error", err)
		}
	})
}

// evictStaleThrottle drops throttle stamps whose session has surely ended (no
// activity for longer than the store record could survive without a slide).
func (t *SessionWorkflowTracker) evictStaleThrottle() {
	cutoff := 2 * t.slideEvery
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for id, last := range t.lastSlide {
		if now.Sub(last) > cutoff {
			delete(t.lastSlide, id)
		}
	}
}
