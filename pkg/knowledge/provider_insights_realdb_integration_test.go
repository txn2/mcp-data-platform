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
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
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

// TestInsightsProvider_RealDB_AppliedInsightsCrossIdentity is the #980 B2 gate
// against real Postgres. sqlmock does not parse SQL, so it cannot tell whether
// the insight-status expression is even valid, let alone whether it selects the
// right rows; only this test exercises the COALESCE/NULLIF predicate and the
// migration 000095 index against the real schema.
//
// It asserts the property the benchmark measured as a cross-identity transfer
// gap: knowledge one person captured and had applied reaches a different person,
// and nothing short of applied does.
func TestInsightsProvider_RealDB_AppliedInsightsCrossIdentity(t *testing.T) {
	store := memory.NewPostgresStore(testdb.New(t))
	adapter, ok := knowledgekit.NewMemoryInsightAdapter(store).(knowledgekit.SearchableInsightStore)
	require.True(t, ok, "the memory insight adapter must be searchable")
	provider := NewInsightsProvider(adapter)
	ctx := context.Background()

	const (
		alice = "alice@example.com"
		bob   = "bob@example.com"
		urn   = "urn:li:dataset:(urn:li:dataPlatform:trino,retail.public.refunds,PROD)"
		token = "zqrefundwindow" // distinctive lexical token shared by every record
	)
	mk := func(id, capturedBy, insightStatus string) memory.Record {
		return memory.Record{
			ID:         id,
			CreatedBy:  capturedBy,
			Dimension:  memory.DimensionKnowledge,
			Category:   "business_context",
			Source:     "user",
			Status:     memory.StatusActive,
			Content:    token + " refunds are booked net of tax",
			EntityURNs: []string{urn},
			Metadata:   map[string]any{memory.MetaKeyInsightStatus: insightStatus},
		}
	}
	require.NoError(t, store.Insert(ctx, mk("bob-applied", bob, knowledgekit.StatusApplied)))
	require.NoError(t, store.Insert(ctx, mk("bob-pending", bob, memory.InsightStatusPending)))
	require.NoError(t, store.Insert(ctx, mk("bob-approved", bob, knowledgekit.StatusApproved)))
	require.NoError(t, store.Insert(ctx, mk("alice-pending", alice, memory.InsightStatusPending)))

	caller := Caller{Email: alice}

	for _, arm := range []struct {
		name  string
		query Query
	}{
		{"text path", Query{Intent: token, Caller: caller, Limit: 20}},
		{"entity path", Query{EntityURNs: []string{urn}, Caller: caller, Limit: 20}},
	} {
		t.Run(arm.name, func(t *testing.T) {
			hits, err := provider.Search(ctx, arm.query)
			require.NoError(t, err)
			assert.ElementsMatch(t, []string{"bob-applied", "alice-pending"}, refIDs(hits),
				"alice must receive her own capture plus bob's applied insight, and nothing else")
		})
	}

	// The read side agrees with the search side: the reference alice received is
	// dereferenceable, and bob's unapplied captures are not.
	doc, owned, err := provider.Fetch(ctx, knowledgepage.InsightRef("bob-applied"), caller)
	require.NoError(t, err)
	require.True(t, owned)
	assert.Contains(t, doc.Body, token, "alice must be able to read the applied insight in full")

	for _, id := range []string{"bob-pending", "bob-approved"} {
		_, owned, err := provider.Fetch(ctx, knowledgepage.InsightRef(id), caller)
		assert.True(t, owned)
		assert.ErrorIs(t, err, ErrNotFound, "%s must not be readable by another capturer", id)
	}
}
