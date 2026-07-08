package platform

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// platformFindToolsName is the discovery tool's own name. It is
// excluded from the index so a find-tools query never ranks the
// discovery tool itself, and is passed to the index queue as the
// tool corpus's excluded name.
const platformFindToolsName = "platform_find_tools"

// platformToolEnumerator adapts Platform's unexported enumerateGlobalTools to
// the indexqueue.ToolEnumerator interface, supplying the queue's tools source
// with the live in-process tool corpus.
type platformToolEnumerator struct{ p *Platform }

// EnumerateGlobalTools satisfies indexqueue.ToolEnumerator by delegating to the
// platform's in-process tool enumeration.
func (e platformToolEnumerator) EnumerateGlobalTools(ctx context.Context) ([]*mcp.Tool, error) {
	return e.p.enumerateGlobalTools(ctx)
}

// enumerateGlobalTools returns the globally-visible tool descriptors by
// running tools/list over an unauthenticated in-memory session. With no
// caller identity the visibility middleware resolves no roles, so it
// applies only the global allow/deny patterns and skips persona
// filtering (see pkg/middleware/mcp_visibility.go filterTools): the
// result is the persona-neutral corpus to embed once. Descriptions are
// post-override (the description middleware runs in the same chain).
func (p *Platform) enumerateGlobalTools(ctx context.Context) ([]*mcp.Tool, error) {
	if p.mcpServer == nil {
		return nil, errors.New("tools index: mcp server not initialized")
	}
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := p.mcpServer.Connect(ctx, t1, nil)
	if err != nil {
		return nil, fmt.Errorf("tools index: server connect: %w", err)
	}
	defer func() { _ = serverSession.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "tools-index-internal", Version: "v1"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		return nil, fmt.Errorf("tools index: client connect: %w", err)
	}
	defer func() { _ = cs.Close() }()

	var out []*mcp.Tool
	params := &mcp.ListToolsParams{}
	for {
		res, err := cs.ListTools(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("tools index: list tools: %w", err)
		}
		out = append(out, res.Tools...)
		if res.NextCursor == "" {
			break
		}
		params.Cursor = res.NextCursor
	}
	return out, nil
}
