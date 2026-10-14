package knowledge

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// SourceGovernance is the provenance label for governance-vocabulary hits.
const SourceGovernance = "governance"

const (
	// vocabularyPageLimit is how many definitions one search ranks. A glossary,
	// a tag list and a domain list together run to hundreds, not thousands; the
	// ranking happens here because a search intent is a question ("what does
	// net revenue mean") while a definition is a name.
	vocabularyPageLimit = 1000

	// carrierLimit bounds the datasets and columns listed under one definition.
	// A widely applied tag can be on thousands of them, and a fetch is a read
	// of the definition, not an export of its membership.
	carrierLimit = 25

	// childLimit bounds the terms and nodes listed under one glossary node.
	childLimit = 200
)

// DefinitionReader is what the governance provider reads from the platform
// catalog: definitions, the glossary tree, and what carries a definition. The
// catalog store implements it.
type DefinitionReader interface {
	GetDefinition(ctx context.Context, ref catalog.Ref) (*catalog.Definition, error)
	ListDefinitions(ctx context.Context, q catalog.DefinitionQuery) ([]catalog.DefinitionSummary, error)
	Uses(ctx context.Context, ref catalog.Ref, limit int) ([]catalog.Use, error)
}

// GovernanceProvider makes the catalog's glossary terms, glossary nodes, tags,
// domains and data products first-class in search and fetch. Asking what a
// business term means returns the term and its definition, not only the
// datasets tagged with it, and fetching a glossary node lists what is under it,
// which is how the glossary is browsed.
//
// It is shared: a definition belongs to no connection and is readable by
// anyone who can search. The datasets and columns listed under a definition do
// belong to connections, and each is put through the caller's connection
// boundary, with what was removed reported rather than the list shortening
// silently.
type GovernanceProvider struct {
	reader DefinitionReader
	bound  BoundName
}

// NewGovernanceProvider builds the governance provider over the catalog's
// definitions. A nil bound treats a connection's instance name as the name
// persona rules match.
func NewGovernanceProvider(reader DefinitionReader, bound BoundName) *GovernanceProvider {
	return &GovernanceProvider{reader: reader, bound: bound}
}

// Name returns the provenance label.
func (*GovernanceProvider) Name() string { return SourceGovernance }

// Scope marks the governance vocabulary shared (global, always queried).
func (*GovernanceProvider) Scope() Scope { return ScopeShared }

// GovernanceEntity is the full content of a fetched definition: the definition
// itself, the glossary entries under a node, and the datasets and columns that
// carry a term, tag or domain, which is what makes a definition actionable
// ("Net Revenue means this, and these three tables report it").
type GovernanceEntity struct {
	Reference string       `json:"reference"`
	Kind      catalog.Kind `json:"kind"`
	Name      string       `json:"name"`
	// Description is a term's definition or a tag's or domain's description.
	Description string `json:"description,omitempty"`
	// Parent is the glossary node a term or node is filed under.
	Parent             string            `json:"parent,omitempty"`
	Owners             []catalog.Owner   `json:"owners,omitempty"`
	CustomProperties   map[string]string `json:"custom_properties,omitempty"`
	DocumentationLinks []catalog.Link    `json:"documentation_links,omitempty"`
	// Children are the glossary terms and nodes directly under a node; fetching
	// one browses further down.
	Children []catalog.DefinitionSummary `json:"children,omitempty"`
	// Carriers are the datasets and columns carrying a term, tag or domain,
	// bounded by carrierLimit and filtered to the caller's connections.
	Carriers []catalog.Use `json:"carriers,omitempty"`
	// MoreCarriers reports that more carry it than are listed.
	MoreCarriers bool `json:"more_carriers,omitempty"`
	// CarriersWithheld counts the carriers the caller's connection boundary
	// removed, and Notice explains it.
	CarriersWithheld int    `json:"carriers_withheld,omitempty"`
	Notice           string `json:"notice,omitempty"`
}

// kindLabels name each definition kind in a hit's heading.
var kindLabels = map[catalog.Kind]string{
	catalog.KindGlossaryTerm: "glossary term",
	catalog.KindGlossaryNode: "glossary node",
	catalog.KindTag:          "tag",
	catalog.KindDomain:       "domain",
	catalog.KindDataProduct:  "data product",
}

// Search ranks the definitions against the intent by the words they share: a
// definition's name, slug and description against the words of the question.
// A query with no intent yields nothing; a definition is reached by reference
// through fetch, not through the entity path.
func (p *GovernanceProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	tokens := strings.Fields(strings.ToLower(q.Intent))
	if len(tokens) == 0 {
		return nil, nil
	}
	defs, err := p.candidates(ctx, tokens)
	if err != nil {
		return nil, err
	}
	type scored struct {
		def   catalog.DefinitionSummary
		score float64
	}
	matches := make([]scored, 0, len(defs))
	for _, d := range defs {
		if s := tokenOverlapScore(d.Name+" "+d.Slug+" "+d.Description, tokens); s > 0 {
			matches = append(matches, scored{def: d, score: s})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	if q.Limit > 0 && len(matches) > q.Limit {
		matches = matches[:q.Limit]
	}
	hits := make([]Hit, 0, len(matches))
	for _, m := range matches {
		hits = append(hits, Hit{
			Text:      governanceText(m.def),
			Source:    SourceGovernance,
			Ref:       m.def.Reference,
			Score:     m.score,
			Reference: m.def.Reference,
		})
	}
	return hits, nil
}

// maxSearchTokens bounds the words of an intent a definition search reads
// candidates for.
const maxSearchTokens = 16

// candidates reads the definitions holding at least one word of the intent in
// their name, slug or description: one read per word, each bounded, joined by
// reference. Reading by word rather than one page of every definition keeps a
// definition past the first page in name order findable.
func (p *GovernanceProvider) candidates(ctx context.Context, tokens []string) ([]catalog.DefinitionSummary, error) {
	if len(tokens) > maxSearchTokens {
		tokens = tokens[:maxSearchTokens]
	}
	seen := make(map[string]bool)
	out := make([]catalog.DefinitionSummary, 0)
	for _, tok := range tokens {
		defs, err := p.reader.ListDefinitions(ctx, catalog.DefinitionQuery{Text: tok, Limit: vocabularyPageLimit})
		if err != nil {
			return nil, fmt.Errorf("listing definitions: %w", err)
		}
		for _, d := range defs {
			if !seen[d.Reference] {
				seen[d.Reference] = true
				out = append(out, d)
			}
		}
	}
	return out, nil
}

// governanceText renders a definition as a knowledge snippet: its kind and
// name, then its description when it has one.
func governanceText(d catalog.DefinitionSummary) string {
	heading := kindLabels[d.Kind] + ": " + d.Name
	if d.Description == "" {
		return heading
	}
	return heading + "\n" + scrub(d.Description)
}

// Fetch dereferences a definition reference: a glossary term, glossary node,
// tag, domain or data product. A node lists the terms and nodes under it; a
// term, tag or domain lists the datasets and columns carrying it that the
// caller's connections reach. Any other reference is declined.
func (p *GovernanceProvider) Fetch(ctx context.Context, ref string, caller Caller) (*Document, bool, error) {
	parsed, err := catalog.ParseRef(ref)
	if err != nil || kindLabels[parsed.Kind] == "" {
		return nil, false, nil //nolint:nilerr // not a definition reference: decline so the Router tries the next provider
	}
	d, err := p.reader.GetDefinition(ctx, parsed)
	if err != nil {
		return nil, true, catalogError(err)
	}
	a := d.Aspects.WithText(scrub)
	entity := GovernanceEntity{
		Reference:          d.Reference,
		Kind:               d.Kind,
		Name:               d.Name,
		Description:        a.Description,
		Parent:             d.Parent,
		Owners:             a.Owners,
		CustomProperties:   a.CustomProperties,
		DocumentationLinks: a.DocumentationLinks,
	}
	if d.Kind == catalog.KindGlossaryNode {
		children, err := p.reader.ListDefinitions(ctx, catalog.DefinitionQuery{Parent: d.Reference, Limit: childLimit})
		if err != nil {
			return nil, true, fmt.Errorf("listing the entries under %s: %w", d.Reference, err)
		}
		for i := range children {
			children[i].Description = scrub(children[i].Description)
		}
		entity.Children = children
	}
	if err := p.attachCarriers(ctx, parsed, &entity, caller); err != nil {
		return nil, true, err
	}
	return &Document{
		Reference: d.Reference,
		Source:    SourceGovernance,
		Title:     d.Name,
		Body:      a.Description,
		Content:   entity,
	}, true, nil
}

// attachCarriers lists the datasets and columns carrying the definition that
// the caller may see, and says how many it withheld.
func (p *GovernanceProvider) attachCarriers(ctx context.Context, ref catalog.Ref, e *GovernanceEntity, caller Caller) error {
	uses, err := p.reader.Uses(ctx, ref, carrierLimit+1)
	if err != nil {
		return fmt.Errorf("listing what carries %s: %w", e.Reference, err)
	}
	if len(uses) > carrierLimit {
		uses, e.MoreCarriers = uses[:carrierLimit], true
	}
	withheld := 0
	for _, u := range uses {
		if !caller.allowsConnection(p.boundName(u.Dataset)) {
			withheld++
			continue
		}
		e.Carriers = append(e.Carriers, u)
	}
	e.CarriersWithheld = withheld
	e.Notice = withheldContentNotice(withheld, caller.Persona)
	return nil
}

// boundName is the persona-rule name of the connection a dataset reference
// names, or "" (platform-level, always visible) for one that does not parse.
func (p *GovernanceProvider) boundName(reference string) string {
	r, err := catalog.ParseRef(reference)
	if err != nil {
		return ""
	}
	if p.bound == nil {
		return r.Connection
	}
	return p.bound(r.ConnectionKind, r.Connection)
}
