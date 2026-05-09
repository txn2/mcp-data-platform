package apigateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"sort"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
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

// specHash returns the sha256 of the raw OpenAPI spec, hex-encoded.
// Used as the cache key for pre-computed embeddings: when an
// operator updates a connection's spec, the hash changes and the
// next non-lexical call recomputes embeddings against the new
// operation set.
func specHash(spec string) string {
	sum := sha256.Sum256([]byte(spec))
	return hex.EncodeToString(sum[:])
}

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

func rankWithMode(ctx context.Context, r rankRequest) (ranked []OperationSummary, fallback bool) {
	q := strings.TrimSpace(r.query)
	// Empty query has no semantic signal — the cosine of the
	// embedding-of-empty-string is meaningless. Lexical's "return
	// all up to limit" is the right answer for both empty-query
	// and explicit-lexical-mode.
	if r.mode == RankingLexical || q == "" {
		return rankOperations(r.ops, r.query, r.limit), false
	}
	queryVec, ok := r.tk.queryVectorFor(ctx, r.conn, q)
	if !ok {
		return rankOperations(r.ops, r.query, r.limit), true
	}
	scored := scoreOperations(r.conn, r.ops, q, queryVec, r.mode)
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})
	out := make([]OperationSummary, 0, len(scored))
	for _, s := range scored {
		out = append(out, s.op)
	}
	return capSlice(out, r.limit), false
}

// queryVectorFor centralizes the "do we have everything we need to
// rank semantically?" check. Returns the embedded query vector and
// true when an embedder is wired, the per-connection embeddings are
// populated, and the query embedding itself is non-zero. Any of
// those failing yields false → the caller falls back to lexical
// and surfaces a note. Splits out of rankWithMode to keep its
// cyclomatic complexity under the project's gocyclo ceiling.
func (t *Toolkit) queryVectorFor(ctx context.Context, c *conn, query string) ([]float32, bool) {
	// Snapshot t.embedder under the read lock so a concurrent
	// SetEmbeddingProvider cannot race with the nil check + dereference
	// below. SetEmbeddingProvider holds t.mu.Lock; without this read
	// lock, -race would fire on any deployment that swapped providers
	// at runtime. Production v1 only wires once at startup, so the
	// race is latent today — but matching the file's existing
	// TokenStore() / handleListEndpoints lock pattern keeps it that way.
	t.mu.RLock()
	embedder := t.embedder
	t.mu.RUnlock()
	if embedder == nil {
		return nil, false
	}
	if !t.ensureEmbeddings(ctx, c, embedder) {
		return nil, false
	}
	vec, err := embedder.Embed(ctx, query)
	if err != nil || zeroVector(vec) {
		return nil, false
	}
	return vec, true
}

// scoreOperations builds the per-op score slice. Operations whose
// embedding cannot be located in the connection's index get a 0
// score — won't rank above anything with positive similarity but
// won't be silently dropped either.
func scoreOperations(c *conn, ops []OperationSummary, query string, queryVec []float32, mode RankingMode) []scoredOp {
	scored := make([]scoredOp, 0, len(ops))
	for _, op := range ops {
		idx := indexOf(c.operations, op)
		if idx < 0 || idx >= len(c.embeddings) {
			scored = append(scored, scoredOp{op: op, score: 0})
			continue
		}
		score := scoreFor(mode, query, op, queryVec, c.embeddings[idx])
		scored = append(scored, scoredOp{op: op, score: score})
	}
	return scored
}

// scoredOp pairs an operation with its rank score so we can sort
// by score then strip back to the slim summary.
type scoredOp struct {
	op    OperationSummary
	score float64
}

// scoreFor returns the per-operation rank score under the given
// mode. Pure semantic uses the normalized cosine (mapped to [0,1])
// directly; hybrid blends with the lexical 0/1 signal.
func scoreFor(mode RankingMode, query string, op OperationSummary, queryVec, opVec []float32) float64 {
	cos := cosineSimilarity(queryVec, opVec)
	semantic := (cos + 1) / 2 // map [-1, 1] → [0, 1]
	if mode == RankingSemantic {
		return semantic
	}
	// Hybrid.
	lex := lexicalMatchAbsent
	if operationMatches(op, strings.ToLower(query)) {
		lex = lexicalMatchPresent
	}
	return hybridSemanticWeight*semantic + (1-hybridSemanticWeight)*lex
}

// indexOf returns the position of op in ops by (Method, Path) — the
// stable identity per the OpenAPI spec. -1 when not found.
func indexOf(ops []OperationSummary, target OperationSummary) int {
	for i, op := range ops {
		if op.Method == target.Method && op.Path == target.Path {
			return i
		}
	}
	return -1
}

// ensureEmbeddings populates c.embeddings if not already done for
// the current spec hash. Returns true when the conn has usable
// embeddings after the call (either populated this call or already
// populated by a prior call). Returns false on provider failure or
// on a connection that has no operations to embed.
//
// Concurrent callers serialize through c.embedMu so the embedding
// service is hit at most once per (connection, spec_hash) lifecycle.
//
// Failure handling distinguishes transient from definitive:
//   - ctx.Err() (caller-side cancellation, deadline exceeded) and a
//     length-mismatched batch (provider misconfiguration that may
//     resolve on a hot-reconfig) are NOT sticky — the next call
//     re-attempts. Without this carve-out, a single MCP-client
//     cancellation during a slow first embed would permanently
//     disable semantic ranking until pod restart.
//   - Any other provider error sets c.embedFailed so a misbehaving
//     embedding service does not get re-hammered every call.
//
// embedder is taken as an explicit argument (rather than reading
// t.embedder again) so the snapshot taken under t.mu.RLock in the
// caller is the value used here too — no second unguarded read.
func (*Toolkit) ensureEmbeddings(ctx context.Context, c *conn, embedder embedding.Provider) bool {
	c.embedMu.Lock()
	defer c.embedMu.Unlock()
	if len(c.embedTexts) == 0 {
		return false
	}
	if len(c.embeddings) == len(c.embedTexts) {
		return true
	}
	if c.embedFailed {
		return false
	}
	vectors, err := embedder.EmbedBatch(ctx, c.embedTexts)
	if err != nil || len(vectors) != len(c.embedTexts) {
		// Transient signals do NOT poison the cache. Definitive
		// provider errors do, so a misconfigured Ollama URL doesn't
		// produce a fresh hit on every tool call.
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			c.embedFailed = true
		}
		return false
	}
	c.embeddings = vectors
	return true
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
