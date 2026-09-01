package knowledge

import (
	"math"
	"sort"
)

// maxNormalizedScore is the score assigned to a provider's hits when they have
// no spread to min-max scale (a single hit, or all-equal scores): they are
// treated as equally, maximally relevant within that provider.
const maxNormalizedScore = 1.0

// Allocator defaults. These are deliberately conservative starting points
// (#645 leaves them to be tuned empirically against real agent behavior):
//
//   - floorPerSource gives every source with any relevant hit at least this
//     many display slots, so breadth is always visible even when a source is
//     not the strongest. A floor of 1 is the minimum that still proves a
//     source matched.
//   - ceilingFraction caps any single source at this fraction of the total
//     budget during the balanced fill, so no one source runs away with the
//     response. Leftover budget that no other source can absorb is then
//     redistributed by relaxing the ceiling, so the budget is never wasted
//     when only a few sources have matches.
const (
	floorPerSource  = 1
	ceilingFraction = 0.5
)

// SourceGroup is the displayed hits for one source, in that source's own
// relevance order. Grouping by source (rather than one flat relevance list) is
// the anti-tunnel shape: the agent sees that answers exist across memory, the
// catalog, endpoints, and prompts at once, instead of a top list one strong
// source dominates.
type SourceGroup struct {
	Source string `json:"source"`
	Hits   []Hit  `json:"hits"`
}

// SourceCoverage reports, per source, how many candidates matched the query and
// how many of those are shown in the grouped result. Matched can exceed Shown
// when the balanced allocator spent its budget elsewhere; that gap is the
// anti-tunnel signal that tells the agent where unshown answers live ("14
// datasets matched, 3 shown").
//
// MatchedCapped says which kind of number Matched is (#1585). The router asks
// each provider for a bounded candidate list, so a source with more matching
// records than that bound returns the bound. Without the flag those two states
// are one number on the wire: a source that matched exactly the bound and a
// source that matched thousands both read as "matched 25, shown 25", which
// tells the agent it has seen everything precisely when it has not. The flag
// makes Matched a floor rather than a total: false (omitted) means the count is
// exact, true means at least that many. A count the connection boundary
// shortened is exact for what it says -- Matched is what the caller may see,
// and Withheld carries the rest -- so a withheld-shortened arm is not flagged.
//
// Withheld is the separate, security-shaped gap: candidates that matched the
// query and were then removed because they belong to a connection the caller's
// persona may not reach (#1108). It is reported rather than silently subtracted
// because search tells agents it is the one way to discover: a result set that
// quietly shortens reads as "this does not exist" and sends the agent off to
// re-derive it, while a withheld count reads as "present, but not yours to see",
// which is an actionable state.
type SourceCoverage struct {
	Source        string `json:"source"`
	Matched       int    `json:"matched"`
	Shown         int    `json:"shown"`
	MatchedCapped bool   `json:"matched_capped,omitempty"`
	Withheld      int    `json:"withheld,omitempty"`
}

// sourceResult is one provider arm's outcome: the candidates it returned after
// the connection boundary was applied, how many that boundary removed, and
// whether the arm had more candidates than the router asked to look at. The
// withheld count travels beside the hits (rather than being derivable from them)
// because a source can be filtered down to nothing and must still report why,
// and capped travels beside them because it is a fact about the fetch the
// trimmed list no longer records.
type sourceResult struct {
	source   string
	hits     []Hit
	withheld int
	capped   bool
}

// sourceState holds one source's normalized candidate list, how many of those
// candidates the allocator has taken into the display set so far, and the count
// the connection boundary removed before allocation.
type sourceState struct {
	source   string
	cands    []Hit // normalized to [0,1], sorted by descending score
	matched  int
	taken    int
	withheld int
	capped   bool
}

// allocate turns each provider's locally-scored candidate list into a balanced,
// budget-bounded, grouped-by-source display set plus a coverage summary. It
// replaces the old flat normalize-and-fuse sort, whose single relevance list
// let one strong source dominate the top (the fused form of the same topology
// tunnel #645 set out to break).
//
// Each source's scores are min-max normalized into [0,1] independently so a
// provider that emits larger raw numbers cannot dominate. The display set is
// then built in three passes over the normalized candidates:
//
//  1. floor: every source with any candidate gets floorPerSource slots, in
//     priority order, so breadth is always visible;
//  2. balanced fill: remaining budget is filled by relevance across sources,
//     each capped at a per-source ceiling so no source runs away;
//  3. redistribute: any budget the ceilinged fill could not place is filled by
//     relevance with the ceiling relaxed, so leftover slots flow to the
//     sources that actually have more relevant hits rather than going to waste.
//
// Coverage is reported for every source that returned at least one candidate,
// including sources squeezed out of the display set (Shown == 0), because that
// is exactly the breadth signal the agent would otherwise miss. A source whose
// every candidate was withheld by the connection boundary is reported too, with
// Matched 0 and a non-zero Withheld: that is the difference between "nothing
// matched" and "matches you may not see".
func allocate(results []sourceResult, budget int) ([]SourceGroup, []SourceCoverage) {
	states := buildStates(results)
	if len(states) == 0 {
		return nil, nil
	}
	if budget > 0 {
		fillFloors(states, budget)
		ceiling := allocCeiling(budget)
		remaining := budget - taken(states)
		remaining = fillByRelevance(states, remaining, ceiling)
		_ = fillByRelevance(states, remaining, math.MaxInt)
	}
	return groupsFrom(states), coverageFrom(states)
}

// buildStates normalizes each provider's candidate list, sorts it by descending
// score, and orders the sources deterministically: by their top candidate's
// score, then by source name. (After per-source min-max the top is 1.0 for every
// source, so this is effectively source-name order; keeping the score comparison
// makes the priority explicit if normalization ever changes.) A result with no
// candidates still becomes a state when the connection boundary withheld some,
// so coverage can report the withheld count; it sorts last (no top score) and
// contributes no display slots.
func buildStates(results []sourceResult) []*sourceState {
	states := make([]*sourceState, 0, len(results))
	for _, r := range results {
		if len(r.hits) == 0 && r.withheld == 0 {
			continue
		}
		norm := normalizeProvider(r.hits)
		sort.SliceStable(norm, func(i, j int) bool {
			if norm[i].Score != norm[j].Score {
				return norm[i].Score > norm[j].Score
			}
			return norm[i].Ref < norm[j].Ref
		})
		states = append(states, &sourceState{
			source: r.source, cands: norm, matched: len(norm),
			withheld: r.withheld, capped: r.capped,
		})
	}
	sort.SliceStable(states, func(i, j int) bool {
		if topScore(states[i]) != topScore(states[j]) {
			return topScore(states[i]) > topScore(states[j])
		}
		return states[i].source < states[j].source
	})
	return states
}

// topScore is a state's leading candidate score, or -1 when it has no candidates
// (a source the connection boundary emptied), which sorts it below every source
// that returned something.
func topScore(s *sourceState) float64 {
	if len(s.cands) == 0 {
		return -1
	}
	return s.cands[0].Score
}

// fillFloors gives each source floorPerSource display slots in priority order,
// stopping when the budget is exhausted. When the budget cannot floor every
// source, the highest-priority sources get their floor first.
func fillFloors(states []*sourceState, budget int) {
	remaining := budget
	for _, s := range states {
		for s.taken < floorPerSource && s.taken < len(s.cands) && remaining > 0 {
			s.taken++
			remaining--
		}
		if remaining == 0 {
			return
		}
	}
}

// fillByRelevance places up to budget more candidates, repeatedly taking the
// highest-scored not-yet-taken candidate among sources still below ceiling. It
// is a k-way merge over the per-source sorted lists; ties break by source name
// then ref so the result is reproducible. Returns the budget left unspent
// (sources exhausted or all at ceiling).
func fillByRelevance(states []*sourceState, budget, ceiling int) int {
	for budget > 0 {
		best := -1
		for i, s := range states {
			if s.taken >= ceiling || s.taken >= len(s.cands) {
				continue
			}
			if best == -1 || nextBeats(s, states[best]) {
				best = i
			}
		}
		if best == -1 {
			return budget
		}
		states[best].taken++
		budget--
	}
	return budget
}

// nextBeats reports whether a's next untaken candidate should be placed before
// b's: higher score wins, ties break by source name then ref.
func nextBeats(a, b *sourceState) bool {
	ah, bh := a.cands[a.taken], b.cands[b.taken]
	if ah.Score != bh.Score {
		return ah.Score > bh.Score
	}
	if a.source != b.source {
		return a.source < b.source
	}
	return ah.Ref < bh.Ref
}

// allocCeiling is the per-source display cap during the balanced fill: a
// fraction of the total budget, never below the floor.
func allocCeiling(budget int) int {
	c := int(math.Ceil(float64(budget) * ceilingFraction))
	if c < floorPerSource {
		return floorPerSource
	}
	return c
}

// taken sums the slots taken across all sources.
func taken(states []*sourceState) int {
	n := 0
	for _, s := range states {
		n += s.taken
	}
	return n
}

// groupsFrom builds the display groups from the taken counts, ordered so the
// source contributing the most to the result leads (shown desc, then source
// name). Sources with nothing shown are omitted from the groups (they still
// appear in coverage).
func groupsFrom(states []*sourceState) []SourceGroup {
	groups := make([]SourceGroup, 0, len(states))
	for _, s := range states {
		if s.taken == 0 {
			continue
		}
		hits := make([]Hit, s.taken)
		copy(hits, s.cands[:s.taken])
		groups = append(groups, SourceGroup{Source: s.source, Hits: hits})
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if len(groups[i].Hits) != len(groups[j].Hits) {
			return len(groups[i].Hits) > len(groups[j].Hits)
		}
		return groups[i].Source < groups[j].Source
	})
	return groups
}

// coverageFrom reports matched, shown, withheld, and capped counts for every
// source that returned candidates or had candidates withheld, ordered by matched
// desc then source name. Sources squeezed out of the display set (Shown == 0)
// are included on purpose: their match counts are the breadth signal that keeps
// the agent from tunneling, and their withheld counts are what turns an absent
// source into an explainable one.
func coverageFrom(states []*sourceState) []SourceCoverage {
	cov := make([]SourceCoverage, 0, len(states))
	for _, s := range states {
		cov = append(cov, SourceCoverage{
			Source: s.source, Matched: s.matched, Shown: s.taken,
			MatchedCapped: s.capped, Withheld: s.withheld,
		})
	}
	sort.SliceStable(cov, func(i, j int) bool {
		if cov[i].Matched != cov[j].Matched {
			return cov[i].Matched > cov[j].Matched
		}
		return cov[i].Source < cov[j].Source
	})
	return cov
}

// normalizeProvider min-max scales one provider's hit scores into [0,1],
// returning a copy with Score rewritten. An empty input yields nil; a set with
// no score spread is normalized to 1.0 (all equally relevant).
func normalizeProvider(hits []Hit) []Hit {
	if len(hits) == 0 {
		return nil
	}
	minScore, maxScore := hits[0].Score, hits[0].Score
	for _, h := range hits[1:] {
		if h.Score < minScore {
			minScore = h.Score
		}
		if h.Score > maxScore {
			maxScore = h.Score
		}
	}
	span := maxScore - minScore
	out := make([]Hit, len(hits))
	for i, h := range hits {
		if span == 0 {
			h.Score = maxNormalizedScore
		} else {
			h.Score = (h.Score - minScore) / span
		}
		out[i] = h
	}
	return out
}
