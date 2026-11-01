package schemagate

import (
	"context"
	"errors"
	"fmt"

	trinoclient "github.com/txn2/mcp-trino/pkg/client"
	"github.com/txn2/mcp-trino/pkg/multiserver"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// ToolkitLister lists the live toolkits.
type ToolkitLister interface {
	All() []registry.Toolkit
}

// errNoConnection is a connection no live Trino toolkit serves.
var errNoConnection = errors.New("no trino connection by that name")

// TrinoDescriber reads schemas through the live Trino toolkits: the connection
// a statement runs on, under the same engine user the statement itself runs
// as. Toolkits are listed on every call, because connections are added and
// removed while the platform runs.
type TrinoDescriber struct {
	Toolkits ToolkitLister
}

// Namespace resolves a connection name, empty for the default connection, to
// its name and default catalog and schema. A Trino connection's name is both
// the name a call binds and the instance name a catalog reference carries
// (connid reads a connection lister's name as both), so a fetch of
// mcp:dataset:(trino,<name>,...) and a statement on <name> meet on one key.
func (d TrinoDescriber) Namespace(connection string) (Namespace, bool) {
	_, info, ok := d.find(connection)
	if !ok {
		return Namespace{}, false
	}
	return Namespace{Connection: info.Name, Catalog: info.Catalog, Schema: info.Schema}, true
}

// Describe returns a table's columns on a connection.
func (d TrinoDescriber) Describe(ctx context.Context, connection, catalog, schema, table string) ([]Column, error) {
	m, info, ok := d.find(connection)
	if !ok {
		return nil, errNoConnection
	}
	client, err := m.Client(info.Name)
	if err != nil {
		return nil, fmt.Errorf("resolving trino connection: %w", err)
	}
	ti, err := client.DescribeTable(ctx, catalog, schema, table)
	if err != nil {
		return nil, fmt.Errorf("describing %s.%s.%s: %w", catalog, schema, table, err)
	}
	return columnsOf(ti.Columns), nil
}

// columnsOf is the engine's column list as the gate's columns.
func columnsOf(defs []trinoclient.ColumnDef) []Column {
	cols := make([]Column, 0, len(defs))
	for _, c := range defs {
		cols = append(cols, Column{Name: c.Name, Type: c.Type, Comment: c.Comment})
	}
	return cols
}

// find returns the manager serving a connection and the connection's details.
// An empty name is the default connection.
func (d TrinoDescriber) find(connection string) (*multiserver.Manager, multiserver.ConnectionInfo, bool) {
	if d.Toolkits == nil {
		return nil, multiserver.ConnectionInfo{}, false
	}
	for _, tk := range d.Toolkits.All() {
		t, ok := tk.(*trinokit.Toolkit)
		if !ok || t.Manager() == nil {
			continue
		}
		for _, info := range t.Manager().ConnectionInfos() {
			if (connection == "" && info.IsDefault) || (connection != "" && info.Name == connection) {
				return t.Manager(), info, true
			}
		}
	}
	return nil, multiserver.ConnectionInfo{}, false
}
