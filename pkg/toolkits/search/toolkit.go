// Package search exposes the universal, topology-free discovery entry point
// (#645) as the search MCP tool. It is a thin surface over knowledge.Router: it
// resolves the caller identity from the platform context, runs one query across
// every searchable source the persona can access, and returns a balanced,
// grouped-by-source result set plus a coverage summary so the agent sees the
// shape of the answer space (datasets, memory, insights, assets, prompts, API
// endpoints, connections) without first having to know the topology. The router
// owns per-source scope enforcement and the balanced allocator; this package
// owns only the tool schema and the request/response shape.
package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// toolName is the MCP tool name for the universal search entry point; fetchToolName
// is its companion read verb that dereferences a search reference to full content.
const (
	toolName      = "search"
	fetchToolName = "fetch"
)

// searchInput is the deserialized search input. Intent is the natural-language
// description of what the caller wants; Context is optional surrounding detail
// folded into the same query to sharpen ranking. EntityURNs is an exact,
// entity-keyed lookup that unions every source linked to those datasets (the
// catalog entity, URN-linked insights, and URN-linked memory), expanded along
// lineage. Status optionally filters by review state. Sources optionally
// narrows the federation to named sources (it only narrows; it never opts into
// a source the persona could not otherwise access). At least one of intent or
// entity_urns must be set.
type searchInput struct {
	Intent     string   `json:"intent,omitempty"`
	Context    string   `json:"context,omitempty"`
	EntityURNs []string `json:"entity_urns,omitempty"`
	Status     string   `json:"status,omitempty"`
	Sources    []string `json:"sources,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	// Offset selects browse (enumeration) mode together with a single `sources`
	// entry and no intent/entity_urns: it is the 0-based start of the page. It is
	// ignored in search (relevance) mode.
	Offset int `json:"offset,omitempty"`
}

// browseOutput is the enumeration response (#695): the source enumerated, its total
// member count, the effective offset/limit of this page, the number shown, and the
// page of members (each a navigational item carrying a `reference` that `fetch`
// reads in full). Unlike searchOutput it is a single flat, unranked list, since a
// browse enumerates one source exhaustively rather than ranking across many.
type browseOutput struct {
	Source string          `json:"source"`
	Total  int             `json:"total"`
	Offset int             `json:"offset"`
	Limit  int             `json:"limit"`
	Count  int             `json:"count"`
	Items  []knowledge.Hit `json:"items"`
}

// searchOutput is the search response: the balanced display set grouped by
// source, a coverage summary (per-source matched vs shown counts, the
// anti-tunnel signal), the total hits shown, and the ranking mode (hybrid when
// an embedder is configured, lexical otherwise). It serializes the router's
// grouped contract directly.
type searchOutput struct {
	Groups   []knowledge.SourceGroup    `json:"groups"`
	Coverage []knowledge.SourceCoverage `json:"coverage"`
	Count    int                        `json:"count"`
	Ranking  string                     `json:"ranking"`
	// UnknownSources echoes any requested `sources` names that match no known
	// source, so a typo (e.g. "documnets") is reported instead of silently
	// returning nothing.
	UnknownSources []string `json:"unknown_sources,omitempty"`
	// WithheldNotice explains the coverage block's withheld counts in one line:
	// how many results the caller's persona hid, from which sources, and how to
	// get access (#1108). Present only when something was withheld. Without it an
	// agent reads a shortened result set as "this does not exist" and goes off to
	// re-derive what it was not permitted to see.
	WithheldNotice string `json:"withheld_notice,omitempty"`
}

// searchSchema is the JSON Schema for the search tool input.
var searchSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "intent": {
      "type": "string",
      "description": "Natural-language description of what you are looking for, across every source you can access: the technical catalog, the governance vocabulary (glossary terms, tags, domains), context documents, canonical knowledge pages (business/domain ontology), your memory, captured insights, your feedback, saved assets, uploaded reference material (resources), prompts, managed scripts, the queries and API calls you have already run (calls), the sessions you ran them in (sessions), API endpoints, and connections. Ranked by relevance and grouped by source. Provide intent, entity_urns, or both."
    },
    "context": {
      "type": "string",
      "description": "Optional surrounding context (the task, table, or question at hand) folded into the intent to sharpen relevance."
    },
    "entity_urns": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Exact entity-keyed lookup: return everything linked to these DataHub URNs (the catalog entity, insights about it, and your memory linked to it), expanded along lineage. Use when you have specific datasets in hand rather than a natural-language question."
    },
    "status": {
      "type": "string",
      "description": "Optional filter by insight review status (pending, approved, rejected, applied, superseded, rolled_back)."
    },
    "sources": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Optional: narrow the search to specific sources (e.g. [\"catalog\"], [\"memory\",\"endpoints\"]). Omit to search every source you can access. This only narrows results; it never opts you into a source your access would otherwise exclude. Known sources: catalog, governance, context_documents, knowledge_pages, memory, insights, feedback, assets, resources, prompts, scripts, calls, sessions, endpoints, connections. An unrecognized name is reported back in unknown_sources rather than silently ignored. To BROWSE (enumerate) a source instead of searching it, pass exactly one source here with no intent and no entity_urns (browsable sources: knowledge_pages, context_documents)."
    },
    "limit": {
      "type": "integer",
      "description": "Search mode: total results to display across all sources (display budget, default 10, max 50). Browse mode: page size (default 50, max 100)."
    },
    "offset": {
      "type": "integer",
      "description": "Browse (enumeration) mode only: the 0-based start of the page. Use with exactly one sources entry and no intent/entity_urns to page the complete set of that source (the response carries a total so you know how many pages remain). Ignored in search mode."
    }
  }
}`)

// fetchInput is the deserialized fetch input: a single reference string, the
// canonical citation search emits on every result (mcp:knowledge_page:<id>,
// urn:li:document:<id>, urn:li:dataset:<id>, mcp:asset:<id>, mcp:prompt:<id>, or
// mcp:connection:(kind,name)).
type fetchInput struct {
	Reference string `json:"reference"`
}

// fetchOutput is the fetch response. Found reports whether the reference resolved;
// when false, Document is nil and Message explains why (stale, unknown form, or out
// of the caller's scope), so a dangling citation is a normal, structured answer
// rather than a tool error. When true, Document carries the full content.
type fetchOutput struct {
	Found     bool                `json:"found"`
	Reference string              `json:"reference"`
	Document  *knowledge.Document `json:"document,omitempty"`
	Message   string              `json:"message,omitempty"`
}

// fetchSchema is the JSON Schema for the fetch tool input.
var fetchSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["reference"],
  "properties": {
    "reference": {
      "type": "string",
      "description": "A reference to read in full. References come in two namespaces: urn:li:... is the external DataHub catalog scheme, mcp:... is the internal-platform scheme. fetch dereferences any well-formed reference of these forms: knowledge pages (mcp:knowledge_page:<id>), context documents (urn:li:document:<id>), catalog datasets (urn:li:dataset:<id>), glossary terms (urn:li:glossaryTerm:<id>), tags (urn:li:tag:<id>), domains (urn:li:domain:<id>), saved assets (mcp:asset:<id>), uploaded reference material (mcp:resource:<id>), prompts (mcp:prompt:<id>), managed scripts (mcp:script:<id>), recorded calls (mcp:call:<id>), your own sessions (mcp:session:<id>), connections (mcp:connection:(kind,name)), captured insights (mcp:insight:<id>), and your personal memory (mcp:memory:<id>). A glossary term, tag, or domain comes back with its definition and the datasets that carry it. The usual source is a search result's \"reference\" field (pass it verbatim), but a reference you already hold from another tool works too (for example a urn:li:dataset:... from datahub_get_lineage or an entity_urns lookup). A text resource comes back with its contents inline; a binary one comes back as metadata plus its mcp:// URI and size. A recorded call comes back as the statement or request that ran, what it was for, what came of it, and how many later sessions re-ran it; reading one is what makes your own re-run of it count as reuse. A session comes back as the work it was: what its calls were for, the assets and insights it produced as references you can follow, and its timeline in order, each cataloged call carrying its own mcp:call: reference and outcome. A managed script comes back as its contract — what it is, what parameters it takes, whether a version is approved, its schedule, and what its last successful run produced — not as its source code; finding one grants nothing, and running it is still run_script. Your memory is scoped to you; so are your insights until one is applied, at which point it is organization knowledge anyone can read. Returns the full content the search snippet was a preview of."
    }
  }
}`)

// searchResultSchema is the declared OutputSchema for the search tool. The
// search tool serves both relevance mode (searchOutput) and browse mode
// (browseOutput), so the two response shapes are merged into one open schema; see
// mergedSearchOutputSchema. fetchResultSchema is the fetch tool's schema.
var (
	searchResultSchema = middleware.OpenToolOutputSchema(mergedSearchOutputSchema())
	fetchResultSchema  = middleware.MustOutputSchema[fetchOutput]()
)

// structuredResult renders v as a search tool result: v is placed in
// StructuredContent (so the declared OutputSchema describes what the tool
// actually emits, and structured-output clients receive the body) and, as
// indented JSON, in a TextContent block for content-only clients. A marshal
// failure falls back to an in-band error result, matching how the handlers
// report failures.
func structuredResult(v any) (*mcp.CallToolResult, any, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return toolkit.ErrorResult("internal error marshaling response: " + err.Error()), nil, nil
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(b)}},
		StructuredContent: v,
	}, nil, nil
}

// withResourceLinks appends one mcp.ResourceLink content block per hit that
// carries a client-attachable file (today: managed resources), so a client with
// native resource support can attach the file itself rather than only the JSON
// pointer. It follows the in-tree precedent of the enrichment middleware's
// schema:// and availability:// links on DataHub results. Duplicate URIs are
// emitted once. A nil result or an error result is returned untouched.
func withResourceLinks(res *mcp.CallToolResult, groups []knowledge.SourceGroup) *mcp.CallToolResult {
	if res == nil || res.IsError {
		return res
	}
	seen := make(map[string]bool)
	for _, g := range groups {
		for i := range g.Hits {
			link := g.Hits[i].Link
			if link == nil || link.URI == "" || seen[link.URI] {
				continue
			}
			seen[link.URI] = true
			res.Content = append(res.Content, &mcp.ResourceLink{
				URI:         link.URI,
				Name:        link.Name,
				Description: link.Description,
				MIMEType:    link.MIMEType,
			})
		}
	}
	return res
}

// mergedSearchOutputSchema derives a single object schema whose properties are
// the union of searchOutput (relevance mode) and browseOutput (browse mode),
// since both are returned by the one search tool depending on the arguments. It
// panics on a reflection failure, a programming error covered by tests.
func mergedSearchOutputSchema() *jsonschema.Schema {
	s, err := jsonschema.For[searchOutput](nil)
	if err != nil {
		panic("search: derive searchOutput schema: " + err.Error())
	}
	b, err := jsonschema.For[browseOutput](nil)
	if err != nil {
		panic("search: derive browseOutput schema: " + err.Error())
	}
	for _, name := range b.PropertyOrder {
		if _, ok := s.Properties[name]; ok {
			continue
		}
		s.Properties[name] = b.Properties[name]
		s.PropertyOrder = append(s.PropertyOrder, name)
	}
	return s
}

// Toolkit registers the search tool over a knowledge.Router.
type Toolkit struct {
	name             string
	router           *knowledge.Router
	personasForRoles func(roles []string) []string
}

// New builds the search toolkit over a router.
func New(name string, router *knowledge.Router) *Toolkit {
	return &Toolkit{name: name, router: router}
}

// SetPersonasForRoles binds the resolver that maps a caller's roles to every
// persona they BELONG TO. Sources whose visibility rule is persona membership
// (managed resources) scope on that set rather than on the single resolved
// persona, which falls back to the configured default persona for a caller whose
// roles match none — a fallback that would hand an unmatched caller the default
// persona's material. Optional: with no resolver bound, the caller carries only
// the resolved persona, matching what the resources middleware does when it has
// no resolver either. Call once at wiring time.
func (t *Toolkit) SetPersonasForRoles(fn func(roles []string) []string) {
	t.personasForRoles = fn
}

// Kind returns the toolkit kind.
func (*Toolkit) Kind() string { return "search" }

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string { return t.name }

// Connection returns the connection name for audit logging (none).
func (*Toolkit) Connection() string { return "" }

// RegisterTools registers the search tool with the MCP server.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  toolName,
		Title: "Search",
		Description: "The one way to discover. Call this FIRST, before any other tool, to find what is " +
			"already known and to learn where the answer to a question lives. One query fans across every " +
			"source you can access (the technical catalog, the governance vocabulary, context documents, canonical " +
			"knowledge pages, your memory, " +
			"captured insights, your feedback, saved assets, uploaded reference material, prompts, API endpoints, and " +
			"connections) and returns results grouped by source with a coverage " +
			"summary, so you see the full shape of the answer space instead of tunneling into the first tool " +
			"that comes to mind. For example 'how do we calculate churn' or 'customer retention'. A question " +
			"about what a business term MEANS answers itself here: the governance source returns the glossary " +
			"term, tag, or domain with its definition in the hit, not just the datasets that carry it. Results are " +
			"navigational pointers (title, reference, source); read one in full with fetch (pass its reference) " +
			"or drill in with a scoped tool (trino_query, api_invoke_endpoint). Pass entity_urns to pull what " +
			"you know about specific datasets. Personal results are scoped to you, except that an insight " +
			"someone had applied is knowledge the organization holds and reaches everyone (its captured_by " +
			"names who recorded it). To enumerate a whole source " +
			"instead of relevance-ranking it (to audit or migrate it), call with exactly one `sources` entry, " +
			"no intent, and an `offset`: this browses the complete set with a total count (browsable: " +
			"knowledge_pages, context_documents). Freshness: catalog and context-document results come from " +
			"DataHub's search index, which is eventually consistent and can briefly lag a recent catalog write, so " +
			"a result may still show pre-edit text right after you change it. To confirm a specific entity's " +
			"current state after a write, read it directly (datahub_get_entity, or the resulting_state in the " +
			"apply_knowledge apply response), not search.",
		InputSchema:  searchSchema,
		OutputSchema: searchResultSchema,
	}, t.handleSearch)

	mcp.AddTool(s, &mcp.Tool{
		Name:  fetchToolName,
		Title: "Fetch",
		Description: "Read a reference in full. search returns navigational pointers with truncated " +
			"snippets; fetch dereferences one pointer's reference back to its complete content (a knowledge " +
			"page's body, a context document's full text, a dataset's catalog context, a glossary term's, " +
			"tag's, or domain's definition plus the datasets that carry it, an asset's metadata, " +
			"an uploaded resource's contents, a prompt, a connection descriptor, a captured insight, " +
			"or one of your personal memory records). A reference is either a urn:li:... form (the external " +
			"DataHub catalog scheme) or an mcp:... form (the internal-platform scheme); fetch accepts both. " +
			"The usual source is a search result's \"reference\" field (pass it verbatim), but a well-formed " +
			"reference you already hold from another tool works too (for example a urn:li:dataset:... from " +
			"datahub_get_lineage or an entity_urns lookup). A reference that is stale, unknown, or outside what " +
			"you can access returns found=false rather than an error, so a dangling citation is a clean answer. " +
			"fetch never reads content you could not have found with search: your own personal records, plus " +
			"the insights the organization has applied.",
		InputSchema:  fetchSchema,
		OutputSchema: fetchResultSchema,
	}, t.handleFetch)
}

// Tools returns the list of tool names provided by this toolkit.
func (*Toolkit) Tools() []string { return []string{toolName, fetchToolName} }

// SetSemanticProvider is a no-op: search reads through the router's providers,
// not the enrichment semantic provider.
func (*Toolkit) SetSemanticProvider(semantic.Provider) {}

// SetQueryProvider is a no-op: search does not execute queries.
func (*Toolkit) SetQueryProvider(query.Provider) {}

// Close releases resources (none).
func (*Toolkit) Close() error { return nil }

// handleSearch runs a search call. It resolves the caller identity from the
// platform context (per-user providers scope on it), folds any context into the
// intent, and returns the router's balanced, grouped, coverage-reported result.
// The query may be text (intent), entity-keyed (entity_urns), or both.
func (t *Toolkit) handleSearch(ctx context.Context, _ *mcp.CallToolRequest, input searchInput) (*mcp.CallToolResult, any, error) {
	searchText := strings.TrimSpace(input.Intent)
	if c := strings.TrimSpace(input.Context); c != "" {
		searchText = strings.TrimSpace(searchText + "\n" + c)
	}
	if searchText == "" && len(input.EntityURNs) == 0 {
		// No relevance query: this is either a browse (enumerate one source) or a
		// malformed call. handleBrowse decides and explains.
		return t.handleBrowse(ctx, input)
	}

	caller := t.callerFromContext(ctx)
	res, err := t.router.Search(ctx, knowledge.Query{
		Intent:     searchText,
		EntityURNs: input.EntityURNs,
		Status:     strings.TrimSpace(input.Status),
		Sources:    input.Sources,
		Caller:     caller,
		Limit:      input.Limit,
	})
	if err != nil {
		return toolkit.ErrorResult("search failed: " + err.Error()), nil, nil
	}

	groups := res.Groups
	if groups == nil {
		groups = []knowledge.SourceGroup{}
	}
	coverage := res.Coverage
	if coverage == nil {
		coverage = []knowledge.SourceCoverage{}
	}
	shown := 0
	for _, g := range groups {
		shown += len(g.Hits)
	}
	result, structured, err := structuredResult(searchOutput{
		Groups:         groups,
		Coverage:       coverage,
		Count:          shown,
		Ranking:        res.Ranking,
		UnknownSources: res.UnknownSources,
		WithheldNotice: knowledge.WithheldNotice(coverage, caller.Persona),
	})
	return withResourceLinks(result, groups), structured, err
}

// handleFetch dereferences a search reference to its full content. It resolves the
// caller identity (the router re-applies the same per-user scope search uses, so a
// reference the caller could not have searched returns not-found, not content), and
// renders three outcomes distinctly: a resolved reference returns the document; a
// stale, unknown, or out-of-scope reference returns a structured found=false (not an
// error), so a dangling citation is a normal answer; a real backend failure returns
// a tool error.
func (t *Toolkit) handleFetch(ctx context.Context, _ *mcp.CallToolRequest, input fetchInput) (*mcp.CallToolResult, any, error) {
	ref := strings.TrimSpace(input.Reference)
	if ref == "" {
		return toolkit.ErrorResult("fetch requires a reference"), nil, nil
	}

	doc, err := t.router.Fetch(ctx, ref, t.callerFromContext(ctx))
	if err != nil {
		if errors.Is(err, knowledge.ErrNotFound) {
			return structuredResult(fetchOutput{
				Found:     false,
				Reference: ref,
				Message: "no content found for this reference; it may be stale, not a recognized " +
					"reference form, or outside what you can access",
			})
		}
		return toolkit.ErrorResult("fetch failed: " + err.Error()), nil, nil
	}

	return structuredResult(fetchOutput{
		Found:     true,
		Reference: ref,
		Document:  doc,
	})
}

// handleBrowse enumerates one source in full (#695). It is reached when a call
// carries no intent and no entity_urns: with exactly one browsable source named it
// pages that source (offset/limit) and reports a total; otherwise it explains that
// a call needs either a relevance query (intent/entity_urns) or exactly one source
// to browse. A source that is unknown or not enumerable is reported distinctly so a
// typo reads differently from "that source cannot be listed".
func (t *Toolkit) handleBrowse(ctx context.Context, input searchInput) (*mcp.CallToolResult, any, error) {
	sources := nonBlank(input.Sources)
	if len(sources) != 1 {
		return toolkit.ErrorResult("provide intent or entity_urns to search, or exactly one `sources` entry " +
			"(with no intent/entity_urns) to browse it; browsable sources: " +
			strings.Join(t.router.BrowsableSources(), ", ")), nil, nil
	}
	source := sources[0]

	page, err := t.router.Browse(ctx, source, knowledge.BrowseQuery{
		Caller: t.callerFromContext(ctx),
		Offset: input.Offset,
		Limit:  input.Limit,
	})
	if err != nil {
		switch {
		case errors.Is(err, knowledge.ErrUnknownSource):
			return toolkit.ErrorResult(fmt.Sprintf("unknown source %q; known sources: %s",
				source, strings.Join(knowledge.KnownSources(), ", "))), nil, nil
		case errors.Is(err, knowledge.ErrSourceNotBrowsable):
			return toolkit.ErrorResult(fmt.Sprintf("source %q cannot be enumerated here; browsable sources: %s",
				source, strings.Join(t.router.BrowsableSources(), ", "))), nil, nil
		default:
			return toolkit.ErrorResult("browse failed: " + err.Error()), nil, nil
		}
	}

	items := page.Hits
	if items == nil {
		items = []knowledge.Hit{}
	}
	return structuredResult(browseOutput{
		Source: source,
		Total:  page.Total,
		Offset: page.Offset,
		Limit:  page.Limit,
		Count:  len(items),
		Items:  items,
	})
}

// nonBlank returns the input source names with blank entries removed, so a
// stray empty string in the sources array does not read as a named source.
func nonBlank(sources []string) []string {
	out := make([]string, 0, len(sources))
	for _, s := range sources {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// callerFromContext resolves the requester identity from the platform context.
// A request without a platform context (or without identity) yields an
// anonymous caller, for which the router skips every per-user provider.
//
// Personas is the caller's persona MEMBERSHIP (resolved from roles), distinct
// from the single resolved Persona the request acts as; providers whose
// visibility rule is membership scope on it. See Toolkit.SetPersonasForRoles.
func (t *Toolkit) callerFromContext(ctx context.Context) knowledge.Caller {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return knowledge.Caller{}
	}
	caller := knowledge.Caller{UserID: pc.UserID, Email: pc.UserEmail, Persona: pc.PersonaName, SessionID: pc.SessionID}
	if t.personasForRoles != nil {
		caller.Personas = t.personasForRoles(pc.Roles)
	}
	return caller
}

// Verify interface compliance with registry.Toolkit.
var _ interface {
	Kind() string
	Name() string
	Connection() string
	RegisterTools(s *mcp.Server)
	Tools() []string
	SetSemanticProvider(provider semantic.Provider)
	SetQueryProvider(provider query.Provider)
	Close() error
} = (*Toolkit)(nil)
