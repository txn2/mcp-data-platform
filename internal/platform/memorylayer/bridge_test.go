package memorylayer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/memory"
)

// entityLookupStore embeds memory.Store so only EntityLookup needs a body; the
// bridge's recall path calls EntityLookup exclusively. Any other method call
// would panic on the nil embedded interface, which is the point: it proves the
// bridge touches nothing else.
type entityLookupStore struct {
	memory.Store
	records []memory.Record
}

func (s *entityLookupStore) EntityLookup(context.Context, string, string, string) ([]memory.Record, error) {
	return s.records, nil
}

// TestMemoryMiddlewareBridge_PopulatesReference proves the bridge maps memory
// records to middleware snippets and fills the canonical fetch reference
// (mcp:memory:<id>) the summary-first rendering points at (issue #761).
func TestMemoryMiddlewareBridge_PopulatesReference(t *testing.T) {
	store := &entityLookupStore{records: []memory.Record{
		{ID: "mem-abc", Content: "Revenue includes deferred amounts", Dimension: "knowledge", Category: "business_context", Confidence: "high"},
	}}
	bridge := &middlewareBridge{store: store}

	snippets, err := bridge.RecallForEntities(
		context.Background(),
		[]string{"urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.revenue,PROD)"},
		"analyst", 5,
	)
	require.NoError(t, err)
	require.Len(t, snippets, 1)

	assert.Equal(t, "mem-abc", snippets[0].ID)
	assert.Equal(t, "mcp:memory:mem-abc", snippets[0].Reference)
	assert.Equal(t, "Revenue includes deferred amounts", snippets[0].Content)
	assert.Equal(t, "knowledge", snippets[0].Dimension)
}
