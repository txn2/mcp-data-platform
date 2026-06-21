// Package knowledgesearch exposes the unified knowledge read path (#632) as the
// knowledge_search MCP tool. It is a thin surface over knowledge.Router: it
// resolves the caller identity from the platform context, runs one search across
// every registered knowledge provider, and returns the fused, ranked,
// provenance-tagged hits. The router owns scope enforcement and cross-source
// fusion; this package owns only the tool schema and the request/response shape.
package knowledgesearch

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

// toolName is the MCP tool name for the unified knowledge search.
const toolName = "knowledge_search"

// searchInput is the deserialized knowledge_search input. Intent is the
// natural-language description of what the caller wants; Context is optional
// surrounding detail folded into the same query to sharpen ranking.
type searchInput struct {
	Intent  string `json:"intent"`
	Context string `json:"context,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

// searchOutput is the knowledge_search response: the fused hits, their count,
// and the ranking mode (hybrid when an embedder is configured, lexical
// otherwise). It serializes knowledge.Hit directly, which already carries the
// wire JSON tags, so there is no parallel response struct to keep in sync.
type searchOutput struct {
	Hits    []knowledge.Hit `json:"hits"`
	Count   int             `json:"count"`
	Ranking string          `json:"ranking"`
}

// searchSchema is the JSON Schema for the knowledge_search tool input.
var searchSchema = json.RawMessage(`{
  "type": "object",
  "required": ["intent"],
  "additionalProperties": false,
  "properties": {
    "intent": {
      "type": "string",
      "description": "Natural-language description of the knowledge you are looking for, across captured memory, insights, and saved assets. Ranked by semantic relevance (hybrid vector + lexical) when an embedding provider is configured, and by lexical relevance otherwise.",
      "minLength": 1
    },
    "context": {
      "type": "string",
      "description": "Optional surrounding context (the task, table, or question at hand) folded into the query to sharpen relevance."
    },
    "limit": {
      "type": "integer",
      "description": "Maximum number of results to return (default 10, max 50)."
    }
  }
}`)

// Toolkit registers the knowledge_search tool over a knowledge.Router.
type Toolkit struct {
	name   string
	router *knowledge.Router
}

// New builds the knowledge_search toolkit over a router.
func New(name string, router *knowledge.Router) *Toolkit {
	return &Toolkit{name: name, router: router}
}

// Kind returns the toolkit kind.
func (*Toolkit) Kind() string { return "knowledge_search" }

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string { return t.name }

// Connection returns the connection name for audit logging (none).
func (*Toolkit) Connection() string { return "" }

// RegisterTools registers the knowledge_search tool with the MCP server.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  toolName,
		Title: "Search Knowledge",
		Description: "Search all platform knowledge from one place: your captured memory, " +
			"reviewed insights, and saved assets, ranked together by relevance. " +
			"Call this to find what is already known before re-asking the user or re-deriving it, " +
			"for example 'how do we calculate churn' or 'what did we learn about the orders table'. " +
			"Each result is tagged with its source. Personal results are scoped to you.",
		InputSchema: searchSchema,
	}, t.handleSearch)
}

// Tools returns the list of tool names provided by this toolkit.
func (*Toolkit) Tools() []string { return []string{toolName} }

// SetSemanticProvider is a no-op: knowledge_search reads through the router's
// providers, not the enrichment semantic provider.
func (*Toolkit) SetSemanticProvider(semantic.Provider) {}

// SetQueryProvider is a no-op: knowledge_search does not execute queries.
func (*Toolkit) SetQueryProvider(query.Provider) {}

// Close releases resources (none).
func (*Toolkit) Close() error { return nil }

// handleSearch runs a knowledge_search call. It resolves the caller identity
// from the platform context (per-user providers scope on it), folds any context
// into the intent, and returns the router's fused, source-tagged hits.
func (t *Toolkit) handleSearch(ctx context.Context, _ *mcp.CallToolRequest, input searchInput) (*mcp.CallToolResult, any, error) {
	intent := strings.TrimSpace(input.Intent)
	if intent == "" {
		return errorResult("intent is required for knowledge_search"), nil, nil
	}

	caller := callerFromContext(ctx)
	searchText := intent
	if c := strings.TrimSpace(input.Context); c != "" {
		searchText += "\n" + c
	}

	res, err := t.router.Search(ctx, searchText, caller, input.Limit)
	if err != nil {
		return errorResult("knowledge search failed: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
	}

	hits := res.Hits
	if hits == nil {
		hits = []knowledge.Hit{}
	}
	return jsonResult(searchOutput{
		Hits:    hits,
		Count:   len(hits),
		Ranking: res.Ranking,
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
	return knowledge.Caller{UserID: pc.UserID, Email: pc.UserEmail}
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
