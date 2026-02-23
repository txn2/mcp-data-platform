package middleware

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionWorkflowTracker_RecordDiscovery(t *testing.T) {
	tracker := NewSessionWorkflowTracker(nil, nil, 30*time.Minute)

	assert.False(t, tracker.HasPerformedDiscovery("s1"))
	assert.Equal(t, 0, tracker.DiscoveryToolCount("s1"))

	tracker.RecordToolCall("s1", "datahub_search")
	assert.True(t, tracker.HasPerformedDiscovery("s1"))
	assert.Equal(t, 1, tracker.DiscoveryToolCount("s1"))

	tracker.RecordToolCall("s1", "datahub_get_entity")
	assert.Equal(t, 2, tracker.DiscoveryToolCount("s1"))
}

func TestSessionWorkflowTracker_RecordQuery(t *testing.T) {
	tracker := NewSessionWorkflowTracker(nil, nil, 30*time.Minute)

	tracker.RecordToolCall("s1", "trino_query")
	assert.False(t, tracker.HasPerformedDiscovery("s1"), "query tool should not count as discovery")
}

func TestSessionWorkflowTracker_WarningResetOnDiscovery(t *testing.T) {
	tracker := NewSessionWorkflowTracker(nil, nil, 30*time.Minute)

	// Simulate 3 warnings from queries without discovery
	count := tracker.IncrementWarningCount("s1")
	assert.Equal(t, 1, count)
	count = tracker.IncrementWarningCount("s1")
	assert.Equal(t, 2, count)
	count = tracker.IncrementWarningCount("s1")
	assert.Equal(t, 3, count)
	assert.Equal(t, 3, tracker.WarningCount("s1"))

	// Discovery resets warning count
	tracker.RecordToolCall("s1", "datahub_search")
	assert.Equal(t, 0, tracker.WarningCount("s1"))
}

func TestSessionWorkflowTracker_EmptySession(t *testing.T) {
	tracker := NewSessionWorkflowTracker(nil, nil, 30*time.Minute)

	assert.False(t, tracker.HasPerformedDiscovery("nonexistent"))
	assert.Equal(t, 0, tracker.DiscoveryToolCount("nonexistent"))
	assert.Equal(t, 0, tracker.WarningCount("nonexistent"))
}

func TestSessionWorkflowTracker_IsQueryTool(t *testing.T) {
	tracker := NewSessionWorkflowTracker(nil, nil, 30*time.Minute)

	assert.True(t, tracker.IsQueryTool("trino_query"))
	assert.True(t, tracker.IsQueryTool("trino_execute"))
	assert.False(t, tracker.IsQueryTool("datahub_search"))
	assert.False(t, tracker.IsQueryTool("trino_describe_table"))
}

func TestSessionWorkflowTracker_CustomTools(t *testing.T) {
	tracker := NewSessionWorkflowTracker(
		[]string{"my_discover"},
		[]string{"my_query"},
		30*time.Minute,
	)

	// Custom query tool recognized
	assert.True(t, tracker.IsQueryTool("my_query"))
	assert.False(t, tracker.IsQueryTool("trino_query")) // default not included

	// Custom discovery tool recognized
	tracker.RecordToolCall("s1", "my_discover")
	assert.True(t, tracker.HasPerformedDiscovery("s1"))
}

func TestSessionWorkflowTracker_SessionIsolation(t *testing.T) {
	tracker := NewSessionWorkflowTracker(nil, nil, 30*time.Minute)

	tracker.RecordToolCall("s1", "datahub_search")
	assert.True(t, tracker.HasPerformedDiscovery("s1"))
	assert.False(t, tracker.HasPerformedDiscovery("s2"), "sessions should be isolated")
}

func TestSessionWorkflowTracker_ConcurrentAccess(t *testing.T) {
	tracker := NewSessionWorkflowTracker(nil, nil, 30*time.Minute)

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sid := "session-concurrent"
			if n%2 == 0 {
				tracker.RecordToolCall(sid, "datahub_search")
			} else {
				tracker.RecordToolCall(sid, "trino_query")
				tracker.IncrementWarningCount(sid)
			}
			_ = tracker.HasPerformedDiscovery(sid)
			_ = tracker.WarningCount(sid)
		}(i)
	}
	wg.Wait()

	// No panics means the test passed; just verify state is readable
	assert.True(t, tracker.HasPerformedDiscovery("session-concurrent"))
}

func TestSessionWorkflowTracker_Cleanup(t *testing.T) {
	tracker := NewSessionWorkflowTracker(nil, nil, 50*time.Millisecond)

	tracker.RecordToolCall("s-expire", "datahub_search")
	require.True(t, tracker.HasPerformedDiscovery("s-expire"))

	// Wait for the session to expire
	time.Sleep(60 * time.Millisecond)

	// Run cleanup directly (don't rely on background goroutine for test reliability)
	tracker.cleanup()

	assert.False(t, tracker.HasPerformedDiscovery("s-expire"), "expired session should be cleaned up")
}

func TestSessionWorkflowTracker_StartCleanupAndStop(t *testing.T) {
	tracker := NewSessionWorkflowTracker(nil, nil, 10*time.Millisecond)
	tracker.StartCleanup(10 * time.Millisecond)

	tracker.RecordToolCall("s-bg", "datahub_search")

	// Give cleanup time to run
	time.Sleep(50 * time.Millisecond)

	// Stop should not panic
	tracker.Stop()

	// Session should be cleaned up
	assert.False(t, tracker.HasPerformedDiscovery("s-bg"))
}

func TestDefaultDiscoveryTools(t *testing.T) {
	// Verify all datahub_* tools are in the default list
	assert.Contains(t, DefaultDiscoveryTools, "datahub_search")
	assert.Contains(t, DefaultDiscoveryTools, "datahub_get_entity")
	assert.Contains(t, DefaultDiscoveryTools, "datahub_get_schema")
	assert.Contains(t, DefaultDiscoveryTools, "datahub_get_lineage")
	assert.Contains(t, DefaultDiscoveryTools, "datahub_get_column_lineage")
	assert.Contains(t, DefaultDiscoveryTools, "datahub_get_queries")
	assert.Contains(t, DefaultDiscoveryTools, "datahub_get_glossary_term")
	assert.Contains(t, DefaultDiscoveryTools, "datahub_get_data_product")
	assert.Contains(t, DefaultDiscoveryTools, "datahub_list_data_products")
	assert.Contains(t, DefaultDiscoveryTools, "datahub_list_domains")
	assert.Contains(t, DefaultDiscoveryTools, "datahub_list_tags")
	assert.Len(t, DefaultDiscoveryTools, 11)
}

func TestDefaultQueryTools(t *testing.T) {
	assert.Contains(t, DefaultQueryTools, "trino_query")
	assert.Contains(t, DefaultQueryTools, "trino_execute")
	assert.Len(t, DefaultQueryTools, 2)
}

func TestSessionWorkflowTracker_DiscoveryToolNames(t *testing.T) {
	tracker := NewSessionWorkflowTracker(
		[]string{"datahub_search", "datahub_get_entity"},
		nil,
		30*time.Minute,
	)
	names := tracker.DiscoveryToolNames()
	assert.Len(t, names, 2)
	assert.ElementsMatch(t, []string{"datahub_search", "datahub_get_entity"}, names)
}

func TestSessionWorkflowTracker_QueryToolNames(t *testing.T) {
	tracker := NewSessionWorkflowTracker(
		nil,
		[]string{"trino_query", "trino_execute"},
		30*time.Minute,
	)
	names := tracker.QueryToolNames()
	assert.Len(t, names, 2)
	assert.ElementsMatch(t, []string{"trino_query", "trino_execute"}, names)
}
