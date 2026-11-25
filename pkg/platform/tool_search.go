package platform

import (
	"context"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/connroute"
	"github.com/txn2/mcp-data-platform/internal/platform/toolsearch"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/knowledge/federation"
	"github.com/txn2/mcp-data-platform/pkg/platform/instructions"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/tools/toolsindex"
)

// searchToolName is the search tool's name: never ranked as a tool hit of
// itself, and left out of the tools index (#2809).
const searchToolName = "search"

// toolRanker is search's tools source (#2809): the platform's own tools,
// ranked by the tools index and filtered by the persona predicate tools/list
// applies. Every dependency is read at call time, since search is assembled
// before the MCP server and the index queue exist.
func (p *Platform) toolRanker() *toolsearch.Ranker {
	return &toolsearch.Ranker{
		Enumerate: p.enumerateGlobalTools,
		Embedder:  func() embedding.Provider { return p.embeddingProv },
		Index:     func() *toolsindex.Store { return p.indexQueue.ToolsIndexStore() },
		Allowed: func(ctx context.Context, roles []string, tool string) bool {
			if p.authorizer == nil {
				return true
			}
			return p.personaAllowsTool(ctx, roles, tool)
		},
		Exclude: []string{searchToolName},
	}
}

// connectionRoutes is what the connection routing layer reads (#2812): every
// connection with its kind, the kind of the toolkit registering a tool, and
// the tools a kind's toolkits register, all read per call.
func (p *Platform) connectionRoutes() connroute.Deps {
	return connroute.Deps{
		Connections: func() []knowledge.ConnectionInfo {
			return federation.NewConnectionLister(p.toolkitRegistry).Connections()
		},
		ToolKind: func(tool string) string { return p.toolkitRegistry.GetToolkitForTool(tool).Kind },
		KindTools: func(kind string) []string {
			var out []string
			for _, tk := range p.toolkitRegistry.GetByKind(kind) {
				for _, t := range tk.Tools() {
					if !slices.Contains(out, t) {
						out = append(out, t)
					}
				}
			}
			return out
		},
	}
}

// connectionKinds is every connection kind the deployment has connections of,
// in name order, with the tools of it the caller may call (#2812).
func (p *Platform) connectionKinds(accessibleTools []string) []instructions.ConnectionKind {
	routes := p.connectionRoutes()
	seen := map[string]bool{}
	var kinds []string
	for _, c := range routes.Connections() {
		if !seen[c.Kind] {
			seen[c.Kind] = true
			kinds = append(kinds, c.Kind)
		}
	}
	slices.Sort(kinds)
	out := make([]instructions.ConnectionKind, 0, len(kinds))
	for _, k := range kinds {
		var tools []string
		for _, t := range routes.KindTools(k) {
			if slices.Contains(accessibleTools, t) {
				tools = append(tools, t)
			}
		}
		slices.Sort(tools)
		out = append(out, instructions.ConnectionKind{Name: connroute.KindName(k), Tools: tools, Script: connroute.ScriptRoute(k)})
	}
	return out
}

// marshalToolResult renders a tool output struct as a result carrying both a
// JSON text block and StructuredContent, so a declared OutputSchema describes
// what the tool actually emits.
func marshalToolResult(out any) (*mcp.CallToolResult, any, error) {
	data, err := wirejson.MarshalIndent(out, "", "  ")
	if err != nil {
		return toolErrorResult("failed to encode result: " + err.Error()), nil, nil
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(data)}},
		StructuredContent: out,
	}, nil, nil
}

// toolErrorResult builds an MCP error result (protocol-level tool
// error, not a Go error).
func toolErrorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "Error: " + msg}},
		IsError: true,
	}
}

// personaAllowsTool is the single persona authorization predicate shared by
// the tools/list visibility middleware (addToolVisibilityMiddleware) and
// search's tools source. Keeping one implementation prevents the two from
// drifting: if they diverged, search could surface a tool the persona cannot
// call, or hide one it can. Assumes a non-nil authorizer (callers guard the
// no-authorizer case as "allow all", matching the middleware, which only wires
// this predicate when an authorizer exists).
func (p *Platform) personaAllowsTool(ctx context.Context, roles []string, toolName string) bool {
	allowed, _, _ := p.authorizer.IsAuthorized(ctx, "", roles, toolName, "")
	return allowed
}
