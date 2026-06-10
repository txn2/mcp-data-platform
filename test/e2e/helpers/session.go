//go:build integration

package helpers

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"time"

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

// DataHubReachable reports whether the configured DataHub endpoint accepts a TCP
// connection within a short timeout. IsDataHubAvailable only checks that a URL
// is configured (it has a default, so it is always true); this probes the real
// service, so an enrichment assertion can skip cleanly when DataHub is not up
// rather than fail. The Trino-only assertions need no DataHub and do not gate on
// this.
func DataHubReachable(cfg *E2EConfig) bool {
	if cfg == nil || cfg.DataHubURL == "" {
		return false
	}
	u, err := url.Parse(cfg.DataHubURL)
	if err != nil {
		return false
	}
	host := u.Host
	if host == "" {
		host = cfg.DataHubURL
	}
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "8080")
	}
	conn, err := net.DialTimeout("tcp", host, 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
