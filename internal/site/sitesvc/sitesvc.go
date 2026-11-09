// Package sitesvc is what can be done to a site (#2518): the one service the
// manage_site tool and an application's release use. It decides who may do
// what, writes a release's files once under their own prefix, and moves an
// environment's pointer for a promote or a rollback, which copies nothing.
package sitesvc

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitestore"
)

// Service carries out the commands.
type Service struct {
	Store   site.Store
	Objects Objects
	Bucket  string
	Config  siteconfig.Config
	// Live, when set, is the configuration in force and Config only its seed.
	Live      *siteconfig.Live
	Apps      Applications
	Approvals Approvals
	// External reports whether the person at an address is external; nil is
	// no one.
	External func(ctx context.Context, email string) bool
	// PortalHost is the portal's hostname, which no domain may claim.
	PortalHost string
	// ReservedHosts are the other hostnames the platform answers on itself.
	ReservedHosts []string
	// CookieDomain is the session cookie's Domain, under which no served
	// origin may sit.
	CookieDomain string
	// PublicScheme and PublicPort are what an address is written with.
	PublicScheme, PublicPort string
	Invalidate               Invalidate
}

// Actor is who is acting.
type Actor struct {
	Email   string
	Persona string
	Admin   bool
	// EmailUnverified is true when the identity provider said in so many words
	// that it has not verified Email, which then matches no owner or grant.
	EmailUnverified bool
}

// RefusedError is a command refused for a reason the caller can act on.
type RefusedError struct{ Msg string }

func (e *RefusedError) Error() string { return e.Msg }

func refused(format string, args ...any) error {
	return &RefusedError{Msg: fmt.Sprintf(format, args...)}
}

var errNeedsAdmin = fmt.Errorf("this needs an administrator: %w", site.ErrNotAllowed)

// cfg is the configuration in force.
func (s *Service) cfg() siteconfig.Config {
	if s.Live != nil {
		return s.Live.Get()
	}
	return s.Config
}

// noEnvironment is the error for an environment the site does not have.
func noEnvironment(name string) error {
	return fmt.Errorf("environment %q: %w", name, site.ErrNoEnvironment)
}

// Find reads a site by name or id.
func (s *Service) Find(ctx context.Context, ref string) (*site.Site, error) {
	if strings.HasPrefix(ref, "site_") {
		return s.Store.Get(ctx, ref) //nolint:wrapcheck // read by identity
	}
	return s.Store.GetByName(ctx, ref) //nolint:wrapcheck // read by identity
}

// Create makes a site with its default environment; its creator is its owner.
// A site and an application share the name space of the pages domain, so a
// name one of them holds is refused.
func (s *Service) Create(ctx context.Context, actor Actor, name, description string) (*site.Site, error) {
	if actor.Email == "" {
		return nil, refused("a site needs an owner with an email")
	}
	st := &site.Site{
		ID: sitestore.NewSiteID(), Name: name, Description: description, OwnerEmail: actor.Email, CreatedBy: actor.Email,
		Environments: []site.Environment{{Name: site.EnvDefault}},
	}
	if err := st.Validate(); err != nil {
		return nil, refused("%s", err.Error())
	}
	if s.Apps != nil {
		if _, err := s.Apps.GetByName(ctx, name); err == nil {
			return nil, refused("%q is the name of an application, and a site and an application cannot share a name", name)
		}
	}
	if err := s.Store.Create(ctx, st); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	return s.Store.Get(ctx, st.ID) //nolint:wrapcheck // read by identity
}

// UpdateRequest is a change to a site's record; a nil field is left alone.
type UpdateRequest struct {
	Description  *string
	Owner        *string
	InternalView *bool
}

// Update changes a site's record. The description and the owner are the
// owner's and administrators'; opening the site to everyone internal is
// sharing it, so the people who manage it can do that too.
func (s *Service) Update(ctx context.Context, actor Actor, ref string, u UpdateRequest) (*site.Site, error) {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := s.applyUpdate(ctx, actor, st, u); err != nil {
		return nil, err
	}
	if err := s.Store.Update(ctx, st); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	return s.Store.Get(ctx, st.ID) //nolint:wrapcheck // read by identity
}

func (s *Service) applyUpdate(ctx context.Context, actor Actor, st *site.Site, u UpdateRequest) error {
	if u.Description != nil || u.Owner != nil {
		if err := s.mayOwn(ctx, actor, st); err != nil {
			return err
		}
	}
	if u.InternalView != nil {
		if err := s.mayEdit(ctx, actor, st); err != nil {
			return err
		}
		st.InternalView = *u.InternalView
	}
	if u.Description != nil {
		if len(*u.Description) > site.MaxDescription {
			return refused("description: at most %d bytes", site.MaxDescription)
		}
		st.Description = *u.Description
	}
	if u.Owner != nil {
		return s.moveOwner(ctx, st, *u.Owner)
	}
	return nil
}

func (s *Service) moveOwner(ctx context.Context, st *site.Site, owner string) error {
	email := site.NormalizeEmail(owner)
	if !strings.Contains(email, "@") {
		return refused("owner: an email address")
	}
	if s.External != nil && s.External(ctx, email) {
		return refused("%s is external, and only internal people can own a site", email)
	}
	st.OwnerEmail = email
	return nil
}

// Delete removes a site. Administrators only: a site can hold live domains.
func (s *Service) Delete(ctx context.Context, actor Actor, ref string) error {
	if !actor.Admin {
		return errNeedsAdmin
	}
	st, err := s.Find(ctx, ref)
	if err != nil {
		return err
	}
	keys := s.objectKeys(ctx, st.ID)
	if err := s.Store.Delete(ctx, st.ID); err != nil {
		return err //nolint:wrapcheck // read by identity
	}
	s.removeObjects(ctx, keys)
	return nil
}

// objectKeys lists every object the site's releases hold, read before the
// rows that name them go.
func (s *Service) objectKeys(ctx context.Context, siteID string) []string {
	rels, err := s.Store.Releases(ctx, siteID)
	if err != nil {
		return nil
	}
	keys := []string{}
	for _, r := range rels {
		files, err := s.Store.Files(ctx, r.ID)
		if err != nil {
			continue
		}
		for _, f := range files {
			keys = append(keys, ObjectKey(siteID, r.ID, "", f.Path))
			if f.GzipSize > 0 {
				keys = append(keys, ObjectKey(siteID, r.ID, "gz", f.Path))
			}
			if f.BrotliSize > 0 {
				keys = append(keys, ObjectKey(siteID, r.ID, "br", f.Path))
			}
		}
	}
	return keys
}

// removeObjects deletes a deleted site's objects, a few at a time. A failure
// leaves an object behind and is logged: the site is already gone.
func (s *Service) removeObjects(ctx context.Context, keys []string) {
	if s.Objects == nil {
		return
	}
	var g errgroup.Group
	g.SetLimit(parallelUploads)
	for _, key := range keys {
		g.Go(func() error {
			if err := s.Objects.DeleteObject(context.WithoutCancel(ctx), s.Bucket, key); err != nil {
				slog.WarnContext(ctx, "sites: removing a deleted site's object failed", "error", logsan.SanitizeForLog(err.Error()))
			}
			return nil
		})
	}
	_ = g.Wait()
}

// SetEnvironment adds an environment or changes whether it requires approval.
// Taking the rule off an environment is an administrator's: it is what the
// approval exists to hold.
func (s *Service) SetEnvironment(ctx context.Context, actor Actor, ref, name string, requiresApproval bool) (*site.Site, error) {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := s.mayEdit(ctx, actor, st); err != nil {
		return nil, err
	}
	if err := site.ValidateEnvironment(name); err != nil {
		return nil, refused("%s", err.Error())
	}
	if env := st.Environment(name); env != nil && env.RequiresApproval && !requiresApproval && !actor.Admin {
		return nil, fmt.Errorf("an administrator removes an environment's approval rule: %w", site.ErrNotAllowed)
	}
	if err := s.Store.SetEnvironment(ctx, st.ID, name, requiresApproval, actor.Email); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	return s.Store.Get(ctx, st.ID) //nolint:wrapcheck // read by identity
}

// repoint is one decided move of an environment's pointer.
type repoint struct {
	st     *site.Site
	env    *site.Environment
	to     int
	action string
}

func (s *Service) move(ctx context.Context, actor Actor, m repoint) error {
	err := s.Store.Point(ctx, site.Pointer{
		SiteID: m.st.ID, Environment: m.env.Name, After: m.env.Release, To: m.to, Action: m.action, Actor: actor.Email,
	})
	if err != nil {
		return err //nolint:wrapcheck // read by identity
	}
	if s.Invalidate != nil {
		s.Invalidate(m.st.Name)
	}
	return nil
}

// OwnAddress is where a site's own address answers, "" with no pages domain.
func (s *Service) OwnAddress(siteName string) string {
	domain := s.cfg().PagesDomain
	if domain == "" {
		return ""
	}
	return s.Origin(siteName + "." + domain)
}

// Address is where an environment answers: the site's own address for the
// default environment, otherwise the first domain added to it, "" for none.
func (s *Service) Address(ctx context.Context, st *site.Site, environment string) string {
	if environment == site.EnvDefault {
		return s.OwnAddress(st.Name)
	}
	domains, err := s.Store.SiteDomains(ctx, st.ID)
	if err != nil {
		return ""
	}
	for _, d := range domains {
		if d.Environment == environment {
			return s.Origin(d.Hostname)
		}
	}
	return ""
}

// PageOrigin is where an application's pages answer, "" with no pages domain.
func (s *Service) PageOrigin(applicationName string) string {
	domain := s.cfg().PagesDomain
	if domain == "" {
		return ""
	}
	return s.Origin(applicationName + "." + domain)
}

// Origin is the browser origin a hostname answers at.
func (s *Service) Origin(host string) string {
	out := s.PublicScheme + "://" + host
	if s.PublicPort != "" {
		out += ":" + s.PublicPort
	}
	return out
}

// LatestRelease is a site's newest release number, 0 for none.
func (s *Service) LatestRelease(ctx context.Context, ref string) (int, error) {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return 0, err
	}
	rels, err := s.Store.Releases(ctx, st.ID)
	if err != nil || len(rels) == 0 {
		return 0, err //nolint:wrapcheck // read by identity
	}
	return rels[0].Number, nil
}

// EnvironmentOwner is the owner of the site holding the environment, for an
// application adding the environment as a part.
func (s *Service) EnvironmentOwner(ctx context.Context, ref, environment string) (string, error) {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return "", err
	}
	if st.Environment(environment) == nil {
		return "", noEnvironment(environment)
	}
	return st.OwnerEmail, nil
}

// ReleaseExists reports whether a site has the numbered release.
func (s *Service) ReleaseExists(ctx context.Context, ref string, number int) bool {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return false
	}
	_, err = s.Store.Release(ctx, st.ID, number)
	return err == nil
}

// ApplyPin points an environment at the release an application's release or
// rollback pins. An application release is an administrator's act, so it
// needs no separate approval.
func (s *Service) ApplyPin(ctx context.Context, actorEmail, siteRef, environment string, number int) error {
	st, err := s.Find(ctx, siteRef)
	if err != nil {
		return err
	}
	env := st.Environment(environment)
	if env == nil {
		return noEnvironment(environment)
	}
	if env.Release == number {
		return nil
	}
	return s.move(ctx, Actor{Email: actorEmail, Admin: true}, repoint{st: st, env: env, to: number, action: site.ActionApplication})
}
