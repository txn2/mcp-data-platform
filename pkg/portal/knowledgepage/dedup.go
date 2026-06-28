package knowledgepage

import (
	"context"
	"fmt"
)

// DefaultDedupThreshold is the cosine similarity at or above which a create is
// treated as a near-duplicate of an existing page (#705). It is a raw cosine in
// [0,1] from SemanticSearch (NOT the fused hybrid search score), so the value reads
// directly as "how similar": 0.85 catches "same topic, different slug" duplicates
// (e.g. "Return Policy" vs "ACME Returns Policy") while leaving genuinely distinct
// pages free to be created. Deployments can override it; 0 disables the gate.
const DefaultDedupThreshold = 0.85

// dedupSearchLimit bounds how many ranked pages the gate inspects. The gate only
// needs the top matches to decide near-duplication and to show the agent where to
// consolidate, so a small fixed window keeps the probe cheap.
const dedupSearchLimit = 5

// DuplicateProber ranks pages by pure embedding cosine similarity for the dedup
// gate (#705). It is the SemanticSearch slice of the store, declared separately
// from Searcher because the gate needs the raw cosine, not Search's fused
// semantic+lexical score (which is uncalibrated as a similarity threshold).
type DuplicateProber interface {
	SemanticSearch(ctx context.Context, embedding []float32, limit int) ([]ScoredPage, error)
}

// DedupCandidate is an existing page the dedup gate flags as a near-duplicate of a
// page being created (#705): enough to either re-apply against its slug (an update)
// or, with force_new, knowingly create a separate page. Score is the cosine
// similarity in [0,1].
type DedupCandidate struct {
	ID    string  `json:"id"`
	Slug  string  `json:"slug,omitempty"`
	Title string  `json:"title"`
	Score float64 `json:"score"`
}

// NearDuplicatePages returns the existing pages whose cosine similarity to the
// candidate is at or above threshold, the create-time dedup gate shared by the MCP
// apply path and the portal REST create path (#705). It is the recall-first analog
// of memory_capture, adapted for shared pages: surface-and-require rather than
// auto-supersede, so a human or agent owns the merge decision.
//
// The gate is meaningful only with a real embedding: a nil embedding makes it a
// no-op (returns no candidates) and the create proceeds, the same graceful
// degradation the platform applies wherever no embedding provider is configured. A
// non-positive threshold also disables the gate.
//
// The probe ranks by SemanticSearch (pure cosine), not Search (fused
// semantic+lexical), so threshold is a true similarity. The caller embeds the
// candidate with IndexText so the query vector lives in the same text space as the
// stored page embeddings.
func NearDuplicatePages(ctx context.Context, p DuplicateProber, embedding []float32, threshold float64) ([]DedupCandidate, error) {
	if threshold <= 0 || len(embedding) == 0 {
		return nil, nil
	}
	scored, err := p.SemanticSearch(ctx, embedding, dedupSearchLimit)
	if err != nil {
		return nil, fmt.Errorf("dedup semantic search: %w", err)
	}
	var candidates []DedupCandidate
	for i := range scored {
		if scored[i].Score < threshold {
			continue
		}
		candidates = append(candidates, DedupCandidate{
			ID:    scored[i].Page.ID,
			Slug:  scored[i].Page.Slug,
			Title: scored[i].Page.Title,
			Score: scored[i].Score,
		})
	}
	return candidates, nil
}
