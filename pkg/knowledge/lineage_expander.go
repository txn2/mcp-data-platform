package knowledge

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// lineageExpander widens a set of entity URNs along one hop of DataHub lineage
// for the search entity path, mirroring the memory toolkit's lineage collection.
// It is the seam that carries the old memory_recall "graph" strategy into the
// unified search verb, implementing the router's LineageExpander.
type lineageExpander struct {
	semantic semantic.Provider
}

// NewLineageExpander returns a LineageExpander backed by a DataHub semantic
// provider. Pass it to NewRouter so entity-keyed searches recall knowledge about
// upstream and downstream datasets, not just the exact URNs.
func NewLineageExpander(sem semantic.Provider) LineageExpander {
	return &lineageExpander{semantic: sem}
}

// Expand returns the input URNs plus their one-hop upstream and downstream
// lineage neighbors. Lookups that fail (unparseable URN, lineage error) are
// skipped, so expansion never fails the search; the original URNs are always
// returned.
func (e *lineageExpander) Expand(ctx context.Context, urns []string) []string {
	related := make(map[string]bool)
	for _, urn := range urns {
		related[urn] = true
		table, err := memory.ParseURNToTable(urn)
		if err != nil {
			continue
		}
		// nosemgrep: semgrep.unbounded-make-slice-capacity -- fixed 2-element literal, not user input
		for _, dir := range []semantic.LineageDirection{semantic.LineageUpstream, semantic.LineageDownstream} {
			lineage, err := e.semantic.GetLineage(ctx, table, dir, 1)
			if err != nil || lineage == nil {
				continue
			}
			for _, entity := range lineage.Entities {
				related[entity.URN] = true
			}
		}
	}
	out := make([]string, 0, len(related))
	for u := range related {
		out = append(out, u)
	}
	return out
}
