package graphprobe

import (
	"slices"

	"github.com/txn2/mcp-data-platform/bench/internal/graphfix"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
)

// SearchCall is one search an episode issued. The limit is recorded because
// it is the variable the lookup-shaped attempt's gate missed: an agent that
// raises it sees half this corpus in one call, and any claim about what
// search could or could not have delivered has to be read against the limits
// the episodes actually asked for.
type SearchCall struct {
	Intent string `json:"intent"`
	// Limit is the row count the agent asked for, 0 when it left the default.
	Limit int `json:"limit,omitempty"`
}

// CompletionReading is what one completion episode did with the corpus.
type CompletionReading struct {
	Searches []SearchCall `json:"searches"`
	Fetches  []Fetch      `json:"fetches"`
	// PagesRead are the fixture pages successfully fetched, in first-read
	// order. Grounding is membership here.
	PagesRead []string `json:"pages_read"`
	// ConstraintPagesRead counts the distinct constraint-set pages read.
	ConstraintPagesRead int `json:"constraint_pages_read"`
	// OffSetFetches counts reads of planted pages outside the constraint set:
	// the browsing cost of the corpus's ordinary neighbors.
	OffSetFetches int `json:"off_set_fetches"`
	// ReadEntry reports whether the cell's entry page was read.
	ReadEntry bool `json:"read_entry"`
	// MaxDepthRead is the greatest reference distance from the entry of any
	// page the episode read, however it got there; -1 when it read nothing on
	// the entry's graph.
	MaxDepthRead int `json:"max_depth_read"`
	// MaxTraversalDepth is the greatest such distance reached through a
	// reference the episode could only have learned from a page it had
	// already read. This is the traversal measure proper: a fetch of a
	// search-returned reference is retrieval, not navigation.
	MaxTraversalDepth int `json:"max_traversal_depth"`
}

// ConstraintResult is one constraint graded against one episode.
type ConstraintResult struct {
	ID string `json:"id"`
	// Entry marks a within-episode control constraint (stated on the entry
	// page); the kill conditions never read these.
	Entry bool `json:"entry,omitempty"`
	// Covered reports the signature appeared in the final document.
	Covered bool `json:"covered"`
	// Grounded reports Covered and at least one source page read. Covered
	// without Grounded is confabulation or prior knowledge (the gate proves
	// search hit text carries no signature) and is never counted as coverage.
	Grounded bool `json:"grounded"`
	// Pattern is the signature that matched, for the audit trail.
	Pattern string `json:"pattern,omitempty"`
}

// Coverage is one episode's graded document.
type Coverage struct {
	Constraints []ConstraintResult `json:"constraints"`
	// OffEntry* are the numbers every kill condition reads.
	OffEntryTotal    int `json:"off_entry_total"`
	OffEntryCovered  int `json:"off_entry_covered"`
	OffEntryGrounded int `json:"off_entry_grounded"`
	// Entry* are the within-episode control.
	EntryTotal   int `json:"entry_total"`
	EntryCovered int `json:"entry_covered"`
	// UnreadCovered counts off-entry constraints covered without any source
	// page read.
	UnreadCovered int `json:"unread_covered"`
}

// ReadCompletion reconstructs what one completion episode read, in call
// order, classifying every dereference by where its reference could first
// have been learned (the same one-pass provenance walk as the lookup
// classifier: a call is classified against what was known before it). The
// corpus supplies the reference graph the cell's depths are read over, so the
// probe fixture and a generated corpus grade under one classifier.
func ReadCompletion(transcript []llm.Message, corpus graphfix.Corpus, cell graphfix.CompletionCell, planted Planted) CompletionReading {
	r := CompletionReading{MaxDepthRead: -1, MaxTraversalDepth: -1}
	depths := corpus.Depths(cell)
	setPages := cell.AllConstraintPages()
	firstSeen := map[string]string{}
	results := resultIndex(transcript)
	for _, msg := range transcript {
		for _, call := range msg.ToolCalls {
			switch toolName(call.Name) {
			case toolSearch:
				r.Searches = append(r.Searches, SearchCall{
					Intent: argString(call.Args, "intent"),
					Limit:  argInt(call.Args, "limit"),
				})
			case toolFetch:
				r.observeFetch(call, cell, planted, depths, setPages, firstSeen, results)
			}
		}
		for _, res := range msg.ToolResults {
			learnRefs(firstSeen, res.Text, results.class[res.CallID])
		}
	}
	return r
}

// observeFetch records one dereference against what the episode knew before it.
func (r *CompletionReading) observeFetch(call llm.ToolCall, cell graphfix.CompletionCell, planted Planted,
	depths map[string]int, setPages []string, firstSeen map[string]string, results resultLookup,
) {
	ref := argString(call.Args, "reference")
	f := Fetch{Reference: ref, Depth: -1, Provenance: ProvenanceUnseen}
	ref = trimRef(ref)
	if seen, ok := firstSeen[ref]; ok {
		f.Provenance = seen
	}
	f.Failed = results.failed[call.ID]
	if key, ok := planted.KeyForReference(ref); ok {
		f.PageKey = key
		if d, reachable := depths[key]; reachable {
			f.Depth = d
		}
	}
	r.Fetches = append(r.Fetches, f)
	if f.Failed || f.PageKey == "" {
		return
	}
	r.credit(f, cell, setPages)
}

// credit folds one successful dereference of a planted page into the reading.
func (r *CompletionReading) credit(f Fetch, cell graphfix.CompletionCell, setPages []string) {
	if !slices.Contains(r.PagesRead, f.PageKey) {
		r.PagesRead = append(r.PagesRead, f.PageKey)
		if slices.Contains(setPages, f.PageKey) {
			r.ConstraintPagesRead++
		} else {
			r.OffSetFetches++
		}
	}
	if f.PageKey == cell.EntryKey {
		r.ReadEntry = true
	}
	if f.Depth > r.MaxDepthRead {
		r.MaxDepthRead = f.Depth
	}
	if f.Provenance == ProvenancePage && f.Depth > r.MaxTraversalDepth {
		r.MaxTraversalDepth = f.Depth
	}
}

// GradeCoverage scores one episode's final document against the cell's
// constraint set, grounding each covered constraint in the pages the episode
// actually read.
func GradeCoverage(finalText string, cell graphfix.CompletionCell, reading CompletionReading) Coverage {
	var out Coverage
	for _, k := range cell.Constraints {
		res := ConstraintResult{ID: k.ID, Entry: cell.Entry(k)}
		res.Covered, res.Pattern = k.Covered(finalText)
		if res.Covered {
			res.Grounded = slices.ContainsFunc(k.Pages, func(key string) bool {
				return slices.Contains(reading.PagesRead, key)
			})
		}
		out.Constraints = append(out.Constraints, res)
		out.tally(res)
	}
	return out
}

// tally folds one constraint result into the episode totals.
func (c *Coverage) tally(res ConstraintResult) {
	if res.Entry {
		c.EntryTotal++
		if res.Covered {
			c.EntryCovered++
		}
		return
	}
	c.OffEntryTotal++
	if res.Covered {
		c.OffEntryCovered++
	}
	if res.Grounded {
		c.OffEntryGrounded++
	}
	if res.Covered && !res.Grounded {
		c.UnreadCovered++
	}
}

// argInt reads an integer argument, tolerating the float64 JSON decoding and
// a missing or non-numeric value.
func argInt(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}
