package apigateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
)

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

// RankingMode selects the algorithm api_discover uses to score
// candidate operations against the model's query.
//
// Lexical is the substring-match filter that v1 shipped: fast,
// deterministic, no embedding-provider dependency. It is the floor
// when no embedding index is available, and the explicit opt-out
// when a caller wants to bypass semantic ranking. Misses on
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

// RankingMode values exposed on the api_discover schema.
const (
	// RankingLexical is the v1 substring-match filter. It is the
	// floor when no embedding index is available; with embeddings
	// present, an omitted ranking defaults to hybrid (#858).
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

// semanticNeighborLimit bounds the operations a ranked result may add beyond
// the ones that matched. Hybrid scores the whole catalog, so without a bound
// the answer to any query is the catalog itself, ordered (#1626). Five is
// enough to recover a query phrased in the caller's words rather than the spec
// author's, and few enough to read whole.
const semanticNeighborLimit = 5

// hybridScoreFloor is the score an operation with no lexical match must reach
// to be offered as an intent neighbor. It marks where the embedding model
// stops discriminating, so it was set against a real one: on nomic-embed-text
// unrelated text sits near a 0.72 normalized cosine (0.43 blended), while a
// query the model separates lifts its answer to 0.82 (0.49 blended). Measured
// runs are in build/1626/acceptance.md.
//
// The floor is on the blended score, so pure-semantic ranking -- whose score
// is the cosine itself -- clears it almost always and is bounded by
// semanticNeighborLimit instead. The floor refuses a field the model did not
// separate; the limit bounds one it did.
const hybridScoreFloor = 0.45

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

// rankedResult is one ranked operations level: the rows, and where the
// boundary between "contains what I asked for" and "close by intent" fell.
// fallbackReason is empty unless a non-lexical ranking was forced back to
// lexical (provider unwired, embed failed, catalog not indexed); the caller
// renders it as the response note.
type rankedResult struct {
	operations     []RankedOperationSummary
	matchedLexical int
	shownSemantic  int
	fallbackReason string
}

// rankWithMode dispatches to the per-mode ranker and bounds what comes back.
// Lexical is an AND filter and needs no further cut; semantic and hybrid score
// every visible operation, so boundByRelevance is what stops the sorted
// catalog being the answer. Falls back to lexical, with the reason on the
// result, when the embedding pipeline is unavailable.
func rankWithMode(ctx context.Context, r rankRequest) rankedResult {
	q := strings.TrimSpace(r.query)
	// Empty query has no semantic signal: the cosine of the
	// embedding-of-empty-string is meaningless, and "return all up to limit"
	// is the answer. Such a result is not ranked, so its rows carry neither a
	// score nor a match flag.
	if q == "" {
		return rankedResult{operations: plainSummaries(rankOperations(r.ops, r.query, r.limit))}
	}
	if r.mode == RankingLexical {
		return lexicalResult(rankOperations(r.ops, r.query, r.limit))
	}
	queryVec, err := r.tk.queryVectorFor(ctx, r.conn, q)
	if err != nil {
		slog.Warn("apigateway: semantic ranking fell back to lexical",
			logKeyConnection, r.conn.cfg.ConnectionName,
			"mode", string(r.mode), logKeyError, err)
		out := lexicalResult(rankOperations(r.ops, r.query, r.limit))
		out.fallbackReason = err.Error()
		return out
	}
	scored := scoreOperations(r.conn, r.ops, q, queryVec, r.mode)
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})
	return boundByRelevance(scored, r.limit)
}

// plainSummaries renders an unranked list: the operations as they are, with no
// score and no match flag, because nothing was matched against.
func plainSummaries(ops []OperationSummary) []RankedOperationSummary {
	out := make([]RankedOperationSummary, 0, len(ops))
	for _, op := range ops {
		out = append(out, RankedOperationSummary{OperationSummary: op})
	}
	return out
}

// lexicalResult renders the AND filter's output. Every row matched by
// construction, and the score is positional because the filter computes no
// other, so order still carries into the result.
func lexicalResult(ops []OperationSummary) rankedResult {
	out := make([]RankedOperationSummary, 0, len(ops))
	for i, op := range ops {
		score, matched := positionalScore(i, len(ops)), true
		out = append(out, RankedOperationSummary{
			OperationSummary: op, Score: &score, LexicalMatch: &matched,
		})
	}
	return rankedResult{operations: out, matchedLexical: len(out)}
}

// boundByRelevance is where a scored result stops: every operation containing
// every token, in score order, then at most semanticNeighborLimit that contain
// none but clear hybridScoreFloor.
//
// The matches lead whatever their scores, because the caller asked for them by
// name -- the blend can rank a perfect cosine above a token match, and a
// result opening with the neighbor reads as though the query was ignored.
// limit still caps the total but no longer decides where relevance ends, which
// is what left a 50-row page of unrelated operations behind every query on a
// large catalog (#1626).
func boundByRelevance(scored []scoredOp, limit int) rankedResult {
	matched := make([]scoredOp, 0, len(scored))
	neighbors := make([]scoredOp, 0, semanticNeighborLimit)
	for _, s := range scored {
		switch {
		case s.lexical:
			matched = append(matched, s)
		case len(neighbors) < semanticNeighborLimit && s.score >= hybridScoreFloor:
			neighbors = append(neighbors, s)
		}
	}
	kept := capScored(append(matched, neighbors...), limit)
	out := make([]RankedOperationSummary, 0, len(kept))
	res := rankedResult{}
	for _, s := range kept {
		score, lexical := s.score, s.lexical
		out = append(out, RankedOperationSummary{
			OperationSummary: s.op, Score: &score, LexicalMatch: &lexical,
		})
		if lexical {
			res.matchedLexical++
		} else {
			res.shownSemantic++
		}
	}
	res.operations = out
	return res
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
// ranking api_discover exposes, aggregated across connections instead of
// scoped to one.
//
// Per-connection route policy is applied first (ctx-scoped), so the result
// never includes an operation the caller's persona could not invoke. That is
// the per-source access scope for the endpoints corpus, enforced fail-closed by
// the same filter api_discover uses; a search that federates endpoints
// cannot leak a route a scoped api_discover call would have hidden.
func (t *Toolkit) SearchOperations(ctx context.Context, query string, perConnLimit int) []RankedOperation {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	if perConnLimit <= 0 {
		perConnLimit = defaultDiscoverLimit
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

// embeddingsAvailable reports whether this connection can be ranked
// semantically right now: an embedding provider is wired on the
// toolkit AND the connection has persisted operation vectors loaded.
// It is the gate for the default-ON hybrid upgrade in
// discoverOperations — when true, an omitted ranking resolves to
// hybrid rather than lexical, so the semantic path is the default
// whenever its requirement is met (#858). Snapshots t.embedder under
// the read lock, mirroring queryVectorFor, so a concurrent
// SetEmbeddingProvider cannot race the nil check.
func (t *Toolkit) embeddingsAvailable(c *conn) bool {
	t.mu.RLock()
	embedder := t.embedder
	t.mu.RUnlock()
	if embedder == nil {
		return false
	}
	return checkEmbeddingsReady(c) == nil
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
		lexical := lexicalScore(op, query) == lexicalMatchPresent
		vec, ok := c.embedVectors[embedKey{Spec: op.Spec, OperationID: op.OperationID}]
		if !ok {
			scored = append(scored, scoredOp{op: op, score: scoreWithoutVector(mode, query, op), lexical: lexical})
			continue
		}
		score := scoreFor(mode, query, op, queryVec, vec)
		scored = append(scored, scoredOp{op: op, score: score, lexical: lexical})
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
// by score then strip back to the slim summary. lexical records whether the
// operation contains every token of the query, which is both what the caller
// is told (lexical_match) and what decides which side of the relevance
// boundary the operation falls on.
type scoredOp struct {
	op      OperationSummary
	score   float64
	lexical bool
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
