// Package connreach answers, in the deployment's own terms, which connections
// one caller's authority reaches.
//
// It is the enumeration a picker is filled from and the enumeration an automatic
// approval is checked against, and it is one implementation because those two
// have to agree: a form offering a connection the run then refuses, or an
// approval refusing a connection the picker offered, are the same defect read
// from opposite ends.
//
// The persona predicate is connscope's, which delegates to the same
// persona.ToolFilter rule the authorizer applies to a tool call, so nothing here
// re-derives what a persona may reach.
package connreach

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/platform/connscope"
	"github.com/txn2/mcp-data-platform/pkg/connview"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// Connection is one connection a caller reaches: the value a call binds, and
// what a person needs to pick it by.
type Connection struct {
	// Name is the name a persona's rules match and a tool call's connection
	// argument carries — not always the name the enumeration leads with, since a
	// single-connection toolkit's entry carries its INSTANCE name.
	Name        string
	Kind        string
	Description string
}

// Deps are the live registries a listing reads. They are held rather than
// snapshotted, so a connection added or a persona edited through the admin API
// takes effect on the next call.
type Deps struct {
	// Toolkits is the live toolkit registry. Nil yields a nil Lister, which
	// answers nothing rather than answering "reaches no connection".
	Toolkits *registry.Registry
	// Personas resolves a persona's connection rules. Nil denies every named
	// connection, matching the fail-closed action path.
	Personas *persona.Registry
}

// Lister enumerates connections for one caller at a time.
type Lister struct {
	toolkits *registry.Registry
	personas *persona.Registry
	scope    *connscope.Scope
}

// New builds a Lister, or nil when there is no toolkit registry to read.
//
// Nil is meaningful: a deployment that cannot enumerate its connections should
// serve no set at all rather than an empty one, which a form renders as "this
// reaches nothing" and an approval would read as a refusal.
func New(deps Deps) *Lister {
	if deps.Toolkits == nil {
		return nil
	}
	return &Lister{
		toolkits: deps.Toolkits,
		personas: deps.Personas,
		scope:    connscope.New(connscope.Deps{Registry: deps.Personas}),
	}
}

// ForPersona lists what one resolved persona reaches. unrestricted lifts the
// persona boundary for an administrator, whose reach is unrestricted by design.
func (l *Lister) ForPersona(ctx context.Context, personaName string, unrestricted bool) []Connection {
	if l == nil {
		return nil
	}
	var permit connview.Permit
	if !unrestricted {
		permit = func(_, name string) bool { return l.scope.AllowConnection(personaName, name) }
	}
	// The knowledge-page enrichment is deliberately not asked for: a picker needs
	// a name and a sentence, and one reverse lookup per connection is a cost the
	// list_connections tool pays for a different purpose.
	out := connview.Build(ctx, l.toolkits.All(), nil, nil, permit)
	conns := make([]Connection, 0, len(out.Connections))
	for _, c := range out.Connections {
		conns = append(conns, Connection{Name: value(c), Kind: c.Kind, Description: c.Description})
	}
	return conns
}

// value is the name a call actually binds, which is not always the name the
// enumeration leads with: a single-connection toolkit's entry carries its
// INSTANCE name in Name and its connection name in Connection, and the
// connection name is what a persona's rules match and what a script's grant
// lists. Offering the instance name would produce a picker whose every value the
// run then refuses.
func value(entry connview.Entry) string {
	if entry.Connection != "" {
		return entry.Connection
	}
	return entry.Name
}
