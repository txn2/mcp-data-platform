package sitesvc

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/site"
)

// DomainRequest maps one hostname.
type DomainRequest struct {
	Hostname    string
	Site        string
	Environment string
	// Public makes the domain answer anyone (#2619). It is the owner's or an
	// administrator's, and where the environment requires approval someone
	// who is not an administrator adds the domain private and the request
	// goes to Approvals.
	Public bool
	// Reason goes with a request to make the domain public.
	Reason string
	// RequiresApproval, Keep and Place are applied to the environment the
	// domain names, whether the request creates it or it exists: whether it
	// requires approval, the release limit (nil leaves it, #2602) and where
	// it goes (#2617). A field left nil is left alone.
	RequiresApproval *bool
	Keep             *int
	Place            *site.Place
	Application      string
}

// DomainResult is what adding a domain, or changing its access, did: the
// domain as it now stands, and whether making it public waits on Approvals.
type DomainResult struct {
	*site.Domain
	// Status is StatusDone, or StatusAwaitingApproval when making the domain
	// public waits on Approvals; the domain is private meanwhile.
	Status   string            `json:"status"`
	Approval *approval.Request `json:"approval,omitempty"`
}

// AddDomain maps a hostname to one of a site's environments, creating the
// environment when it does not exist, or to an application. It routes as soon
// as it is added, private unless public is asked for; the platform does not
// check who owns the name. A site's owner and the people who manage it add its
// domains, and only an administrator maps one to an application.
func (s *Service) AddDomain(ctx context.Context, actor Actor, req DomainRequest) (*DomainResult, error) {
	host := site.NormalizeHost(req.Hostname)
	if err := s.claimable(host); err != nil {
		return nil, err
	}
	d := &site.Domain{Hostname: host, CreatedBy: actor.Email}
	t, err := s.target(ctx, actor, d, req)
	if err != nil {
		return nil, err
	}
	if req.Public && t != nil {
		waits, err := s.publicApproval(ctx, actor, t, nil, req.Reason)
		if err != nil {
			return nil, err
		}
		d.Public = waits == nil
	}
	out := &DomainResult{Status: StatusDone}
	if out.Domain, err = s.storeDomain(ctx, d); err != nil {
		return nil, err
	}
	if req.Public && !d.Public {
		out.Status = StatusAwaitingApproval
		if out.Approval, err = s.requestPublic(ctx, actor, t.st, out.Domain, req.Reason); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// storeDomain stores a domain, has every replica route it, and reads it back.
func (s *Service) storeDomain(ctx context.Context, d *site.Domain) (*site.Domain, error) {
	if err := s.Store.AddDomain(ctx, d); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if s.Invalidate != nil {
		s.Invalidate(d.Hostname)
	}
	return s.Store.Domain(ctx, d.Hostname) //nolint:wrapcheck // read by identity
}

// claimable refuses a hostname a domain may not map (site.Claimable).
func (s *Service) claimable(host string) error {
	d := site.Domain{Hostname: host}
	err := d.Claimable(site.ClaimRules{PagesDomain: s.cfg().PagesDomain, PortalHost: s.PortalHost, Reserved: s.ReservedHosts, CookieDomain: s.CookieDomain})
	if err != nil {
		return refusedBy(err)
	}
	return nil
}

// target decides what a domain maps to, filling d, and returns the site
// environment it serves, nil for an application.
func (s *Service) target(ctx context.Context, actor Actor, d *site.Domain, req DomainRequest) (*envTarget, error) {
	switch {
	case (req.Site == "") == (req.Application == ""):
		return nil, refused("a domain maps to a site and environment, or to an application")
	case req.Application != "":
		if req.Public {
			return nil, refused("an application's domain answers by the application's own rules, so it has no public setting")
		}
		return nil, s.applicationTarget(ctx, actor, d, req)
	}
	st, err := s.domainTarget(ctx, actor, d, &req)
	if err != nil {
		return nil, err
	}
	if err := s.ensureEnvironment(ctx, actor, st, req); err != nil {
		return nil, err
	}
	if st, err = s.Store.Get(ctx, st.ID); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	d.SiteID, d.Environment = st.ID, req.Environment
	return &envTarget{st: st, name: req.Environment, env: st.Environment(req.Environment)}, nil
}

// domainTarget is the site a domain is added to, for someone who manages it
// (the owner or an administrator to add it public), checked before any
// environment is made for it: dev takes no domain, and a hostname maps once.
func (s *Service) domainTarget(ctx context.Context, actor Actor, d *site.Domain, req *DomainRequest) (*site.Site, error) {
	st, err := s.Find(ctx, req.Site)
	if err != nil {
		return nil, err
	}
	if err := s.mayEdit(ctx, actor, st); err != nil {
		return nil, err
	}
	req.Environment, _ = site.CanonicalEnvironment(req.Environment)
	if req.Environment == site.EnvDev {
		return nil, refused("dev is the working copy and cannot have a domain: add the domain to the environment people use, and promote dev's release to it")
	}
	if err := s.unclaimed(ctx, d.Hostname); err != nil {
		return nil, err
	}
	if req.Public {
		if err := s.mayOwn(ctx, actor, st); err != nil {
			return nil, fmt.Errorf("only the owner of site %q or an administrator makes a domain public: %w", st.Name, site.ErrNotAllowed)
		}
	}
	return st, nil
}

// unclaimed refuses a hostname already mapped, before an environment is made
// for it.
func (s *Service) unclaimed(ctx context.Context, hostname string) error {
	_, err := s.Store.Domain(ctx, hostname)
	switch {
	case err == nil:
		return fmt.Errorf("hostname %s: %w", hostname, site.ErrDomainTaken)
	case errors.Is(err, site.ErrNoDomain):
		return nil
	}
	return fmt.Errorf("reading the domain: %w", err)
}

// ensureEnvironment creates the environment a domain names, placed where the
// request says (#2617), and applies what the request sets to one that exists:
// every site holds prod, so requiring approval of it with its domain is a
// change to an environment that is already there. SetEnvironment keeps its
// rules: taking approval off, and moving past a gated environment, are an
// administrator's.
func (s *Service) ensureEnvironment(ctx context.Context, actor Actor, st *site.Site, req DomainRequest) error {
	if st.Environment(req.Environment) != nil && req.RequiresApproval == nil && req.Keep == nil && req.Place == nil {
		return nil
	}
	_, err := s.SetEnvironment(ctx, actor, st.ID, req.Environment, EnvironmentChange{
		RequiresApproval: req.RequiresApproval, Keep: req.Keep, Place: req.Place,
	})
	return err
}

func (s *Service) applicationTarget(ctx context.Context, actor Actor, d *site.Domain, req DomainRequest) error {
	if !actor.Admin {
		return errNeedsAdmin
	}
	if s.Apps == nil {
		return refused("this deployment has no applications")
	}
	a, err := s.Apps.GetByName(ctx, req.Application)
	if err != nil {
		return fmt.Errorf("application %q: %w", req.Application, err)
	}
	d.ApplicationID = a.ID
	return nil
}

// SetDomainAccess makes a site's domain public or private (#2619). Making it
// public names that one domain and is the owner's or an administrator's; where
// the environment requires approval, someone who is not an administrator files
// a request and the domain opens when they ask again after it is approved.
// Anyone who manages the site makes a domain private.
func (s *Service) SetDomainAccess(ctx context.Context, actor Actor, hostname string, public bool, reason string) (*DomainResult, error) {
	d, st, err := s.domainSite(ctx, actor, hostname)
	if err != nil {
		return nil, err
	}
	out := &DomainResult{Status: StatusDone, Domain: d}
	if public && !d.Public {
		if out.Approval, err = s.mayOpen(ctx, actor, st, d, reason); err != nil {
			return nil, err
		}
		if out.Approval != nil {
			out.Status = StatusAwaitingApproval
			return out, nil
		}
	}
	if public == d.Public {
		return out, nil
	}
	if err := s.Store.SetDomainPublic(ctx, d.Hostname, public); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if s.Invalidate != nil {
		s.Invalidate(d.Hostname)
	}
	if out.Domain, err = s.Store.Domain(ctx, d.Hostname); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	return out, nil
}

// domainSite is a site's domain and its site, for someone who manages the
// site; an application's domain has no public setting.
func (s *Service) domainSite(ctx context.Context, actor Actor, hostname string) (*site.Domain, *site.Site, error) {
	d, err := s.Store.Domain(ctx, site.NormalizeHost(hostname))
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // read by identity
	}
	if d.SiteID == "" {
		return nil, nil, refused("%s maps to an application, which answers by its own rules: it has no public setting", d.Hostname)
	}
	st, err := s.Store.Get(ctx, d.SiteID)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // read by identity
	}
	if err := s.mayEdit(ctx, actor, st); err != nil {
		return nil, nil, err
	}
	return d, st, nil
}

// mayOpen is nil when the actor may make the domain public now, or the
// request it waits on; refused for someone who neither owns the site nor
// administers the platform.
func (s *Service) mayOpen(ctx context.Context, actor Actor, st *site.Site, d *site.Domain, reason string) (*approval.Request, error) {
	if err := s.mayOwn(ctx, actor, st); err != nil {
		return nil, fmt.Errorf("only the owner of site %q or an administrator makes a domain public: %w", st.Name, site.ErrNotAllowed)
	}
	return s.publicApproval(ctx, actor, &envTarget{st: st, name: d.Environment, env: st.Environment(d.Environment)}, d, reason)
}

// publicApproval is nil when making the domain public may go ahead, or the
// request it now waits on. An administrator goes ahead; so does anyone on an
// environment that requires no approval. d is nil for a domain not added yet,
// which waits whenever the environment requires approval; its request is
// filed once it exists.
func (s *Service) publicApproval(ctx context.Context, actor Actor, t *envTarget, d *site.Domain, reason string) (*approval.Request, error) {
	if t.env == nil || !t.env.RequiresApproval || actor.Admin {
		return nil, nil //nolint:nilnil // nil request is "go ahead"
	}
	if s.Approvals == nil {
		return nil, refused("this deployment has no approvals queue, so a domain of environment %q cannot be made public", t.name)
	}
	if d == nil {
		return &approval.Request{}, nil
	}
	if s.Approvals.EnvironmentApproved(ctx, t.st.ID, d.Hostname, d.PublicStamp()) {
		return nil, nil //nolint:nilnil // nil request is "go ahead"
	}
	return s.requestPublic(ctx, actor, t.st, d, reason)
}

// requestPublic files a request to make a domain public: about the domain, as
// it was when asked.
func (s *Service) requestPublic(ctx context.Context, actor Actor, st *site.Site, d *site.Domain, reason string) (*approval.Request, error) {
	r, err := s.Approvals.SubmitEnvironment(ctx, approval.Actor{Email: actor.Email, Persona: actor.Persona, Admin: actor.Admin}, approval.SubmitEnvironment{
		SiteID: st.ID, Site: st.Name, Target: d.Hostname, Stamp: d.PublicStamp(), Reason: reason,
	})
	if err != nil {
		return nil, fmt.Errorf("requesting approval: %w", err)
	}
	return r, nil
}

// RemoveDomain stops routing a hostname.
func (s *Service) RemoveDomain(ctx context.Context, actor Actor, hostname string) error {
	host := site.NormalizeHost(hostname)
	d, err := s.Store.Domain(ctx, host)
	if err != nil {
		return err //nolint:wrapcheck // read by identity
	}
	if err := s.mayRemoveDomain(ctx, actor, d); err != nil {
		return err
	}
	if err := s.Store.RemoveDomain(ctx, host); err != nil {
		return err //nolint:wrapcheck // read by identity
	}
	if s.Invalidate != nil {
		s.Invalidate(host)
	}
	return nil
}

func (s *Service) mayRemoveDomain(ctx context.Context, actor Actor, d *site.Domain) error {
	if d.SiteID == "" {
		if !actor.Admin {
			return errNeedsAdmin
		}
		return nil
	}
	st, err := s.Store.Get(ctx, d.SiteID)
	if err != nil {
		return err //nolint:wrapcheck // read by identity
	}
	if err := s.mayEdit(ctx, actor, st); err != nil {
		return err
	}
	// Moving the name off an environment that requires approval and onto one
	// that does not would publish to it unapproved, so like taking the rule
	// off, it is an administrator's.
	if env := st.Environment(d.Environment); env != nil && env.RequiresApproval && !actor.Admin {
		return fmt.Errorf("an administrator removes a domain from an environment that requires approval: %w", site.ErrNotAllowed)
	}
	return nil
}
