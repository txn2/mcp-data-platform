package knowledge

import (
	"context"
	"slices"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// fakeLineageSemantic embeds the semantic noop and overrides GetLineage to
// return a fixed upstream neighbor, so the expander's traversal is exercised
// without a real DataHub.
type fakeLineageSemantic struct {
	*semantic.NoopProvider
	neighbor string
}

func (f fakeLineageSemantic) GetLineage(_ context.Context, _ semantic.TableIdentifier, dir semantic.LineageDirection, _ int) (*semantic.LineageInfo, error) {
	if dir == semantic.LineageUpstream {
		return &semantic.LineageInfo{Entities: []semantic.LineageEntity{{URN: f.neighbor}}}, nil
	}
	return &semantic.LineageInfo{}, nil
}

func TestLineageExpander_AddsNeighbors(t *testing.T) {
	const urn = "urn:li:dataset:(urn:li:dataPlatform:trino,opensearch.default.orders,PROD)"
	const neighbor = "urn:li:dataset:(urn:li:dataPlatform:trino,opensearch.default.upstream,PROD)"
	exp := NewLineageExpander(fakeLineageSemantic{NoopProvider: &semantic.NoopProvider{}, neighbor: neighbor})

	got := exp.Expand(context.Background(), []string{urn})
	if !slices.Contains(got, urn) {
		t.Errorf("expanded set must include the original URN: %v", got)
	}
	if !slices.Contains(got, neighbor) {
		t.Errorf("expanded set must include the lineage neighbor: %v", got)
	}
}

func TestLineageExpander_UnparseableURNPassesThrough(t *testing.T) {
	exp := NewLineageExpander(fakeLineageSemantic{NoopProvider: &semantic.NoopProvider{}, neighbor: "x"})
	got := exp.Expand(context.Background(), []string{"not-a-dataset-urn"})
	if len(got) != 1 || got[0] != "not-a-dataset-urn" {
		t.Errorf("unparseable URN should pass through unchanged, got %v", got)
	}
}
