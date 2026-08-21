package script

import (
	"context"
	"strings"
)

// Search result limits. DefaultSearchLimit is the top-K returned when the
// caller names no limit; maxSearchLimit bounds an explicit request so one
// ranked query cannot ask for an unbounded result set. They match the prompt
// library's, so a federated search cannot be skewed by one source quietly
// returning more candidates than another.
const (
	DefaultSearchLimit = 20
	maxSearchLimit     = 100
)

// IndexText composes the text a script is embedded on and shown as in a search
// result: its title (display name, falling back to the name an agent calls it
// by), its description, the names of the parameters a run binds, the category
// it is filed under and its tags, and one line stating whether anything will
// execute it. Empty parts are skipped so a sparse script does not pad the text
// with blank lines.
//
// The execution note is part of the document rather than decoration: it changes
// what the script IS FOR. A script in service is something to run; a disabled
// or retired one is not, and reading a result should not leave that ambiguous.
//
// The source code is deliberately absent. docs/scripts/security.md admits the
// contract to anyone the scope rules admit and the source only to the owner and
// to administrators; one vector per row cannot be split along that line, so a
// vector built partly from source would let code a caller may not read decide
// how their results rank. The source also churns on every code edit while a
// description changes rarely, so indexing it would re-embed the corpus for
// changes that do not alter what the script is for.
//
// The indexjobs scripts consumer and the discovery source MUST agree on this
// composition — a stored embedding has to live in the same space as the text a
// caller is shown — so it is defined once here for both.
func IndexText(s *Script) string {
	parts := make([]string, 0, 5)
	parts = append(parts, Title(s))
	if s.Description != "" {
		parts = append(parts, s.Description)
	}
	if names := ParamSummary(s.Params); names != "" {
		parts = append(parts, "parameters: "+names)
	}
	if facets := strings.TrimSpace(s.Category + " " + strings.Join(s.Tags, " ")); facets != "" {
		parts = append(parts, facets)
	}
	parts = append(parts, ExecutionNote(s))
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// Title renders a script's human label: its display name, falling back to the
// name an agent would call it by.
func Title(s *Script) string {
	if s.DisplayName != "" {
		return s.DisplayName
	}
	return s.Name
}

// ExecutionNote states a script's execution state in one sentence.
func ExecutionNote(s *Script) string {
	if err := RefuseRun(s); err != nil {
		return "Nothing will execute this script: " + err.Error() + "."
	}
	return "Call run_script to execute it."
}

// SearchQuery describes a relevance ranking request over the script library.
//
// Visibility is applied before ranking, as a predicate rather than a filter over
// the answer: a script the caller cannot see must cost neither a row nor a
// decision. The rule is Script.OwnedBy expressed in SQL — the caller's own
// scripts and nothing else. An unidentified caller therefore matches nothing,
// which is the fail-closed answer.
type SearchQuery struct {
	// Embedding is the query vector. A nil Embedding selects lexical-only
	// ranking, which is exactly the behavior a deployment with no embedding
	// provider has always had; a non-nil one selects hybrid ranking over the
	// vectors the indexjobs scripts consumer writes.
	Embedding []float32
	// QueryText is the raw intent text the lexical ranking matches.
	QueryText string
	// OwnerEmail is the caller identity, which is the whole visibility
	// predicate. Empty matches no script at all.
	OwnerEmail string
	// Limit caps the candidates returned; see EffectiveLimit.
	Limit int
}

// EffectiveLimit clamps the requested limit into [1, maxSearchLimit],
// defaulting an unset or out-of-range value to DefaultSearchLimit.
func (q SearchQuery) EffectiveLimit() int {
	if q.Limit <= 0 || q.Limit > maxSearchLimit {
		return DefaultSearchLimit
	}
	return q.Limit
}

// ScoredScript pairs a script with its relevance score in [0,1].
type ScoredScript struct {
	Script Script  `json:"script"`
	Score  float64 `json:"score"`
}

// Searcher ranks scripts by relevance within the caller's visibility, and
// resolves one script's whole contract by id. The two halves are the two halves
// of discovery: search says a script exists and what it takes, and the contract
// read says everything a caller needs to decide whether to use it.
//
// It is a capability separate from Store, so only a backing store that can rank
// (the PostgreSQL one) implements it and the feature degrades to absent rather
// than forcing every Store to carry a ranking query.
type Searcher interface {
	Search(ctx context.Context, q SearchQuery) ([]ScoredScript, error)

	// Contract composes the contract document for one script: the script's own
	// record and parameter contract, its cadence when it has one, and its last
	// successful run. It applies no
	// visibility rule of its own — the caller has already established that this
	// script may be seen.
	Contract(ctx context.Context, id string) (*Contract, error)
}
