package sitesvc

import (
	"context"
	"fmt"
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
	return Permissions{Manage: level == site.LevelManage}, nil
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

// Grant gives a person access. The people who manage a site share view access;
// only its owner and administrators add or change someone who manages it, and
// that person must be internal.
func (s *Service) Grant(ctx context.Context, actor Actor, ref, email, level string) (*site.Grant, error) {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	email = site.NormalizeEmail(email)
	if !site.ValidLevel(level) {
		return nil, refused("level: %s or %s", site.LevelManage, site.LevelView)
	}
	// Authorized first: what checkGrant refuses says who owns the site and who
	// is external, which someone with no rights here must not learn by asking.
	if err := s.mayChange(ctx, actor, st, email, level); err != nil {
		return nil, err
	}
	if err := s.checkGrant(ctx, st, email, level); err != nil {
		return nil, err
	}
	g := site.Grant{Email: email, Level: level, GrantedBy: actor.Email}
	if err := s.Store.SetGrant(ctx, st.ID, g); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	return &g, nil
}

func (s *Service) checkGrant(ctx context.Context, st *site.Site, email, level string) error {
	switch {
	case !strings.Contains(email, "@"):
		return refused("email: an email address")
	case strings.EqualFold(email, st.OwnerEmail):
		return refused("%s owns this site", email)
	case level == site.LevelManage && s.External != nil && s.External(ctx, email):
		return refused("%s is external, and only internal people can manage a site", email)
	}
	return nil
}

// mayChange decides whether actor may set email to level: touching someone
// who manages the site, or making someone do so, is the owner's.
func (s *Service) mayChange(ctx context.Context, actor Actor, st *site.Site, email, level string) error {
	current, err := s.Store.GrantLevel(ctx, st.ID, email)
	if err != nil {
		return err //nolint:wrapcheck // read by identity
	}
	if level == site.LevelManage || current == site.LevelManage {
		return s.mayOwn(ctx, actor, st)
	}
	return s.mayEdit(ctx, actor, st)
}

// Revoke takes a person's access away; it applies on their next request.
func (s *Service) Revoke(ctx context.Context, actor Actor, ref, email string) error {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return err
	}
	email = site.NormalizeEmail(email)
	current, err := s.Store.GrantLevel(ctx, st.ID, email)
	if err != nil {
		return err //nolint:wrapcheck // read by identity
	}
	if current == "" {
		return site.ErrNoGrant
	}
	if err := s.mayChange(ctx, actor, st, email, current); err != nil {
		return err
	}
	return s.Store.RemoveGrant(ctx, st.ID, email) //nolint:wrapcheck // read by identity
}
