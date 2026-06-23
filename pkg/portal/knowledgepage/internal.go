// Package knowledgepage is the store and ranked-search backend for canonical
// business/domain knowledge pages (#633): org-shared markdown documents stored
// inline in Postgres so their content is vector- and full-text searchable. It is
// a sibling of the portal package (not part of it) so the portal package stays
// within its size budget; the portal REST handler and the unified-search
// provider consume this package's Store and Searcher.
package knowledgepage

import sq "github.com/Masterminds/squirrel"

// psq is the PostgreSQL statement builder with dollar placeholders, mirroring
// pkg/portal so this subpackage does not depend on portal-internal state.
var psq = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// Search result limits, mirroring pkg/portal so every ranked surface clamps the
// same way. DefaultSearchLimit is the top-K when the caller does not specify
// one; maxSearchLimit bounds an explicit request.
const (
	DefaultSearchLimit = 20
	maxSearchLimit     = 100
)

// clampSearchLimit clamps a requested limit into [1, maxSearchLimit], defaulting
// an unset or out-of-range value to DefaultSearchLimit.
func clampSearchLimit(limit int) int {
	if limit <= 0 || limit > maxSearchLimit {
		return DefaultSearchLimit
	}
	return limit
}

// lexRankNormalization is the ts_rank_cd normalization bitmask: bit 1 divides by
// 1+log(doc length) so short dense matches outrank long single-mentions; bit 32
// maps into (0,1). Mirrors pkg/portal/asset_search.go.
const lexRankNormalization = 1 | 32

// Hybrid fusion: blend semantic cosine similarity with a binary lexical-match
// flag, matching the asset/collection/prompt/memory rankers (alpha 0.6). Kept in
// step with pkg/portal so all surfaces rank on the same curve.
const (
	hybridSemanticWeight = 0.6
	lexicalMatchPresent  = 1.0
	lexicalMatchAbsent   = 0.0
)

// fuseHybridScore blends a row's cosine similarity (mapped from [-1,1] to [0,1])
// with a binary lexical-match flag into a rank score in [0,1].
func fuseHybridScore(cosineSim float64, lexMatch bool) float64 {
	semantic := (cosineSim + 1) / 2
	lex := lexicalMatchAbsent
	if lexMatch {
		lex = lexicalMatchPresent
	}
	return hybridSemanticWeight*semantic + (1-hybridSemanticWeight)*lex
}
