package memory

// hybridSemanticWeight is the alpha in the hybrid recall score:
//
//	score = α * semantic + (1 − α) * lexical
//
// where semantic is the query/record cosine mapped to [0,1] and lexical
// is a binary "the content matched the full-text query" signal. 0.6
// leans semantic so free-form intent queries still rank by meaning,
// while the 0.4 lexical term gives an exact-identifier match (entity
// URN, column name, error code) a decisive boost over a merely
// semantically-near row that does not contain the term. The weight and
// the binary-lexical blend deliberately match
// pkg/toolkits/apigateway/ranking.go (hybridSemanticWeight = 0.6) so the
// two toolkits rank hybrid results on the same curve; keep them in step
// if either is tuned.
const hybridSemanticWeight = 0.6

// lexical component values before blending, named to keep the magic
// 0.0/1.0 out of the formula (matches the api-gateway precedent).
const (
	lexicalMatchPresent = 1.0
	lexicalMatchAbsent  = 0.0
)

// fuseHybridScore blends a record's cosine similarity with its lexical
// match flag into a single rank score in [0,1]. cosineSim is the raw
// cosine in [-1,1] (1 - the pgvector `<=>` distance); it is mapped to
// [0,1] before blending. lexMatch is true when the record's content
// matched the lexical full-text query.
//
// This is the on-request-path ranking function: HybridSearch calls it
// for every candidate row, so it is exercised by every hybrid recall
// (and is the unit covered by the ranking evaluation test).
func fuseHybridScore(cosineSim float64, lexMatch bool) float64 {
	semantic := (cosineSim + 1) / 2 // map [-1,1] -> [0,1]
	lex := lexicalMatchAbsent
	if lexMatch {
		lex = lexicalMatchPresent
	}
	return hybridSemanticWeight*semantic + (1-hybridSemanticWeight)*lex
}
