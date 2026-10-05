package knowledge

import (
	"context"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// SourceScripts is the provenance label for managed-script hits.
const SourceScripts = "scripts"

// ScriptSearcher is what the scripts provider needs from the managed-script
// store: relevance search over the scripts in service (the text path) and the
// contract document for one script by id (fetch). The concrete PostgreSQL
// script store satisfies it.
type ScriptSearcher interface {
	Search(ctx context.Context, q script.SearchQuery) ([]script.ScoredScript, error)
	Contract(ctx context.Context, id string) (*script.Contract, error)
}

// ScriptOutput is one file a script produced that the reader can open.
type ScriptOutput struct {
	// Kind is asset, collection or resource.
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Reference is the citation fetch dereferences to the file.
	Reference string `json:"reference"`
}

// ScriptOutputSet is what a script produced as one caller may see it, among
// the files it wrote most recently.
type ScriptOutputSet struct {
	// Open are the files the caller can open.
	Open []ScriptOutput
	// Hidden counts the others, which are not named.
	Hidden int
	// More is true when the script wrote more files than were read.
	More bool
}

// ScriptOutputs lists what a script produced as one caller may see it (#2027).
type ScriptOutputs interface {
	Outputs(ctx context.Context, scriptID string, caller Caller) (ScriptOutputSet, error)
}

// scriptDocument is the structured content of a fetched script: its contract
// and what it produced, as the caller may see them.
type scriptDocument struct {
	script.Contract
	Outputs []ScriptOutput `json:"outputs"`
	// OutputsHidden counts the files it produced that the caller cannot open.
	OutputsHidden int `json:"outputs_hidden"`
	// OutputsMore is true when the script wrote more files than were read;
	// the list and the count cover its most recent ones.
	OutputsMore bool `json:"outputs_more,omitempty"`
	// OutputsUnavailable is true when what it produced could not be read.
	OutputsUnavailable bool `json:"outputs_unavailable,omitempty"`
}

// ScriptsProvider exposes managed scripts to the router (#1302). A script is
// the most reusable artifact the platform holds: a solved process, saved, and
// often running on a cadence, and the record of how a resource, an asset or a
// table is produced.
//
// A script's definition is readable by everyone signed in (#1866, #2027), so
// every identified caller finds every script in service and fetches any
// script's contract and source. What its runs produced is filtered per reader,
// and its last run and saved state are its owner's and an administrator's
// (script.Contract.ForReader).
//
// Discovery grants nothing. Running a script is still run_script, which only
// its owner, an administrator or a grantee may call.
type ScriptsProvider struct {
	searcher ScriptSearcher
	outputs  ScriptOutputs
}

// NewScriptsProvider builds the scripts provider over a script searcher.
func NewScriptsProvider(searcher ScriptSearcher) *ScriptsProvider {
	return &ScriptsProvider{searcher: searcher}
}

// SetOutputs wires what a fetched script lists as its outputs. Without it a
// fetched script lists none.
func (p *ScriptsProvider) SetOutputs(o ScriptOutputs) { p.outputs = o }

// Name returns the provenance label.
func (*ScriptsProvider) Name() string { return SourceScripts }

// Scope marks this provider per-user: a script's definition is readable by
// everyone signed in and by no one else, so a caller with no identity has
// nothing here and the Router skips it.
func (*ScriptsProvider) Scope() Scope { return ScopePerUser }

// Search returns scripts in service, ranked by relevance to the intent, over
// their card and their source (#2027). It responds to the text path only; a
// query with no intent yields nothing.
//
// The router's query vector is passed through, so ranking is hybrid wherever
// the scripts consumer has embedded the corpus and lexical wherever it has
// not. The snippet is the script's card (script.IndexText), the first chunk it
// is embedded as.
func (p *ScriptsProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	if q.Intent == "" {
		return nil, nil
	}

	scored, err := p.searcher.Search(ctx, script.SearchQuery{
		Embedding: q.Embedding,
		QueryText: q.Intent,
		Limit:     q.Limit,
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

// Fetch dereferences an mcp:script:<id> reference for any identified caller:
// the script's contract (what it is, what it takes, whether anything will
// execute it, when it next runs), the files it produced that the caller can
// open with a count of the rest, and its current source, since reading the
// code is what an agent that found the script needs next. Its last run and
// saved state are included for its owner and administrators only.
//
// It owns only the script reference form; any other reference is declined
// (owned=false). No lifecycle filter applies: a caller holding a reference to a
// retired script gets the document, whose refusal states plainly that it will
// not run.
func (p *ScriptsProvider) Fetch(ctx context.Context, ref string, caller Caller) (*Document, bool, error) {
	parsed, err := knowledgepage.ParseEntityRef(ref)
	if err != nil || parsed.TargetType != knowledgepage.RefTargetScript {
		// Not a script reference: decline so the Router tries the next provider.
		return nil, false, nil //nolint:nilerr // a non-script reference is a decline, not a failure
	}
	if caller.Email == "" && caller.OnBehalfOf == "" {
		return nil, true, ErrNotFound
	}
	c, err := p.searcher.Contract(ctx, parsed.ScriptID)
	if err != nil {
		return nil, true, fmt.Errorf("getting script %s: %w", parsed.ScriptID, err)
	}
	if c == nil {
		return nil, true, ErrNotFound
	}
	doc := scriptDocument{Contract: c.ForReader(caller.IsAdmin || c.OwnedBy(caller.Email)), Outputs: []ScriptOutput{}}
	if p.outputs != nil {
		set, err := p.outputs.Outputs(ctx, c.ID, caller)
		if err != nil {
			doc.OutputsUnavailable = true
		} else {
			doc.Outputs, doc.OutputsHidden, doc.OutputsMore = set.Open, set.Hidden, set.More
		}
	}
	refs := make([]DocumentRef, 0, len(doc.Outputs))
	for _, o := range doc.Outputs {
		refs = append(refs, DocumentRef{Reference: o.Reference, Type: o.Kind})
	}
	return &Document{
		Reference:  ref,
		Source:     SourceScripts,
		Title:      c.Title(),
		Body:       scriptBody(doc),
		Content:    doc,
		References: refs,
	}, true, nil
}

// scriptBody renders a fetched script as text: its contract, what it
// produced, and its source.
func scriptBody(doc scriptDocument) string {
	body := doc.Text() + "\n" + outputsLine(doc)
	if doc.Source == "" {
		return body
	}
	source := doc.Source
	if !strings.HasSuffix(source, "\n") {
		source += "\n"
	}
	return body + fmt.Sprintf("\n\nSource (version %d):\n```python\n%s```", doc.Version, source)
}

// outputsLine states what the script produced as the caller may see it.
func outputsLine(doc scriptDocument) string {
	if doc.OutputsUnavailable {
		return "Produced: what this script produced could not be read."
	}
	if len(doc.Outputs) == 0 && doc.OutputsHidden == 0 {
		return "Produced: nothing recorded."
	}
	names := make([]string, 0, len(doc.Outputs))
	for _, o := range doc.Outputs {
		label := o.Name
		if label == "" {
			label = o.ID
		}
		names = append(names, fmt.Sprintf("%s (%s)", label, o.Reference))
	}
	line := "Produced: " + strings.Join(names, ", ")
	if len(names) == 0 {
		line = "Produced:"
	}
	if doc.OutputsHidden > 0 {
		sep := ";"
		if len(names) == 0 {
			sep = ""
		}
		line += fmt.Sprintf("%s %d more you cannot open", sep, doc.OutputsHidden)
	}
	if doc.OutputsMore {
		line += "; these are its most recent outputs, and it wrote older ones too"
	}
	return line + "."
}
