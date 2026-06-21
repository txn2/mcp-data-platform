package knowledge

import (
	"context"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// SourcePrompts is the provenance label for prompt hits.
const SourcePrompts = "prompts"

// promptSearcher is the relevance-search capability the prompts provider needs.
// It matches prompt.Searcher; declared locally so the provider depends on the
// capability and tests can supply a fake.
type promptSearcher interface {
	Search(ctx context.Context, q prompt.SearchQuery) ([]prompt.ScoredPrompt, error)
}

// PromptsProvider exposes operational prompts to the router. Prompt visibility
// is mixed: global prompts are visible to everyone, while persona- and
// personal-scoped prompts are visible only to the matching caller. The
// underlying searcher enforces that visibility from the caller identity, so the
// provider is shared (always queried, returning at least the global prompts even
// for an anonymous caller) yet never leaks another caller's personal prompts.
type PromptsProvider struct {
	searcher promptSearcher
}

// NewPromptsProvider builds the prompts provider over a prompt searcher.
func NewPromptsProvider(searcher promptSearcher) *PromptsProvider {
	return &PromptsProvider{searcher: searcher}
}

// Name returns the provenance label.
func (*PromptsProvider) Name() string { return SourcePrompts }

// Scope marks prompts shared (always queried); the searcher self-filters
// persona/personal prompts to the caller.
func (*PromptsProvider) Scope() Scope { return ScopeShared }

// Search returns prompts visible to the caller, ranked by relevance to the
// intent. It responds to the text path only; a query with no intent yields
// nothing.
func (p *PromptsProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	if q.Intent == "" {
		return nil, nil
	}

	scored, err := p.searcher.Search(ctx, prompt.SearchQuery{
		QueryText:  q.Intent,
		Embedding:  q.Embedding,
		OwnerEmail: q.Caller.Email,
		Persona:    q.Caller.Persona,
		Limit:      q.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("prompt search: %w", err)
	}

	hits := make([]Hit, 0, len(scored))
	for i := range scored {
		hits = append(hits, Hit{
			Text:   promptHitText(scored[i].Prompt),
			Source: SourcePrompts,
			Ref:    scored[i].Prompt.Name,
			Score:  scored[i].Score,
		})
	}
	return hits, nil
}

// promptHitText renders a prompt as a knowledge snippet: its display name and
// its description when present.
func promptHitText(p prompt.Prompt) string {
	name := p.DisplayName
	if name == "" {
		name = p.Name
	}
	if p.Description == "" {
		return name
	}
	return strings.TrimSpace(name + "\n" + p.Description)
}
