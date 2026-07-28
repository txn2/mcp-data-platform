// Package connscope answers, for discovery surfaces, the question the authorizer
// answers for tool calls: may this caller's persona reach this connection?
//
// A persona's connections.allow list is deny-by-default and was enforced only
// when a caller acted, so a persona restricted to one connection still saw every
// dataset, connection, and API endpoint on the platform through search and
// list_connections (#1108). This package is the shared predicate both surfaces
// now consult. It delegates to persona.ToolFilter.IsConnectionAllowed rather
// than reimplementing the glob rules, so discovery and authorization cannot
// drift, and it resolves a caller's persona the same way the argument-completion
// gate does (by name, since every surface that reaches discovery carries a
// resolved persona).
//
// It satisfies knowledge.ConnectionScope and pkg/connview's permit predicate;
// the platform composition root is the compile-time proof, so this package
// imports neither.
package connscope

import (
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// Deps are the inputs a Scope needs.
type Deps struct {
	// Registry resolves a persona name to its rules. Held live (not snapshotted)
	// so a persona edited through the admin API takes effect on the next lookup.
	// A nil registry denies every named connection, matching the fail-closed
	// action path.
	Registry *persona.Registry

	// URNConnections maps a catalog URN to the names of the connections that
	// could serve it. Nil (or an empty result) leaves a URN unattributable, which
	// keeps the entity visible rather than hiding it on a guess.
	URNConnections func(urn string) []string
}

// Scope is the persona connection boundary as discovery sees it.
type Scope struct {
	registry *persona.Registry
	filter   *persona.ToolFilter
	urnConns func(urn string) []string
}

// New builds a Scope from deps.
func New(deps Deps) *Scope {
	return &Scope{
		registry: deps.Registry,
		filter:   persona.NewToolFilter(deps.Registry),
		urnConns: deps.URNConnections,
	}
}

// AllowConnection reports whether the named persona may reach the named
// connection, by the same rules the authorizer applies to a tool call: deny
// takes precedence, an explicit allow is required, and a persona that does not
// resolve is denied every named connection.
func (s *Scope) AllowConnection(personaName, connection string) bool {
	if s == nil {
		return false
	}
	return s.filter.IsConnectionAllowed(s.personaByName(personaName), connection)
}

// ConnectionsForURN returns the connections that could serve a catalog URN, or
// nil when the deployment carries no mapping for it.
func (s *Scope) ConnectionsForURN(urn string) []string {
	if s == nil || s.urnConns == nil {
		return nil
	}
	return s.urnConns(urn)
}

// personaByName resolves a persona name to its rules, or nil when the registry
// is absent or the name is unknown. A nil persona denies, which is
// IsConnectionAllowed's own fail-closed default.
func (s *Scope) personaByName(name string) *persona.Persona {
	if s.registry == nil || name == "" {
		return nil
	}
	p, ok := s.registry.Get(name)
	if !ok {
		return nil
	}
	return p
}
