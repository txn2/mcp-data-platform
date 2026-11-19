package sqlrank

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A preferred column breaks ties after the score, true first, ahead of the
// name order (#2503); without one the order is as before.
func TestBuildRankedPrefer(t *testing.T) {
	r := Ranking{Candidates: "SELECT id, name, embedding, fts, true AS preferred FROM t", FTS: "fts", QueryText: "q", Limit: 5}
	plain, _ := BuildRanked(r)
	assert.Contains(t, plain, "ORDER BY score DESC, c.name, c.id")
	assert.NotContains(t, plain, "preferred DESC")

	r.Prefer = "preferred"
	preferred, _ := BuildRanked(r)
	assert.Contains(t, preferred, "ORDER BY score DESC, c.preferred DESC, c.name, c.id")
	assert.Less(t, strings.Index(preferred, "score DESC"), strings.Index(preferred, "c.preferred DESC"),
		"relevance first: the preference only orders equally scored rows")
}
