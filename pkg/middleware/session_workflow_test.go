package middleware

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/searchgate"
)

func TestSessionWorkflowTracker_RecordDiscovery(t *testing.T) {
	tracker := NewSessionWorkflowTracker(nil, nil, nil, 30*time.Minute)
	ctx := context.Background()

	assert.False(t, tracker.HasPerformedDiscovery(ctx, "s1"))

	tracker.RecordToolCall(ctx, "s1", "search")
	assert.True(t, tracker.HasPerformedDiscovery(ctx, "s1"))

	// A datahub_* tool also counts as discovery (broad default set).
	tracker.RecordToolCall(ctx, "s2", "datahub_browse")
	assert.True(t, tracker.HasPerformedDiscovery(ctx, "s2"))
}

func TestSessionWorkflowTracker_NonPositiveTimeoutGuarded(t *testing.T) {
	// A non-positive timeout must be defaulted, not left to make slideEvery 0
	// and the store TTL 0 (which would expire every record instantly).
	tracker := NewSessionWorkflowTracker(nil, nil, nil, 0)
	ctx := context.Background()
	tracker.RecordToolCall(ctx, "s", "search")
	assert.True(t, tracker.HasPerformedDiscovery(ctx, "s"),
		"a guarded default timeout keeps discovery records alive")
}

func TestSessionWorkflowTracker_RecordQuery(t *testing.T) {
	tracker := NewSessionWorkflowTracker(nil, nil, nil, 30*time.Minute)
	ctx := context.Background()

	tracker.RecordToolCall(ctx, "s1", "trino_query")
	assert.False(t, tracker.HasPerformedDiscovery(ctx, "s1"), "query tool should not count as discovery")
}

func TestSessionWorkflowTracker_EmptySession(t *testing.T) {
	tracker := NewSessionWorkflowTracker(nil, nil, nil, 30*time.Minute)
	assert.False(t, tracker.HasPerformedDiscovery(context.Background(), "nonexistent"))
}

// TestSessionWorkflowTracker_UngateableEmptyKey covers the fail-open path for a
// caller with no stable identity (empty scope key): the gate cannot track
// discovery for it, so it must allow the call rather than block forever, and a
// record for the empty key must not pollute another scope's state.
func TestSessionWorkflowTracker_UngateableEmptyKey(t *testing.T) {
	ctx := context.Background()
	tracker := NewSessionWorkflowTracker(nil, nil, nil, 30*time.Minute)

	// An empty scope key is ungateable and fails open.
	assert.True(t, tracker.HasPerformedDiscovery(ctx, ""),
		"empty scope key must fail open (no identity to gate on)")

	// Recording under the empty key is a no-op: it must not mark discovery for a
	// real scope, and must not make the empty key itself look discovered via the
	// throttle map (locallyKnownDiscovered stays false).
	tracker.RecordToolCall(ctx, "", toolNameSearch)
	assert.False(t, tracker.locallyKnownDiscovered(""),
		"empty scope key must not be recorded in the throttle map")
	assert.False(t, tracker.HasPerformedDiscovery(ctx, "real-scope"),
		"recording under the empty key must not open the gate for another scope")
}

func TestSessionWorkflowTracker_IsQueryTool(t *testing.T) {
	tracker := NewSessionWorkflowTracker(nil, nil, nil, 30*time.Minute)

	assert.True(t, tracker.IsQueryTool("trino_query"))
	assert.True(t, tracker.IsQueryTool("trino_execute"))
	assert.False(t, tracker.IsQueryTool("datahub_search"))
	assert.False(t, tracker.IsQueryTool("trino_describe_table"))
}

func TestSessionWorkflowTracker_CustomTools(t *testing.T) {
	tracker := NewSessionWorkflowTracker(
		[]string{"my_discover"},
		[]string{"my_query"},
		nil,
		30*time.Minute,
	)
	ctx := context.Background()

	// Custom query tool recognized
	assert.True(t, tracker.IsQueryTool("my_query"))
	assert.False(t, tracker.IsQueryTool("trino_query")) // default not included

	// Custom discovery tool recognized
	tracker.RecordToolCall(ctx, "s1", "my_discover")
	assert.True(t, tracker.HasPerformedDiscovery(ctx, "s1"))
}

func TestSessionWorkflowTracker_SessionIsolation(t *testing.T) {
	tracker := NewSessionWorkflowTracker(nil, nil, nil, 30*time.Minute)
	ctx := context.Background()

	tracker.RecordToolCall(ctx, "s1", "search")
	assert.True(t, tracker.HasPerformedDiscovery(ctx, "s1"))
	assert.False(t, tracker.HasPerformedDiscovery(ctx, "s2"), "sessions should be isolated")
}

// TestSessionWorkflowTracker_SharedStoreAcrossReplicas is the regression test
// for #789: two trackers backed by ONE shared store (simulating two replicas
// pointed at the same database) must see each other's discovery. Discovery
// recorded via tracker A opens the gate for a query checked on tracker B.
func TestSessionWorkflowTracker_SharedStoreAcrossReplicas(t *testing.T) {
	ctx := context.Background()
	shared := searchgate.NewMemoryStore(30 * time.Minute)

	replicaA := NewSessionWorkflowTracker(nil, nil, shared, 30*time.Minute)
	replicaB := NewSessionWorkflowTracker(nil, nil, shared, 30*time.Minute)

	// Before discovery, neither replica reports it.
	assert.False(t, replicaB.HasPerformedDiscovery(ctx, "sess"))

	// search lands on replica A.
	replicaA.RecordToolCall(ctx, "sess", "search")

	// A query load-balanced to replica B must now be allowed.
	assert.True(t, replicaB.HasPerformedDiscovery(ctx, "sess"),
		"discovery recorded on one replica must be visible to another via the shared store")
}

// errStore is a searchgate.Store whose read/write always error, used to verify
// the tracker's fail-open behavior on a transient store fault.
type errStore struct{}

func (errStore) MarkDiscovered(context.Context, string) error        { return assert.AnError }
func (errStore) HasDiscovered(context.Context, string) (bool, error) { return false, assert.AnError }
func (errStore) Cleanup(context.Context) error                       { return assert.AnError }
func (errStore) Close() error                                        { return assert.AnError }

// flakyStore fails the first failWrites MarkDiscovered calls, then persists.
// Reads reflect only what has actually persisted (the store is source of truth).
type flakyStore struct {
	failWrites int
	writes     int
	marked     map[string]bool
}

func (f *flakyStore) MarkDiscovered(_ context.Context, s string) error {
	f.writes++
	if f.writes <= f.failWrites {
		return assert.AnError
	}
	if f.marked == nil {
		f.marked = map[string]bool{}
	}
	f.marked[s] = true
	return nil
}

func (f *flakyStore) HasDiscovered(_ context.Context, s string) (bool, error) {
	return f.marked[s], nil
}
func (*flakyStore) Cleanup(context.Context) error { return nil }
func (*flakyStore) Close() error                  { return nil }

// TestSessionWorkflowTracker_RetriesAfterWriteFailure covers the write-failure
// path: because the store is the single source of truth (no divergent
// replica-local bit), a failed discovery write persists nothing, so the gate
// stays CLOSED (fail-closed, SEARCH_REQUIRED) rather than bypassing on a write
// error; a forced write always re-attempts, so the agent's next search persists
// once writes recover and opens the gate on every replica.
func TestSessionWorkflowTracker_RetriesAfterWriteFailure(t *testing.T) {
	store := &flakyStore{failWrites: 1}
	tracker := NewSessionWorkflowTracker(nil, nil, store, 30*time.Minute)
	ctx := context.Background()

	tracker.RecordToolCall(ctx, "s", "search") // write 1 fails, nothing persisted
	assert.False(t, tracker.HasPerformedDiscovery(ctx, "s"),
		"a failed write leaves no shared record; the gate stays closed (never bypasses on a write error)")

	tracker.RecordToolCall(ctx, "s", "search") // write 2 succeeds (force always re-attempts)
	assert.True(t, tracker.HasPerformedDiscovery(ctx, "s"),
		"re-searching after a failed write persists and opens the gate on every replica")
}

// TestSessionWorkflowTracker_ExpiredRecordNotResurrectedBySlide is the
// regression test for the finding that a non-discovery tool call could
// resurrect an EXPIRED discovery record via a stale local throttle stamp,
// opening the gate for a query that should be re-gated after inactivity. Once
// the shared record's TTL lapses, a non-discovery slide must NOT recreate it.
func TestSessionWorkflowTracker_ExpiredRecordNotResurrectedBySlide(t *testing.T) {
	const ttl = 40 * time.Millisecond
	store := searchgate.NewMemoryStore(ttl)
	tracker := NewSessionWorkflowTracker(nil, nil, store, ttl)
	ctx := context.Background()

	tracker.RecordToolCall(ctx, "user:alice", "search") // discover: stamp + record
	require.True(t, tracker.HasPerformedDiscovery(ctx, "user:alice"))

	time.Sleep(60 * time.Millisecond) // exceed the record TTL: the shared record expires

	// A non-discovery tool call by the same scope must not resurrect the expired
	// record off the lingering local stamp; the scope is re-gated.
	tracker.RecordToolCall(ctx, "user:alice", "some_other_tool")
	assert.False(t, tracker.HasPerformedDiscovery(ctx, "user:alice"),
		"an expired discovery record must not be resurrected by a non-discovery slide")
}

// TestSessionWorkflowTracker_FailsOpenOnReadError covers the deliberate
// fail-open policy: a store read error for an un-cached session allows the call
// rather than walling off every query during a database outage.
func TestSessionWorkflowTracker_FailsOpenOnReadError(t *testing.T) {
	tracker := NewSessionWorkflowTracker(nil, nil, errStore{}, 30*time.Minute)
	assert.True(t, tracker.HasPerformedDiscovery(context.Background(), "never-recorded"),
		"a store read error should fail open (allow), not block every query")
}

// TestSessionWorkflowTracker_SlidesWithActivity covers #789 review finding #1:
// a session that discovered once and stays active must not be re-gated when the
// original discovery TTL elapses — activity slides the window.
func TestSessionWorkflowTracker_SlidesWithActivity(t *testing.T) {
	const ttl = 100 * time.Millisecond
	store := searchgate.NewMemoryStore(ttl)
	tracker := NewSessionWorkflowTracker(nil, nil, store, ttl)
	ctx := context.Background()

	tracker.RecordToolCall(ctx, "s", "search") // discover once at t0

	// Stay active with query-tool calls past the original TTL. Each call goes
	// through RecordToolCall (as the real middleware does) before the gate check.
	for range 3 {
		time.Sleep(60 * time.Millisecond)
		tracker.RecordToolCall(ctx, "s", "trino_query")
		require.True(t, tracker.HasPerformedDiscovery(ctx, "s"),
			"an active session must stay discovered as the window slides")
	}
	// ~180ms elapsed, well past the 100ms TTL, yet still discovered.
}

// TestSessionWorkflowTracker_SlidesOnNonQueryActivity covers the third-round
// review finding: a discovered session doing only non-query, non-discovery work
// must still keep the gate open (the window slides on any activity, not just
// query tools).
func TestSessionWorkflowTracker_SlidesOnNonQueryActivity(t *testing.T) {
	const ttl = 100 * time.Millisecond
	store := searchgate.NewMemoryStore(ttl)
	tracker := NewSessionWorkflowTracker(nil, nil, store, ttl)
	ctx := context.Background()

	tracker.RecordToolCall(ctx, "s", "search") // discover once

	// Only non-query, non-discovery activity from here, past the original TTL.
	for range 3 {
		time.Sleep(60 * time.Millisecond)
		tracker.RecordToolCall(ctx, "s", "s3_list_objects")
	}
	assert.True(t, tracker.HasPerformedDiscovery(ctx, "s"),
		"non-query activity by a discovered session must keep the gate open")
}

func TestSessionWorkflowTracker_ConcurrentAccess(t *testing.T) {
	tracker := NewSessionWorkflowTracker(nil, nil, nil, 30*time.Minute)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sid := "session-concurrent"
			if n%2 == 0 {
				tracker.RecordToolCall(ctx, sid, "search")
			} else {
				tracker.RecordToolCall(ctx, sid, "trino_query")
			}
			_ = tracker.HasPerformedDiscovery(ctx, sid)
		}(i)
	}
	wg.Wait()

	// No panics means the test passed; just verify state is readable
	assert.True(t, tracker.HasPerformedDiscovery(ctx, "session-concurrent"))
}

func TestSessionWorkflowTracker_Cleanup(t *testing.T) {
	// A short-TTL store lets the discovery record expire; the store is the
	// source of truth, so once its row is cleaned the gate closes again.
	store := searchgate.NewMemoryStore(50 * time.Millisecond)
	tracker := NewSessionWorkflowTracker(nil, nil, store, 50*time.Millisecond)
	ctx := context.Background()

	tracker.RecordToolCall(ctx, "s-expire", "search")
	require.True(t, tracker.HasPerformedDiscovery(ctx, "s-expire"))

	// Wait for the store record to expire, with no activity to slide it.
	time.Sleep(80 * time.Millisecond)

	require.NoError(t, store.Cleanup(ctx))
	tracker.evictStaleThrottle()

	assert.False(t, tracker.HasPerformedDiscovery(ctx, "s-expire"), "expired discovery should be gone")
}

func TestSessionWorkflowTracker_StartCleanupAndStop(t *testing.T) {
	tracker := NewSessionWorkflowTracker(nil, nil, nil, 10*time.Millisecond)
	tracker.StartCleanup(10 * time.Millisecond)

	tracker.RecordToolCall(context.Background(), "s-bg", "search")

	// Give cleanup time to run
	time.Sleep(50 * time.Millisecond)

	// Stop should not panic, and is idempotent.
	tracker.Stop()
	tracker.Stop()
}

func TestDefaultDiscoveryTools(t *testing.T) {
	// search is the front door; the datahub_* tools also satisfy the gate so
	// personas granted datahub_* (but not search) are not deadlocked.
	assert.Contains(t, DefaultDiscoveryTools, "search")
	assert.Contains(t, DefaultDiscoveryTools, "datahub_get_lineage")
	assert.Contains(t, DefaultDiscoveryTools, "datahub_browse")
	assert.Len(t, DefaultDiscoveryTools, 3)
	// The by-URN reads folded into fetch (#1590) are gone from the set: a name
	// the platform no longer registers must not count as discovery either.
	for _, retired := range []string{
		"datahub_get_entity", "datahub_get_schema", "datahub_get_queries",
		"datahub_get_glossary_term", "datahub_get_data_product",
	} {
		assert.NotContains(t, DefaultDiscoveryTools, retired)
	}
}

func TestDefaultQueryTools(t *testing.T) {
	assert.Contains(t, DefaultQueryTools, "trino_query")
	assert.Contains(t, DefaultQueryTools, "trino_execute")
	assert.Len(t, DefaultQueryTools, 2)
}

func TestSessionWorkflowTracker_DiscoveryToolNames(t *testing.T) {
	tracker := NewSessionWorkflowTracker(
		[]string{"datahub_search", "datahub_get_lineage"},
		nil,
		nil,
		30*time.Minute,
	)
	names := tracker.DiscoveryToolNames()
	assert.Len(t, names, 2)
	assert.ElementsMatch(t, []string{"datahub_search", "datahub_get_lineage"}, names)
}

func TestSessionWorkflowTracker_QueryToolNames(t *testing.T) {
	tracker := NewSessionWorkflowTracker(
		nil,
		[]string{"trino_query", "trino_execute"},
		nil,
		30*time.Minute,
	)
	names := tracker.QueryToolNames()
	assert.Len(t, names, 2)
	assert.ElementsMatch(t, []string{"trino_query", "trino_execute"}, names)
}
