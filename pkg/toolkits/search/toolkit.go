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

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
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
}

// searchSchema is the JSON Schema for the search tool input.
var searchSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "intent": {
      "type": "string",
      "description": "Natural-language description of what you are looking for, across every source you can access: the technical catalog, context documents, canonical knowledge pages (business/domain ontology), your memory, captured insights, your feedback, saved assets, prompts, API endpoints, and connections. Ranked by relevance and grouped by source. Provide intent, entity_urns, or both."
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
      "description": "Optional: narrow the search to specific sources (e.g. [\"catalog\"], [\"memory\",\"endpoints\"]). Omit to search every source you can access. This only narrows results; it never opts you into a source your access would otherwise exclude. Known sources: catalog, context_documents, knowledge_pages, memory, insights, feedback, assets, prompts, endpoints, connections. An unrecognized name is reported back in unknown_sources rather than silently ignored."
    },
    "limit": {
      "type": "integer",
      "description": "Total number of results to display across all sources (the display budget, default 10, max 50). Each source is floored so breadth stays visible and capped so none dominates; coverage reports how many more matched beyond what is shown."
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
      "description": "A reference to read in full. References come in two namespaces: urn:li:... is the external DataHub catalog scheme, mcp:... is the internal-platform scheme. fetch dereferences any well-formed reference of these forms: knowledge pages (mcp:knowledge_page:<id>), context documents (urn:li:document:<id>), catalog datasets (urn:li:dataset:<id>), saved assets (mcp:asset:<id>), prompts (mcp:prompt:<id>), and connections (mcp:connection:(kind,name)). The usual source is a search result's \"reference\" field (pass it verbatim), but a reference you already hold from another tool works too (for example a urn:li:dataset:... from datahub_get_lineage or an entity_urns lookup). Returns the full content the search snippet was a preview of."
    }
  }
}`)

// Toolkit registers the search tool over a knowledge.Router.
type Toolkit struct {
	name   string
	router *knowledge.Router
}

// New builds the search toolkit over a router.
func New(name string, router *knowledge.Router) *Toolkit {
	return &Toolkit{name: name, router: router}
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
			"source you can access (the technical catalog, context documents, canonical knowledge pages, your memory, " +
			"captured insights, your feedback, saved assets, prompts, API endpoints, and connections) and returns results " +
			"grouped by source with a coverage " +
			"summary, so you see the full shape of the answer space instead of tunneling into the first tool " +
			"that comes to mind. For example 'how do we calculate churn' or 'customer retention'. Results are " +
			"navigational pointers (title, reference, source); read one in full with fetch (pass its reference) " +
			"or drill in with a scoped tool (trino_query, api_invoke_endpoint). Pass entity_urns to pull what " +
			"you know about specific datasets. Personal results are scoped to you.",
		InputSchema: searchSchema,
	}, t.handleSearch)

	mcp.AddTool(s, &mcp.Tool{
		Name:  fetchToolName,
		Title: "Fetch",
		Description: "Read a reference in full. search returns navigational pointers with truncated " +
			"snippets; fetch dereferences one pointer's reference back to its complete content (a knowledge " +
			"page's body, a context document's full text, a dataset's catalog context, an asset's metadata, " +
			"a prompt, or a connection descriptor). A reference is either a urn:li:... form (the external " +
			"DataHub catalog scheme) or an mcp:... form (the internal-platform scheme); fetch accepts both. " +
			"The usual source is a search result's \"reference\" field (pass it verbatim), but a well-formed " +
			"reference you already hold from another tool works too (for example a urn:li:dataset:... from " +
			"datahub_get_lineage or an entity_urns lookup). A reference that is stale, unknown, or outside what " +
			"you can access returns found=false rather than an error, so a dangling citation is a clean answer. " +
			"Personal results stay scoped to you: fetch never reads content you could not have found with search.",
		InputSchema: fetchSchema,
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
		return errorResult("search requires intent or entity_urns"), nil, nil
	}

	res, err := t.router.Search(ctx, knowledge.Query{
		Intent:     searchText,
		EntityURNs: input.EntityURNs,
		Status:     strings.TrimSpace(input.Status),
		Sources:    input.Sources,
		Caller:     callerFromContext(ctx),
		Limit:      input.Limit,
	})
	if err != nil {
		return errorResult("search failed: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
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
	return jsonResult(searchOutput{
		Groups:         groups,
		Coverage:       coverage,
		Count:          shown,
		Ranking:        res.Ranking,
		UnknownSources: res.UnknownSources,
	})
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
		return errorResult("fetch requires a reference"), nil, nil
	}

	doc, err := t.router.Fetch(ctx, ref, callerFromContext(ctx))
	if err != nil {
		if errors.Is(err, knowledge.ErrNotFound) {
			return jsonResult(fetchOutput{
				Found:     false,
				Reference: ref,
				Message: "no content found for this reference; it may be stale, not a recognized " +
					"reference form, or outside what you can access",
			})
		}
		return errorResult("fetch failed: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
	}

	return jsonResult(fetchOutput{
		Found:     true,
		Reference: ref,
		Document:  doc,
	})
}

// callerFromContext resolves the requester identity from the platform context.
// A request without a platform context (or without identity) yields an
// anonymous caller, for which the router skips every per-user provider.
func callerFromContext(ctx context.Context) knowledge.Caller {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return knowledge.Caller{}
	}
	return knowledge.Caller{UserID: pc.UserID, Email: pc.UserEmail, Persona: pc.PersonaName}
}

// errorResult creates an error CallToolResult.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf(`{"error": %q}`, msg)},
		},
		IsError: true,
	}
}

// jsonResult marshals a value to JSON and returns it as a CallToolResult.
func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errorResult("internal error marshaling response"), nil, nil //nolint:nilerr // MCP protocol
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(data)},
		},
	}, nil, nil
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
