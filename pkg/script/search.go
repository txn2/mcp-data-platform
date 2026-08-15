package script

import "context"

// Search result limits. DefaultSearchLimit is the top-K returned when the
// caller names no limit; maxSearchLimit bounds an explicit request so one
// ranked query cannot ask for an unbounded result set. They match the prompt
// library's, so a federated search cannot be skewed by one source quietly
// returning more candidates than another.
const (
	DefaultSearchLimit = 20
	maxSearchLimit     = 100
)

// SearchQuery describes a relevance ranking request over the script library.
//
// Visibility is applied before ranking, as a predicate rather than a filter over
// the answer: a script the caller cannot see must cost neither a row nor a
// decision. The rule is Script.VisibleTo expressed in SQL — global scripts,
// persona-scoped scripts of a persona the caller belongs to, and the caller's
// own personal scripts — with one deliberate difference from the manage_script
// listing, which scopes on the single persona a request resolved to. Discovery
// scopes on the whole membership set for the same reason the managed-resources
// provider does: membership is an entitlement the caller holds, while the acting
// persona is a property of one request. An empty set therefore means "belongs to
// no persona", which is the fail-closed answer and also what a deployment that
// never wires a persona resolver gets.
type SearchQuery struct {
	// QueryText is the raw intent text the lexical ranking matches.
	QueryText string
	// OwnerEmail is the caller identity, for personal-scope visibility. Empty
	// leaves the caller seeing no personal scripts at all, including their own.
	OwnerEmail string
	// Personas is every persona the caller belongs to, for persona-scope
	// visibility.
	Personas []string
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
	// record, the approved version's parameter contract and approval stamp, its
	// cadence when it has one, and its last successful run. It applies no
	// visibility rule of its own — the caller has already established that this
	// script may be seen.
	Contract(ctx context.Context, id string) (*Contract, error)
}
