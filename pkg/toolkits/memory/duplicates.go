package memory

import (
	"encoding/json"
	"time"

	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
)

// review_duplicates response bounds (#783). The command previously returned two
// complete records per pair (each up to the 4,000-char content cap plus all
// metadata), so its default listing could produce a tens-of-kilobytes result
// that overran an MCP client's output budget and forced a spill-to-file before
// the agent could read it. The listing is now summary-first and byte-bounded:
// each pair returns ids, score, status, timestamps, owner, and a bounded content
// preview, and a response never exceeds duplicatePairBudgetBytes. The agent
// fetches mcp:memory:<id> or uses memory_manage list to read a full record before
// consolidating.
//
// Deliberately NOT offset-paginated. The candidate set is small (bounded by the
// store's own MaxLimit), score-ordered, and shrinks from the top as the agent
// consolidates the pairs it is steered toward (a consolidated duplicate goes
// inactive and drops out of the active-pair scan). Positional offset paging over
// that mutating set would silently skip pairs that shifted behind the offset
// after a consolidation. The correct pagination is the existing loop —
// consolidate the surfaced pairs and re-run — which always re-presents the
// current highest-similarity pairs, so no pair is ever skipped. more_pairs on the
// response signals when the byte budget (or the requested limit) hid lower-scored
// pairs, telling the agent to consolidate and re-run.
const (
	// previewMaxLen bounds each side's content preview, in runes. The
	// consolidation decision needs enough text to recognize the duplicate, not
	// the whole record; the full content stays one fetch away.
	previewMaxLen = 200

	// duplicatePairBudgetBytes bounds the cumulative serialized size of the
	// summary pairs in one review_duplicates response, so the response never
	// overruns the MCP output token limit regardless of content length. Chosen
	// with wide margin under a ~25k-token (~100k-byte at four bytes/token) output
	// cap. Each summary pair carries two previewMaxLen-bounded previews plus small
	// scalar fields (~800 bytes), so this budget admits dozens of pairs.
	duplicatePairBudgetBytes = 40000
)

// recordPreview is the bounded, per-side view of a memory record in a
// review_duplicates summary pair: enough to identify the record and judge the
// duplicate (id, owner, status, timestamps, a content preview) without the full
// content or the metadata/embedding payload.
type recordPreview struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	CreatedBy string    `json:"created_by"`
	Status    string    `json:"status"`
	Preview   string    `json:"content_preview"`
}

// pairSummary is the summary-first shape of one high-similarity pair returned by
// review_duplicates: the two sides as bounded previews plus the similarity score.
type pairSummary struct {
	Older recordPreview `json:"older"`
	Newer recordPreview `json:"newer"`
	Score float64       `json:"score"`
}

// toRecordPreview projects a full memory record onto its bounded preview.
func toRecordPreview(r memstore.Record) recordPreview {
	return recordPreview{
		ID:        r.ID,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
		CreatedBy: r.CreatedBy,
		Status:    r.Status,
		Preview:   truncateRunes(r.Content, previewMaxLen),
	}
}

// toPairSummary projects a full similar-pair onto its summary shape.
func toPairSummary(p memstore.SimilarPair) pairSummary {
	return pairSummary{
		Older: toRecordPreview(p.Older),
		Newer: toRecordPreview(p.Newer),
		Score: p.Score,
	}
}

// truncateRunes trims s to at most n runes, appending an ellipsis marker when
// truncated. It slices at the n-th rune's byte offset without a full []rune
// allocation, so the result is always valid UTF-8.
func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i] + "..."
		}
		count++
	}
	return s
}

// maxDuplicatePageSize is the largest review_duplicates page. It is deliberately
// one below the store's MaxLimit — not equal to it — so the handler can over-fetch
// by one (want+1 <= MaxLimit) to detect whether more pairs exist beyond the page.
// If the page could reach MaxLimit, the store would clamp the +1 probe away and a
// >MaxLimit backlog would be reported as complete (#783 review). The byte budget
// usually bounds a page well below this ceiling anyway.
const maxDuplicatePageSize = memstore.MaxLimit - 1

// effectiveDuplicateLimit clamps the requested review_duplicates page size to
// [1, maxDuplicatePageSize], defaulting a non-positive request to DefaultLimit.
// Its ceiling is intentionally maxDuplicatePageSize rather than the store's
// MaxLimit (see that constant), so this is a distinct rule from the store's
// clampStoreLimit, not a copy of it.
func effectiveDuplicateLimit(limit int) int {
	switch {
	case limit <= 0:
		return memstore.DefaultLimit
	case limit > maxDuplicatePageSize:
		return maxDuplicatePageSize
	}
	return limit
}

// budgetSummaries projects the score-ordered candidate pairs onto summary shapes,
// stopping once including the next pair would push the cumulative serialized size
// past byteBudget, so the response stays under the output cap regardless of
// individual content lengths (#783). The first pair is always included so a
// single large pair cannot yield an empty response; this is safe because each
// summary drops the uncapped fields (full content, metadata, embedding) and
// bounds each preview to previewMaxLen, so no single pair approaches the cap.
// truncated reports whether the budget dropped any lower-scored pairs — the
// signal to consolidate the shown pairs and re-run for the rest. pairs must be
// ordered highest-similarity first (as SimilarActivePairs returns them), so the
// dropped tail is always the least similar.
func budgetSummaries(pairs []memstore.SimilarPair, byteBudget int) (summaries []pairSummary, truncated bool) {
	summaries = make([]pairSummary, 0, len(pairs))
	size := 0
	for i, p := range pairs {
		s := toPairSummary(p)
		itemSize := summarySize(s)
		if i > 0 && size+itemSize > byteBudget {
			truncated = true
			break
		}
		summaries = append(summaries, s)
		size += itemSize
	}
	return summaries, truncated
}

// summarySize reports the serialized size a summary pair contributes to the
// response, measured at the same two-level nesting jsonResult emits it at (inside
// result["pairs"][]), so the byte budget reflects the bytes actually sent rather
// than a shallower estimate. Wrapping in {"pairs":[s]} reproduces that
// indentation depth and adds a small fixed framing overhead, making the estimate
// conservative (the budget bites slightly early, never late). json.MarshalIndent
// of the wrapper cannot fail (no unmarshalable fields), so the error is ignored.
func summarySize(s pairSummary) int {
	b, _ := json.MarshalIndent(map[string]any{"pairs": []pairSummary{s}}, "", "  ")
	return len(b)
}
