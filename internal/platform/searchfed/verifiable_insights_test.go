package searchfed

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

const verifiableInsightURN = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.orders,PROD)"

// oneInsightStore is a searchable insight store holding a single applied insight
// about a resolvable entity.
type oneInsightStore struct {
	knowledgekit.InsightStore
}

func (oneInsightStore) Search(context.Context, knowledgekit.InsightSearchQuery) ([]knowledgekit.ScoredInsight, error) {
	return []knowledgekit.ScoredInsight{{
		Insight: knowledgekit.Insight{
			ID:          "i-1",
			CapturedBy:  "analyst@example.com",
			Status:      knowledgekit.StatusApplied,
			InsightText: "The orders table holds 1140 rows.",
			EntityURNs:  []string{verifiableInsightURN},
		},
		Score: 0.9,
	}}, nil
}

// ordersQueryProvider reports the insight's entity as an available table.
type ordersQueryProvider struct {
	query.NoopProvider
}

func (*ordersQueryProvider) GetTableAvailability(_ context.Context, urn string) (*query.TableAvailability, error) {
	if urn != verifiableInsightURN {
		return &query.TableAvailability{Available: false}, nil
	}
	return &query.TableAvailability{
		Available:  true,
		QueryTable: "iceberg.retail.orders",
		Connection: "primary",
	}, nil
}

// insightHit returns the single insight hit of a federated search.
func insightHit(t *testing.T, h *Handle) knowledge.Hit {
	t.Helper()

	res, err := h.Router().Search(context.Background(), knowledge.Query{
		Intent: "orders rows",
		Caller: knowledge.Caller{UserID: "u1", Email: "analyst@example.com"},
		Limit:  20,
	})
	require.NoError(t, err)

	for _, g := range res.Groups {
		if g.Source == knowledge.SourceInsights {
			require.Len(t, g.Hits, 1)
			return g.Hits[0]
		}
	}
	t.Fatal("no insight hit was returned")
	return knowledge.Hit{}
}

// The verifier the owner passes must reach the insights provider: without the
// wiring the field is silently absent everywhere, which reads exactly like a
// deployment that has no query provider.
func TestNew_InsightVerifierReachesTheProvider(t *testing.T) {
	h := New(Config{
		ToolkitName:        "default",
		InsightStore:       oneInsightStore{InsightStore: knowledgekit.NewNoopStore()},
		VerifiableInsights: true,
		QueryProvider:      &ordersQueryProvider{},
		Registry:           registry.NewRegistry(),
	})

	hit := insightHit(t, h)
	require.NotNil(t, hit.Verifiable, "a delivered insight the platform can query must say so")
	assert.Equal(t, "iceberg.retail.orders", hit.Verifiable.QueryTable)
	assert.Equal(t, "primary", hit.Verifiable.Connection)
}

func TestNew_NoInsightVerifierLeavesHitsUnchanged(t *testing.T) {
	t.Run("marker turned off", func(t *testing.T) {
		h := New(Config{
			ToolkitName:   "default",
			InsightStore:  oneInsightStore{InsightStore: knowledgekit.NewNoopStore()},
			QueryProvider: &ordersQueryProvider{},
			Registry:      registry.NewRegistry(),
		})
		assert.Nil(t, insightHit(t, h).Verifiable)
	})

	t.Run("no query provider to resolve with", func(t *testing.T) {
		h := New(Config{
			ToolkitName:        "default",
			InsightStore:       oneInsightStore{InsightStore: knowledgekit.NewNoopStore()},
			VerifiableInsights: true,
			Registry:           registry.NewRegistry(),
		})
		assert.Nil(t, insightHit(t, h).Verifiable)
	})
}
