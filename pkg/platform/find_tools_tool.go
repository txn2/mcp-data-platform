package platform

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/tools/toolsindex"
)

const (
	defaultFindToolsLimit = 10
	maxFindToolsLimit     = 50
)

// findToolsInput is the platform_find_tools argument set.
type findToolsInput struct {
	Query string `json:"query" jsonschema:"natural-language description of what you want to do; the tool returns the most relevant registered tools by intent"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum tools to return (default 10, max 50)"`
}

// findToolDescriptor is one ranked tool in the response.
type findToolDescriptor struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Score       float64 `json:"score"`
}

// findToolsOutput is the platform_find_tools response.
type findToolsOutput struct {
	Tools []findToolDescriptor `json:"tools"`
	// Note is set when semantic ranking was unavailable and the
	// results are a lexical substring fallback, mirroring the
	// api_list_endpoints fallback UX.
	Note string `json:"note,omitempty"`
}

// registerFindToolsTool registers platform_find_tools: semantic
// discovery over the platform's own tool catalog (#440). The agent
// calls it once at the start of a task to find the relevant tools by
// intent instead of scanning every name.
func (p *Platform) registerFindToolsTool() {
	mcp.AddTool(p.mcpServer, &mcp.Tool{
		Name:        platformFindToolsName,
		Title:       "Find Tools",
		Description: "Find the most relevant platform tools for a natural-language task description, ranked by semantic similarity. Call this once at the start of a task to discover which tools to use instead of reading every tool name. Returns only tools your persona is permitted to call.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input findToolsInput) (*mcp.CallToolResult, any, error) {
		return p.handleFindTools(ctx, req, input)
	})
}

// handleFindTools ranks the persona-permitted tools by similarity to
// the query. Embeddings are persona-neutral (indexed once for the
// whole catalog); the persona filter is applied here at read time, so
// the model only sees tools it is allowed to call.
func (p *Platform) handleFindTools(ctx context.Context, _ *mcp.CallToolRequest, input findToolsInput) (*mcp.CallToolResult, any, error) {
	query := strings.TrimSpace(input.Query)
	limit := input.Limit
	if limit <= 0 || limit > maxFindToolsLimit {
		limit = defaultFindToolsLimit
	}

	tools, err := p.enumerateGlobalTools(ctx)
	if err != nil {
		//nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError, not as Go errors
		return toolErrorResult("failed to enumerate tools: " + err.Error()), nil, nil
	}
	descByName := make(map[string]*mcp.Tool, len(tools))
	for _, t := range tools {
		descByName[t.Name] = t
	}
	permit := p.toolPermitter(ctx)

	out := p.rankFindTools(ctx, query, limit, descByName, permit)
	return marshalToolResult(out)
}

// rankFindTools produces the ranked, persona-filtered result. It uses
// the semantic index when available and falls back to a lexical
// substring match (with an explanatory note) otherwise.
func (p *Platform) rankFindTools(ctx context.Context, query string, limit int,
	descByName map[string]*mcp.Tool, permit func(string) bool,
) findToolsOutput {
	// No query, no embedder, or no index -> lexical only.
	if query == "" || !embedding.IsConfigured(p.embeddingProv) || p.toolsIndexStore == nil {
		out := findToolsOutput{}
		if query != "" && p.toolsIndexStore == nil {
			out.Note = "semantic ranking unavailable (tool index not enabled); returning name/description matches"
		}
		out.Tools = lexicalFindTools(query, descByName, permit, limit)
		return out
	}

	tools, note, ok := p.semanticFindTools(ctx, query, limit, descByName, permit)
	if ok {
		return findToolsOutput{Tools: tools}
	}
	return findToolsOutput{Note: note, Tools: lexicalFindTools(query, descByName, permit, limit)}
}

// semanticFindTools embeds the query, ranks the index by cosine
// similarity, and returns the persona-filtered, capped descriptors.
// ok is false (with an explanatory note) when the semantic path is
// unavailable — an embedding failure or an empty index — so the caller
// falls back to lexical.
func (p *Platform) semanticFindTools(ctx context.Context, query string, limit int,
	descByName map[string]*mcp.Tool, permit func(string) bool,
) (tools []findToolDescriptor, note string, ok bool) {
	qv, err := p.embeddingProv.Embed(ctx, query)
	if err != nil || zeroVector(qv) {
		return nil, "semantic ranking unavailable (query could not be embedded); returning name/description matches", false
	}
	scored, err := p.toolsIndexStore.RankBySimilarity(ctx, toolsindex.SourceID, qv)
	if err != nil || len(scored) == 0 {
		return nil, "semantic ranking unavailable (tools not indexed yet); returning name/description matches", false
	}
	out := make([]findToolDescriptor, 0, limit)
	for _, s := range scored {
		t, visible := descByName[s.ToolName]
		if !visible || !permit(s.ToolName) {
			continue
		}
		out = append(out, findToolDescriptor{Name: s.ToolName, Description: t.Description, Score: s.Score})
		if len(out) >= limit {
			break
		}
	}
	return out, "", true
}

// toolPermitter returns a predicate that reports whether the caller's
// persona may use a tool. It mirrors the tools/list visibility
// middleware exactly: with no authorizer or no resolved roles, no
// persona filter is applied (the global-visible set already excludes
// hidden tools).
func (p *Platform) toolPermitter(ctx context.Context) func(string) bool {
	if p.authorizer == nil {
		return func(string) bool { return true }
	}
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil || len(pc.Roles) == 0 {
		return func(string) bool { return true }
	}
	roles := pc.Roles
	return func(name string) bool {
		return p.personaAllowsTool(ctx, roles, name)
	}
}

// personaAllowsTool is the single persona authorization predicate
// shared by the tools/list visibility middleware
// (addToolVisibilityMiddleware) and platform_find_tools' read-time
// filter. Keeping one implementation prevents the two from drifting:
// if they diverged, find_tools could surface a tool the persona cannot
// call, or hide one it can. Assumes a non-nil authorizer (callers
// guard the no-authorizer case as "allow all", matching the
// middleware, which only wires this predicate when an authorizer
// exists).
func (p *Platform) personaAllowsTool(ctx context.Context, roles []string, toolName string) bool {
	allowed, _, _ := p.authorizer.IsAuthorized(ctx, "", roles, toolName, "")
	return allowed
}

// lexicalFindTools is the fallback ranking: case-insensitive substring
// match of the query against tool name and description, persona-
// filtered and capped. An empty query returns the permitted tools up
// to the limit (a plain catalog listing). Excludes the discovery tool.
func lexicalFindTools(query string, descByName map[string]*mcp.Tool, permit func(string) bool, limit int) []findToolDescriptor {
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]findToolDescriptor, 0, limit)
	names := make([]string, 0, len(descByName))
	for name := range descByName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == platformFindToolsName || !permit(name) {
			continue
		}
		t := descByName[name]
		if q != "" && !strings.Contains(strings.ToLower(name), q) &&
			!strings.Contains(strings.ToLower(t.Description), q) {
			continue
		}
		out = append(out, findToolDescriptor{Name: name, Description: t.Description})
		if len(out) >= limit {
			break
		}
	}
	return out
}

// zeroVector reports whether v is all zeros, the signature of an
// unconfigured/noop embedder; such a vector makes cosine meaningless,
// so the caller falls back to lexical.
func zeroVector(v []float32) bool {
	for _, f := range v {
		if f != 0 {
			return false
		}
	}
	return true
}

// marshalToolResult renders a tool output struct as a JSON text result.
func marshalToolResult(out any) (*mcp.CallToolResult, any, error) {
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		//nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError, not as Go errors
		return toolErrorResult("failed to encode result: " + err.Error()), nil, nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
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
