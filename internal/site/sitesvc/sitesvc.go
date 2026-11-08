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
	Store     site.Store
	Objects   Objects
	Bucket    string
	Config    siteconfig.Config
	Apps      Applications
	Approvals Approvals
	Verifier  Verifier
	// PortalHost is the portal's hostname, which no domain may claim.
	PortalHost string
	// CookieDomain is the session cookie's Domain, under which no served
	// origin may sit.
	CookieDomain string
	// PublicScheme and PublicPort are what a preview URL is written with.
	PublicScheme, PublicPort string
	Invalidate               Invalidate
}

// Actor is who is acting.
type Actor struct {
	Email   string
	Persona string
	Admin   bool
}

// RefusedError is a command refused for a reason the caller can act on.
type RefusedError struct{ Msg string }

func (e *RefusedError) Error() string { return e.Msg }

func refused(format string, args ...any) error {
	return &RefusedError{Msg: fmt.Sprintf(format, args...)}
}

var errNeedsAdmin = fmt.Errorf("administrators map hostnames and delete sites: %w", site.ErrNotAllowed)

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

func mayEdit(actor Actor, st *site.Site) error {
	if actor.Admin || (actor.Email != "" && strings.EqualFold(actor.Email, st.OwnerEmail)) {
		return nil
	}
	return fmt.Errorf("site %q is changed by its owner or an administrator: %w", st.Name, site.ErrNotAllowed)
}

// Create makes a site with dev, stage and prod environments; prod requires
// approval.
func (s *Service) Create(ctx context.Context, actor Actor, name, description string) (*site.Site, error) {
	if actor.Email == "" {
		return nil, refused("a site needs an owner with an email")
	}
	st := &site.Site{
		ID: sitestore.NewSiteID(), Name: name, Description: description, OwnerEmail: actor.Email, CreatedBy: actor.Email,
		Environments: []site.Environment{{Name: site.EnvDev}, {Name: "stage"}, {Name: site.EnvProd, RequiresApproval: true}},
	}
	if err := st.Validate(); err != nil {
		return nil, refused("%s", err.Error())
	}
	if err := s.Store.Create(ctx, st); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	return s.Store.Get(ctx, st.ID) //nolint:wrapcheck // read by identity
}

// Update changes the description, or moves the site to another owner.
func (s *Service) Update(ctx context.Context, actor Actor, ref, description, owner string) (*site.Site, error) {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := mayEdit(actor, st); err != nil {
		return nil, err
	}
	if description != "" {
		if len(description) > site.MaxDescription {
			return nil, refused("description: at most %d bytes", site.MaxDescription)
		}
		st.Description = description
	}
	if owner != "" {
		st.OwnerEmail = owner
	}
	if err := s.Store.Update(ctx, st); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	return s.Store.Get(ctx, st.ID) //nolint:wrapcheck // read by identity
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
	if err := mayEdit(actor, st); err != nil {
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

// PreviewURL is where an environment answers without a domain, "" with no
// pages domain configured.
func (s *Service) PreviewURL(siteName, environment string) string {
	if s.Config.PagesDomain == "" {
		return ""
	}
	return s.Origin(environment + site.PreviewSeparator + siteName + "." + s.Config.PagesDomain)
}

// PageOrigin is where an application's pages answer, "" with no pages domain.
func (s *Service) PageOrigin(applicationName string) string {
	if s.Config.PagesDomain == "" {
		return ""
	}
	return s.Origin(applicationName + "." + s.Config.PagesDomain)
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
