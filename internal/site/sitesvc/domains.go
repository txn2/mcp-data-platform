package sitesvc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// DomainRequest maps one hostname.
type DomainRequest struct {
	Hostname    string
	Site        string
	Environment string
	// RequiresApproval and Public are applied when the request creates the
	// environment, which is private unless Public asks otherwise.
	RequiresApproval bool
	Public           bool
	Application      string
}

// AddDomain maps a hostname to one of a site's environments, creating the
// environment when it does not exist, or to an application. It routes as soon
// as it is added; the platform does not check who owns the name. A site's
// owner and the people who manage it add its domains, and only an
// administrator maps one to an application.
func (s *Service) AddDomain(ctx context.Context, actor Actor, req DomainRequest) (*site.Domain, error) {
	host := site.NormalizeHost(req.Hostname)
	if err := s.claimable(host); err != nil {
		return nil, err
	}
	d := &site.Domain{Hostname: host, CreatedBy: actor.Email}
	if err := s.target(ctx, actor, d, req); err != nil {
		return nil, err
	}
	if err := s.Store.AddDomain(ctx, d); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if s.Invalidate != nil {
		s.Invalidate(host)
	}
	return s.Store.Domain(ctx, host) //nolint:wrapcheck // read by identity
}

// claimable refuses a hostname that is not a plain name, is one the platform
// answers on itself, or sits where a served page would receive the session
// cookie. The gateway answers an added hostname before the platform's routes,
// so a name the platform uses must never be added.
func (s *Service) claimable(host string) error {
	if err := site.ValidateHostname(host); err != nil {
		return refused("%s", err.Error())
	}
	switch {
	case site.SameOrUnder(host, s.cfg().PagesDomain):
		return refused("%s is under the pages domain, which the platform assigns", host)
	case host == s.PortalHost || s.reserved(host):
		return refused("%s is a hostname this platform answers on itself", host)
	case site.SameOrUnder(host, strings.TrimPrefix(s.CookieDomain, ".")):
		return refused("%s is under the session cookie's domain %s, so its pages would receive the portal's session", host, s.CookieDomain)
	}
	return nil
}

func (s *Service) reserved(host string) bool {
	return slices.ContainsFunc(s.ReservedHosts, func(r string) bool { return site.SameOrUnder(host, site.NormalizeHost(r)) })
}

func (s *Service) target(ctx context.Context, actor Actor, d *site.Domain, req DomainRequest) error {
	switch {
	case (req.Site == "") == (req.Application == ""):
		return refused("a domain maps to a site and environment, or to an application")
	case req.Application != "":
		return s.applicationTarget(ctx, actor, d, req)
	}
	st, err := s.Find(ctx, req.Site)
	if err != nil {
		return err
	}
	if err := s.mayEdit(ctx, actor, st); err != nil {
		return err
	}
	req.Environment, _ = site.CanonicalEnvironment(req.Environment)
	if req.Environment == site.EnvDev {
		return refused("dev is the working copy and cannot have a domain: add the domain to the environment people use, and promote dev's release to it")
	}
	if err := s.unclaimed(ctx, d.Hostname); err != nil {
		return err
	}
	if err := s.ensureEnvironment(ctx, actor, st, req); err != nil {
		return err
	}
	d.SiteID, d.Environment = st.ID, req.Environment
	return nil
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

// ensureEnvironment creates the environment a domain names, private unless
// the request asks for public, which is the owner's or an administrator's.
func (s *Service) ensureEnvironment(ctx context.Context, actor Actor, st *site.Site, req DomainRequest) error {
	if st.Environment(req.Environment) != nil {
		return nil
	}
	c := EnvironmentChange{RequiresApproval: &req.RequiresApproval}
	if req.Public {
		c.Public = &req.Public
	}
	if err := s.checkEnvironmentChange(ctx, actor, envTarget{st: st, name: req.Environment}, c); err != nil {
		return err
	}
	if req.Public && req.RequiresApproval && !actor.Admin {
		return refused("an environment that requires approval is made public through Approvals: add the domain private, then make %s public", req.Environment)
	}
	return s.Store.SetEnvironment(ctx, st.ID, req.Environment, site.EnvironmentSettings{ //nolint:wrapcheck // read by identity
		RequiresApproval: &req.RequiresApproval, Public: &req.Public,
	}, actor.Email)
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
