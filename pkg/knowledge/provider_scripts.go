package knowledge

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// SourceScripts is the provenance label for managed-script hits.
const SourceScripts = "scripts"

// ScriptSearcher is what the scripts provider needs from the managed-script
// store: relevance search over the caller's visible scripts (the text path) and
// the contract document for one script by id (fetch). The concrete PostgreSQL
// script store satisfies it; declared here so the provider depends on the
// capability and the platform asserts one authority for "a searchable,
// fetchable script store".
type ScriptSearcher interface {
	Search(ctx context.Context, q script.SearchQuery) ([]script.ScoredScript, error)
	Contract(ctx context.Context, id string) (*script.Contract, error)
}

// ScriptsProvider exposes managed scripts to the router (#1302). A script is
// the most reusable artifact the platform holds — a solved process, saved,
// and often running on a cadence — and until now it was the one first-class
// entity search could not find, which matters most under the search-first
// gate: search is the entry point an agent is required to use.
//
// Visibility is mixed the way prompts and resources are: global scripts are
// visible to everyone, persona-scoped scripts to a caller belonging to that
// persona, and personal scripts to their owner. The searcher enforces that in
// its store predicate from the caller identity, so the provider is shared
// (always queried, returning at least the global scripts) and never leaks
// another caller's personal script.
//
// Discovery grants nothing. A hit says a script exists and what it takes;
// running it is still run_script, executed as the script's own principal with
// the executing version's captured author roles.
type ScriptsProvider struct {
	searcher ScriptSearcher
}

// NewScriptsProvider builds the scripts provider over a script searcher.
func NewScriptsProvider(searcher ScriptSearcher) *ScriptsProvider {
	return &ScriptsProvider{searcher: searcher}
}

// Name returns the provenance label.
func (*ScriptsProvider) Name() string { return SourceScripts }

// Scope marks this provider per-user: a script is its owner's, so a caller with
// no identity has nothing here to find and the Router skips it entirely.
func (*ScriptsProvider) Scope() Scope { return ScopePerUser }

// Search returns the caller's own scripts, ranked by relevance to the intent. It responds to the text path only; a query with no intent yields
// nothing.
//
// The router's query vector is passed through, so ranking is hybrid wherever
// the scripts consumer has embedded the corpus and lexical wherever it has not
// — the same degradation every other source has. The snippet is
// script.IndexText, the exact text the vector was built from, so what a caller
// reads is what was ranked.
func (p *ScriptsProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	if q.Intent == "" {
		return nil, nil
	}

	scored, err := p.searcher.Search(ctx, script.SearchQuery{
		Embedding:  q.Embedding,
		QueryText:  q.Intent,
		OwnerEmail: q.Caller.Email,
		Limit:      q.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("script search: %w", err)
	}

	hits := make([]Hit, 0, len(scored))
	for i := range scored {
		sc := scored[i].Script
		hits = append(hits, Hit{
			Text:       script.IndexText(&sc),
			Source:     SourceScripts,
			Ref:        sc.ID,
			Score:      scored[i].Score,
			Status:     sc.Status,
			CapturedBy: sc.OwnerEmail,
			Reference:  knowledgepage.ScriptRef(sc.ID),
		})
	}
	return hits, nil
}

// Fetch dereferences an mcp:script:<id> reference to the script's contract: what
// it is, what it takes, whether anything will execute it, when it next runs, and
// what its last successful run produced. The source is deliberately not part of
// the document — reading the Starlark is what manage_script get is for, and what
// a reviewer does.
//
// It owns only the script reference form; any other reference is declined
// (owned=false). The contract read applies no visibility rule of its own, so
// this re-applies the same rule the search predicate enforces: a script the
// caller does not own and one that never existed are both a clean ErrNotFound,
// so fetch reveals neither the contract nor the existence of a script the caller
// could not have searched.
//
// Unlike search, no lifecycle filter applies. A deprecated or superseded script
// is not ranked (it names a dead end), but a caller holding a reference to one —
// from a prompt that attaches it, or an earlier search — gets the document,
// whose refusal states plainly that it will not run. A not-found there would
// read as though the script had never existed.
func (p *ScriptsProvider) Fetch(ctx context.Context, ref string, caller Caller) (*Document, bool, error) {
	parsed, err := knowledgepage.ParseEntityRef(ref)
	if err != nil || parsed.TargetType != knowledgepage.RefTargetScript {
		// Not a script reference: decline so the Router tries the next provider.
		return nil, false, nil //nolint:nilerr // a non-script reference is a decline, not a failure
	}
	c, err := p.searcher.Contract(ctx, parsed.ScriptID)
	if err != nil {
		return nil, true, fmt.Errorf("getting script %s: %w", parsed.ScriptID, err)
	}
	if c == nil || !c.OwnedBy(caller.Email) {
		return nil, true, ErrNotFound
	}
	return &Document{
		Reference: ref,
		Source:    SourceScripts,
		Title:     c.Title(),
		Body:      c.Text(),
		Content:   c,
	}, true, nil
}
