// Package siteshared lists the site environments shared with a person
// (#2718): each environment a grant to their verified email or to their
// persona opens, with its address and whether it is online. It is what
// someone without the Sites area, an external person above all, has to find a
// site they were given: Home lists it, and nothing in it names the site's other
// environments or who else they are shared with, which stay the managers' to
// know.
package siteshared

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/reqlog"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
)

// Route is where the list is served.
const Route = "GET /api/v1/portal/shared-sites"

// errShared wraps a store failure reading the list.
const errShared = "shared sites: %w"

// Store is what the list reads: the sites with a grant naming the person,
// each site and its domains, and the access check of one environment.
type Store interface {
	GrantedSiteIDs(ctx context.Context, email, persona string) ([]string, error)
	Get(ctx context.Context, id string) (*site.Site, error)
	SiteDomains(ctx context.Context, siteID string) ([]site.Domain, error)
	SiteAccess(ctx context.Context, siteID, env, email, persona string) (*site.Access, error)
}

// Environment is one environment shared with the caller.
type Environment struct {
	Site        string `json:"site" example:"partner-review"`
	Environment string `json:"environment" example:"dev"`
	// Address is where the environment is reached: its primary domain, else
	// its address on the pages domain; "" when it has neither.
	Address string `json:"address" example:"https://partner-review--dev.pages.example.net"`
	// Online is false while the site's managers have taken it offline or
	// pointed it elsewhere; Message is what they said, if anything.
	Online  bool   `json:"online"`
	Message string `json:"message,omitempty"`
}

// List is the environments shared with the caller, in site name order and,
// within a site, the order its environments are listed in.
type List struct {
	Environments []Environment `json:"environments"`
}

// Shared is the environments a grant opens for actor. A grant that names a
// site the actor's grant no longer opens, or a site removed meanwhile, adds
// nothing.
func Shared(ctx context.Context, store Store, at site.Addressing, actor sitesvc.Actor) ([]Environment, error) {
	ids, err := store.GrantedSiteIDs(ctx, actor.Email, actor.Persona)
	if err != nil {
		return nil, fmt.Errorf(errShared, err)
	}
	out := []Environment{}
	for _, id := range ids {
		envs, err := sharedOf(ctx, store, at, actor, id)
		if err != nil {
			return nil, err
		}
		out = append(out, envs...)
	}
	sortBySite(out)
	return out, nil
}

// sharedOf is the environments of one site a grant opens for actor.
func sharedOf(ctx context.Context, store Store, at site.Addressing, actor sitesvc.Actor, id string) ([]Environment, error) {
	st, err := store.Get(ctx, id)
	if errors.Is(err, site.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf(errShared, err)
	}
	var domains []site.Domain
	var out []Environment
	for i := range st.Environments {
		e := &st.Environments[i]
		a, err := store.SiteAccess(ctx, st.ID, e.Name, actor.Email, actor.Persona)
		if err != nil {
			return nil, fmt.Errorf(errShared, err)
		}
		if !sitesvc.GrantOpens(actor, a) {
			continue
		}
		if domains == nil {
			if domains, err = store.SiteDomains(ctx, st.ID); err != nil {
				return nil, fmt.Errorf(errShared, err)
			}
		}
		out = append(out, Environment{
			Site: st.Name, Environment: e.Name, Address: at.Address(st, e.Name, domains),
			Online: e.Online(), Message: e.StatusMessage,
		})
	}
	return out, nil
}

// sortBySite orders by site name, keeping each site's own environment order.
func sortBySite(envs []Environment) {
	slices.SortStableFunc(envs, func(a, b Environment) int { return strings.Compare(a.Site, b.Site) })
}

// Handler serves the list. actorOf resolves the caller and answers the
// request itself when it cannot; addressing is read on each request, so a
// changed pages domain shows at once.
//
// @Summary      List the site environments shared with me
// @Description  Each environment of a site that a grant to the caller's verified email or to their persona opens, with its address and whether it is online (#2718). Nothing else of those sites is named: not their other environments and not who else they are shared with. Every signed-in person may ask, an external person included; one with no grant gets an empty list.
// @Tags         Sites
// @Produce      json
// @Success      200  {object}  List
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/shared-sites [get]
func Handler(store Store, addressing func() site.Addressing, actorOf func(http.ResponseWriter, *http.Request) (sitesvc.Actor, bool)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorOf(w, r)
		if !ok {
			return
		}
		envs, err := Shared(r.Context(), store, addressing(), actor)
		if err != nil {
			reqlog.Failed(r, "sites: listing the environments shared with a person failed", err)
			httpjson.WriteError(w, http.StatusInternalServerError, "the sites shared with you could not be read")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = wirejson.Encode(w, List{Environments: envs})
	}
}
