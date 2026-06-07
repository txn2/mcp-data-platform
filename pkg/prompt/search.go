package prompt

import (
	"context"
	"strings"
)

// Search result limits. DefaultSearchLimit is the top-K returned when the
// caller does not specify one; maxSearchLimit bounds an explicit request so a
// single ranked query cannot ask for an unbounded result set.
const (
	DefaultSearchLimit = 20
	maxSearchLimit     = 100
)

// IndexText composes the text a prompt is embedded and lexically indexed on for
// semantic discovery: its title (display name, falling back to the name),
// description, body, and tags. The indexjobs prompts consumer and the
// request-path search MUST agree on this composition so a stored embedding
// lives in the same space as the query; it is defined once here for both.
// Empty fields are skipped so a sparse prompt does not pad the text with blank
// lines.
func IndexText(p *Prompt) string {
	title := p.DisplayName
	if title == "" {
		title = p.Name
	}
	parts := make([]string, 0, 4)
	if title != "" {
		parts = append(parts, title)
	}
	if p.Description != "" {
		parts = append(parts, p.Description)
	}
	if p.Content != "" {
		parts = append(parts, p.Content)
	}
	if len(p.Tags) > 0 {
		parts = append(parts, strings.Join(p.Tags, " "))
	}
	return strings.Join(parts, "\n")
}

// SearchQuery describes a relevance ranking request over the prompt library.
// Visibility is applied before ranking (you cannot rank a prompt you cannot
// read): a non-admin caller sees global prompts, persona prompts matching
// Persona, and their own personal prompts; an admin sees every approved prompt.
// Only approved, enabled prompts are ever ranked. A nil Embedding selects
// lexical-only ranking (the graceful-degradation path when no embedding
// provider is configured); a non-nil Embedding selects hybrid ranking.
type SearchQuery struct {
	Embedding  []float32 // query vector; nil selects lexical-only ranking
	QueryText  string    // raw query text for the lexical arm
	OwnerEmail string    // caller identity, for personal-scope visibility
	Persona    string    // caller persona, for persona-scope visibility
	IsAdmin    bool      // admin callers rank across all approved prompts
	Scope      string    // optional explicit scope filter ("" = all visible)
	Limit      int       // max results; see EffectiveLimit
}

// EffectiveLimit clamps the requested limit into [1, maxSearchLimit], defaulting
// an unset or out-of-range value to DefaultSearchLimit.
func (q SearchQuery) EffectiveLimit() int {
	if q.Limit <= 0 || q.Limit > maxSearchLimit {
		return DefaultSearchLimit
	}
	return q.Limit
}

// ScoredPrompt pairs a prompt with its relevance score in [0,1].
type ScoredPrompt struct {
	Prompt Prompt  `json:"prompt"`
	Score  float64 `json:"score"`
}

// Searcher ranks approved prompts by relevance to a query within the caller's
// visibility. It is a capability separate from Store: only a backing store that
// can rank (the PostgreSQL store with pgvector) implements it, so the feature
// degrades to absent rather than forcing every Store implementation to carry a
// ranking query.
type Searcher interface {
	Search(ctx context.Context, q SearchQuery) ([]ScoredPrompt, error)
}
