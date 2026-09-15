package platform

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/cfgmap"
	"github.com/txn2/mcp-data-platform/internal/platform/connstored"
	"github.com/txn2/mcp-data-platform/pkg/connview"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// connectionEntry and listConnectionsOutput are the list_connections view types,
// owned by pkg/connview (kept out of pkg/platform for the size budget). The aliases
// preserve the existing platform-internal names and JSON shape.
type connectionEntry = connview.Entry

type listConnectionsOutput = connview.Output

// listConnectionsInput is empty since this tool has no parameters.
type listConnectionsInput struct{}

// registerConnectionsTool registers the list_connections tool with the MCP server.
func (p *Platform) registerConnectionsTool() {
	mcp.AddTool(p.mcpServer, &mcp.Tool{
		Name:  toolListConns,
		Title: "List Connections",
		Description: "List all configured data connections across toolkits (Trino, DataHub, S3, etc.). " +
			"Each connection includes a count and a bounded sample of the canonical knowledge pages that document it.",
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
		OutputSchema: connectionsOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ listConnectionsInput) (*mcp.CallToolResult, any, error) {
		return p.handleListConnections(ctx, req)
	})
}

// handleListConnections handles the list_connections tool call, delegating the view
// build (and the knowledge-page reverse-lookup enrichment) to pkg/connview.
//
// The enumeration is narrowed to the connections the caller's persona is granted
// (#1108) by the same predicate search and fetch apply, and reports how many it
// withheld: an operator who grants one connection should not have the tool hand
// back the inventory of the rest.
func (p *Platform) handleListConnections(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, any, error) {
	var src connview.SourceResolver
	if p.connectionSources != nil {
		src = p.connectionSources
	}
	var pages connview.PageLookup
	if kp := p.portalStore.KnowledgePageStore(); kp != nil {
		pages = kp
	}

	personaName := ""
	if pc := middleware.GetPlatformContext(ctx); pc != nil {
		personaName = pc.PersonaName
	}
	var permit connview.Permit
	if scope := connectionScopeFor(p.personaRegistry, p.connectionSources, p.toolkitRegistry); scope != nil {
		permit = func(_, name string) bool { return scope.AllowConnection(personaName, name) }
	}

	out := connview.Build(ctx, p.toolkitRegistry.All(), connview.Deps{
		Source: src, Pages: pages, Permit: permit, Stored: StoredConnections(p),
	})
	out.Notice = knowledge.ConnectionsWithheldNotice(out.Withheld, personaName)

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "Error: " + err.Error()},
			},
			IsError: true,
		}, nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(data)},
		},
		StructuredContent: out,
	}, nil, nil
}

// StoredConnections is the deployment's connection inventory: the rows that
// say which connections exist, which every replica of a deployment shares.
// Nil where connections are kept in the configuration file alone, in which
// case what this process was handed is the whole inventory (#1757).
//
// It is a function rather than a method because *Platform is at its
// god-object ceiling: this reads the platform's stores and adds no behavior
// to the type.
func StoredConnections(p *Platform) connview.StoreLister {
	return connstored.New(p.connectionStore, p.APIGatewayCatalogStore,
		func(inst ConnectionInstance) connstored.Row {
			return connstored.Row{
				Kind: inst.Kind, Name: inst.Name, Description: inst.Description,
				CatalogID: cfgmap.String(inst.Config, "catalog_id"),
			}
		})
}
