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
// datasets matched, 3 shown"). Matched is the count of candidates the provider
// returned for this query, capped at the per-source candidate fetch limit, not
// a full-corpus count.
type SourceCoverage struct {
	Source  string `json:"source"`
	Matched int    `json:"matched"`
	Shown   int    `json:"shown"`
}

// sourceState holds one source's normalized candidate list and how many of
// those candidates the allocator has taken into the display set so far.
type sourceState struct {
	source  string
	cands   []Hit // normalized to [0,1], sorted by descending score
	matched int
	taken   int
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
// is exactly the breadth signal the agent would otherwise miss.
func allocate(perProvider [][]Hit, budget int) ([]SourceGroup, []SourceCoverage) {
	states := buildStates(perProvider)
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

// buildStates normalizes each non-empty provider candidate list, sorts it by
// descending score, and orders the sources deterministically: by their top
// candidate's score, then by source name. (After per-source min-max the top is
// 1.0 for every source, so this is effectively source-name order; keeping the
// score comparison makes the priority explicit if normalization ever changes.)
func buildStates(perProvider [][]Hit) []*sourceState {
	states := make([]*sourceState, 0, len(perProvider))
	for _, hits := range perProvider {
		if len(hits) == 0 {
			continue
		}
		norm := normalizeProvider(hits)
		sort.SliceStable(norm, func(i, j int) bool {
			if norm[i].Score != norm[j].Score {
				return norm[i].Score > norm[j].Score
			}
			return norm[i].Ref < norm[j].Ref
		})
		states = append(states, &sourceState{source: norm[0].Source, cands: norm, matched: len(norm)})
	}
	sort.SliceStable(states, func(i, j int) bool {
		if states[i].cands[0].Score != states[j].cands[0].Score {
			return states[i].cands[0].Score > states[j].cands[0].Score
		}
		return states[i].source < states[j].source
	})
	return states
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

// coverageFrom reports matched and shown counts for every source that returned
// candidates, ordered by matched desc then source name. Sources squeezed out of
// the display set (Shown == 0) are included on purpose: their match counts are
// the breadth signal that keeps the agent from tunneling.
func coverageFrom(states []*sourceState) []SourceCoverage {
	cov := make([]SourceCoverage, 0, len(states))
	for _, s := range states {
		cov = append(cov, SourceCoverage{Source: s.source, Matched: s.matched, Shown: s.taken})
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
