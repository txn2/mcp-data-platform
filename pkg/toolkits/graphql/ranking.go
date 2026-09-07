package graphql

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/gqlschema"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/opranking"
)

// RankingMode selects how graphql_discover scores an operation against
// the caller's query. The three modes and the arithmetic behind them
// are the platform's, shared with the API gateway through
// internal/opranking, so a caller's experience of relevance does not
// depend on which kind their connection is.
type RankingMode string

// RankingMode values exposed on the graphql_discover schema.
const (
	// RankingLexical is the substring AND filter: every token of the
	// query must appear in the operation's text. Deterministic and
	// needs no embedding provider.
	RankingLexical RankingMode = RankingMode(opranking.ModeLexical)
	// RankingSemantic ranks by embedding cosine similarity alone.
	RankingSemantic RankingMode = RankingMode(opranking.ModeSemantic)
	// RankingHybrid blends the lexical signal with the cosine. It is
	// what an omitted ranking resolves to whenever the connection has
	// an embedding index.
	RankingHybrid RankingMode = RankingMode(opranking.ModeHybrid)
)

// Why a ranking fell back to lexical. Each is reported to the caller as
// the response note, because the four causes have different remedies:
// operator configuration, an index that has not run, an upstream
// failure, and a misconfigured model.
var (
	errEmbedderNotWired     = errors.New("embedding provider not configured on this deployment")
	errEmbeddingsNoOps      = errors.New("connection has no operations to embed")
	errEmbeddingsNotIndexed = errors.New("this schema version has not been indexed yet; ranking is lexical until the indexer runs")
	errEmbeddingsZeroVector = errors.New("query embedding is the zero vector (misconfigured embedding model)")
)

// ParseRankingMode resolves the caller's ranking argument. An empty
// value is resolved by the caller, which upgrades it to hybrid when the
// connection has an index.
func ParseRankingMode(s string) (RankingMode, error) {
	switch RankingMode(strings.ToLower(strings.TrimSpace(s))) {
	case "":
		return "", nil
	case RankingLexical:
		return RankingLexical, nil
	case RankingSemantic:
		return RankingSemantic, nil
	case RankingHybrid:
		return RankingHybrid, nil
	default:
		return "", fmt.Errorf("graphql: invalid ranking %q (want lexical, semantic, or hybrid)", s)
	}
}

// rankedOperations is one ranked page: the operations, where the
// boundary between "contains what I asked for" and "close by intent"
// fell, and why a non-lexical ranking was forced back to lexical.
type rankedOperations struct {
	operations     []RankedOperation
	matchedLexical int
	shownSemantic  int
	fallbackReason string
}

// rankRequest bundles what one ranking pass needs. A struct rather than a
// parameter list, for the reason the API gateway's own rankRequest is one:
// it keeps the call site self-documenting and under the argument ceiling.
type rankRequest struct {
	conn  *conn
	ops   []gqlschema.Operation
	query string
	limit int
	mode  RankingMode
}

// rank scores a connection's visible operations against a query. An
// empty query is not ranked at all: there is nothing to match, and the
// answer is the operations themselves up to the limit.
func (t *Toolkit) rank(ctx context.Context, r rankRequest) rankedOperations {
	q := strings.TrimSpace(r.query)
	if q == "" {
		return rankedOperations{operations: summaries(capOps(r.ops, r.limit), nil)}
	}
	byID, cands := candidates(r.conn, r.ops)
	if r.mode == RankingLexical {
		scored := opranking.Filter(cands, q, r.limit)
		return rankedOperations{operations: summaries(nil, resolve(byID, scored)), matchedLexical: len(scored)}
	}
	queryVec, err := t.queryVectorFor(ctx, r.conn, q)
	if err != nil {
		slog.Warn("graphql: semantic ranking fell back to lexical",
			logKeyConnection, logsan.SanitizeForLog(r.conn.cfg.ConnectionName),
			"mode", string(r.mode), logKeyError, err)
		scored := opranking.Filter(cands, q, r.limit)
		return rankedOperations{
			operations:     summaries(nil, resolve(byID, scored)),
			matchedLexical: len(scored),
			fallbackReason: err.Error(),
		}
	}
	bounded := opranking.Bound(opranking.Score(cands, q, queryVec, opranking.Mode(r.mode)), r.limit)
	return rankedOperations{
		operations:     summaries(nil, resolve(byID, bounded.Kept)),
		matchedLexical: bounded.MatchedLexical,
		shownSemantic:  bounded.ShownSemantic,
	}
}

// scoredOperation pairs an operation with the verdict the shared ranker
// reached about it.
type scoredOperation struct {
	op      gqlschema.Operation
	score   float64
	lexical bool
}

// candidates projects a connection's operations onto the shared
// ranker's input and returns the map back to them.
func candidates(c *conn, ops []gqlschema.Operation) (map[string]gqlschema.Operation, []opranking.Candidate) {
	c.schemaMu.RLock()
	vectors := c.vectors
	c.schemaMu.RUnlock()
	byID := make(map[string]gqlschema.Operation, len(ops))
	cands := make([]opranking.Candidate, 0, len(ops))
	for _, op := range ops {
		byID[op.ID] = op
		cands = append(cands, opranking.Candidate{
			ID:     op.ID,
			Fields: gqlschema.SearchFields(op),
			Vector: vectors[op.ID],
		})
	}
	return byID, cands
}

// resolve maps the shared ranker's verdicts back onto operations,
// dropping any id the index no longer holds.
func resolve(byID map[string]gqlschema.Operation, scored []opranking.Scored) []scoredOperation {
	out := make([]scoredOperation, 0, len(scored))
	for _, s := range scored {
		op, ok := byID[s.ID]
		if !ok {
			continue
		}
		out = append(out, scoredOperation{op: op, score: s.Score, lexical: s.Lexical})
	}
	return out
}

// summaries renders either an unranked list (plain, no score) or a
// ranked one. Exactly one of the two arguments is non-nil.
func summaries(plain []gqlschema.Operation, scored []scoredOperation) []RankedOperation {
	if plain != nil {
		out := make([]RankedOperation, 0, len(plain))
		for _, op := range plain {
			out = append(out, RankedOperation{OperationSummary: summarize(op)})
		}
		return out
	}
	out := make([]RankedOperation, 0, len(scored))
	for _, s := range scored {
		score, lexical := s.score, s.lexical
		out = append(out, RankedOperation{
			OperationSummary: summarize(s.op), Score: &score, LexicalMatch: &lexical,
		})
	}
	return out
}

// capOps trims an operation slice to at most limit entries.
func capOps(ops []gqlschema.Operation, limit int) []gqlschema.Operation {
	if limit > 0 && len(ops) > limit {
		return ops[:limit]
	}
	return ops
}

// queryVectorFor returns the query's embedding, or the reason semantic
// ranking cannot proceed for this call. The error drives the lexical
// fallback and becomes the response note, so the model and the operator
// reading the log can tell operator configuration from an index that
// has not run from an upstream failure.
func (t *Toolkit) queryVectorFor(ctx context.Context, c *conn, query string) ([]float32, error) {
	t.mu.RLock()
	embedder := t.embedder
	t.mu.RUnlock()
	if embedder == nil {
		return nil, errEmbedderNotWired
	}
	if err := embeddingsReady(c); err != nil {
		return nil, err
	}
	vec, err := embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if opranking.IsZero(vec) {
		return nil, errEmbeddingsZeroVector
	}
	return vec, nil
}

// embeddingsAvailable reports whether this connection can be ranked
// semantically right now. It is the gate for the default-on hybrid
// upgrade: an omitted ranking resolves to hybrid whenever its
// requirement is met.
func (t *Toolkit) embeddingsAvailable(c *conn) bool {
	t.mu.RLock()
	embedder := t.embedder
	t.mu.RUnlock()
	return embedder != nil && embeddingsReady(c) == nil
}

// embeddingsReady reports whether persisted operation vectors are
// populated for this connection's current schema.
func embeddingsReady(c *conn) error {
	c.schemaMu.RLock()
	defer c.schemaMu.RUnlock()
	if len(c.operations) == 0 {
		return errEmbeddingsNoOps
	}
	if len(c.vectors) == 0 {
		return errEmbeddingsNotIndexed
	}
	return nil
}
