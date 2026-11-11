package sitesvc

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// Permissions is what one person may do to one site.
type Permissions struct {
	// Manage publishes, promotes, rolls back, adds domains and shares view access.
	Manage bool `json:"manage"`
	// Own edits the description, moves the owner, and adds or removes the
	// people who manage.
	Own bool `json:"own"`
	// Delete removes the site.
	Delete bool `json:"delete"`
}

// isOwner reports whether actor's email is the site's owner. An email the
// identity provider said it has not verified matches no one.
func isOwner(actor Actor, st *site.Site) bool {
	return actor.Email != "" && !actor.EmailUnverified && strings.EqualFold(actor.Email, st.OwnerEmail)
}

// PermissionsOf is what actor may do to st.
func (s *Service) PermissionsOf(ctx context.Context, actor Actor, st *site.Site) (Permissions, error) {
	if actor.Admin {
		return Permissions{Manage: true, Own: true, Delete: true}, nil
	}
	if isOwner(actor, st) {
		return Permissions{Manage: true, Own: true}, nil
	}
	if actor.Email == "" || actor.EmailUnverified {
		return Permissions{}, nil
	}
	level, err := s.Store.GrantLevel(ctx, st.ID, site.NormalizeEmail(actor.Email))
	if err != nil {
		return Permissions{}, err //nolint:wrapcheck // read by identity
	}
	// Only internal people manage a site. A grant made to an address before
	// its first sign-in could not be judged then; it is judged on use.
	manage := level == site.LevelManage && (s.External == nil || !s.External(ctx, actor.Email))
	return Permissions{Manage: manage}, nil
}

// mayEdit refuses anyone who does not manage the site.
func (s *Service) mayEdit(ctx context.Context, actor Actor, st *site.Site) error {
	p, err := s.PermissionsOf(ctx, actor, st)
	if err != nil {
		return err
	}
	if !p.Manage {
		return fmt.Errorf("site %q is changed by its owner, the people they add, or an administrator: %w", st.Name, site.ErrNotAllowed)
	}
	return nil
}

// mayOwn refuses anyone but the owner and administrators.
func (s *Service) mayOwn(ctx context.Context, actor Actor, st *site.Site) error {
	p, err := s.PermissionsOf(ctx, actor, st)
	if err != nil {
		return err
	}
	if !p.Own {
		return fmt.Errorf("only the owner of site %q or an administrator can do this: %w", st.Name, site.ErrNotAllowed)
	}
	return nil
}

// Grants lists who has access to a site, for someone who manages it.
func (s *Service) Grants(ctx context.Context, actor Actor, ref string) ([]site.Grant, error) {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := s.mayEdit(ctx, actor, st); err != nil {
		return nil, err
	}
	return s.Store.Grants(ctx, st.ID) //nolint:wrapcheck // read by identity
}

// GrantRequest gives a person, by email, or a persona access. A persona is
// always given view. Environments narrow a view grant to those private
// environments; none is every private environment.
type GrantRequest struct {
	Email        string
	Persona      string
	Level        string
	Environments []string
}

// Grant gives a person or a persona access. The people who manage a site share
// view access; only its owner and administrators add or change someone who
// manages it, and that person must be internal.
func (s *Service) Grant(ctx context.Context, actor Actor, ref string, req GrantRequest) (*site.Grant, error) {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	g := &site.Grant{Email: site.NormalizeEmail(req.Email), Persona: strings.TrimSpace(req.Persona), Level: req.Level, GrantedBy: actor.Email}
	if g.Persona != "" && g.Level == "" {
		g.Level = site.LevelView
	}
	if !site.ValidLevel(g.Level) {
		return nil, refused("level: %s or %s", site.LevelManage, site.LevelView)
	}
	if (g.Email == "") == (g.Persona == "") {
		return nil, refused("a grant names an email or a persona, not both")
	}
	// Authorized first: what checkGrant refuses says who owns the site and who
	// is external, which someone with no rights here must not learn by asking.
	if err := s.mayChange(ctx, actor, st, g.Email, g.Level); err != nil {
		return nil, err
	}
	if g.Environments, err = s.grantEnvironments(st, g.Level, req.Environments); err != nil {
		return nil, err
	}
	if err := s.checkGrant(ctx, st, g); err != nil {
		return nil, err
	}
	if err := s.Store.SetGrant(ctx, st.ID, g); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	return g, nil
}

// grantEnvironments is the environments a grant names, each one the site has.
// A manage grant is site-wide.
func (*Service) grantEnvironments(st *site.Site, level string, names []string) ([]string, error) {
	out := []string{}
	if len(names) == 0 {
		return out, nil
	}
	if level == site.LevelManage {
		return nil, refused("a manage grant is for the whole site; environments narrow a view grant")
	}
	for _, n := range names {
		n, _ = site.CanonicalEnvironment(strings.TrimSpace(n))
		if st.Environment(n) == nil {
			return nil, noEnvironment(n)
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

func (s *Service) checkGrant(ctx context.Context, st *site.Site, g *site.Grant) error {
	if g.Persona != "" {
		switch {
		case g.Level != site.LevelView:
			return refused("a persona is given view; manage is given to a person")
		case s.PersonaExists != nil && !s.PersonaExists(g.Persona):
			return refused("persona %q does not exist", g.Persona)
		}
		return nil
	}
	switch {
	case !strings.Contains(g.Email, "@"):
		return refused("email: an email address")
	case strings.EqualFold(g.Email, st.OwnerEmail):
		return refused("%s owns this site", g.Email)
	case g.Level == site.LevelManage && s.External != nil && s.External(ctx, g.Email):
		return refused("%s is external, and only internal people can manage a site", g.Email)
	}
	return nil
}

// mayChange decides whether actor may set email to level: touching someone
// who manages the site, or making someone do so, is the owner's. A persona is
// only ever given view, which anyone who manages the site shares.
func (s *Service) mayChange(ctx context.Context, actor Actor, st *site.Site, email, level string) error {
	current := ""
	if email != "" {
		var err error
		if current, err = s.Store.GrantLevel(ctx, st.ID, email); err != nil {
			return err //nolint:wrapcheck // read by identity
		}
	}
	if level == site.LevelManage || current == site.LevelManage {
		return s.mayOwn(ctx, actor, st)
	}
	return s.mayEdit(ctx, actor, st)
}

// Revoke takes access away from a person by email or, with email "", from a
// persona; it applies on their next request.
func (s *Service) Revoke(ctx context.Context, actor Actor, ref, email, persona string) error {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return err
	}
	email, persona = site.NormalizeEmail(email), strings.TrimSpace(persona)
	if (email == "") == (persona == "") {
		return refused("name an email or a persona")
	}
	level := site.LevelView
	if email != "" {
		if level, err = s.Store.GrantLevel(ctx, st.ID, email); err != nil {
			return err //nolint:wrapcheck // read by identity
		}
		if level == "" {
			return site.ErrNoGrant
		}
	}
	if err := s.mayChange(ctx, actor, st, email, level); err != nil {
		return err
	}
	return s.Store.RemoveGrant(ctx, st.ID, email, persona) //nolint:wrapcheck // read by identity
}

// OpenableEnvironments is the environments of a site the actor can open: every
// public one, and each private one the access check opens for them. It is what
// someone who does not manage the site is shown.
func (s *Service) OpenableEnvironments(ctx context.Context, actor Actor, st *site.Site) ([]site.Environment, error) {
	out := []site.Environment{}
	for _, e := range st.Environments {
		if e.Public {
			out = append(out, e)
			continue
		}
		ok, err := s.opens(ctx, actor, st, e.Name)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// opens is the gateway's check of one private environment, for the portal.
func (s *Service) opens(ctx context.Context, actor Actor, st *site.Site, env string) (bool, error) {
	if actor.Admin {
		return true, nil
	}
	a, err := s.Store.SiteAccess(ctx, st.ID, env, actor.Email, actor.Persona)
	if err != nil {
		return false, err //nolint:wrapcheck // read by identity
	}
	if a.ByPersona || s.internalOpens(ctx, actor, a) {
		return true, nil
	}
	verified := actor.Email != "" && !actor.EmailUnverified
	return verified && (a.ByEmail || strings.EqualFold(actor.Email, a.OwnerEmail)), nil
}

// internalOpens reports whether an environment open to everyone internal
// opens for the actor: someone with a persona that is not external.
func (s *Service) internalOpens(ctx context.Context, actor Actor, a *site.Access) bool {
	return a.InternalView && actor.Persona != "" && (s.External == nil || !s.External(ctx, actor.Email))
}
