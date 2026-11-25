package sitewire

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epaddr"
)

// EndpointHosts is every origin an application's endpoints answer on beside
// the platform's own host (#2822): its page origin and the domains mapped to
// it, which run its last environment, and each site environment part's
// addresses by that environment's name. A read that fails leaves its hosts
// out rather than failing the page asking.
func (rt *Runtime) EndpointHosts(ctx context.Context, a *application.Application) epaddr.Hosts {
	addr := rt.Service.Addressing()
	h := epaddr.Hosts{App: a.Name, PageOrigins: []string{}, Sites: map[string][]string{}}
	if origin := addr.PageOrigin(a.Name); origin != "" {
		h.PageOrigins = append(h.PageOrigins, origin)
	}
	if domains, err := rt.Store.Domains(ctx); err == nil {
		for _, d := range domains {
			if d.ApplicationID == a.ID || (d.ApplicationID == "" && d.Application == a.Name) {
				h.PageOrigins = append(h.PageOrigins, addr.Origin(d.Hostname))
			}
		}
	}
	byEnv, err := rt.Store.ApplicationHostsByEnvironment(ctx, a.ID, rt.Live.Get().PagesDomain)
	if err != nil {
		return h
	}
	for env, hosts := range byEnv {
		for _, host := range hosts {
			h.Sites[env] = append(h.Sites[env], addr.Origin(host))
		}
	}
	return h
}
