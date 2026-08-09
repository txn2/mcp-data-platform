package graphprobe

import (
	"regexp"
	"strings"

	"github.com/txn2/mcp-data-platform/bench/internal/graphfix"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
)

// Tool names as they appear in a transcript, once the client's
// mcp__<server>__ namespace prefix is stripped.
const (
	toolSearch = "search"
	toolFetch  = "fetch"
)

// Provenance records where an episode could first have learned a reference. It
// is the probe's primary distinction: a reference the agent could only have got
// from a page it read is navigation, while one search handed it is not.
const (
	// ProvenanceSearch: the reference first appeared in a search result.
	ProvenanceSearch = "search"
	// ProvenancePage: the reference first appeared in a fetched document, either
	// in its structured references array or in its body.
	ProvenancePage = "page"
	// ProvenanceUnseen: the reference appeared in no earlier tool result, so the
	// agent constructed it. Recorded rather than discarded because a constructed
	// reference is a real behavior, not a parse failure.
	ProvenanceUnseen = "unseen"
)

// trailingPunct is the sentence punctuation trimmed from the tail of a
// reference token, matching knowledgepage.trailingPunct so this reading and the
// platform's own scanner agree on where a reference ends.
const trailingPunct = ".,;:!?"

// namespacePrefix strips a client's MCP tool namespace (mcp__<server>__tool).
var namespacePrefix = regexp.MustCompile(`^.*__`)

// pageRefToken matches a knowledge-page reference anywhere in a tool result.
// Scanning the raw text rather than the structured shape covers both places a
// reference reaches the agent: the fetch document's references array and a
// reference written inline in a page body.
var pageRefToken = regexp.MustCompile(`mcp:knowledge_page:[A-Za-z0-9_.\-]+`)

// Fetch is one dereference an episode performed, in call order.
type Fetch struct {
	// Reference is the reference argument as the agent passed it.
	Reference string `json:"reference"`
	// PageKey is the fixture page the reference resolved to, or "" when the
	// reference is not a planted page.
	PageKey string `json:"page_key,omitempty"`
	// Depth is the page's position on this cell's chain, or -1 off the chain.
	Depth int `json:"depth"`
	// Provenance is where the reference could first have been learned.
	Provenance string `json:"provenance"`
	// Failed reports a fetch whose tool result was an error.
	Failed bool `json:"failed,omitempty"`
}

// Reading is what one episode did with the corpus.
type Reading struct {
	Searches []string `json:"searches"`
	Fetches  []Fetch  `json:"fetches"`
	// MaxDepthRead is the deepest chain position the episode actually read,
	// however it got there. -1 when it read no page on the chain.
	MaxDepthRead int `json:"max_depth_read"`
	// MaxTraversalDepth is the deepest chain position the episode read through a
	// reference it could only have learned from a page it had already read. This
	// is the measure the probe's kill conditions are written against: depth 1
	// here is a fetch of a search result, so only depths above 1 are volunteered
	// navigation.
	MaxTraversalDepth int `json:"max_traversal_depth"`
	// OffPathFetches counts dereferences of planted pages that are not on this
	// cell's chain: the cost of the sibling references being real choices.
	OffPathFetches int `json:"off_path_fetches"`
	// ReadAnswerPage reports whether the page holding the ground truth was read.
	ReadAnswerPage bool `json:"read_answer_page"`
	// FetchedAnyReference reports whether the episode dereferenced anything at
	// all. The floor kill condition is written on this.
	FetchedAnyReference bool `json:"fetched_any_reference"`
}

// Outcome is one lookup-era episode's graded answer, retained so archived
// runs decode; completion runs grade coverage instead and never write it.
type Outcome struct {
	FinalAnswer string   `json:"final_answer"`
	Value       *float64 `json:"value,omitempty"`
	Unavailable bool     `json:"unavailable"`
	Correct     bool     `json:"correct"`
}

// Read reconstructs what an episode read, in call order, and classifies every
// dereference by where its reference could first have been learned.
//
// The classification walks the transcript once: a tool call is classified
// against the references known before it, and its result is folded in
// afterwards, so a reference the same call returned can never be credited as
// the reason that call happened.
func Read(transcript []llm.Message, cell graphfix.Cell, planted Planted) Reading {
	r := Reading{MaxDepthRead: -1, MaxTraversalDepth: -1}
	firstSeen := map[string]string{}
	results := resultIndex(transcript)
	for _, msg := range transcript {
		for _, call := range msg.ToolCalls {
			switch toolName(call.Name) {
			case toolSearch:
				r.Searches = append(r.Searches, argString(call.Args, "intent"))
			case toolFetch:
				r.observeFetch(call, cell, planted, firstSeen, results)
			}
		}
		for _, res := range msg.ToolResults {
			learnRefs(firstSeen, res.Text, results.class[res.CallID])
		}
	}
	return r
}

// observeFetch records one dereference against what the episode knew before it.
func (r *Reading) observeFetch(call llm.ToolCall, cell graphfix.Cell, planted Planted,
	firstSeen map[string]string, results resultLookup,
) {
	ref := argString(call.Args, "reference")
	f := Fetch{Reference: ref, Depth: -1, Provenance: ProvenanceUnseen}
	// Look the reference up in its trimmed form, the form the platform's own
	// scanner records, so a reference the agent copied out of prose with its
	// sentence punctuation attached still matches the page that supplied it.
	ref = trimRef(ref)
	if seen, ok := firstSeen[ref]; ok {
		f.Provenance = seen
	}
	f.Failed = results.failed[call.ID]
	if key, ok := planted.KeyForReference(ref); ok {
		f.PageKey = key
		f.Depth = cell.DepthOf(key)
	}
	r.Fetches = append(r.Fetches, f)
	if !f.Failed {
		r.credit(f, cell)
	}
}

// credit folds one successful dereference into the episode's reading: an
// off-path page is a cost, an on-chain page moves the depth read, and only a
// reference the episode could not have had from search moves the traversal
// depth.
func (r *Reading) credit(f Fetch, cell graphfix.Cell) {
	r.FetchedAnyReference = true
	if f.Depth < 0 {
		if f.PageKey != "" {
			r.OffPathFetches++
		}
		return
	}
	if f.Depth > r.MaxDepthRead {
		r.MaxDepthRead = f.Depth
	}
	if f.Provenance == ProvenancePage && f.Depth > r.MaxTraversalDepth {
		r.MaxTraversalDepth = f.Depth
	}
	if f.PageKey == cell.AnswerKey() {
		r.ReadAnswerPage = true
	}
}

// resultLookup indexes tool results by the call that produced them.
type resultLookup struct {
	// class maps a call id to the tool class of the call ("search", "fetch", or
	// "" for anything else), so a result's references are attributed to the kind
	// of tool that returned them.
	class map[string]string
	// failed marks the calls whose result was an error. A fetch that errored read
	// nothing, so it must not count as a page the episode reached.
	failed map[string]bool
}

// resultIndex builds the call-id index in one pass over the transcript.
func resultIndex(transcript []llm.Message) resultLookup {
	out := resultLookup{class: map[string]string{}, failed: map[string]bool{}}
	for _, msg := range transcript {
		for _, call := range msg.ToolCalls {
			out.class[call.ID] = toolName(call.Name)
		}
		for _, res := range msg.ToolResults {
			out.failed[res.CallID] = res.IsError
		}
	}
	return out
}

// learnRefs folds one tool result's references into the first-seen index. A
// reference already known keeps its original provenance: what matters is where
// the episode could FIRST have learned it.
func learnRefs(firstSeen map[string]string, text, toolClass string) {
	var provenance string
	switch toolClass {
	case toolSearch:
		provenance = ProvenanceSearch
	case toolFetch:
		provenance = ProvenancePage
	default:
		return
	}
	for _, ref := range pageRefToken.FindAllString(text, -1) {
		// A reference written in prose immediately before sentence punctuation
		// absorbs it into the token, exactly as the platform's own scanner sees
		// it (knowledgepage.trailingPunct). Without the trim, a reference learned
		// from a page body would not match the reference the agent then passed to
		// fetch, and a real traversal would be recorded as an invented reference.
		ref = strings.TrimRight(ref, trailingPunct)
		if _, known := firstSeen[ref]; !known {
			firstSeen[ref] = provenance
		}
	}
}

// toolName strips the client's MCP namespace prefix from a transcript tool name.
func toolName(name string) string { return namespacePrefix.ReplaceAllString(name, "") }

// trimRef trims sentence punctuation from a reference token, matching the
// platform's own scanner so both readings agree on where a reference ends.
func trimRef(ref string) string { return strings.TrimRight(ref, trailingPunct) }

// argString reads a string argument, tolerating a missing or non-string value.
func argString(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}
