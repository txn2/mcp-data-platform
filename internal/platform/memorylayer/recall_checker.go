package memorylayer

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	memorykit "github.com/txn2/mcp-data-platform/pkg/toolkits/memory"
)

// recallCandidateK is how many nearest neighbors the recall-first check fetches
// before applying the entity-URN gate. Small: a true restatement scores near the
// top, so a handful of candidates is enough to find the right-entity match even
// when an unrelated note about another table edges slightly higher on text alone.
// It is one more than the five candidates it has always meant, because the
// candidate record is stored before the check runs and is its own best match
// (#1987).
const recallCandidateK = 6

// recallChecker implements memorykit.RecallChecker by running a raw cosine
// (vector-only) similarity search over the caller's own memory and returning
// every match clearing the threshold that also shares an entity URN with the
// candidate (when it has any). It uses VectorSearch's raw cosine (not the search
// router's min-max normalization, nor the fused hybrid score) so MinScore reads
// as a true cosine. The candidate embedding is supplied by the caller (the
// vector the memory index job just wrote), so this type needs no embedder of
// its own.
type recallChecker struct {
	store memory.Store
}

// Matches returns the caller's memories similar to the candidate, best first, or
// nil when there is no embedding, no caller, or nothing clears MinScore and the
// entity-URN gate. The candidate is already stored when this runs (#1987), so
// it matches itself and the caller skips its own id. Superseded rows are
// excluded from the search: without that, a superseded (near-identical) predecessor can outrank its active successor,
// absorb the supersede, and leave two active duplicates standing (#762). Stale
// rows remain matchable — a restatement is exactly how a stale record gets
// corrected — and archived rows are excluded by the store's default.
func (c *recallChecker) Matches(ctx context.Context, q memorykit.RecallQuery) ([]memorykit.RecallMatch, error) {
	if q.CallerEmail == "" || c.store == nil || len(q.Embedding) == 0 {
		return nil, nil
	}
	res, err := c.store.VectorSearch(ctx, memory.VectorQuery{
		Embedding:       q.Embedding,
		CreatedBy:       q.CallerEmail,
		MinScore:        q.MinScore,
		ExcludeStatuses: []string{memory.StatusSuperseded},
		Limit:           recallCandidateK,
	})
	if err != nil {
		return nil, fmt.Errorf("recall-first similarity: %w", err)
	}
	// Results are sorted by descending similarity. Keep every one that clears the
	// threshold and, when the candidate concerns specific entities, shares an
	// entity URN — so knowledge about table A never supersedes knowledge about B.
	var matches []memorykit.RecallMatch
	for i := range res {
		if res[i].Score < q.MinScore {
			break
		}
		if len(q.EntityURNs) > 0 && !sharesAny(res[i].Record.EntityURNs, q.EntityURNs) {
			continue
		}
		matches = append(matches, memorykit.RecallMatch{ID: res[i].Record.ID, Score: res[i].Score, CreatedAt: res[i].Record.CreatedAt})
	}
	return matches, nil
}

// sharesAny reports whether a and b have at least one element in common.
func sharesAny(a, b []string) bool {
	set := make(map[string]struct{}, len(a))
	for _, x := range a {
		set[x] = struct{}{}
	}
	for _, y := range b {
		if _, ok := set[y]; ok {
			return true
		}
	}
	return false
}
