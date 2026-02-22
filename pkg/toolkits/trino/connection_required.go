package trino

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"
)

// ConnectionDescription holds display information about a connection
// for error messages when the connection parameter is missing.
type ConnectionDescription struct {
	Name        string
	Description string
	IsDefault   bool
}

// ConnectionRequiredMiddleware rejects tool calls that omit the connection
// parameter when multiple Trino connections are configured. The error message
// lists all available connections with their descriptions so the LLM can
// choose the correct one.
type ConnectionRequiredMiddleware struct {
	connections []ConnectionDescription
}

// NewConnectionRequiredMiddleware creates a middleware that enforces explicit
// connection selection. The connections slice describes all available backends.
func NewConnectionRequiredMiddleware(connections []ConnectionDescription) *ConnectionRequiredMiddleware {
	// Sort by name for deterministic error messages.
	sorted := make([]ConnectionDescription, len(connections))
	copy(sorted, connections)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	return &ConnectionRequiredMiddleware{connections: sorted}
}

// Before checks that the connection parameter is set for tools that need it.
func (m *ConnectionRequiredMiddleware) Before(ctx context.Context, tc *trinotools.ToolContext) (context.Context, error) {
	// list_connections doesn't need a connection parameter.
	if tc.Name == trinotools.ToolListConnections {
		return ctx, nil
	}

	conn := extractConnectionFromInput(tc.Input)
	if conn != "" {
		return ctx, nil
	}

	return ctx, fmt.Errorf("multiple Trino connections are configured — you must specify the 'connection' parameter.\n\n%s",
		m.formatAvailableConnections())
}

// After is a no-op — validation happens before execution.
func (*ConnectionRequiredMiddleware) After(
	_ context.Context,
	_ *trinotools.ToolContext,
	result *mcp.CallToolResult,
	handlerErr error,
) (*mcp.CallToolResult, error) {
	return result, handlerErr
}

// extractConnectionFromInput extracts the Connection field from a tool input
// struct using reflection. All Trino tool inputs (except ListConnectionsInput)
// have a Connection string field.
func extractConnectionFromInput(input any) string {
	if input == nil {
		return ""
	}
	v := reflect.ValueOf(input)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	f := v.FieldByName("Connection")
	if !f.IsValid() || f.Kind() != reflect.String {
		return ""
	}
	return f.String()
}

// formatAvailableConnections builds a human-readable list of available connections.
func (m *ConnectionRequiredMiddleware) formatAvailableConnections() string {
	var b strings.Builder
	b.WriteString("Available connections:\n")
	for _, c := range m.connections {
		fmt.Fprintf(&b, "  - %s", c.Name)
		if c.IsDefault {
			b.WriteString(" (default)")
		}
		if c.Description != "" {
			fmt.Fprintf(&b, ": %s", c.Description)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
