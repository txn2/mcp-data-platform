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

const revenueURN = "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.revenue,PROD)"

// TestMemoryMiddlewareBridge_PopulatesReference proves the bridge maps memory
// records to middleware snippets and publishes a fetch reference only where one
// would actually dereference for the reader (issues #761, #1220).
//
// The push path is persona-scoped, not caller-scoped: it delivers other people's
// records and does not know whose. A knowledge-dimension record is an insight,
// which fetch serves to its capturer or, once applied, to everyone — and which
// fetch always declines under an mcp:memory: reference. So applied is the only
// insight status whose handle is certain to resolve for whoever receives it, and
// anything else is published with no handle rather than one that answers
// not-found.
func TestMemoryMiddlewareBridge_PopulatesReference(t *testing.T) {
	tests := []struct {
		name          string
		dimension     string
		metadata      map[string]any
		wantReference string
		wantInsight   bool
	}{
		{
			name:          "applied insight is citable",
			dimension:     memory.DimensionKnowledge,
			metadata:      map[string]any{memory.MetaKeyInsightStatus: "applied"},
			wantReference: "mcp:insight:mem-abc",
			wantInsight:   true,
		},
		{
			name:        "approved insight is another capturer's unpublished record",
			dimension:   memory.DimensionKnowledge,
			metadata:    map[string]any{memory.MetaKeyInsightStatus: "approved"},
			wantInsight: true,
		},
		{
			name:        "markerless knowledge record carries no citable status",
			dimension:   memory.DimensionKnowledge,
			wantInsight: true,
		},
		{
			name:          "a migrated record resolves through its legacy marker",
			dimension:     memory.DimensionKnowledge,
			metadata:      map[string]any{memory.MetaKeyLegacyStatus: "applied"},
			wantReference: "mcp:insight:mem-abc",
			wantInsight:   true,
		},
		{
			name:          "other dimensions are memory",
			dimension:     memory.DimensionPreference,
			wantReference: "mcp:memory:mem-abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &entityLookupStore{records: []memory.Record{{
				ID:         "mem-abc",
				Content:    "Revenue includes deferred amounts",
				Dimension:  tt.dimension,
				Category:   "business_context",
				Confidence: "high",
				EntityURNs: []string{revenueURN},
				Metadata:   tt.metadata,
			}}}
			bridge := &middlewareBridge{store: store}

			snippets, err := bridge.RecallForEntities(
				context.Background(), []string{revenueURN}, "analyst", 5,
			)
			require.NoError(t, err)
			require.Len(t, snippets, 1)

			assert.Equal(t, "mem-abc", snippets[0].ID)
			assert.Equal(t, tt.wantReference, snippets[0].Reference)
			assert.Equal(t, "Revenue includes deferred amounts", snippets[0].Content)
			assert.Equal(t, tt.dimension, snippets[0].Dimension)
			assert.Equal(t, tt.wantInsight, snippets[0].Insight)
			// The record's own entities travel with it: the verification marker is
			// resolved against them, not against the URN the recall was keyed by.
			assert.Equal(t, []string{revenueURN}, snippets[0].EntityURNs)
		})
	}
}
