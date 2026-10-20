// Package trinowiring attaches the platform's dependencies to the live Trino
// toolkits, beside apigwwiring and graphqlwiring for their kinds.
package trinowiring

import (
	"github.com/txn2/mcp-data-platform/pkg/registry"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// ToolkitSource is the registry the live toolkits are read from.
type ToolkitSource interface {
	All() []registry.Toolkit
}

// Toolkits returns the live Trino toolkits in a registry.
func Toolkits(source ToolkitSource) []*trinokit.Toolkit {
	if source == nil {
		return nil
	}
	var out []*trinokit.Toolkit
	for _, tk := range source.All() {
		if trino, ok := tk.(*trinokit.Toolkit); ok {
			out = append(out, trino)
		}
	}
	return out
}

// First returns the first live Trino toolkit, or nil when none is registered.
func First(source ToolkitSource) *trinokit.Toolkit {
	if tks := Toolkits(source); len(tks) > 0 {
		return tks[0]
	}
	return nil
}

// ConnectionStore attaches the store a Trino toolkit answers a request for a
// connection it does not serve from, so a connection saved on another replica
// has its scratch target and write setting here from the moment that save
// returns rather than when its announcement arrives (#2218). A nil store, a
// deployment whose connections do not outlive the process, attaches nothing.
func ConnectionStore(source ToolkitSource, store trinokit.ConnectionStore) {
	if store == nil {
		return
	}
	for _, trino := range Toolkits(source) {
		trino.SetConnectionStore(store)
	}
}
