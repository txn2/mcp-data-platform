package knowledgepage

import (
	"context"
	"fmt"
	"sort"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
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

// CandidateEmbeddings embeds a page-to-be-written the way the dedup gate probes
// with it: split into IndexChunks sized to the provider's own input budget, then
// embedded as one batch. It returns nil (gate disabled, create proceeds) when no
// real provider is configured or the embed call fails, matching
// embedding.EmbedForSearch's degradation rule.
//
// Both write surfaces — the MCP apply path and the portal REST create — call this
// rather than embedding the composed text themselves, so the query side of the
// gate cannot drift from the stored side, and neither surface can silently probe
// with only the head of a large candidate.
func CandidateEmbeddings(ctx context.Context, p embedding.Provider, title, body string, tags []string) [][]float32 {
	return embedding.EmbedChunksForSearch(ctx, p, IndexChunks(title, body, tags, embedding.MaxInputBytes(p)))
}

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
// The gate is meaningful only with a real embedding: an empty embedding set makes
// it a no-op (returns no candidates) and the create proceeds, the same graceful
// degradation the platform applies wherever no embedding provider is configured. A
// non-positive threshold also disables the gate.
//
// The candidate is supplied as the embeddings of its IndexChunks, so a candidate
// larger than the provider's input budget is compared in full rather than by its
// head alone (#1242); a candidate that fits is one vector and the probe is a
// single query. Each probe ranks by SemanticSearch (pure cosine), not Search
// (fused semantic+lexical), so threshold is a true similarity, and a page keeps
// its highest score across the probes — the best candidate chunk against the best
// page chunk. Chunks are composed through IndexText, so the query vectors live in
// the same text space as the stored page chunks.
func NearDuplicatePages(ctx context.Context, p DuplicateProber, embeddings [][]float32, threshold float64) ([]DedupCandidate, error) {
	if threshold <= 0 || len(embeddings) == 0 {
		return nil, nil
	}
	best := make(map[string]DedupCandidate, dedupSearchLimit)
	for _, emb := range embeddings {
		if len(emb) == 0 {
			continue
		}
		scored, err := p.SemanticSearch(ctx, emb, dedupSearchLimit)
		if err != nil {
			return nil, fmt.Errorf("dedup semantic search: %w", err)
		}
		mergeCandidates(best, scored, threshold)
	}
	return rankCandidates(best), nil
}

// mergeCandidates folds one probe's results into the per-page best scores,
// keeping a page's highest score across probes and dropping anything below the
// threshold.
func mergeCandidates(best map[string]DedupCandidate, scored []ScoredPage, threshold float64) {
	for i := range scored {
		if scored[i].Score < threshold {
			continue
		}
		if prev, ok := best[scored[i].Page.ID]; ok && prev.Score >= scored[i].Score {
			continue
		}
		best[scored[i].Page.ID] = DedupCandidate{
			ID:    scored[i].Page.ID,
			Slug:  scored[i].Page.Slug,
			Title: scored[i].Page.Title,
			Score: scored[i].Score,
		}
	}
}

// rankCandidates flattens the per-page best scores into a deterministic order:
// most similar first, ties broken by id so a caller's rendering is stable.
func rankCandidates(best map[string]DedupCandidate) []DedupCandidate {
	if len(best) == 0 {
		return nil
	}
	out := make([]DedupCandidate, 0, len(best))
	for _, c := range best {
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	return out
}
