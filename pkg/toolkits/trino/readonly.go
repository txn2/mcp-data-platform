// Package trino provides a Trino toolkit adapter for the MCP data platform.
package trino

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"
)

// connectionContextKey types the context value holding the connection a tool
// call resolved to.
type connectionContextKey struct{}

// ReadOnlyInterceptor blocks write operations on read-only connections.
// It delegates write detection to the upstream mcp-trino IsWriteSQL function.
//
// A multi-connection toolkit routes every one of its connections through a
// single interceptor, so the decision is per connection: Before records the
// connection the call named — or the default connection, when it named none,
// matching multiserver.Manager.Client("") — on the context, and Intercept
// rejects write SQL unless that connection is configured write-capable. Such
// an interceptor therefore registers as both a query interceptor and a tool
// middleware; with only the interceptor registered no connection resolves and
// every write is refused, so a wiring mistake closes the door rather than
// opening it.
//
// A nil readOnly map means every connection is read-only unconditionally.
// That is the single-connection case, where the connection argument selects
// nothing, and trino_export, which is SELECT-only whatever the deployment
// configures.
type ReadOnlyInterceptor struct {
	// defaultConn is the connection a call that omits the connection
	// argument resolves to. Immutable after construction.
	defaultConn string

	// mu guards readOnly, which AddConnection/RemoveConnection mutate while
	// tool calls read it.
	mu       sync.RWMutex
	readOnly map[string]bool
}

// NewReadOnlyInterceptor creates a query interceptor that rejects write SQL on
// every connection.
func NewReadOnlyInterceptor() *ReadOnlyInterceptor {
	return &ReadOnlyInterceptor{}
}

// NewConnectionReadOnlyInterceptor creates a query interceptor that rejects
// write SQL only on the connections whose readOnly entry is true. defaultConn
// names the connection a call that omits the connection argument resolves to.
func NewConnectionReadOnlyInterceptor(defaultConn string, readOnly map[string]bool) *ReadOnlyInterceptor {
	// A non-nil map is what separates per-connection enforcement from the
	// unconditional interceptor, so copy into a made map even when empty.
	byConn := make(map[string]bool, len(readOnly))
	maps.Copy(byConn, readOnly)
	return &ReadOnlyInterceptor{defaultConn: defaultConn, readOnly: byConn}
}

// SetConnection records whether a connection added at runtime is read-only.
// A no-op on the unconditional interceptor, where every connection already is.
func (i *ReadOnlyInterceptor) SetConnection(name string, readOnly bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.readOnly == nil {
		return
	}
	i.readOnly[name] = readOnly
}

// ForgetConnection drops a removed connection's read-only setting.
func (i *ReadOnlyInterceptor) ForgetConnection(name string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.readOnly, name)
}

// Before records the connection this tool call is bound for so Intercept can
// apply that connection's read-only setting.
func (i *ReadOnlyInterceptor) Before(ctx context.Context, tc *trinotools.ToolContext) (context.Context, error) {
	conn := extractConnectionFromInput(tc.Input)
	if conn == "" {
		conn = i.defaultConn
	}
	return context.WithValue(ctx, connectionContextKey{}, conn), nil
}

// After is a no-op — enforcement happens in Intercept, before execution.
func (*ReadOnlyInterceptor) After(
	_ context.Context,
	_ *trinotools.ToolContext,
	result *mcp.CallToolResult,
	handlerErr error,
) (*mcp.CallToolResult, error) {
	return result, handlerErr
}

// Intercept checks if the query is a write operation and blocks it when the
// connection it is bound for may not accept writes.
func (i *ReadOnlyInterceptor) Intercept(ctx context.Context, sql string, _ trinotools.ToolName) (string, error) {
	if !trinotools.IsWriteSQL(sql) {
		return sql, nil
	}
	if err := i.checkWritable(ctx); err != nil {
		return "", err
	}
	return sql, nil
}

// checkWritable reports why the connection this call is bound for may not run
// write SQL, or nil when it may. Anything it cannot establish is a refusal:
// an unresolved connection means the middleware half of this interceptor did
// not run, and a name this interceptor holds no setting for is not one the
// toolkit routes to. Read SQL never reaches here, so a mistyped connection on
// a read still gets the manager's own "unknown connection" error.
func (i *ReadOnlyInterceptor) checkWritable(ctx context.Context) error {
	i.mu.RLock()
	defer i.mu.RUnlock()

	if i.readOnly == nil {
		return errors.New("write operations not allowed in read-only mode")
	}
	conn, resolved := ctx.Value(connectionContextKey{}).(string)
	if !resolved {
		return errors.New("write operations not allowed: the target connection could not be resolved")
	}
	readOnly, configured := i.readOnly[conn]
	switch {
	case !configured:
		return fmt.Errorf("write operations not allowed: %q is not a configured Trino connection", conn)
	case readOnly:
		return fmt.Errorf("write operations not allowed: connection %q is read-only", conn)
	}
	return nil
}

// perConnection reports whether this interceptor decides per connection and so
// needs its Before hook registered to resolve one.
func (i *ReadOnlyInterceptor) perConnection() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.readOnly != nil
}

// Verify interface compliance.
var (
	_ trinotools.QueryInterceptor = (*ReadOnlyInterceptor)(nil)
	_ trinotools.ToolMiddleware   = (*ReadOnlyInterceptor)(nil)
)
