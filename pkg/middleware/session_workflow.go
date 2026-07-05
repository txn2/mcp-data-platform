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
// querying, per discovery scope. The scope key is chosen by the caller via
// PlatformContext.DiscoveryScopeKey (the authenticated user when known, else the
// session ID), so the signal survives clients that open a fresh MCP session for
// every tool call. The shared searchgate.Store is the single source of truth so
// the signal is consistent across replicas (#789): the tracker never holds a
// replica-local "discovered" bit that could diverge from it. Discovery reads
// consult the store; an active scope's record is slid forward on any tool
// activity (store writes throttled per slideEvery) so a long session is not
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
	recordTTL  time.Duration // lifetime of a shared discovery record (= store TTL)

	mu        sync.RWMutex
	lastSlide map[string]time.Time // per-scope throttle for store writes

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
		// The store is created with this same timeout as its TTL, so a throttle
		// stamp older than recordTTL implies the shared record has expired.
		recordTTL: sessionTimeout,
		lastSlide: make(map[string]time.Time),
		done:      make(chan struct{}),
	}
}

// RecordToolCall records a tool invocation for the scope. A discovery tool
// records/refreshes discovery in the shared store. Any other tool call by a
// scope this replica already knows has discovered slides the record forward
// (throttled), so continuous activity of any kind keeps the gate open for the
// life of the session rather than only query activity. An empty scopeKey (an
// unauthenticated caller with no session) is untrackable and is ignored.
func (t *SessionWorkflowTracker) RecordToolCall(ctx context.Context, scopeKey, toolName string) {
	if scopeKey == "" {
		return
	}
	switch {
	case t.discoverySet[toolName]:
		t.mark(ctx, scopeKey, true) // a discovery call always (re)persists
	case t.locallyKnownDiscovered(scopeKey):
		t.mark(ctx, scopeKey, false) // other activity slides, throttled
	}
}

// HasPerformedDiscovery returns true if a discovery tool has been called in the
// scope. The shared store is authoritative. On a positive result the record is
// slid forward (throttled) so continuous query activity keeps the gate open.
//
// The gate decision follows the read: it fails open (returns true) only when it
// cannot read a definitive answer, never on a write problem (failing open on a
// write error would let one caller's transient write blip open the gate for
// others). Two fail-open cases, both deliberate — the gate is a workflow quality
// guard, not a security boundary:
//   - Empty scopeKey (an unauthenticated caller with no session): there is no
//     stable identity to track discovery against.
//   - Store read error: a total database outage should not block every query.
//
// A store WRITE outage (reads succeed, writes fail) is handled fail-closed: the
// discovery simply does not persist, so the read returns false and the caller
// is gated (SEARCH_REQUIRED) until writes recover. That is the safe direction —
// it never bypasses the gate — and a forced discovery write always re-attempts
// on the next search (see mark).
//
// Either fail-open true also suppresses the soft discovery note in
// appendDiscoveryNoteIfNeeded: an empty-scope or read-degraded caller gets no
// nudge. The nudge is non-essential and returns once a real scope discovers or
// the store recovers.
func (t *SessionWorkflowTracker) HasPerformedDiscovery(ctx context.Context, scopeKey string) bool {
	if scopeKey == "" {
		return true
	}
	ok, err := t.store.HasDiscovered(ctx, scopeKey)
	if err != nil {
		slog.Warn("search gate: discovery check failed; allowing the call (fail-open)",
			"error", err, "scope_key", scopeKey)
		return true
	}
	if ok {
		t.mark(ctx, scopeKey, false) // slide the shared TTL on active use (throttled)
	}
	return ok
}

// locallyKnownDiscovered reports whether this replica has recently recorded or
// confirmed discovery for the scope (a cheap read used to decide whether a
// non-discovery tool call should slide the shared record).
//
// It requires the throttle stamp to be younger than recordTTL. A stamp older
// than the record's lifetime means the shared record has already expired, so
// treating the scope as "known" would let a non-discovery slide RESURRECT the
// expired record (mark upserts), opening the gate for a query that should be
// re-gated after inactivity — and only on the replica that still holds the stale
// stamp, reintroducing the cross-replica divergence #789 removed. The stamp is
// otherwise cleaned lazily (evictStaleThrottle), which can lag; this check makes
// the decision exact.
func (t *SessionWorkflowTracker) locallyKnownDiscovered(scopeKey string) bool {
	t.mu.RLock()
	last, ok := t.lastSlide[scopeKey]
	t.mu.RUnlock()
	return ok && time.Since(last) < t.recordTTL
}

// mark records or extends the scope's discovery in the shared store. A
// force=true call (a discovery tool, or a retry after a gated query) always
// attempts the write, so a failed discovery write is re-attempted the next time
// the agent calls search. force=false calls (activity slides) are throttled to
// at most once per slideEvery, and a failed slide is not retried until the next
// window: this keeps the gate from hammering an unhealthy database with a write
// on every query when writes fail but reads still succeed.
//
// If the store's writes are failing (reads still succeeding), a discovery write
// does not persist, so HasPerformedDiscovery reads false and the caller is gated
// (SEARCH_REQUIRED) until writes recover. That is deliberate fail-closed
// behavior: the gate is a workflow guard, and failing open on a write error
// would let one caller's transient write blip open the gate for every other
// caller. During a sustained write outage that blocks otherwise-workable
// queries; operators who need queries during a store-write outage can disable
// the gate (workflow.require_search: false).
func (t *SessionWorkflowTracker) mark(ctx context.Context, scopeKey string, force bool) {
	now := time.Now()

	t.mu.Lock()
	last, seen := t.lastSlide[scopeKey]
	due := force || !seen || now.Sub(last) >= t.slideEvery
	if due {
		t.lastSlide[scopeKey] = now
	}
	t.mu.Unlock()

	if !due {
		return
	}

	if err := t.store.MarkDiscovered(ctx, scopeKey); err != nil {
		slog.Warn("search gate: failed to persist discovery",
			"error", err, "scope_key", scopeKey, "forced", force)
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
