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

// recallOutcome bundles a strategy's records with metadata describing
// how they were ranked, so handleRecall can surface the ranking mode and
// any graceful-degradation signal (lexical fallback) on the response.
type recallOutcome struct {
	records  []memstore.ScoredRecord
	ranking  string
	degraded bool
	note     string
}

// handleRecall performs multi-strategy memory retrieval.
func (t *Toolkit) handleRecall(ctx context.Context, _ *mcp.CallToolRequest, input recallInput) (*mcp.CallToolResult, any, error) {
	pc := middleware.GetPlatformContext(ctx)

	limit := clampLimit(input.Limit)
	strategy := input.Strategy
	if strategy == "" {
		strategy = strategyAuto
	}

	outcome, err := t.dispatchRecall(ctx, strategy, input, pc.PersonaName)
	if err != nil {
		return errorResult("recall failed: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	records := outcome.records

	// Filter by dimension if specified.
	if input.Dimension != "" {
		filtered := records[:0]
		for _, r := range records {
			if r.Record.Dimension == input.Dimension {
				filtered = append(filtered, r)
			}
		}
		records = filtered
	}

	// Trim to limit.
	if len(records) > limit {
		records = records[:limit]
	}

	result := map[string]any{
		"memories": records,
		"count":    len(records),
		"strategy": strategy,
		"ranking":  outcome.ranking,
	}
	if outcome.degraded {
		result["degraded"] = true
		result["note"] = outcome.note
	}
	return jsonResult(result), nil, nil
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

// dispatchRecall routes to the appropriate recall strategy and wraps the
// result in a recallOutcome carrying the ranking mode and degradation
// signal. Entity and graph are exact lookups (no ranking degradation);
// semantic is hybrid-or-lexical; lexical is forced full-text.
func (t *Toolkit) dispatchRecall(ctx context.Context, strategy string, input recallInput, persona string) (recallOutcome, error) {
	switch strategy {
	case strategyEntity:
		r, err := t.recallByEntity(ctx, input.EntityURNs, persona)
		return recallOutcome{records: r, ranking: strategyEntity}, err
	case strategySemantic:
		return t.recallBySemantic(ctx, input.Query, persona, input.IncludeStale)
	case strategyLexical:
		return t.recallByLexical(ctx, input.Query, persona, input.IncludeStale)
	case strategyGraph:
		r, err := t.recallByGraph(ctx, input.EntityURNs, persona)
		return recallOutcome{records: r, ranking: strategyGraph}, err
	case strategyAuto:
		return t.recallAuto(ctx, input, persona), nil
	default:
		return recallOutcome{}, fmt.Errorf("unknown strategy %q: use entity, semantic, lexical, graph, or auto", strategy)
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
				// Score 1.0 is intentional: entity matches are exact URN lookups,
				// not similarity-based, so they always receive the maximum score.
				results = append(results, memstore.ScoredRecord{Record: r, Score: 1.0})
			}
		}
	}

	return results, nil
}

// recallBySemantic performs hybrid (vector + lexical) recall, degrading
// to lexical-only when no embedding provider is available. It embeds the
// query; if the embedder errors or returns a zero vector (the noop
// placeholder), it falls back to LexicalSearch and flags the result as
// degraded so the caller knows the results are keyword-only, not
// semantic. Otherwise it fuses vector and lexical signals via
// HybridSearch. Unlike the prior vector-only path, a missing embedder is
// no longer an error.
func (t *Toolkit) recallBySemantic(ctx context.Context, query, persona string, includeStale bool) (recallOutcome, error) {
	if query == "" {
		return recallOutcome{}, fmt.Errorf("query required for semantic strategy")
	}

	status := statusFilter(includeStale)

	emb, err := t.embedder.Embed(ctx, query)
	if err != nil || isZeroVector(emb) {
		if err != nil {
			slog.Debug("embedding query failed; falling back to lexical recall", "error", err)
		}
		return t.lexicalOutcome(ctx, query, persona, status, true)
	}

	results, err := t.store.HybridSearch(ctx, memstore.HybridQuery{
		Embedding: emb,
		QueryText: query,
		Limit:     maxRecallLimit,
		Persona:   persona,
		Status:    status,
	})
	if err != nil {
		return recallOutcome{}, fmt.Errorf("hybrid search: %w", err)
	}
	return recallOutcome{records: results, ranking: rankingHybrid}, nil
}

// recallByLexical performs forced lexical-only recall (no embedding
// call). It is the explicit counterpart to the automatic fallback and is
// not flagged degraded, because lexical was the caller's choice.
func (t *Toolkit) recallByLexical(ctx context.Context, query, persona string, includeStale bool) (recallOutcome, error) {
	if query == "" {
		return recallOutcome{}, fmt.Errorf("query required for lexical strategy")
	}
	return t.lexicalOutcome(ctx, query, persona, statusFilter(includeStale), false)
}

// statusFilter maps the include-stale flag to the store status filter:
// the empty string (no status constraint, so stale rows are eligible)
// when stale is included, else active-only. Shared by the semantic,
// lexical, and auto-fallback paths so the active-vs-stale policy lives
// in one place.
func statusFilter(includeStale bool) string {
	if includeStale {
		return ""
	}
	return memstore.StatusActive
}

// lexicalOutcome runs LexicalSearch and wraps the result. degraded marks
// whether this was an automatic fallback (true) or an explicit lexical
// request (false).
func (t *Toolkit) lexicalOutcome(ctx context.Context, query, persona, status string, degraded bool) (recallOutcome, error) {
	results, err := t.store.LexicalSearch(ctx, memstore.LexicalQuery{
		QueryText: query,
		Limit:     maxRecallLimit,
		Persona:   persona,
		Status:    status,
	})
	if err != nil {
		return recallOutcome{}, fmt.Errorf("lexical search: %w", err)
	}
	out := recallOutcome{records: results, ranking: rankingLexical, degraded: degraded}
	if degraded {
		out.note = degradedNote
	}
	return out, nil
}

// isZeroVector reports whether every element is zero, the signature of
// the noop embedding provider (real embeddings are never all-zero). A
// zero query vector yields meaningless cosine similarity, so it forces
// the lexical fallback.
func isZeroVector(v []float32) bool {
	for _, x := range v {
		if x != 0 {
			return false
		}
	}
	return true
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

// recallAuto runs the semantic (hybrid-or-lexical) and graph strategies
// in parallel, then merges. The merged outcome's ranking reflects the
// semantic arm when a query was provided (hybrid, or lexical when the
// embedder was unavailable), else the graph arm; a semantic-arm
// degradation propagates so the caller still sees the lexical-only
// signal even through auto.
func (t *Toolkit) recallAuto(ctx context.Context, input recallInput, persona string) recallOutcome {
	var mu sync.Mutex
	var allResults []memstore.ScoredRecord
	var wg sync.WaitGroup

	merged := recallOutcome{ranking: strategyGraph}

	// Run semantic (hybrid/lexical) search if query is provided.
	if input.Query != "" {
		wg.Go(func() {
			sem, err := t.recallBySemantic(ctx, input.Query, persona, input.IncludeStale)
			if err != nil {
				slog.Debug("semantic recall failed in auto", "error", err)
				return
			}
			mu.Lock()
			allResults = append(allResults, sem.records...)
			merged.ranking = sem.ranking
			merged.degraded = sem.degraded
			merged.note = sem.note
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

	if len(allResults) == 0 && (input.Query != "" || len(input.EntityURNs) > 0) {
		slog.Warn("auto recall returned no results from any strategy",
			"query", input.Query, "entity_urns", input.EntityURNs)
	}

	merged.records = dedup(allResults)
	return merged
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
