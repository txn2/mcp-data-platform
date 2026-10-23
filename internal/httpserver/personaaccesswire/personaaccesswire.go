// Package personaaccesswire assembles the persona effective-access service
// (#2257) from the platform: the live toolkits and connections, the scratch
// catalogs the Trino toolkit carries, the API operations the gateway's
// catalogs declare, and the stores the references are read from.
//
// It is a package of its own rather than a function in the composition root
// because that root is at its size budget.
package personaaccesswire

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/admin/personaaccess"
	"github.com/txn2/mcp-data-platform/internal/notification/notifychannel"
	"github.com/txn2/mcp-data-platform/internal/platform/connreach"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// kindTrino is the toolkit whose connections carry a scratch catalog.
const kindTrino = "trino"

// Service builds the service for p.
func Service(p *platform.Platform) *personaaccess.Service {
	cfg := p.Config()
	keys := make([][]string, 0, len(cfg.Auth.APIKeys.Keys))
	for _, k := range cfg.Auth.APIKeys.Keys {
		keys = append(keys, k.Roles)
	}
	tools := func() []string {
		return platform.RegisteredToolNames(p.ToolkitRegistry().AllTools(), p.PlatformTools())
	}
	return personaaccess.New(personaaccess.Config{
		Personas: p.PersonaRegistry(), AdminPersona: cfg.Admin.Persona,
		RoleMapping:       cfg.Personas.RoleMapping.OIDCToPersona,
		ExcludedFromCalls: cfg.Calls.ExcludePersonas,
		Inventory: inventory(p.ToolkitRegistry(), tools, p.DB(),
			connreach.New(connreach.Deps{Toolkits: p.ToolkitRegistry(), Personas: p.PersonaRegistry(), Stored: platform.StoredConnections(p)})),
		DB: p.DB(), FileKeyRoles: keys,
	})
}

// Mount mounts the route behind wrap, the admin API's authentication.
func Mount(mux *http.ServeMux, p *platform.Platform, wrap func(http.Handler) http.Handler) {
	if p.PersonaRegistry() == nil || p.ToolkitRegistry() == nil {
		return
	}
	personaaccess.Register(mux, wrap, Service(p))
}

// lister enumerates every connection, unrestricted.
type lister interface {
	ForPersona(ctx context.Context, personaName string, unrestricted bool) []connreach.Connection
}

// inventory reads what the deployment serves from the toolkits: every
// registered tool with its kind, every connection with the scratch catalog a
// Trino one carries, the operations each api connection's catalog declares,
// and the registrations on each connection.
func inventory(reg *registry.Registry, tools func() []string, db *sql.DB, conns lister) func(context.Context) personaaccess.Inventory {
	return func(ctx context.Context) personaaccess.Inventory {
		inv := personaaccess.Inventory{Registrations: personaaccess.Registrations(ctx, db)}
		for _, name := range tools() {
			inv.Tools = append(inv.Tools, toolOf(reg, name))
		}
		channels := channelsByConnection(ctx, db)
		exec := scratchExecutor(reg)
		for _, c := range conns.ForPersona(ctx, "", true) {
			conn := personaaccess.Connection{Name: c.Name, Kind: c.Kind, Channels: channels[c.Name]}
			if c.Kind == kindTrino {
				conn.Scratch = scratchOf(exec, c.Name)
			}
			inv.Connections = append(inv.Connections, conn)
		}
		inv.Operations = operations(reg)
		return inv
	}
}

// toolOf is a tool with the toolkit kind and connection that serve it.
func toolOf(reg *registry.Registry, name string) personaaccess.Tool {
	t := personaaccess.Tool{Name: name}
	if m := reg.GetToolkitForTool(name); m.Found {
		t.Kind = m.Kind
		if m.ConnectionResolved {
			t.Connection = m.Connection
		}
	}
	return t
}

// scratchOf is a Trino connection's scratch catalog, or nil when it has none.
func scratchOf(exec tableregister.Executor, name string) *personaaccess.Scratch {
	if exec == nil {
		return nil
	}
	target, ok := exec.ScratchTarget(name)
	if !ok || !target.Configured() {
		return nil
	}
	return &personaaccess.Scratch{Catalog: target.Catalog, Schema: target.Schema, Writes: exec.AcceptsWrites(name)}
}

// channelsByConnection names the notification channels that deliver through
// each connection, whose persona authorization is the channel's; none without
// a database or when the channels cannot be read.
func channelsByConnection(ctx context.Context, db *sql.DB) map[string][]string {
	out := map[string][]string{}
	if db == nil {
		return out
	}
	list, err := notifychannel.NewPostgresStore(db).List(ctx)
	if err != nil {
		return out
	}
	for _, ch := range list {
		if ch.Connection != "" {
			out[ch.Connection] = append(out[ch.Connection], ch.Name)
		}
	}
	return out
}

// scratchExecutor is the Trino toolkit, which carries each connection's
// scratch catalog; nil without one.
func scratchExecutor(reg *registry.Registry) tableregister.Executor {
	for _, tk := range reg.GetByKind(kindTrino) {
		if exec, ok := tk.(tableregister.Executor); ok {
			return exec
		}
	}
	return nil
}

// operations are the operations every api connection's catalog declares.
func operations(reg *registry.Registry) []personaaccess.Operation {
	var out []personaaccess.Operation
	for _, tk := range reg.All() {
		api, ok := tk.(*apigatewaykit.Toolkit)
		if !ok {
			continue
		}
		for _, c := range api.CatalogConnections() {
			for _, op := range c.Operations {
				out = append(out, personaaccess.Operation{Connection: c.Name, Method: op.Method, Path: op.Path})
			}
		}
	}
	return out
}
