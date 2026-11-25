package schemagate

import (
	"fmt"
	"regexp"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// catalogNotFound is Trino's refusal of a catalog name it does not have, in
// either wording it uses.
var catalogNotFound = regexp.MustCompile(`(?i)catalog '([^']+)' (?:not found|does not exist)`)

// catalogHint adds, to a statement that failed because it named a catalog the
// engine does not have, the catalog the connection of that name reads (#2812):
// agents write a connection's name where its catalog goes, and Trino's error
// does not say the two differ. A catalog name that is no connection, or a
// connection whose catalog has that name, adds nothing.
func (g *Gate) catalogHint(res mcp.Result) {
	cr, ok := res.(*mcp.CallToolResult)
	if !ok || cr == nil || !cr.IsError {
		return
	}
	name := missingCatalog(cr.Content)
	if name == "" {
		return
	}
	ns, known := g.cfg.Describer.Namespace(name)
	if !known || ns.Connection != name || ns.Catalog == "" || ns.Catalog == name {
		return
	}
	cr.Content = append(cr.Content, &mcp.TextContent{Text: fmt.Sprintf(
		"%q is a connection, not a catalog: the connection %s reads catalog %q. Name the catalog in the SQL "+
			"(%s.<schema>.<table>) and the connection in the connection argument.",
		name, name, ns.Catalog, ns.Catalog)})
}

// missingCatalog is the catalog the engine's first "not found" error names,
// "" when the content carries none.
func missingCatalog(content []mcp.Content) string {
	for _, c := range content {
		tc, isText := c.(*mcp.TextContent)
		if !isText {
			continue
		}
		if m := catalogNotFound.FindStringSubmatch(tc.Text); m != nil {
			return m[1]
		}
	}
	return ""
}
