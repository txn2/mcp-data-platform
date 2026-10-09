// Package shutdown is what a shutdown does with the work still in flight
// (#2058): the drains components register to stop taking new work and finish
// what they hold, run in the background from the signal beside the HTTP
// drain, and the close of live MCP sessions that sends connected agents to the
// new build while leaving the in-process sessions to the work they carry.
package shutdown

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/inprocess"
)

// Drains is the set of drains a lifecycle runs before it stops its
// components: each stops taking new work and finishes what it holds, bounded
// by the context it is given. A managed-script run claimed seconds before a
// SIGTERM finishes in its drain instead of being cut off; what a drain does
// not finish is the component's stop to release. The zero value is ready.
type Drains struct {
	mu     sync.Mutex
	drains []func(context.Context) error
}

// Add registers a drain. Nil is ignored.
func (d *Drains) Add(drain func(context.Context) error) {
	if drain == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.drains = append(d.drains, drain)
}

// Run runs every drain concurrently and returns when all have, which each does
// by ctx's deadline at the latest. Their errors are joined.
func (d *Drains) Run(ctx context.Context) error {
	d.mu.Lock()
	drains := append([]func(context.Context) error(nil), d.drains...)
	d.mu.Unlock()
	errs := make([]error, len(drains))
	var wg sync.WaitGroup
	for i, drain := range drains {
		wg.Go(func() { errs[i] = drain(ctx) })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("shutdown drains: %w", err)
	}
	return nil
}

// InBackground starts drain under a budget of its own and returns a
// channel closed when it has returned. A nil drain is closed at once.
func InBackground(drain func(context.Context) error, budget time.Duration) <-chan struct{} {
	done := make(chan struct{})
	if drain == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()
		slog.InfoContext(ctx, "shutdown: draining background work", "budget", budget)
		if err := drain(ctx); err != nil {
			slog.WarnContext(ctx, "shutdown: background drain error", "error", err)
		}
	}()
	return done
}

// CloseSessions closes every live MCP session so connected clients drop their
// stale connection and reconnect to the new build (#675). Claude Code auto-reconnects
// HTTP/SSE servers and re-handshakes (fresh tools/list); Claude Desktop requires an
// app restart.
//
// ServerSession.Close is graceful: an idle session's long-lived SSE/streamable stream
// drops immediately, but a session with an in-flight tool call blocks until that call
// returns, and that wait is not bounded by the HTTP grace period. So the closes run in
// a goroutine bounded by ctx (the shutdown deadline): if they do not all finish in
// time, we return and let process exit drop the remaining connections rather than hang
// past terminationGracePeriodSeconds and risk a SIGKILL mid-call.
func CloseSessions(ctx context.Context, mcpServer *mcp.Server) {
	if mcpServer == nil {
		return
	}
	var sessions []*mcp.ServerSession
	for s := range mcpServer.Sessions() {
		// An in-process session has no client to reconnect, and closing it
		// would end the work it carries: a script run's next tools/call
		// would read EOF (#2058).
		if inprocess.Is(s) {
			continue
		}
		sessions = append(sessions, s)
	}
	if len(sessions) == 0 {
		return
	}

	closed := make(chan struct{})
	go func() {
		for _, s := range sessions {
			_ = s.Close() // returns once the session's in-flight requests finish
		}
		close(closed)
	}()

	select {
	case <-closed:
		slog.InfoContext(ctx, "shutdown: closed live MCP sessions so clients reconnect to the new build", "count", len(sessions))
	case <-ctx.Done():
		slog.WarnContext(ctx, "shutdown: MCP session close did not finish before the grace deadline; process exit will drop remaining connections", "count", len(sessions))
	}
}
