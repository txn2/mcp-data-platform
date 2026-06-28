package knowledge

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// SourcePrompts is the provenance label for prompt hits.
const SourcePrompts = "prompts"

// PromptSearcher is what the prompts provider needs from the prompt store:
// relevance search over visible prompts (the text path) and a by-id read (fetch).
// The concrete postgres prompt store satisfies it; declared here so the provider
// depends on the capability and the platform asserts one authority for "a
// searchable, fetchable prompt store".
type PromptSearcher interface {
	Search(ctx context.Context, q prompt.SearchQuery) ([]prompt.ScoredPrompt, error)
	GetByID(ctx context.Context, id string) (*prompt.Prompt, error)
}

// PromptsProvider exposes operational prompts to the router. Prompt visibility
// is mixed: global prompts are visible to everyone, while persona- and
// personal-scoped prompts are visible only to the matching caller. The
// underlying searcher enforces that visibility from the caller identity, so the
// provider is shared (always queried, returning at least the global prompts even
// for an anonymous caller) yet never leaks another caller's personal prompts.
type PromptsProvider struct {
	searcher PromptSearcher
}

// NewPromptsProvider builds the prompts provider over a prompt searcher.
func NewPromptsProvider(searcher PromptSearcher) *PromptsProvider {
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
			Text:      promptHitText(scored[i].Prompt),
			Source:    SourcePrompts,
			Ref:       scored[i].Prompt.Name,
			Score:     scored[i].Score,
			Reference: knowledgepage.PromptRef(scored[i].Prompt.ID),
		})
	}
	return hits, nil
}

// Fetch dereferences an mcp:prompt:<id> reference to the prompt's full content
// (#694), folding what manage_prompt's get returns into the one fetch verb. It owns
// only the prompt reference form; any other reference is declined (owned=false).
//
// GetByID reads a prompt regardless of visibility, so this re-applies the same
// scope filter the search path enforces: a global prompt is visible to everyone, a
// persona prompt only to a caller carrying that persona, and a personal prompt only
// to its owner. A prompt the caller could not have searched (wrong persona, another
// owner's personal prompt) returns ErrNotFound, so fetch never widens persona
// scope; a missing id is likewise ErrNotFound.
func (p *PromptsProvider) Fetch(ctx context.Context, ref string, caller Caller) (*Document, bool, error) {
	parsed, err := knowledgepage.ParseEntityRef(ref)
	if err != nil || parsed.TargetType != knowledgepage.RefTargetPrompt {
		// Not a prompt reference: decline so the Router tries the next provider.
		return nil, false, nil //nolint:nilerr // a non-prompt reference is a decline, not a failure
	}
	pr, err := p.searcher.GetByID(ctx, parsed.PromptID)
	if err != nil {
		return nil, true, fmt.Errorf("getting prompt %s: %w", parsed.PromptID, err)
	}
	if pr == nil || !promptVisibleTo(pr, caller) {
		return nil, true, ErrNotFound
	}
	return &Document{
		Reference: ref,
		Source:    SourcePrompts,
		Title:     promptTitle(pr),
		Body:      pr.Content,
		Content:   pr,
	}, true, nil
}

// promptVisibleTo reports whether caller may read pr, mirroring the search path's
// full visibility rule so fetch never reveals a prompt a search would have hidden.
// The search store gates on lifecycle first (`status = 'approved' AND enabled =
// true`, pkg/prompt/postgres/search.go) and then on scope, so this applies both: a
// disabled or unapproved prompt is invisible regardless of scope (it would not
// appear in search even to its owner), and among live prompts a global one is
// visible to all, a persona one only to a caller carrying that persona, and a
// personal one only to its owner. An unknown scope fails closed.
func promptVisibleTo(pr *prompt.Prompt, caller Caller) bool {
	// Lifecycle gate first: search never surfaces a non-approved or disabled prompt,
	// so fetch must not either (an admin retiring a prompt removes it from both).
	if pr.Status != prompt.StatusApproved || !pr.Enabled {
		return false
	}
	switch pr.Scope {
	case prompt.ScopeGlobal:
		return true
	case prompt.ScopePersona:
		return caller.Persona != "" && slices.Contains(pr.Personas, caller.Persona)
	case prompt.ScopePersonal:
		return caller.Email != "" && pr.OwnerEmail == caller.Email
	default:
		return false
	}
}

// promptTitle renders a prompt's human label: its display name, falling back to its
// unique name.
func promptTitle(pr *prompt.Prompt) string {
	if pr.DisplayName != "" {
		return pr.DisplayName
	}
	return pr.Name
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
