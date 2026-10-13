package sqllineage

import (
	"github.com/txn2/mcp-data-platform/pkg/connid"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// ToolkitLister lists the live toolkits.
type ToolkitLister interface {
	All() []registry.Toolkit
}

// TrinoResolver resolves a Trino connection through the live toolkits: its
// instance name through connid, and its default catalog and schema through
// the toolkit that serves it. Toolkits are listed on every call, because
// connections are added and removed while the platform runs.
func TrinoResolver(toolkits ToolkitLister) Resolver {
	return func(bound string) (Connection, bool) {
		if toolkits == nil {
			return Connection{}, false
		}
		all := toolkits.All()
		if bound == "" {
			bound = defaultConnection(all)
		}
		c, ok := connid.NewResolver(all, nil).ByBound(connectionKind, connid.Bound(bound))
		if !ok {
			return Connection{}, false
		}
		conn := Connection{Instance: string(c.Instance)}
		conn.Catalog, conn.Schema = trinoDefaults(all, bound)
		return conn, true
	}
}

// trinoDefaults returns the catalog and schema a connection resolves an
// unqualified name in.
func trinoDefaults(all []registry.Toolkit, bound string) (catalogName, schema string) {
	for _, tk := range all {
		t, ok := tk.(*trinokit.Toolkit)
		if !ok {
			continue
		}
		if m := t.Manager(); m != nil {
			for _, info := range m.ConnectionInfos() {
				if info.Name == bound {
					return info.Catalog, info.Schema
				}
			}
		}
	}
	return "", ""
}

// defaultConnection is the connection a call that names none runs on.
func defaultConnection(all []registry.Toolkit) string {
	for _, tk := range all {
		t, ok := tk.(*trinokit.Toolkit)
		if !ok {
			continue
		}
		if m := t.Manager(); m != nil {
			for _, info := range m.ConnectionInfos() {
				if info.IsDefault {
					return info.Name
				}
			}
		}
	}
	return ""
}
