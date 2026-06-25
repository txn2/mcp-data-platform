//go:build integration

package knowledge

// End-to-end #684 guard against real Postgres: through the assembled chain
// (memory store -> insight adapter -> InsightsProvider), a recall-first superseded
// insight must not surface in EITHER unfiltered discovery path (intent/text or
// entity-keyed), while its live successor does, and an explicit status filter still
// returns it. This is the single test that fails if any one read arm forgets the
// retraction; its absence is why the #684 leak shipped (the entity arm was tested,
// the text arm was assumed to match).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

func TestInsightsProvider_RealDB_RetractsSupersededAcrossBothPaths(t *testing.T) {
	store := memory.NewPostgresStore(testdb.New(t))
	adapter, ok := knowledgekit.NewMemoryInsightAdapter(store).(knowledgekit.SearchableInsightStore)
	require.True(t, ok, "the memory insight adapter must be searchable")
	provider := NewInsightsProvider(adapter)
	ctx := context.Background()

	const (
		alice = "alice@example.com"
		urn   = "urn:li:dataset:(urn:li:dataPlatform:trino,retail.public.returns,PROD)"
		token = "zqreturnpolicy" // distinctive lexical token shared by both records
	)
	mk := func(id string) memory.Record {
		return memory.Record{
			ID:         id,
			CreatedBy:  alice,
			Dimension:  memory.DimensionKnowledge,
			Category:   "business_context",
			Source:     "user",
			Status:     memory.StatusActive,
			Content:    token + " standard return policy window is thirty days",
			EntityURNs: []string{urn},
			Metadata:   map[string]any{memory.MetaKeyInsightStatus: memory.InsightStatusPending},
		}
	}
	require.NoError(t, store.Insert(ctx, mk("ins-live")))
	require.NoError(t, store.Insert(ctx, mk("ins-old")))

	// Recall-first supersede: ins-old is superseded by ins-live. This advances both
	// the lifecycle status and insight_status to superseded (#682).
	require.NoError(t, store.Supersede(ctx, "ins-old", "ins-live"))

	caller := Caller{Email: alice}

	// Text/intent discovery: only the live insight (the #684 fix; the store still
	// returns the superseded record, the provider must drop it).
	textHits, err := provider.Search(ctx, Query{Intent: token, Caller: caller, Limit: 20})
	require.NoError(t, err)
	assert.Equal(t, []string{"ins-live"}, refIDs(textHits),
		"text path must surface only the live insight, not the superseded one")

	// Entity-keyed discovery: same retraction.
	entHits, err := provider.Search(ctx, Query{EntityURNs: []string{urn}, Caller: caller, Limit: 20})
	require.NoError(t, err)
	assert.Equal(t, []string{"ins-live"}, refIDs(entHits),
		"entity path must surface only the live insight")

	// An explicit status filter still returns the superseded record.
	supHits, err := provider.Search(ctx, Query{
		Intent: token, Status: knowledgekit.StatusSuperseded, Caller: caller, Limit: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"ins-old"}, refIDs(supHits),
		"status=superseded must still return the superseded record")
}

func refIDs(hits []Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Ref)
	}
	return out
}
