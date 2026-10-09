// Package inprocess marks the MCP sessions the platform opens to its own
// server over an in-memory transport: a managed-script run's session, the REST
// gateway shim's, the admin console's tool runs, and the tool listings read
// for the inventory and the tools index.
//
// A shutdown closes every live MCP session after a short settle so connected
// agents reconnect to the new build (#675). An in-process session has no
// client to reconnect: closing it ends the work it is carrying, and a script
// run's next tools/call then reads EOF and the run is recorded as failed
// (#2058). The drain skips the sessions marked here; each ends when the work
// that opened it does, and a script run is released by the worker's stop.
package inprocess

import (
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var sessions sync.Map // *mcp.ServerSession -> struct{}

// Track marks ss as in-process until the returned func is called, which the
// caller does when it closes the session. Nil-safe.
func Track(ss *mcp.ServerSession) (untrack func()) {
	if ss == nil {
		return func() {}
	}
	sessions.Store(ss, struct{}{})
	return func() { sessions.Delete(ss) }
}

// Is reports whether ss was opened in-process and is still tracked.
func Is(ss *mcp.ServerSession) bool {
	_, ok := sessions.Load(ss)
	return ok
}
