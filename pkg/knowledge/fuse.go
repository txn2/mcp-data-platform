package knowledge

import "sort"

// maxNormalizedScore is the score assigned to a provider's hits when they have
// no spread to min-max scale (a single hit, or all-equal scores): they are
// treated as equally, maximally relevant within that provider.
const maxNormalizedScore = 1.0

// normalizeAndFuse turns each provider's locally-scored hits into one ranked
// list on a common scale. Providers score relevance on their own scales (cosine
// fusion, ts_rank_cd, rank position), so a raw merge would let whichever
// provider happens to emit larger numbers dominate. Each provider's scores are
// min-max normalized into [0,1] independently, then all hits are merged and
// sorted by the normalized score.
//
// When a provider returns hits whose scores are all equal (including a single
// hit), min-max has no spread to work with; those hits are treated as equally,
// maximally relevant within that provider (normalized to 1.0) rather than
// dropped to 0.
//
// Ties are broken deterministically by source then ref so the order is stable
// across runs, which keeps the tool output and its tests reproducible.
func normalizeAndFuse(perProvider [][]Hit) []Hit {
	total := 0
	for _, hits := range perProvider {
		total += len(hits)
	}
	if total == 0 {
		return nil
	}
	fused := make([]Hit, 0, total)
	for _, hits := range perProvider {
		fused = append(fused, normalizeProvider(hits)...)
	}
	sort.SliceStable(fused, func(i, j int) bool {
		if fused[i].Score != fused[j].Score {
			return fused[i].Score > fused[j].Score
		}
		if fused[i].Source != fused[j].Source {
			return fused[i].Source < fused[j].Source
		}
		return fused[i].Ref < fused[j].Ref
	})
	return fused
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
