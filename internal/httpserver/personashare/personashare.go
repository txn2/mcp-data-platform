// Package personashare gives the portal the personas a share can be addressed
// to, the people each one reaches (#2089), and whether a person is external:
// their recorded roles resolve to a persona marked external (#2290).
package personashare

import (
	"context"
	"sort"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

// Wire gives the portal the personas a share can be addressed to
// and the people each one reaches (#2089).
func Wire(deps *portal.Deps, pr *persona.Registry, users user.Store) {
	if pr == nil {
		return
	}
	deps.Personas = func() []portal.SharePersona {
		all := pr.All()
		out := make([]portal.SharePersona, 0, len(all))
		for _, p := range all {
			out = append(out, portal.SharePersona{Name: p.Name, DisplayName: p.DisplayName, External: p.External})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out
	}
	if users != nil {
		deps.PersonaMembers = func(ctx context.Context, name string) []string {
			return members(ctx, pr, users, name)
		}
		deps.ExternalAddress = External(pr, users)
	}
}

// External reports whether the person at an address is external: the roles
// the directory last recorded for them resolve to a persona marked external.
// An address the directory does not hold, or cannot read, is not.
func External(pr *persona.Registry, users user.Store) func(ctx context.Context, email string) bool {
	return func(ctx context.Context, email string) bool {
		u, err := users.Get(ctx, email)
		if err != nil || u == nil {
			return false
		}
		return pr.IsExternalRoles(u.Roles)
	}
}

// members lists the confirmed people in the directory whose recorded
// roles resolve to the persona, a page at a time.
func members(ctx context.Context, pr *persona.Registry, users user.Store, name string) []string {
	members := make([]string, 0)
	for offset := 0; ; offset += user.MaxListLimit {
		page, total, err := users.List(ctx, user.Filter{ConfirmedOnly: true, Limit: user.MaxListLimit, Offset: offset})
		if err != nil {
			return members
		}
		for _, u := range page {
			if p, ok := pr.GetForRoles(u.Roles); ok && p.Name == name {
				members = append(members, u.Email)
			}
		}
		if len(page) == 0 || offset+len(page) >= total {
			return members
		}
	}
}
