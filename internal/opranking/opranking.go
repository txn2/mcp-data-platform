// Package opranking holds the relevance arithmetic the platform's
// gateway kinds rank a connection's operations with: how a query's
// tokens are matched against an operation's text, how a lexical signal
// and an embedding cosine are blended, and where a ranked result stops
// being relevant.
//
// Two kinds rank operations — the HTTP API gateway over the endpoints
// of an OpenAPI catalog, and the graphql kind over the operations a
// schema exposes — and both answer the same question about different
// records. The formulas and the thresholds live here so a change to the
// blend weight or to the relevance boundary reaches both, and so the
// boundary a caller experiences does not depend on which kind their
// connection happens to be.
//
// The package holds no record type of its own: a caller scores
// Candidates carrying an opaque id and the text and vector to rank on,
// and maps the ids back to its own operations. That is what keeps the
// OpenAPI operation and the GraphQL operation out of here.
package opranking

import (
	"math"
	"sort"
	"strings"
)

// Mode selects the algorithm a query is scored under.
type Mode string

const (
	// ModeLexical is the substring AND filter: deterministic, no
	// embedding provider needed, and the floor when no embedding index
	// is available.
	ModeLexical Mode = "lexical"
	// ModeSemantic ranks by embedding cosine similarity alone.
	ModeSemantic Mode = "semantic"
	// ModeHybrid blends the lexical signal with the cosine.
	ModeHybrid Mode = "hybrid"
)

// The two values the lexical component takes before blending.
const (
	lexicalMatchPresent = 1.0
	lexicalMatchAbsent  = 0.0
)

// NeighborLimit bounds how many operations a ranked result may add
// beyond the ones that matched the query's tokens. Hybrid scores the
// whole catalog, so without a bound the answer to any query is the
// catalog itself, ordered (#1626). Five is enough to recover a query
// phrased in the caller's words rather than the author's, and few
// enough to read whole.
const NeighborLimit = 5

// ScoreFloor is the blended score an operation with no lexical match
// must reach to be offered as an intent neighbor. It marks where the
// embedding model stops discriminating, so it was set against a real
// one: on nomic-embed-text unrelated text sits near a 0.72 normalized
// cosine (0.43 blended), while a query the model separates lifts its
// answer to 0.82 (0.49 blended). Measured runs are in
// build/1626/acceptance.md.
//
// The floor is on the blended score, so pure-semantic ranking — whose
// score is the cosine itself — clears it almost always and is bounded
// by NeighborLimit instead. The floor refuses a field the model did not
// separate; the limit bounds one it did.
const ScoreFloor = 0.45

// SemanticWeight is the alpha in the hybrid score formula:
//
//	score = α * cosine_normalized + (1 − α) * lexical
//
// 0.6 leans semantic — the study referenced in #371 found semantic
// outperforms lexical on free-form queries, but pure semantic loses the
// precision boost an exact identifier match gives. 0.6 keeps semantic
// dominant while preserving that precision.
const SemanticWeight = 0.6

// Candidate is one rankable operation as this package sees it: an
// opaque id the caller maps back, the text a query's tokens are matched
// against, and the operation's embedding when one is indexed.
type Candidate struct {
	// ID identifies the operation to the caller. This package never
	// interprets it.
	ID string
	// Fields are the operation's searchable texts (identifier, path,
	// summary, tags). A token matches when it is a substring of any one
	// of them, case-insensitively.
	Fields []string
	// Vector is the operation's embedding, or nil when the operation
	// has none indexed.
	Vector []float32
}

// Scored is one candidate's verdict: its score under the mode that
// produced it, and whether it contains every token of the query.
type Scored struct {
	// ID is the candidate's id.
	ID string
	// Score is the mode's score in [0,1].
	Score float64
	// Lexical reports the candidate containing every token of the
	// query. It is both what a caller is told and what decides which
	// side of the relevance boundary the candidate falls on.
	Lexical bool
}

// Result is a bounded ranking: the kept candidates, and where the
// boundary between "contains what I asked for" and "close by intent"
// fell.
type Result struct {
	// Kept are the candidates that survived the bound, in score order
	// with the token matches first.
	Kept []Scored
	// MatchedLexical is how many of Kept contain every token.
	MatchedLexical int
	// ShownSemantic is how many of Kept followed them as neighbors by
	// intent.
	ShownSemantic int
}

// Filter applies the lexical AND filter: the candidates containing
// every token of the query, in input order, capped at limit. An empty
// query matches everything. Each kept candidate carries a positional
// score so order survives into a federated result, and Lexical is true
// on every one of them by construction.
func Filter(cands []Candidate, query string, limit int) []Scored {
	kept := make([]Candidate, 0, len(cands))
	tokens := Tokens(query)
	for _, c := range cands {
		if len(tokens) == 0 || MatchesAll(c.Fields, tokens) {
			kept = append(kept, c)
		}
	}
	if limit > 0 && len(kept) > limit {
		kept = kept[:limit]
	}
	out := make([]Scored, 0, len(kept))
	for i, c := range kept {
		out = append(out, Scored{ID: c.ID, Score: Positional(i, len(kept)), Lexical: len(tokens) > 0})
	}
	return out
}

// Score ranks every candidate against the query and its embedding,
// sorted by descending score. queryVec is the query's embedding; a
// candidate with no vector of its own is scored without a semantic
// signal, which under hybrid still earns it the lexical component so an
// exact identifier match is not buried under unrelated operations that
// happen to have a small positive cosine.
func Score(cands []Candidate, query string, queryVec []float32, mode Mode) []Scored {
	tokens := Tokens(query)
	out := make([]Scored, 0, len(cands))
	for _, c := range cands {
		lexical := MatchesAll(c.Fields, tokens) && len(tokens) > 0
		out = append(out, Scored{ID: c.ID, Score: scoreOne(c, lexical, queryVec, mode), Lexical: lexical})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// scoreOne returns one candidate's score under the mode.
func scoreOne(c Candidate, lexical bool, queryVec []float32, mode Mode) float64 {
	lex := lexicalMatchAbsent
	if lexical {
		lex = lexicalMatchPresent
	}
	if len(c.Vector) == 0 {
		if mode == ModeSemantic {
			return 0
		}
		return (1 - SemanticWeight) * lex
	}
	// Map the cosine's [-1,1] onto [0,1] so the blend's two terms are on
	// one scale.
	semantic := (Cosine(queryVec, c.Vector) + 1) / 2
	if mode == ModeSemantic {
		return semantic
	}
	return SemanticWeight*semantic + (1-SemanticWeight)*lex
}

// Bound is where a scored result stops: every candidate containing
// every token, in score order, then at most NeighborLimit that contain
// none but clear ScoreFloor.
//
// The matches lead whatever their scores, because the caller asked for
// them by name — the blend can rank a perfect cosine above a token
// match, and a result opening with the neighbor reads as though the
// query was ignored. limit still caps the total but no longer decides
// where relevance ends, which is what left a page of unrelated
// operations behind every query on a large catalog (#1626).
func Bound(scored []Scored, limit int) Result {
	matched := make([]Scored, 0, len(scored))
	neighbors := make([]Scored, 0, NeighborLimit)
	for _, s := range scored {
		switch {
		case s.Lexical:
			matched = append(matched, s)
		case len(neighbors) < NeighborLimit && s.Score >= ScoreFloor:
			neighbors = append(neighbors, s)
		}
	}
	kept := make([]Scored, 0, len(matched)+len(neighbors))
	kept = append(kept, matched...)
	kept = append(kept, neighbors...)
	if limit > 0 && len(kept) > limit {
		kept = kept[:limit]
	}
	res := Result{Kept: kept}
	for _, s := range kept {
		if s.Lexical {
			res.MatchedLexical++
			continue
		}
		res.ShownSemantic++
	}
	return res
}

// Tokens splits a query into the lowercased tokens a candidate must
// contain all of.
func Tokens(query string) []string {
	return strings.Fields(strings.ToLower(strings.TrimSpace(query)))
}

// MatchesAll reports whether every token appears as a substring of at
// least one field. Tokens are expected pre-lowercased by Tokens;
// fields are lowercased per check, which is cheap next to caching a
// lowercased copy per candidate. No tokens matches everything.
func MatchesAll(fields, tokens []string) bool {
	for _, tok := range tokens {
		if !fieldsContain(fields, tok) {
			return false
		}
	}
	return true
}

// fieldsContain reports tok appearing in any field.
func fieldsContain(fields []string, tok string) bool {
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), tok) {
			return true
		}
	}
	return false
}

// Positional maps a 0-based rank into a descending score in (0,1] so a
// lexical result still carries order into a federated allocator: the
// top-ranked operation scores highest. n is the number of ranked
// candidates.
func Positional(i, n int) float64 {
	if n <= 0 {
		return 0
	}
	return float64(n-i) / float64(n)
}

// Cosine returns the cosine of the angle between a and b. Returns 0
// when either vector is zero (no signal — empty text fed to the
// embedder, or a noop provider in tests). A length mismatch returns 0
// too: an embedding provider should never produce dimension drift, but
// defending against it keeps a misconfigured pipeline from panicking a
// request handler.
func Cosine(a, b []float32) float64 {
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

// IsZero reports every element being zero. Some embedding providers
// return an all-zero vector when the real model is unreachable;
// treating that as a valid embedding would make every cosine 0 and the
// rank arbitrary insertion order, so a caller forces its lexical
// fallback instead.
func IsZero(v []float32) bool {
	for _, x := range v {
		if x != 0 {
			return false
		}
	}
	return true
}
