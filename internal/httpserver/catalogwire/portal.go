package catalogwire

import (
	"context"
	"net/http"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/httpserver/catalogapi"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/connid"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// PortalRegistrar returns the route registrar for the portal catalog (#1921).
func PortalRegistrar(p *platform.Platform, store catalog.Store, resolver portal.PersonaResolver, isAdmin func([]string) bool) func(*http.ServeMux) {
	var auditLogger audit.Logger
	if s := p.Audit().Store(); s != nil {
		auditLogger = s
	}
	docs, _ := store.(catalogapi.Store)
	handler := catalogapi.New(catalogapi.Deps{
		Store:           docs,
		Connections:     func() []catalogapi.Connection { return Connections(p.ToolkitRegistry()) },
		PersonaResolver: resolver,
		IsAdmin:         isAdmin,
		Audit:           auditLogger,
	})
	return handler.Register
}

// catalogKinds are the connection kinds the catalog scan reads datasets from.
var catalogKinds = []string{"trino", "api", "graphql"}

// Connections lists the live connections the catalog holds datasets
// for, by kind and instance, the names a catalog reference carries.
func Connections(reg *registry.Registry) []catalogapi.Connection {
	out := make([]catalogapi.Connection, 0)
	if reg == nil {
		return out
	}
	for _, c := range connid.NewResolver(reg.All(), nil).All("") {
		if c.Live && slices.Contains(catalogKinds, c.Kind) {
			out = append(out, catalogapi.Connection{Kind: c.Kind, Name: string(c.Instance)})
		}
	}
	return out
}

// Labeler names the catalog definitions a knowledge page cites.
type Labeler struct {
	Store catalog.Store
}

// Labels reads the name of each definition reference; a reference that names
// no definition, or is not one, is left out.
func (l Labeler) Labels(ctx context.Context, refs []string) map[string]string {
	out := make(map[string]string, len(refs))
	for _, s := range refs {
		ref, err := catalog.ParseRef(s)
		if err != nil {
			continue
		}
		if def, err := l.Store.GetDefinition(ctx, ref); err == nil {
			out[s] = def.Name
		}
	}
	return out
}
