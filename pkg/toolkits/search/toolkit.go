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
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// toolName is the MCP tool name for the universal search entry point.
const toolName = "search"

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
}

// searchSchema is the JSON Schema for the search tool input.
var searchSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "intent": {
      "type": "string",
      "description": "Natural-language description of what you are looking for, across every source you can access: the technical catalog (DataHub), your memory, captured insights, saved assets, prompts, API endpoints, and connections. Ranked by relevance and grouped by source. Provide intent, entity_urns, or both."
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
      "description": "Optional: narrow the search to specific sources (e.g. [\"datahub\"], [\"memory\",\"endpoints\"]). Omit to search every source you can access. This only narrows results; it never opts you into a source your access would otherwise exclude. Known sources: datahub, memory, insights, assets, prompts, endpoints, connections."
    },
    "limit": {
      "type": "integer",
      "description": "Total number of results to display across all sources (the display budget, default 10, max 50). Each source is floored so breadth stays visible and capped so none dominates; coverage reports how many more matched beyond what is shown."
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
			"source you can access (the technical catalog, your memory, captured insights, saved assets, " +
			"prompts, API endpoints, and connections) and returns results grouped by source with a coverage " +
			"summary, so you see the full shape of the answer space instead of tunneling into the first tool " +
			"that comes to mind. For example 'how do we calculate churn' or 'customer retention'. Results are " +
			"navigational pointers (title, reference, source); drill in with the scoped tool (trino_query, " +
			"api_invoke_endpoint, datahub_get_entity). Pass entity_urns to pull what you know about specific " +
			"datasets. Personal results are scoped to you.",
		InputSchema: searchSchema,
	}, t.handleSearch)
}

// Tools returns the list of tool names provided by this toolkit.
func (*Toolkit) Tools() []string { return []string{toolName} }

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
		Groups:   groups,
		Coverage: coverage,
		Count:    shown,
		Ranking:  res.Ranking,
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
