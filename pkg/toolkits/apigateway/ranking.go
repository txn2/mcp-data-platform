package apigateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
)

// embedBatchSize is the maximum number of texts fed to the
// embedding provider's EmbedBatch in a single call. Bounded so a
// catalog with hundreds of operations does not POST one giant
// batch that risks timing out the request context, exceeding an
// upstream model's context window, or hitting an HTTP body cap.
// 32 is small enough that a single timed-out batch only loses
// progress for that chunk and large enough to amortize per-call
// overhead. Consumed at spec-write time by the admin handler's
// compute-and-store path; the toolkit no longer batches at
// request time because vectors are already in the store.
const embedBatchSize = 32

// errEmbedderNotWired is the sentinel returned by queryVectorFor
// when no embedding provider has been configured on the toolkit.
// Kept distinct from generic embedder errors so the fallback note
// can phrase the cause correctly: this is operator configuration,
// not an upstream failure.
var errEmbedderNotWired = errors.New("embedding provider not configured on this connection")

// Embedding-state sentinels returned by checkEmbeddingsReady.
// Persisting operation embeddings collapsed the multi-state
// in-process warmer to "the row exists or it does not", so the
// surviving set is:
//
//   - errEmbeddingsNoOps: nothing to embed (the connection has no
//     operations; semantic ranking is meaningless).
//   - errEmbeddingsNotIndexed: the connection's catalog has no
//     persisted vectors. The spec was written without an embedder
//     configured, the embedding compute step failed, or the
//     operator has not yet run the re-embed admin endpoint.
//   - errEmbeddingsZeroVector: the provider returned a zero vector
//     for the query, which produces meaningless cosine similarity;
//     points at a misconfigured embedding model.
var (
	errEmbeddingsNoOps      = errors.New("connection has no operations to embed")
	errEmbeddingsNotIndexed = errors.New("operation embeddings not indexed for this catalog; re-save or re-embed the spec to populate them")
	errEmbeddingsZeroVector = errors.New("query embedding is the zero vector (misconfigured embedding model)")
)

// RankingMode selects the algorithm api_list_endpoints uses to score
// candidate operations against the model's query.
//
// Lexical (default) is the substring-match filter that v1 shipped:
// fast, deterministic, no embedding-provider dependency. Misses on
// natural-language queries when the model's phrasing doesn't share
// vocabulary with the spec author's (e.g. query "create order" vs
// summary "Place a new order").
//
// Semantic uses cosine similarity between the query embedding and
// each operation's pre-computed embedding. Best for free-form
// intent queries; needs an embedding provider wired via
// SetEmbeddingProvider.
//
// Hybrid blends a lexical signal (substring match) with the
// semantic cosine score. The blend recovers the precision of
// substring match for queries that DO share vocabulary while still
// returning semantically-related results when they don't. The blend
// weight (alpha) is fixed at hybridSemanticWeight; tuning is
// deferred to a config knob if a real-world deployment needs it.
type RankingMode string

// RankingMode values exposed on the api_list_endpoints schema.
const (
	// RankingLexical is the v1 substring-match filter (default).
	RankingLexical RankingMode = "lexical"
	// RankingSemantic ranks by embedding cosine similarity only.
	RankingSemantic RankingMode = "semantic"
	// RankingHybrid blends a lexical signal with the cosine score.
	RankingHybrid RankingMode = "hybrid"
)

// lexicalMatchPresent / lexicalMatchAbsent are the two values the
// hybrid scorer assigns to the lexical component before blending.
// Named constants keep the gocyclo-adjacent revive add-constant
// rule satisfied without sprinkling magic 0.0/1.0 in the formula.
const (
	lexicalMatchPresent = 1.0
	lexicalMatchAbsent  = 0.0
)

// hybridSemanticWeight is the alpha in the hybrid score formula:
//
//	score = α * cosine_normalized + (1 − α) * lexical
//
// 0.6 leans semantic — the Speakeasy "100x token reduction" study
// referenced in #371 found semantic outperforms lexical on free-form
// queries, but pure semantic loses the precision boost that comes
// from an exact path/tag match. 0.6 keeps semantic dominant while
// preserving that precision.
const hybridSemanticWeight = 0.6

// rankWithMode dispatches to the per-mode ranker. Falls back to
// lexical (and surfaces a note via the bool return) when semantic
// or hybrid was requested but the embedding pipeline is not
// available — provider unwired, lazy embed failed, or the
// connection has no operations.
//
// Returns (ranked, fallback) where fallback is true iff the call
// was forced into lexical mode despite a non-lexical request.
// Callers should set ListEndpointsOutput.Note when fallback is
// true so the model knows why semantic-style ranking did not run.
// rankRequest bundles the parameters rankWithMode needs. Splitting
// into a struct keeps the function under the project's
// argument-limit lint ceiling and makes the call sites self-
// documenting at the same time.
type rankRequest struct {
	tk    *Toolkit
	conn  *conn
	ops   []OperationSummary
	query string
	limit int
	mode  RankingMode
}

func rankWithMode(ctx context.Context, r rankRequest) (ranked []OperationSummary, fallbackReason string) {
	q := strings.TrimSpace(r.query)
	// Empty query has no semantic signal: the cosine of the
	// embedding-of-empty-string is meaningless. Lexical's "return
	// all up to limit" is the right answer for both empty-query
	// and explicit-lexical-mode.
	if r.mode == RankingLexical || q == "" {
		return rankOperations(r.ops, r.query, r.limit), ""
	}
	queryVec, err := r.tk.queryVectorFor(ctx, r.conn, q)
	if err != nil {
		slog.Warn("apigateway: semantic ranking fell back to lexical",
			logKeyConnection, r.conn.cfg.ConnectionName,
			"mode", string(r.mode), logKeyError, err)
		return rankOperations(r.ops, r.query, r.limit), err.Error()
	}
	scored := scoreOperations(r.conn, r.ops, q, queryVec, r.mode)
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})
	out := make([]OperationSummary, 0, len(scored))
	for _, s := range scored {
		out = append(out, s.op)
	}
	return capSlice(out, r.limit), ""
}

// RankedOperation is one operation matched by SearchOperations, tagged with the
// connection it belongs to and its relevance score under the ranking mode that
// produced it (hybrid cosine when the connection has indexed embeddings, a
// descending positional score from the lexical fallback otherwise). The score is
// comparable within a connection. Across connections the comparison is
// best-effort: when one connection is indexed (cosine scores) and another falls
// back to lexical (positional scores), the two scales differ, so a lexical
// connection's top op can sort above an indexed connection's top op. The
// federated search allocator bounds the impact — endpoints get a floored,
// ceilinged slice of the budget regardless of this intra-group ordering — but it
// does not eliminate it; indexing every API connection's catalog keeps the
// endpoints group on one (semantic) scale.
type RankedOperation struct {
	Connection string
	Operation  OperationSummary
	Score      float64
}

// SearchOperations ranks operations across every connection on this toolkit
// against a free-form query and returns up to perConnLimit per connection, each
// tagged with its connection name and relevance score. It is the federation
// seam behind the universal search tool's endpoints group: the same hybrid
// ranking api_list_endpoints exposes, aggregated across connections instead of
// scoped to one.
//
// Per-connection route policy is applied first (ctx-scoped), so the result
// never includes an operation the caller's persona could not invoke. That is
// the per-source access scope for the endpoints corpus, enforced fail-closed by
// the same filter api_list_endpoints uses; a search that federates endpoints
// cannot leak a route a scoped api_list_endpoints call would have hidden.
func (t *Toolkit) SearchOperations(ctx context.Context, query string, perConnLimit int) []RankedOperation {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	if perConnLimit <= 0 {
		perConnLimit = defaultListEndpointsLimit
	}
	t.mu.RLock()
	policy := t.routePolicy
	conns := make([]*conn, 0, len(t.connections))
	for _, c := range t.connections {
		conns = append(conns, c)
	}
	t.mu.RUnlock()

	var out []RankedOperation
	for _, c := range conns {
		out = append(out, t.searchConn(ctx, policy, c, query, perConnLimit)...)
	}
	return out
}

// searchConn ranks one connection's policy-visible operations against the query
// and returns them tagged with the connection name. It mirrors rankWithMode's
// hybrid path but always emits a score (positional in the lexical-fallback
// case) so the aggregate carries a relevance signal into the search allocator.
func (t *Toolkit) searchConn(ctx context.Context, policy RoutePolicy, c *conn, query string, limit int) []RankedOperation {
	visible := filterByRoutePolicy(ctx, policy, c.cfg.ConnectionName, c.operations)
	if len(visible) == 0 {
		return nil
	}
	queryVec, err := t.queryVectorFor(ctx, c, query)
	if err != nil {
		ranked := rankOperations(visible, query, limit)
		out := make([]RankedOperation, len(ranked))
		for i, op := range ranked {
			out[i] = RankedOperation{Connection: c.cfg.ConnectionName, Operation: op, Score: positionalScore(i, len(ranked))}
		}
		return out
	}
	scored := scoreOperations(c, visible, query, queryVec, RankingHybrid)
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	scored = capScored(scored, limit)
	out := make([]RankedOperation, len(scored))
	for i, s := range scored {
		out[i] = RankedOperation{Connection: c.cfg.ConnectionName, Operation: s.op, Score: s.score}
	}
	return out
}

// positionalScore maps a 0-based rank into a descending score in (0,1] so the
// lexical fallback still carries order into the federated allocator: the
// top-ranked operation scores highest. n is the number of ranked operations.
func positionalScore(i, n int) float64 {
	if n <= 0 {
		return 0
	}
	return float64(n-i) / float64(n)
}

// capScored trims a scored slice to at most limit entries.
func capScored(scored []scoredOp, limit int) []scoredOp {
	if limit > 0 && len(scored) > limit {
		return scored[:limit]
	}
	return scored
}

// queryVectorFor returns the query's embedding vector or a non-nil
// error describing why semantic ranking cannot proceed for this
// call. Error returns drive the lexical fallback in rankWithMode
// AND populate the operator-facing Note on the response so the
// model and the operator reading the log know whether the cause
// is operator configuration (errEmbedderNotWired), an absence of
// persisted vectors (errEmbeddingsNotIndexed), an upstream
// embedding failure (wrapped provider Embed error), or a
// zero-vector reply (errEmbeddingsZeroVector).
//
// Operation vectors are pre-computed by the admin handler at
// spec-upsert time and loaded into c.embedVectors at connection
// registration. queryVectorFor never embeds operations inline.
//
// Snapshots t.embedder under the read lock so a concurrent
// SetEmbeddingProvider cannot race with the nil check below.
func (t *Toolkit) queryVectorFor(ctx context.Context, c *conn, query string) ([]float32, error) {
	t.mu.RLock()
	embedder := t.embedder
	t.mu.RUnlock()
	if embedder == nil {
		return nil, errEmbedderNotWired
	}
	if err := checkEmbeddingsReady(c); err != nil {
		return nil, err
	}
	vec, err := embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if zeroVector(vec) {
		return nil, errEmbeddingsZeroVector
	}
	return vec, nil
}

// checkEmbeddingsReady reports whether persisted operation
// embeddings are populated and usable for ranking. Reduces to a
// row-existence check on c.embedVectors because vectors are
// written at spec-upsert time and reloaded into memory at
// connection registration — there's no in-flight or warming
// state to surface.
func checkEmbeddingsReady(c *conn) error {
	if len(c.operations) == 0 {
		return errEmbeddingsNoOps
	}
	if len(c.embedVectors) == 0 {
		return errEmbeddingsNotIndexed
	}
	return nil
}

// scoreOperations builds the per-op score slice. An operation whose embedding
// cannot be located in the connection's index has no semantic signal, so it is
// scored without a vector: under hybrid ranking it still earns its lexical
// component, so an exact path/summary match is not buried under unrelated ops
// that happen to have a tiny positive cosine.
func scoreOperations(c *conn, ops []OperationSummary, query string, queryVec []float32, mode RankingMode) []scoredOp {
	scored := make([]scoredOp, 0, len(ops))
	for _, op := range ops {
		vec, ok := c.embedVectors[embedKey{Spec: op.Spec, OperationID: op.OperationID}]
		if !ok {
			scored = append(scored, scoredOp{op: op, score: scoreWithoutVector(mode, query, op)})
			continue
		}
		score := scoreFor(mode, query, op, queryVec, vec)
		scored = append(scored, scoredOp{op: op, score: score})
	}
	return scored
}

// scoreWithoutVector scores an operation that has no persisted embedding. There
// is no semantic signal, so pure-semantic mode scores 0; hybrid still credits
// the lexical component (the (1-α) term of the blend), so an exact lexical match
// on an unembedded operation outranks unrelated operations with a small positive
// cosine rather than being floored below them.
func scoreWithoutVector(mode RankingMode, query string, op OperationSummary) float64 {
	if mode == RankingSemantic {
		return 0
	}
	return (1 - hybridSemanticWeight) * lexicalScore(op, query)
}

// scoredOp pairs an operation with its rank score so we can sort
// by score then strip back to the slim summary.
type scoredOp struct {
	op    OperationSummary
	score float64
}

// scoreFor returns the per-operation rank score under the given
// mode. Pure semantic uses the normalized cosine (mapped to [0,1])
// directly; hybrid blends with the lexical signal computed by
// lexicalScore.
func scoreFor(mode RankingMode, query string, op OperationSummary, queryVec, opVec []float32) float64 {
	cos := cosineSimilarity(queryVec, opVec)
	semantic := (cos + 1) / 2 // map [-1, 1] to [0, 1]
	if mode == RankingSemantic {
		return semantic
	}
	return hybridSemanticWeight*semantic + (1-hybridSemanticWeight)*lexicalScore(op, query)
}

// lexicalScore returns lexicalMatchPresent (1.0) when every
// whitespace-separated token of query appears as a substring of at
// least one of the operation's searchable fields, else
// lexicalMatchAbsent (0.0). Shared between rankOperations (the
// pure-lexical filter) and scoreFor (the hybrid lexical signal) so
// a multi-token query that narrows results under "ranking=lexical"
// also gets credit under "ranking=hybrid". Without this sharing
// the hybrid lexical component reverts to phrase-match and
// systematically underweights every multi-token intent query.
func lexicalScore(op OperationSummary, query string) float64 {
	tokens := strings.Fields(strings.ToLower(query))
	if len(tokens) == 0 {
		return lexicalMatchAbsent
	}
	if operationMatchesAllTokens(op, tokens) {
		return lexicalMatchPresent
	}
	return lexicalMatchAbsent
}

// indexOf returns the position of op in ops by (Method, Path, Spec).
// Spec is part of the identity because multi-spec catalogs can
// legitimately host the same (Method, Path) tuple in two specs
// (e.g. a vendor that ships "GET /search" in every component spec
// it publishes). Matching on (Method, Path) alone returned the
// first-seen index and paired the visible op with whichever spec's
// embedding happened to sort first. Returns -1 when not found.
func indexOf(ops []OperationSummary, target OperationSummary) int {
	for i, op := range ops {
		if op.Method == target.Method && op.Path == target.Path && op.Spec == target.Spec {
			return i
		}
	}
	return -1
}

// embedInBatches calls embedder.EmbedBatch in chunks of at most
// batchSize texts. Returns a single flat vector slice in the same
// order as the input. Bounded per-call batch size keeps a connection
// with hundreds of operations from sending one giant request that
// risks timing out the per-call context, exceeding the upstream
// model's context window, or hitting an HTTP body cap. The caller's
// ctx is honored across all batches; the first batch error short-
// circuits the rest.
//
// Implemented on top of embedInBatchesIter so the per-chunk
// callback site exists in one place. Kept for callers that want
// the full slice in one go (ranking-side embedQuery tests, etc.).
func embedInBatches(ctx context.Context, embedder embedding.Provider, texts []string, batchSize int, chunkDone func(completed int)) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	err := embedInBatchesIter(ctx, embedder, texts, batchSize, func(_, _ int, vectors [][]float32) error {
		out = append(out, vectors...)
		if chunkDone != nil {
			chunkDone(len(out))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// embedInBatchesIter is the per-chunk variant of embedInBatches.
// onChunk receives (start, end, vectors) for each successful batch
// in arrival order; a non-nil error from onChunk short-circuits
// the loop and is returned wrapped with the chunk's offsets so
// the caller can correlate logs against the failure point.
//
// The per-chunk shape exists so callers that want to persist
// per-batch (the embed-jobs worker, for crash-resume) do not have
// to wait for the full slice to come back before writing the
// first batch to durable storage. Pre-persist + heartbeat in the
// worker is what closes the doom loop described in #479: progress
// from a partial run survives the next attempt's dedup pass.
func embedInBatchesIter(ctx context.Context, embedder embedding.Provider, texts []string, batchSize int, onChunk func(start, end int, vectors [][]float32) error) error {
	if batchSize <= 0 {
		batchSize = len(texts)
	}
	for start := 0; start < len(texts); start += batchSize {
		end := min(start+batchSize, len(texts))
		chunk := texts[start:end]
		vectors, err := embedder.EmbedBatch(ctx, chunk)
		if err != nil {
			return fmt.Errorf("batch [%d:%d]: %w", start, end, err)
		}
		if len(vectors) != len(chunk) {
			return fmt.Errorf("batch [%d:%d]: provider returned %d vectors for %d texts",
				start, end, len(vectors), len(chunk))
		}
		if err := onChunk(start, end, vectors); err != nil {
			return fmt.Errorf("batch [%d:%d] callback: %w", start, end, err)
		}
	}
	return nil
}

// cosineSimilarity returns the cosine of the angle between a and b.
// Returns 0 when either vector is zero (no signal — empty text fed
// to the embedder, or a noop provider in tests). Length mismatch
// returns 0 too — the embedding provider should never produce
// dimension drift, but defending against it keeps a misconfigured
// pipeline from panicking the request handler.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// zeroVector reports whether every element is zero. Some embedding
// providers (the noop fallback) return all-zero vectors when the
// real model is unreachable; treating them as "valid" embeddings
// would let cosineSimilarity return 0 for everything and the rank
// would be arbitrary insertion order. Force the lexical fallback
// instead.
func zeroVector(v []float32) bool {
	for _, x := range v {
		if x != 0 {
			return false
		}
	}
	return true
}
