//go:build integration

package sqlrank

// SQLSamples renders the ranked statement in both modes over candidates that
// read the real assets table, for the gate that hands store SQL to a real
// PostgreSQL to parse and plan (#1512).
func SQLSamples() map[string]string {
	candidates := "SELECT id, name, embedding, search_fts FROM portal_assets WHERE owner_id = $1"
	args := []any{"u1"}
	hybrid, _ := BuildRanked(Ranking{Candidates: candidates, Args: args, FTS: "search_fts", QueryText: "q", Embedding: make([]float32, 768), Limit: 10})
	lexical, _ := BuildRanked(Ranking{Candidates: candidates, Args: args, FTS: "search_fts", QueryText: "q", Limit: 10})
	return map[string]string{"BuildRanked/hybrid": hybrid, "BuildRanked/lexical": lexical}
}
