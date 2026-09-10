// Package toolinventory compares the tools a deployment says it registers
// with the tools its MCP server actually holds.
//
// The two are written in different places and were never compared. A
// toolkit's Tools() list feeds the admin tool listing, platform_find_tools,
// the instruction baseline and the persona gates, while what a client can
// call is whatever reached mcp.AddTool. 1.131.0 shipped graphql_export named
// by the first and absent from the second, because the tool registers only
// once its dependencies are set and the platform set them after registration
// had run (#1675). Every unit gate was green and the tool was unreachable on
// every deployment (#1680).
//
// It lives here rather than on the platform facade for the reason the other
// seams under internal/platform do: the facade is at its size and method
// budget, and this is cohesive enough to own its own package. pkg/platform
// assembles what only it holds and hands it over.
package toolinventory

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// clientName is what the check connects as. It appears in no audit row:
// tools/list is not a tool call.
const clientName = "tool-inventory-check"

// PlatformTool is one tool the platform registers outside any toolkit. The
// facade's own type is not imported here, so this package stays a leaf.
type PlatformTool struct {
	Name string
	Kind string
}

// Deps is what the comparison reads.
type Deps struct {
	// Server holds the registered handlers. A nil Server is a platform
	// assembled without one, which has no listing to be wrong about.
	Server *mcp.Server
	// Registry holds the toolkits that name their tools.
	Registry *registry.Registry
	// PlatformTools are the tools registered outside any toolkit. The
	// registry does not know them, so a comparison without them reports
	// every one of them as unclaimed.
	PlatformTools []PlatformTool
	// Visible reports whether the operator's tools.allow / tools.deny globs
	// leave a tool in tools/list. A tool the operator hid is absent from the
	// listing by intent, not by fault. Nil hides nothing.
	Visible func(name string) bool
}

// Verify fails when a toolkit or the platform names a tool the MCP server
// does not hold, and warns when the server holds a tool nothing claims.
//
// The listing is read through a real client session, so it is the set a
// caller sees rather than an internal accounting of it.
//
// The missing direction fails; the unexpected direction warns. A name that
// reaches the server with nothing claiming it is a real finding, but a
// fan-out kind that namespaces its tools per upstream can legitimately be
// mid-reconcile, and a deployment that refused to boot over it would trade a
// listing defect for an outage.
func Verify(ctx context.Context, d Deps) error {
	if d.Server == nil {
		return nil
	}
	listed, err := listServerTools(ctx, d.Server)
	if err != nil {
		return fmt.Errorf("reading the MCP server's tool listing: %w", err)
	}

	held := make(map[string]bool, len(listed))
	for _, name := range listed {
		held[name] = true
	}

	if missing := unregistered(d, held); len(missing) > 0 {
		return fmt.Errorf(
			"tool inventory: %s registered on no MCP server handler; "+
				"each of these is named by the deployment and callable by nobody, "+
				"which is a dependency wired after the tools were registered",
			strings.Join(missing, ", "))
	}

	for _, name := range unclaimed(d, listed) {
		slog.WarnContext(ctx, "tool inventory: the MCP server holds a tool no toolkit and no platform tool claims",
			"tool", logsan.SanitizeForLog(name),
			"hint", "the admin tool listing, platform_find_tools and the persona gates will not see it")
	}
	return nil
}

// visible reports whether a tool is one the operator left in the listing.
func (d Deps) visible(name string) bool {
	return d.Visible == nil || d.Visible(name)
}

// unregistered returns "<owner>: <tool>" for every tool the deployment names
// that the server does not hold and the operator did not hide.
func unregistered(d Deps, held map[string]bool) []string {
	var missing []string
	if d.Registry != nil {
		for _, tk := range d.Registry.All() {
			for _, name := range tk.Tools() {
				if held[name] || !d.visible(name) {
					continue
				}
				missing = append(missing, fmt.Sprintf("%s/%s: %s", tk.Kind(), tk.Name(), name))
			}
		}
	}
	for _, pt := range d.PlatformTools {
		if held[pt.Name] || !d.visible(pt.Name) {
			continue
		}
		missing = append(missing, fmt.Sprintf("%s: %s", pt.Kind, pt.Name))
	}
	sort.Strings(missing)
	return missing
}

// unclaimed returns every listed tool that nothing in the deployment names.
func unclaimed(d Deps, listed []string) []string {
	claimed := make(map[string]bool)
	if d.Registry != nil {
		for _, name := range d.Registry.AllTools() {
			claimed[name] = true
		}
	}
	for _, pt := range d.PlatformTools {
		claimed[pt.Name] = true
	}
	var out []string
	for _, name := range listed {
		if !claimed[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// listServerTools reads tools/list over an in-memory transport, the same way
// the admin tool listing reads its titles.
//
// The session carries no credential, so the persona half of the visibility
// filter is skipped and the global allow/deny half is not — which is why
// Deps.Visible applies the same globs to what is expected.
func listServerTools(ctx context.Context, server *mcp.Server) ([]string, error) {
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	if err != nil {
		return nil, fmt.Errorf("server connect: %w", err)
	}
	defer func() { _ = serverSession.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: "v1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		return nil, fmt.Errorf("client connect: %w", err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names, nil
}
