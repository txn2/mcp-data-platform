package middleware

import (
	"sync"
	"time"
)

// DefaultDiscoveryTools lists the tool names that count as DataHub discovery.
var DefaultDiscoveryTools = []string{
	"datahub_search",
	"datahub_get_entity",
	"datahub_get_schema",
	"datahub_get_lineage",
	"datahub_get_column_lineage",
	"datahub_get_queries",
	"datahub_get_glossary_term",
	"datahub_get_data_product",
	"datahub_list_data_products",
	"datahub_list_domains",
	"datahub_list_tags",
}

// DefaultQueryTools lists the tool names that are gated by discovery.
var DefaultQueryTools = []string{
	"trino_query",
	"trino_execute",
}

// workflowState tracks per-session workflow state.
type workflowState struct {
	discoveryTools map[string]time.Time
	queryTools     map[string]time.Time
	warningCount   int
	lastAccess     time.Time
}

// SessionWorkflowTracker tracks whether agents perform discovery before
// querying, per session. It is safe for concurrent use.
type SessionWorkflowTracker struct {
	mu             sync.RWMutex
	sessions       map[string]*workflowState
	sessionTimeout time.Duration
	discoverySet   map[string]bool
	querySet       map[string]bool
	done           chan struct{}
}

// NewSessionWorkflowTracker creates a new tracker. If discoveryTools or
// queryTools are nil/empty, the respective defaults are used.
func NewSessionWorkflowTracker(discoveryTools, queryTools []string, sessionTimeout time.Duration) *SessionWorkflowTracker {
	if len(discoveryTools) == 0 {
		discoveryTools = DefaultDiscoveryTools
	}
	if len(queryTools) == 0 {
		queryTools = DefaultQueryTools
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
		sessions:       make(map[string]*workflowState),
		sessionTimeout: sessionTimeout,
		discoverySet:   dSet,
		querySet:       qSet,
		done:           make(chan struct{}),
	}
}

// RecordToolCall records a tool invocation for the session. If the tool is
// a discovery tool, the warning count is reset to zero.
func (t *SessionWorkflowTracker) RecordToolCall(sessionID, toolName string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	state := t.getOrCreate(sessionID)
	now := time.Now()
	state.lastAccess = now

	if t.discoverySet[toolName] {
		state.discoveryTools[toolName] = now
		state.warningCount = 0
	}
	if t.querySet[toolName] {
		state.queryTools[toolName] = now
	}
}

// HasPerformedDiscovery returns true if at least one discovery tool has been
// called in the session.
func (t *SessionWorkflowTracker) HasPerformedDiscovery(sessionID string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	state, ok := t.sessions[sessionID]
	if !ok {
		return false
	}
	return len(state.discoveryTools) > 0
}

// DiscoveryToolCount returns the number of distinct discovery tools called
// in the session.
func (t *SessionWorkflowTracker) DiscoveryToolCount(sessionID string) int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	state, ok := t.sessions[sessionID]
	if !ok {
		return 0
	}
	return len(state.discoveryTools)
}

// IncrementWarningCount increments the warning counter and returns the new count.
func (t *SessionWorkflowTracker) IncrementWarningCount(sessionID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	state := t.getOrCreate(sessionID)
	state.warningCount++
	return state.warningCount
}

// WarningCount returns the current warning count for the session.
func (t *SessionWorkflowTracker) WarningCount(sessionID string) int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	state, ok := t.sessions[sessionID]
	if !ok {
		return 0
	}
	return state.warningCount
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

// StartCleanup starts a background goroutine that evicts idle sessions.
func (t *SessionWorkflowTracker) StartCleanup(interval time.Duration) {
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

// Stop stops the background cleanup goroutine.
func (t *SessionWorkflowTracker) Stop() {
	close(t.done)
}

// getOrCreate returns the session state, creating it if needed.
// Caller must hold the write lock.
func (t *SessionWorkflowTracker) getOrCreate(sessionID string) *workflowState {
	state, ok := t.sessions[sessionID]
	if !ok {
		state = &workflowState{
			discoveryTools: make(map[string]time.Time),
			queryTools:     make(map[string]time.Time),
			lastAccess:     time.Now(),
		}
		t.sessions[sessionID] = state
	}
	return state
}

// cleanup evicts sessions that have been idle longer than sessionTimeout.
func (t *SessionWorkflowTracker) cleanup() {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	for id, state := range t.sessions {
		if now.Sub(state.lastAccess) > t.sessionTimeout {
			delete(t.sessions, id)
		}
	}
}
