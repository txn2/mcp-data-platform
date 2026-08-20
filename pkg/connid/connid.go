// Package connid owns connection identity: the several names one connection
// carries, which surface keys on which, and which half of the configuration
// owns it.
//
// It is separate from pkg/connview, which builds the list_connections view,
// because the two answer different questions — that one presents connections to
// a caller, this one decides which connection a name means. Every defect in
// this area has been a call site holding one name and using it where another
// belongs, so the names are distinct types and Resolver is the only translation
// between them.
package connid

import (
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// A connection carries more than one name and they are not interchangeable.
//
// Instance is what the connection is configured and stored under: the
// toolkits.<kind>.instances key the file writes, the connection_instances row
// name, and the target of an mcp:connection:(kind,name) reference.
//
// Bound is what a call binds it by: the value a tool call's "connection"
// argument carries, the name a persona's connections rules match, and the key
// the connection source map answers to.
//
// These two are equal for a multi-connection toolkit, which routes on the
// instance key, and for a single-connection toolkit that sets no
// connection_name. They differ for one that does. Every defect in this area has
// been a call site holding one and using it where the other belongs, so they
// are distinct types: a crossing has to go through Resolver, where it is
// written down once, rather than through whichever helper was nearest.
//
// Connection.Toolkit is a third name, described on the field.
type (
	// Instance is the name a connection is configured and stored under.
	Instance string
	// Bound is the name a call binds a connection by.
	Bound string
)

// Connection is one connection's identity: both of its names, its kind, and
// which half of the configuration owns it. Resolver is the only thing that
// builds one, so the pairing is established in a single place.
type Connection struct {
	Kind     string
	Instance Instance
	Bound    Bound
	// FileDeclared marks a connection the platform configuration file declares.
	// The file is the only place it can be changed or removed: a stored record
	// for one reaches the running process but is discarded at the next restart,
	// and deleting that record drops the connection from every live toolkit
	// until a restart puts it back. False means the connection store
	// contributed it, which is also what a resolver with no Declarer reports.
	FileDeclared bool
	// Toolkit is the name of the registered toolkit serving this connection,
	// which is a third identity and not interchangeable with either of the
	// other two. For a single-connection toolkit it equals Instance. For a
	// multi-connection toolkit it is the aggregate toolkit's own name while
	// each Instance is one of its connections, so it names no connection at
	// all — yet the connection source map holds entries under it, seeded from
	// per-kind configuration, and a connection with no entry of its own falls
	// back to it. It is a plain string because nothing translates to or from
	// it; it is only ever compared with itself.
	Toolkit string
	// Live reports whether a registered toolkit currently serves this
	// connection. A stored record whose kind is disabled, or whose toolkit
	// failed to build, resolves with Live false rather than being absent:
	// callers that must not invent a mapping for a connection nothing serves
	// check this instead of re-deriving it from the registry.
	Live bool
}

// IsFile reports whether the configuration file declares this connection.
func (c Connection) IsFile() bool { return c.FileDeclared }

// Declarer reports whether the platform configuration file declares an instance
// of a kind. *platform.Config satisfies it. It is an interface so connview does
// not import the platform package, which imports connview.
//
// A nil Declarer declares nothing, so every connection resolves as the store's.
type Declarer interface {
	DeclaresConnection(kind, instance string) bool
}

// Resolver answers connection-identity questions against a fixed view of the
// live toolkits: which name a call binds an instance by, which instance a bound
// name belongs to, and who owns either.
//
// It holds no mutable state and is cheap to construct, so callers build one per
// operation from the registry rather than caching a view that a hot-added
// connection would make stale.
type Resolver struct {
	toolkits []registry.Toolkit
	declared Declarer
}

// NewResolver returns a Resolver over the given toolkits. Both arguments may be
// nil: no toolkits resolves every connection to itself and not live, and no
// Declarer resolves every connection as the store's.
func NewResolver(toolkits []registry.Toolkit, declared Declarer) *Resolver {
	return &Resolver{toolkits: toolkits, declared: declared}
}

// All enumerates every connection the registered toolkits of a kind serve. An
// empty kind enumerates every kind.
func (r *Resolver) All(kind string) []Connection {
	var out []Connection
	for _, tk := range r.toolkits {
		if kind != "" && tk.Kind() != kind {
			continue
		}
		out = append(out, r.fromToolkit(tk)...)
	}
	return out
}

// fromToolkit reads the connections one toolkit serves. A multi-connection
// toolkit routes on the instance key, so both names are the connection's own; a
// single-connection toolkit is its connection, and its bound name is the
// connection_name it carries or its instance name when it carries none.
func (r *Resolver) fromToolkit(tk registry.Toolkit) []Connection {
	if lister, ok := tk.(toolkit.ConnectionLister); ok {
		conns := lister.ListConnections()
		out := make([]Connection, 0, len(conns))
		for _, c := range conns {
			out = append(out, r.connection(tk.Kind(), Instance(c.Name), Bound(c.Name), tk.Name(), true))
		}
		return out
	}
	return []Connection{
		r.connection(tk.Kind(), Instance(tk.Name()), Bound(policyName(tk)), tk.Name(), true),
	}
}

// connection stamps ownership onto a resolved set of names.
func (r *Resolver) connection(kind string, inst Instance, bound Bound, tkName string, live bool) Connection {
	return Connection{
		Kind: kind, Instance: inst, Bound: bound, Toolkit: tkName, Live: live,
		FileDeclared: r.declared != nil && r.declared.DeclaresConnection(kind, string(inst)),
	}
}

// ByInstance resolves the connection a stored record or an instances key names.
//
// An instance no live toolkit claims resolves to itself with Live false rather
// than to nothing: the caller still needs to know who owns it, and a record for
// a disabled kind still has to answer under some name. Its own name is also the
// only safe fallback, because inventing a different bound name for a connection
// nothing serves would file it where no lookup will ever arrive. Toolkit is
// that same name, which is what it would be if a single-connection toolkit for
// the instance were registered; nothing reads it while Live is false.
func (r *Resolver) ByInstance(kind string, inst Instance) Connection {
	for _, c := range r.All(kind) {
		if c.Instance == inst {
			return c
		}
	}
	return r.connection(kind, inst, Bound(inst), string(inst), false)
}

// ByBound resolves the connection a tool call's argument, a persona rule, or a
// source-map lookup names. The second result is false when no live toolkit
// serves that name, which is the case a caller must not paper over by assuming
// the bound name is also an instance.
func (r *Resolver) ByBound(kind string, bound Bound) (Connection, bool) {
	for _, c := range r.All(kind) {
		if c.Bound == bound {
			return c, true
		}
	}
	return Connection{}, false
}

// policyName is the identity a persona's connection rules match on for a
// single-connection toolkit: its configured connection name, or its instance
// name when it carries none.
func policyName(tk registry.Toolkit) string {
	if conn := tk.Connection(); conn != "" {
		return conn
	}
	return tk.Name()
}
