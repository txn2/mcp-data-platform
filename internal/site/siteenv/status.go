package siteenv

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitestatus"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

// StatusChange is a change to what an environment answers (#2631, #2632).
type StatusChange struct {
	// Status is online, offline or redirect.
	Status string
	// Message is an offline environment's message; "" shows the default.
	Message string
	// BackAt is when visitors are told an offline environment is back.
	BackAt *time.Time
	// RedirectTo and KeepPath are a redirect's target and path rule.
	RedirectTo string
	KeepPath   bool
	// Reason goes with a redirect that waits on Approvals.
	Reason string
}

// StatusResult is what a status change did: the site as it now stands, and
// the request a redirect waits on where the environment requires approval.
type StatusResult struct {
	Site *site.Site `json:"site"`
	// Status is StatusDone or StatusAwaitingApproval.
	Status   string            `json:"status"`
	Approval *approval.Request `json:"approval,omitempty"`
}

// SetStatus takes an environment offline, redirects it, or brings it back
// online. It is anyone's who manages the site, and it takes effect at once:
// an outage cannot wait in a queue. The one exception is a redirect on an
// environment that requires approval, set by someone who is not an
// administrator: it would send every visitor to a site the platform does not
// control, so it waits on Approvals, and the environment keeps answering as it
// does until it is approved and set again. dev is the private working copy
// and is always online. The release pointer is never touched.
func (o Ops) SetStatus(ctx context.Context, actor sitesvc.Actor, ref, name string, c StatusChange) (*StatusResult, error) {
	st, env, err := o.siteFor(ctx, actor, ref, name)
	if err != nil {
		return nil, err
	}
	want, err := o.statusOf(ctx, st, env, c)
	if err != nil {
		return nil, err
	}
	out := &StatusResult{Status: StatusDone}
	if want.Status == site.StatusRedirect {
		if out.Approval, err = o.redirectApproval(ctx, actor, redirecting{st: st, env: env, want: want, reason: c.Reason}); err != nil {
			return nil, err
		}
		if out.Approval != nil {
			out.Status, out.Site = StatusAwaitingApproval, st
			return out, nil
		}
	}
	if err := o.Store.SetStatus(ctx, st.ID, env.Name, want, actor.Email); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	o.invalidate(st.Name)
	if out.Site, err = o.Store.Get(ctx, st.ID); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	return out, nil
}

// statusOf checks a change and returns the status as stored.
func (o Ops) statusOf(ctx context.Context, st *site.Site, env *site.Environment, c StatusChange) (site.EnvironmentStatus, error) {
	var want site.EnvironmentStatus
	switch c.Status {
	case site.StatusOnline:
		want.Status = site.StatusOnline
		return want, nil
	case site.StatusOffline, site.StatusRedirect:
	default:
		return want, refused("status %q: online, offline or redirect", c.Status)
	}
	if env.Name == site.EnvDev {
		return want, refused("dev is the private working copy, so it is always online: take the environment people use offline, or redirect it, instead")
	}
	want.Status = c.Status
	if c.Status == site.StatusOffline {
		msg, err := sitestatus.CleanStatusMessage(c.Message)
		if err != nil {
			return want, refusedBy(err)
		}
		want.Message, want.BackAt = msg, c.BackAt
		return want, nil
	}
	target, err := sitestatus.ParseRedirectTarget(c.RedirectTo, o.ownHosts(ctx, st, env))
	if err != nil {
		return want, refusedBy(err)
	}
	want.RedirectTo, want.KeepPath = target.String(), c.KeepPath
	return want, nil
}

// ownHosts are the hosts a redirect may not point at: the pages domain (every
// site's own addresses sit under it), the portal and the platform's other
// hostnames, the session cookie's domain, and the environment's own domains.
func (o Ops) ownHosts(ctx context.Context, st *site.Site, env *site.Environment) []string {
	own := append([]string{o.Addressing().PagesDomain, o.PortalHost, o.CookieDomain}, o.ReservedHosts...)
	domains, err := o.Store.SiteDomains(ctx, st.ID)
	if err != nil {
		return own
	}
	for _, d := range domains {
		if d.Environment == env.Name {
			own = append(own, d.Hostname)
		}
	}
	return own
}

// redirectApproval is nil when the redirect may go ahead, or the request it
// now waits on: an environment that requires approval, redirected by someone
// who is not an administrator, waits until the redirect, as asked, is approved.
// redirecting is one redirect asked for.
type redirecting struct {
	st     *site.Site
	env    *site.Environment
	want   site.EnvironmentStatus
	reason string
}

func (o Ops) redirectApproval(ctx context.Context, actor sitesvc.Actor, rd redirecting) (*approval.Request, error) {
	st, env, want, reason := rd.st, rd.env, rd.want, rd.reason
	if !env.RequiresApproval || actor.Admin {
		return nil, nil //nolint:nilnil // nil request is "go ahead"
	}
	if o.Approvals == nil {
		return nil, refused("this deployment has no approvals queue, so environment %q, which requires approval, cannot be redirected", env.Name)
	}
	stamp := sitestatus.Stamp(want)
	if o.Approvals.EnvironmentApproved(ctx, st.ID, env.Name, stamp) {
		return nil, nil //nolint:nilnil // nil request is "go ahead"
	}
	r, err := o.Approvals.SubmitEnvironment(ctx, approval.Actor{Email: actor.Email, Persona: actor.Persona, Admin: actor.Admin, Agent: actor.Agent}, approval.SubmitEnvironment{
		SiteID: st.ID, Site: st.Name, Target: env.Name, Stamp: stamp, Reason: reason, Purpose: approval.PurposeSiteRedirect,
	})
	if err != nil {
		if refusal, ok := errors.AsType[*approval.RefusedError](err); ok {
			return nil, refused("%s", refusal.Msg)
		}
		return nil, fmt.Errorf("requesting approval: %w", err)
	}
	return r, nil
}
