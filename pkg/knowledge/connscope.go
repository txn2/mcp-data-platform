package knowledge

import (
	"sort"
	"strconv"
	"strings"
)

// ConnectionScope is the discovery-side view of the persona connection boundary
// (#1108). A persona's connections.allow list is deny-by-default and is enforced
// when a caller acts; this interface applies the same boundary to what a caller
// can SEE, so the topology sources (the technical catalog, connections, API
// endpoints) never surface material belonging to a connection the caller could
// not reach.
//
// The knowledge package declares the capability rather than importing the
// persona registry, keeping the federation engine decoupled the same way
// ConnectionLister and EndpointSearcher do. The platform adapter implements it
// over persona.ToolFilter.IsConnectionAllowed, which is the same predicate the
// authorizer applies to a tool call, so discovery and authorization cannot
// drift.
type ConnectionScope interface {
	// AllowConnection reports whether the named persona may reach the named
	// connection. An unresolvable persona denies every connection, matching the
	// fail-closed action path.
	AllowConnection(persona, connection string) bool

	// ConnectionsForURN returns the connections that could serve a catalog URN.
	// An empty result means the URN cannot be attributed to any connection; the
	// caller keeps such a hit visible rather than hiding it on a guess.
	ConnectionsForURN(urn string) []string
}

// connGate is one provider's view of the connection boundary for a single search
// or fetch: the scope to consult plus the running count of candidates that scope
// removed. The Router builds one per provider arm and never shares it, so a
// provider records its withheld count without synchronization and the Router
// reads it back after the arm returns.
//
// A nil gate (or a gate with no scope) means the deployment wired no connection
// scope, which leaves discovery unfiltered — the behavior before #1108.
type connGate struct {
	scope    ConnectionScope
	withheld int
}

// allowsConnection reports whether the caller may see material belonging to the
// named connection. An empty name is platform-level and always visible, matching
// persona.ToolFilter.IsConnectionAllowed; an unscoped caller sees everything.
func (c Caller) allowsConnection(name string) bool {
	if c.conn == nil || c.conn.scope == nil || name == "" {
		return true
	}
	return c.conn.scope.AllowConnection(c.Persona, name)
}

// allowsURN reports whether the caller may see the catalog entity named by urn.
// A URN that maps to no connection stays visible (the mapping, not the persona,
// is what failed, and hiding on a guess would silently drop entities no
// connection claims). A URN that maps to several connections — the platform
// segment of a DataHub URN is shared by every connection of that platform — is
// visible when the persona may reach ANY of them: the caller can legitimately
// query the entity through that connection.
func (c Caller) allowsURN(urn string) bool {
	if c.conn == nil || c.conn.scope == nil {
		return true
	}
	conns := c.conn.scope.ConnectionsForURN(urn)
	if len(conns) == 0 {
		return true
	}
	for _, name := range conns {
		if c.conn.scope.AllowConnection(c.Persona, name) {
			return true
		}
	}
	return false
}

// withhold records that n candidates were removed by the connection boundary, so
// the Router can report "present but not yours to see" instead of letting the
// results silently shorten. Providers call it once per search arm with the count
// of candidates that matched the query and were then hidden.
func (c Caller) withhold(n int) {
	if c.conn == nil || n <= 0 {
		return
	}
	c.conn.withheld += n
}

// WithheldNotice renders the one-line explanation a discovery surface shows when
// the connection boundary removed results: how many, from which sources, why
// (the caller's persona), and the remedy (ask an administrator). It returns ""
// when nothing was withheld.
//
// It lives here so the MCP search tool, the portal REST search, and
// list_connections all render the same copy from one implementation; a denial
// that does not carry the path in is worse than no denial message at all.
func WithheldNotice(coverage []SourceCoverage, persona string) string {
	total := 0
	var sources []string
	for _, c := range coverage {
		if c.Withheld <= 0 {
			continue
		}
		total += c.Withheld
		sources = append(sources, c.Source)
	}
	if total == 0 {
		return ""
	}
	sort.Strings(sources)
	return withheldSentence(total, persona) + " in " + joinAnd(sources) + "." + withheldRemedy
}

// ConnectionsWithheldNotice renders the same explanation for an enumeration of
// connections (list_connections), where the hidden items are the connections
// themselves rather than results drawn from them. It returns "" when nothing was
// withheld.
func ConnectionsWithheldNotice(withheld int, persona string) string {
	if withheld <= 0 {
		return ""
	}
	if withheld == 1 {
		return "1 connection is hidden because " + personaClause(persona) + " is not granted it." + withheldRemedy
	}
	return strconv.Itoa(withheld) + " connections are hidden because " + personaClause(persona) +
		" is not granted them." + withheldRemedy
}

// withheldContentNotice renders the same explanation for entries removed from
// INSIDE one fetched record — the datasets listed under a governance entity —
// where there is no coverage block to carry the count. It returns "" when nothing
// was withheld. It shares withheldSentence with the search-side notice so the two
// read identically; a short list with no explanation is indistinguishable from a
// governance entity nothing carries.
func withheldContentNotice(withheld int, persona string) string {
	if withheld <= 0 {
		return ""
	}
	return withheldSentence(withheld, persona) + "." + withheldRemedy
}

// withheldRemedy is the path in that every withheld notice carries: what the
// reader does about it. A denial with no remedy is a dead end.
const withheldRemedy = " Ask an administrator to grant your persona access to the connections you need."

// withheldSentence renders the count-and-reason clause, agreeing in number so the
// message reads as product copy rather than a template.
func withheldSentence(total int, persona string) string {
	if total == 1 {
		return "1 result is hidden because " + personaClause(persona) + " is not granted the connection it belongs to"
	}
	return strconv.Itoa(total) + " results are hidden because " + personaClause(persona) +
		" is not granted the connections they belong to"
}

// personaClause names the persona responsible for a denial, or refers to it
// generically when the caller resolved to none (which denies every connection).
func personaClause(persona string) string {
	if persona == "" {
		return "your persona"
	}
	return "your persona (" + persona + ")"
}

// joinAnd renders a short list in prose ("catalog and endpoints"), so the notice
// names exactly which sources are short rather than making the reader diff the
// coverage block.
func joinAnd(items []string) string {
	if len(items) == 0 {
		return ""
	}
	last := len(items) - 1
	if last == 0 {
		return items[last]
	}
	head := strings.Join(items[:last], ", ")
	if last == 1 {
		return head + " and " + items[last]
	}
	return head + ", and " + items[last]
}
