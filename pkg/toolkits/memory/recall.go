package memory

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// handleRecall performs multi-strategy memory retrieval.
func (t *Toolkit) handleRecall(ctx context.Context, _ *mcp.CallToolRequest, input recallInput) (*mcp.CallToolResult, any, error) {
	pc := middleware.GetPlatformContext(ctx)

	limit := clampLimit(input.Limit)
	strategy := input.Strategy
	if strategy == "" {
		strategy = strategyAuto
	}

	records, err := t.dispatchRecall(ctx, strategy, input, pc.PersonaName)
	if err != nil {
		return errorResult("recall failed: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	// Trim to limit.
	if len(records) > limit {
		records = records[:limit]
	}

	return jsonResult(map[string]any{
		"memories": records,
		"count":    len(records),
		"strategy": strategy,
	}), nil, nil
}

// clampLimit constrains the recall limit to valid bounds.
func clampLimit(limit int) int {
	if limit <= 0 {
		return defaultRecallLimit
	}
	if limit > maxRecallLimit {
		return maxRecallLimit
	}
	return limit
}

// dispatchRecall routes to the appropriate recall strategy.
func (t *Toolkit) dispatchRecall(ctx context.Context, strategy string, input recallInput, persona string) ([]memstore.ScoredRecord, error) {
	switch strategy {
	case strategyEntity:
		return t.recallByEntity(ctx, input.EntityURNs, persona)
	case strategySemantic:
		return t.recallBySemantic(ctx, input.Query, persona, input.IncludeStale)
	case strategyGraph:
		return t.recallByGraph(ctx, input.EntityURNs, persona)
	case strategyAuto:
		return t.recallAuto(ctx, input, persona), nil
	default:
		return nil, fmt.Errorf("unknown strategy %q: use entity, semantic, graph, or auto", strategy)
	}
}

// recallByEntity fetches memories linked to specific DataHub URNs.
func (t *Toolkit) recallByEntity(ctx context.Context, urns []string, persona string) ([]memstore.ScoredRecord, error) {
	if len(urns) == 0 {
		return nil, fmt.Errorf("entity_urns required for entity strategy")
	}

	var results []memstore.ScoredRecord
	seen := make(map[string]bool)

	for _, urn := range urns {
		records, err := t.store.EntityLookup(ctx, urn, persona)
		if err != nil {
			return nil, fmt.Errorf("entity lookup for %s: %w", urn, err)
		}
		for _, r := range records {
			if !seen[r.ID] {
				seen[r.ID] = true
				results = append(results, memstore.ScoredRecord{Record: r, Score: 1.0})
			}
		}
	}

	return results, nil
}

// recallBySemantic performs vector similarity search.
func (t *Toolkit) recallBySemantic(ctx context.Context, query, persona string, includeStale bool) ([]memstore.ScoredRecord, error) {
	if query == "" {
		return nil, fmt.Errorf("query required for semantic strategy")
	}

	emb, err := t.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embedding query: %w", err)
	}

	// If all embedding values are zero (noop provider), semantic search is not available.
	allZero := true
	for _, v := range emb {
		if v != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return nil, fmt.Errorf("semantic search unavailable: no embedding provider configured (set memory.embedding.provider to 'ollama')")
	}

	vq := memstore.VectorQuery{
		Embedding: emb,
		Limit:     maxRecallLimit,
		Persona:   persona,
	}
	if !includeStale {
		vq.Status = memstore.StatusActive
	}

	results, err := t.store.VectorSearch(ctx, vq)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	return results, nil
}

// recallByGraph walks DataHub lineage to find memories on related entities.
func (t *Toolkit) recallByGraph(ctx context.Context, urns []string, persona string) ([]memstore.ScoredRecord, error) {
	if len(urns) == 0 {
		return nil, fmt.Errorf("entity_urns required for graph strategy")
	}
	if t.semanticProvider == nil {
		return t.recallByEntity(ctx, urns, persona)
	}

	relatedURNs := make(map[string]bool)
	for _, urn := range urns {
		relatedURNs[urn] = true
		t.collectLineageURNs(ctx, urn, relatedURNs)
	}

	allURNs := make([]string, 0, len(relatedURNs))
	for u := range relatedURNs {
		allURNs = append(allURNs, u)
	}

	return t.recallByEntity(ctx, allURNs, persona)
}

// collectLineageURNs adds upstream and downstream URNs to the set.
func (t *Toolkit) collectLineageURNs(ctx context.Context, urn string, urns map[string]bool) {
	table, err := memstore.ParseURNToTable(urn)
	if err != nil {
		return
	}

	// nosemgrep: semgrep.unbounded-make-slice-capacity -- fixed 2-element literal, not user input
	for _, dir := range []semantic.LineageDirection{semantic.LineageUpstream, semantic.LineageDownstream} {
		lineage, err := t.semanticProvider.GetLineage(ctx, table, dir, 1)
		if err != nil {
			slog.Debug("lineage lookup failed", "urn", urn, "direction", dir, "error", err)
			continue
		}
		for _, entity := range lineage.Entities {
			urns[entity.URN] = true
		}
	}
}

// recallAuto runs entity + semantic + graph in parallel, then merges.
func (t *Toolkit) recallAuto(ctx context.Context, input recallInput, persona string) []memstore.ScoredRecord {
	var mu sync.Mutex
	var allResults []memstore.ScoredRecord
	var wg sync.WaitGroup

	// Run semantic search if query is provided.
	if input.Query != "" {
		wg.Go(func() {
			r, err := t.recallBySemantic(ctx, input.Query, persona, input.IncludeStale)
			if err != nil {
				slog.Debug("semantic recall failed in auto", "error", err)
				return
			}
			mu.Lock()
			allResults = append(allResults, r...)
			mu.Unlock()
		})
	}

	// Run entity/graph lookup if URNs are provided.
	if len(input.EntityURNs) > 0 {
		wg.Go(func() {
			r, err := t.recallByGraph(ctx, input.EntityURNs, persona)
			if err != nil {
				slog.Debug("graph recall failed in auto", "error", err)
				return
			}
			mu.Lock()
			allResults = append(allResults, r...)
			mu.Unlock()
		})
	}

	wg.Wait()

	return dedup(allResults)
}

// dedup removes duplicate records by ID, keeping the highest score.
func dedup(records []memstore.ScoredRecord) []memstore.ScoredRecord {
	seen := make(map[string]int)
	var result []memstore.ScoredRecord

	for _, r := range records {
		if idx, exists := seen[r.Record.ID]; exists {
			if r.Score > result[idx].Score {
				result[idx] = r
			}
		} else {
			seen[r.Record.ID] = len(result)
			result = append(result, r)
		}
	}

	return result
}
