package apigateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
)

// embedBatchSize is the maximum number of texts fed to the
// embedding provider's EmbedBatch in a single call. Bounded so a
// connection with hundreds of operations does not POST one giant
// batch that risks timing out the request context, exceeding an
// upstream model's context window, or hitting an HTTP body cap.
// 32 is small enough that a single timed-out batch only loses
// progress for that chunk and large enough to amortize per-call
// overhead.
const embedBatchSize = 32

// errEmbedderNotWired is the sentinel returned by queryVectorFor
// when no embedding provider has been configured on the toolkit.
// Kept distinct from generic embedder errors so the fallback note
// can phrase the cause correctly: this is operator configuration,
// not an upstream failure.
var errEmbedderNotWired = errors.New("embedding provider not configured on this connection")

// Embedding-state sentinels returned by checkEmbeddingsReady. Each
// one carries enough context for the operator to act:
//
//   - errEmbeddingsNoOps: nothing to embed (the connection has no
//     operations; semantic ranking is meaningless).
//   - errEmbeddingsFailed: a prior warmer attempt failed with a
//     definitive error (not a transient cancel); reload the
//     connection or restart the platform to retry.
//   - errEmbeddingsWarming: the background warmer is in flight;
//     fall back to lexical for this call and retry shortly.
//   - errEmbeddingsNotStarted: no warmer ever ran (typically
//     because the embedding provider was wired AFTER the
//     connection registered and the toolkit was never told to
//     start one); operator should reload the connection.
//   - errEmbeddingsZeroVector: the provider returned a zero vector
//     for the query, which produces meaningless cosine similarity;
//     points at a misconfigured embedding model.
var (
	errEmbeddingsNoOps      = errors.New("connection has no operations to embed")
	errEmbeddingsFailed     = errors.New("operation-embedding warm-up failed; reload the connection to retry")
	errEmbeddingsWarming    = errors.New("operation embeddings still warming; falling back to lexical until ready")
	errEmbeddingsNotStarted = errors.New("operation-embedding warm-up has not started; reload the connection")
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

// queryVectorFor returns the query's embedding vector or a non-nil
// error describing why semantic ranking cannot proceed for this
// call. Error returns drive the lexical fallback in rankWithMode
// AND populate the operator-facing Note on the response so the
// model and the operator reading the log know whether the cause
// is operator configuration (errEmbedderNotWired), an upstream
// embedding failure (provider Embed errors), a previously-cached
// per-connection failure (c.embedFailed), an in-flight cold-start
// warmer that has not yet populated this connection's operation
// embeddings (errEmbeddingsWarming), or a zero-vector reply from
// the provider (errEmbeddingsZeroVector).
//
// The operation-embedding population happens in a background
// warmer kicked off at AddConnection / SetEmbeddingProvider time.
// queryVectorFor never does that work inline. A request that
// arrives before the warmer finishes returns
// errEmbeddingsWarming so the caller falls back to lexical inside
// the request budget instead of synchronously triggering a
// multi-minute per-operation pass.
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

// checkEmbeddingsReady reports whether the per-connection
// operation embeddings are populated and usable. Returns a
// distinct sentinel for every reason a non-lexical rank cannot
// proceed so the operator-facing fallback Note names the actual
// cause: warmer still in flight, prior warmer failed definitively,
// or the connection has no operations to embed in the first place.
func checkEmbeddingsReady(c *conn) error {
	c.embedMu.Lock()
	defer c.embedMu.Unlock()
	if len(c.embedTexts) == 0 {
		return errEmbeddingsNoOps
	}
	if len(c.embeddings) == len(c.embedTexts) {
		return nil
	}
	if c.embedFailed {
		return errEmbeddingsFailed
	}
	if c.embedInFlight {
		return errEmbeddingsWarming
	}
	return errEmbeddingsNotStarted
}

// ensureEmbeddings is the synchronous worker that populates the
// per-connection operation embeddings. Called by warmEmbeddings
// (in a goroutine at AddConnection / SetEmbeddingProvider time)
// and exposed at package scope so unit tests can drive the worker
// with controlled contexts (e.g., a pre-canceled ctx to verify
// the carve-out that keeps a transient cancellation from poisoning
// the cache).
//
// Concurrent callers serialize through the embedInFlight guard:
// the loser returns errEmbeddingsWarming so a second worker does
// not double-hit the embedder for the same connection. Failures
// fall into two buckets: transient (ctx.Canceled / DeadlineExceeded)
// do not poison the cache so a later attempt can succeed; every
// other provider error sets c.embedFailed so a misbehaving
// embedder is not re-hammered on every subsequent call.
func (*Toolkit) ensureEmbeddings(ctx context.Context, c *conn, embedder embedding.Provider) error {
	c.embedMu.Lock()
	if len(c.embedTexts) == 0 {
		c.embedMu.Unlock()
		return errEmbeddingsNoOps
	}
	if len(c.embeddings) == len(c.embedTexts) {
		c.embedMu.Unlock()
		return nil
	}
	if c.embedFailed {
		c.embedMu.Unlock()
		return errEmbeddingsFailed
	}
	if c.embedInFlight {
		c.embedMu.Unlock()
		return errEmbeddingsWarming
	}
	c.embedInFlight = true
	textCount := len(c.embedTexts)
	c.embedMu.Unlock()

	start := time.Now()
	slog.Info("apigateway: warming connection embeddings",
		logKeyConnection, c.cfg.ConnectionName, "count", textCount)
	vectors, err := embedInBatches(ctx, embedder, c.embedTexts, embedBatchSize)
	c.embedMu.Lock()
	c.embedInFlight = false
	switch {
	case err != nil:
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			c.embedFailed = true
		}
		c.embedMu.Unlock()
		return fmt.Errorf("embed operation batch: %w", err)
	case len(vectors) != len(c.embedTexts):
		c.embedFailed = true
		c.embedMu.Unlock()
		return fmt.Errorf("embed operation batch: provider returned %d vectors for %d texts",
			len(vectors), len(c.embedTexts))
	default:
		c.embeddings = vectors
		c.embedMu.Unlock()
		slog.Info("apigateway: embeddings warm-up complete",
			logKeyConnection, c.cfg.ConnectionName,
			"count", textCount, "duration", time.Since(start))
		return nil
	}
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
func embedInBatches(ctx context.Context, embedder embedding.Provider, texts []string, batchSize int) ([][]float32, error) {
	if batchSize <= 0 {
		batchSize = len(texts)
	}
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += batchSize {
		end := min(start+batchSize, len(texts))
		chunk := texts[start:end]
		vectors, err := embedder.EmbedBatch(ctx, chunk)
		if err != nil {
			return nil, fmt.Errorf("batch [%d:%d]: %w", start, end, err)
		}
		if len(vectors) != len(chunk) {
			return nil, fmt.Errorf("batch [%d:%d]: provider returned %d vectors for %d texts",
				start, end, len(vectors), len(chunk))
		}
		out = append(out, vectors...)
	}
	return out, nil
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
