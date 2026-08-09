package middleware

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/query"
)

// countingVerifier records how many resolution passes the enrichment asked for,
// so a test can prove a recalled set with nothing to resolve spends none.
type countingVerifier struct {
	passes int
	got    []string
}

func (c *countingVerifier) Verifiables(_ context.Context, urns []string) map[string]query.Verifiable {
	c.passes++
	c.got = urns
	return nil
}

// A recalled set with no insights in it, or with insights linked to no entity,
// must not spend a lookup pass against the warehouse: there is nothing a query
// could be run against.
func TestVerifiablesFor_NoResolvableRecordsSpendsNoPass(t *testing.T) {
	tests := []struct {
		name     string
		memories []MemorySnippet
	}{
		{
			name:     "no records at all",
			memories: nil,
		},
		{
			name: "only plain memory records",
			memories: []MemorySnippet{
				{ID: "m-1", EntityURNs: []string{"urn:li:dataset:(x,y,PROD)"}},
			},
		},
		{
			name: "insights linked to no entity",
			memories: []MemorySnippet{
				{ID: "i-1", Insight: true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &countingVerifier{}
			if got := verifiablesFor(context.Background(), v, tt.memories); got != nil {
				t.Errorf("verifiablesFor = %v, want nothing resolved", got)
			}
			if v.passes != 0 {
				t.Errorf("resolution passes = %d, want 0", v.passes)
			}
		})
	}
}

// The pass resolves each entity once, however many insights name it.
func TestVerifiablesFor_ResolvesDistinctEntitiesOnce(t *testing.T) {
	const urn = "urn:li:dataset:(urn:li:dataPlatform:trino,cat.schema.orders,PROD)"
	v := &countingVerifier{}

	verifiablesFor(context.Background(), v, []MemorySnippet{
		{ID: "i-1", Insight: true, EntityURNs: []string{urn}},
		{ID: "i-2", Insight: true, EntityURNs: []string{urn}},
		{ID: "m-1", EntityURNs: []string{"urn:li:dataset:(x,other,PROD)"}},
	})

	if v.passes != 1 {
		t.Fatalf("resolution passes = %d, want 1", v.passes)
	}
	if len(v.got) != 1 || v.got[0] != urn {
		t.Errorf("resolved URNs = %v, want only the entity the insights name", v.got)
	}
}

// The byte budget bounds what the enrichment appends, so it has to count the
// verification marker too: a real DataHub URN is long enough that leaving the
// block out of the arithmetic lets a page of marked insights overshoot the
// operator's cap by half again as much as they asked for (#761's bound, #1220's
// field).
func TestRenderMemoryContext_BudgetCountsTheVerifiableBlock(t *testing.T) {
	const urn = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.orders,PROD)"
	verifiables := map[string]query.Verifiable{
		urn: {URN: urn, QueryTable: "iceberg.retail.orders", Connection: "primary"},
	}

	memories := make([]MemorySnippet, 0, 5)
	for i := range 5 {
		memories = append(memories, MemorySnippet{
			ID:         fmt.Sprintf("i-%d", i),
			Reference:  fmt.Sprintf("mcp:insight:i-%d", i),
			Content:    fmt.Sprintf("Insight %d: the orders table holds 1140 rows.", i),
			Dimension:  "knowledge",
			EntityURNs: []string{urn},
			Insight:    true,
		})
	}

	marked, _ := renderMemoryContext(memories, verifiables, 0, 0)
	require.Len(t, marked, 5)
	require.NotNil(t, marked[0].Verifiable, "the fixture must actually be marked")

	// The marker's bytes must widen the record's charge, or the budget is blind
	// to them.
	unmarked, _ := renderMemoryContext(memories, nil, 0, 0)
	require.Len(t, unmarked, 5)
	markedSize := recordSizeEstimate(marked[0])
	unmarkedSize := recordSizeEstimate(unmarked[0])
	assert.Greater(t, markedSize, unmarkedSize+len(urn),
		"the charge must cover at least the URN the block carries")

	// And the budget must act on them: a cap that fits four unmarked records
	// must not silently render five marked ones.
	budget := 4 * markedSize
	rendered, omitted := renderMemoryContext(memories, verifiables, 0, budget)
	total := 0
	for _, rec := range rendered {
		total += recordSizeEstimate(rec)
	}
	assert.LessOrEqual(t, total, budget, "rendered summaries must stay within the budget")
	assert.NotEmpty(t, omitted, "records beyond the budget become fetchable stubs")
	assert.Len(t, rendered, len(memories)-len(omitted), "every record is either rendered or stubbed")
}

// A nil verifier is the no-query-provider (or opted-out) deployment.
func TestVerifiablesFor_NilVerifier(t *testing.T) {
	got := verifiablesFor(context.Background(), nil, []MemorySnippet{
		{ID: "i-1", Insight: true, EntityURNs: []string{"urn:li:dataset:(x,y,PROD)"}},
	})
	if got != nil {
		t.Errorf("verifiablesFor = %v, want nothing with no verifier wired", got)
	}
}
