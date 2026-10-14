//go:build integration

package helpers

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ConnectInMemory wires an in-process MCP client session to the assembled
// server via paired in-memory transports. Calls made on the returned session
// run through the full receiving-middleware chain (auth, authz, audit,
// enrichment) and the real registered tool handlers — the path a deployed
// client takes — so a test can assert end-to-end behavior the
// call-the-middleware-directly pattern cannot. The server session runs in the
// background until the client session is closed.
func ConnectInMemory(ctx context.Context, srv *mcp.Server) (*mcp.ClientSession, error) {
	if srv == nil {
		return nil, fmt.Errorf("nil server")
	}
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, serverT, nil); err != nil {
		return nil, fmt.Errorf("server connect: %w", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e-inproc", Version: "1.0.0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		return nil, fmt.Errorf("client connect: %w", err)
	}
	return cs, nil
}
