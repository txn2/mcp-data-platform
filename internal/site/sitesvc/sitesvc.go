// Package sitesvc is what can be done to a site (#2518): the one service the
// manage_site tool and an application's release use. It decides who may do
// what, writes a release's files once under their own prefix, and moves an
// environment's pointer for a promote or a rollback, which copies nothing.
package sitesvc

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitestore"
)

// Objects stores a release's files.
type Objects interface {
	PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error
	GetObject(ctx context.Context, bucket, key string) ([]byte, string, error)
	DeleteObject(ctx context.Context, bucket, key string) error
}

// Applications finds the application a hostname maps to.
type Applications interface {
	GetByName(ctx context.Context, name string) (*application.Application, error)
}

// Approvals is the queue a promotion that requires approval goes through.
type Approvals interface {
	SubmitRelease(ctx context.Context, actor approval.Actor, in approval.SubmitRelease) (*approval.Request, error)
	ReleaseApproved(ctx context.Context, releaseID, target, digest string) bool
	RecordReleaseSelfApproval(ctx context.Context, actor approval.Actor, in approval.SubmitRelease) error
	// SubmitEnvironment files a request to make an environment public.
	SubmitEnvironment(ctx context.Context, actor approval.Actor, in approval.SubmitEnvironment) (*approval.Request, error)
	// EnvironmentApproved reports whether making an environment public is
	// approved for the access it was asked against.
	EnvironmentApproved(ctx context.Context, siteID, target, stamp string) bool
}

// Invalidate tells this replica's gateway a route changed; nil does nothing.
type Invalidate func(hostOrSite string)

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
	// PersonaExists reports whether a persona a grant names exists; nil takes
	// any name.
	PersonaExists func(name string) bool
	// MediaFolders checks a folder a site mounts and MediaStore keeps the
	// mounts (#2591); either nil refuses every mount.
	MediaFolders MediaFolders
	MediaStore   MediaStore
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
	// rendering lets one render run at a time on a replica, which is what
	// bounds the memory they can hold.
	rendering sync.Mutex
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

// RefusedError is a command refused for a reason the caller can act on. Err,
// when set, is the error it stands for, read by identity.
type RefusedError struct {
	Msg string
	Err error
}

func (e *RefusedError) Error() string { return e.Msg }

// Unwrap is the error the refusal stands for, nil for none.
func (e *RefusedError) Unwrap() error { return e.Err }

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

// Create makes a site with dev, its working copy, and prod, holding the main
// address (#2617); its creator is its owner. A site and an application share
// the name space of the pages domain, so a name one of them holds is refused,
// and so is a site whose dev or prod address could not be one DNS label.
func (s *Service) Create(ctx context.Context, actor Actor, name, description string) (*site.Site, error) {
	if actor.Email == "" {
		return nil, refused("a site needs an owner with an email")
	}
	st := &site.Site{
		ID: sitestore.NewSiteID(), Name: name, Description: description, OwnerEmail: actor.Email, CreatedBy: actor.Email,
		Environments: []site.Environment{{Name: site.EnvDev}, {Name: site.EnvProd}}, MainEnvironment: site.EnvProd,
	}
	if err := st.Validate(); err != nil {
		return nil, refusedBy(err)
	}
	if err := s.notAnApplication(ctx, name); err != nil {
		return nil, err
	}
	for _, env := range []string{site.EnvDev, site.EnvProd} {
		if err := site.ValidateLabel(name, env); err != nil {
			return nil, refusedBy(err)
		}
		if err := s.notAnApplication(ctx, site.EnvLabel(name, env)); err != nil {
			return nil, err
		}
	}
	if err := s.Store.Create(ctx, st); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	return s.Store.Get(ctx, st.ID) //nolint:wrapcheck // read by identity
}

// notAnApplication refuses a pages-domain label an application holds.
func (s *Service) notAnApplication(ctx context.Context, label string) error {
	if s.Apps == nil {
		return nil
	}
	if _, err := s.Apps.GetByName(ctx, label); err == nil {
		return refused("%q is the name of an application, and a site address and an application cannot share a name", label)
	}
	return nil
}

// UpdateRequest is a change to a site's record; a nil field is left alone.
type UpdateRequest struct {
	Description *string
	Owner       *string
	// Media, when set, makes the site's mounts exactly these (#2591).
	Media *[]site.Mount
}

// Update changes a site's record: the description and the owner, which are
// the owner's and administrators'.
func (s *Service) Update(ctx context.Context, actor Actor, ref string, u UpdateRequest) (*site.Site, error) {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := s.applyUpdate(ctx, actor, st, u); err != nil {
		return nil, err
	}
	if u.Media != nil {
		if err := s.setMedia(ctx, actor, st, *u.Media); err != nil {
			return nil, err
		}
	}
	if u.Description != nil || u.Owner != nil {
		if err := s.Store.Update(ctx, st); err != nil {
			return nil, err //nolint:wrapcheck // read by identity
		}
	}
	return s.Store.Get(ctx, st.ID) //nolint:wrapcheck // read by identity
}

func (s *Service) applyUpdate(ctx context.Context, actor Actor, st *site.Site, u UpdateRequest) error {
	if u.Description != nil || u.Owner != nil {
		if err := s.mayOwn(ctx, actor, st); err != nil {
			return err
		}
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

// objectKeys lists every object the site holds, read before the rows that name
// them go.
func (s *Service) objectKeys(ctx context.Context, siteID string) []string {
	files, err := s.Store.StoredFiles(ctx, siteID)
	if err != nil {
		slog.WarnContext(ctx, "sites: listing a deleted site's objects failed", "error", logsan.SanitizeForLog(err.Error()))
		return nil
	}
	keys := make([]string, 0, len(files))
	for _, f := range files {
		keys = append(keys, ObjectKey(siteID, f, ""))
		if f.GzipSize > 0 {
			keys = append(keys, ObjectKey(siteID, f, "gz"))
		}
		if f.BrotliSize > 0 {
			keys = append(keys, ObjectKey(siteID, f, "br"))
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

// EnvironmentChange creates an environment or changes it; a nil field is left
// alone.
type EnvironmentChange struct {
	RequiresApproval *bool
	// Public is refused (#2619): public is a setting of a domain. It is read
	// only so the refusal can say so.
	Public       *bool
	InternalView *bool
	// Keep sets how many releases the environment keeps (0 keeps every one),
	// and KeepDefault returns it to the deployment's default (#2602).
	Keep        *int
	KeepDefault bool
	// Place is where a new environment goes, or where an existing one moves
	// to (#2617).
	Place *site.Place
}

// EnvironmentResult is what setting an environment did.
type EnvironmentResult struct {
	Site *site.Site `json:"site"`
	// Status is StatusDone.
	Status string `json:"status"`
	// Note says the old environment name was read as dev.
	Note string `json:"note,omitempty"`
}

// ApprovalRequest is the request a change waits on in Approvals, named here so
// the layers above read it through the service.
type ApprovalRequest = approval.Request

// StatusDone is a change that took effect.
const StatusDone = "done"

// SetEnvironment adds an environment or changes its settings or its place. A
// new environment is placed between dev and prod: where the caller says, or,
// when there is only one such place, there. dev never requires approval, and
// dev and prod never move. Taking the approval rule off is an administrator's,
// and so is moving an environment forward past one that requires approval.
func (s *Service) SetEnvironment(ctx context.Context, actor Actor, ref, name string, c EnvironmentChange) (*EnvironmentResult, error) {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := s.mayEdit(ctx, actor, st); err != nil {
		return nil, err
	}
	name, aliased := site.CanonicalEnvironment(name)
	t := envTarget{st: st, name: name, env: st.Environment(name)}
	if err := s.checkEnvironmentChange(ctx, actor, t, c); err != nil {
		return nil, err
	}
	place, err := placeFor(actor, t, c.Place)
	if err != nil {
		return nil, err
	}
	set := site.EnvironmentSettings{RequiresApproval: c.RequiresApproval, InternalView: c.InternalView, Keep: c.Keep, KeepDefault: c.KeepDefault, Place: place}
	if err := s.Store.SetEnvironment(ctx, st.ID, name, set, actor.Email); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if s.Invalidate != nil {
		s.Invalidate(st.Name)
	}
	out := &EnvironmentResult{Status: StatusDone}
	if aliased {
		out.Note = site.AliasNote
	}
	if out.Site, err = s.Store.Get(ctx, st.ID); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	return out, nil
}

// publicOnEnvironment is the refusal of public on an environment (#2619).
func publicOnEnvironment(env string) error {
	return refused("public is a setting of a domain, not of an environment: every address of %s on the pages domain asks for a sign-in. "+
		"Add a domain to %s and set public on it (add_domain with public, or set_domain).", env, env)
}

func (s *Service) checkEnvironmentChange(ctx context.Context, actor Actor, t envTarget, c EnvironmentChange) error {
	if c.Public != nil {
		return publicOnEnvironment(t.name)
	}
	if t.env == nil {
		if err := s.checkNewEnvironment(ctx, t.st, t.name); err != nil {
			return err
		}
	}
	if t.name == site.EnvDev && isTrue(c.RequiresApproval) {
		return refused("dev takes every publish and edit directly, so it never requires approval")
	}
	if err := s.checkKeep(ctx, actor, t.st, c); err != nil {
		return err
	}
	if t.env != nil && t.env.TakesApprovalOff(c.RequiresApproval) && !actor.Admin {
		return fmt.Errorf("an administrator removes an environment's approval rule: %w", site.ErrNotAllowed)
	}
	return nil
}

// placeFor is the place an environment is written with: for a new one, the
// place asked for or the one place there is; for an existing one, the place
// it moves to, nil when it stays.
func placeFor(actor Actor, t envTarget, p *site.Place) (*site.Place, error) {
	order := t.st.Order()
	if t.env == nil {
		place, err := site.PlaceNew(order, t.name, p)
		if err != nil {
			return nil, refusedBy(err)
		}
		return place, nil
	}
	if p == nil {
		return nil, nil //nolint:nilnil // nil place: the environment stays where it is
	}
	switch t.name {
	case site.EnvDev:
		return nil, refused("dev is always first, so it cannot be moved. Move an environment between dev and prod instead.")
	case site.EnvProd:
		return nil, refused("prod is always last, so it cannot be moved. Move an environment between dev and prod instead.")
	}
	placed, err := site.Placed(order, t.name, p)
	if err != nil {
		return nil, refusedBy(err)
	}
	if gated := t.st.PassedGated(order, placed, t.name); gated != "" && !actor.Admin {
		return nil, fmt.Errorf("an administrator moves %s past %s, which requires approval: the environment after %s would take a release %s never approved: %w",
			t.name, gated, gated, gated, site.ErrNotAllowed)
	}
	return p, nil
}

// checkNewEnvironment refuses a name that is not one, whose address could not
// be one DNS label, or whose address an application holds.
func (s *Service) checkNewEnvironment(ctx context.Context, st *site.Site, name string) error {
	if err := site.ValidateEnvironment(name); err != nil {
		return refusedBy(err)
	}
	if err := site.ValidateLabel(st.Name, name); err != nil {
		return refusedBy(err)
	}
	return s.notAnApplication(ctx, site.EnvLabel(st.Name, name))
}

// isTrue reports whether an optional setting is set to true.
func isTrue(b *bool) bool { return b != nil && *b }

// refusedBy is a refusal whose reason is err's.
func refusedBy(err error) error { return &RefusedError{Msg: err.Error()} }

// envTarget is one environment of a site, which may not exist yet.
type envTarget struct {
	st   *site.Site
	name string
	env  *site.Environment
}

// SetMainEnvironment gives the site's main address to an environment, or to
// none with env "". It changes what people reach at the site's main address,
// so it is the owner's or an administrator's.
func (s *Service) SetMainEnvironment(ctx context.Context, actor Actor, ref, env string) (*site.Site, error) {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := s.mayOwn(ctx, actor, st); err != nil {
		return nil, err
	}
	env, _ = site.CanonicalEnvironment(strings.TrimSpace(env))
	if env != "" && st.Environment(env) == nil {
		return nil, noEnvironment(env)
	}
	if err := s.Store.SetMainEnvironment(ctx, st.ID, env); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if s.Invalidate != nil {
		s.Invalidate(st.Name)
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
	if errors.Is(err, site.ErrPruned) {
		// A sweep pruned the release after it was checked (#2602).
		return prunedRefusal(m.st, m.to)
	}
	if err != nil {
		return err //nolint:wrapcheck // read by identity
	}
	if s.Invalidate != nil {
		s.Invalidate(m.st.Name)
	}
	return nil
}

// Addressing is where the deployment's sites answer, as the configuration in
// force names it.
func (s *Service) Addressing() site.Addressing {
	return site.Addressing{PagesDomain: s.cfg().PagesDomain, Scheme: s.PublicScheme, Port: s.PublicPort}
}

// Address is where people reach an environment: the first of its domains,
// otherwise its address on the pages domain, "" for none.
func (s *Service) Address(ctx context.Context, st *site.Site, environment string) string {
	domains, err := s.Store.SiteDomains(ctx, st.ID)
	if err != nil {
		domains = nil
	}
	return s.Addressing().Address(st, environment, domains)
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

// SiteEnvironments is every environment of every site, for an application's
// search for parts to add, which chooses among them by its own rule (#2613).
func (s *Service) SiteEnvironments(ctx context.Context) ([]application.SiteEnvironment, error) {
	sites, err := s.Store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing sites: %w", err)
	}
	out := []application.SiteEnvironment{}
	for _, st := range sites {
		for _, e := range st.Environments {
			out = append(out, application.SiteEnvironment{Site: st.Name, Environment: e.Name, Owner: st.OwnerEmail, UpdatedAt: cmp.Or(e.UpdatedAt, st.UpdatedAt)})
		}
	}
	return out, nil
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
	if _, err := s.liveRelease(ctx, st, number); err != nil {
		return err
	}
	return s.move(ctx, Actor{Email: actorEmail, Admin: true}, repoint{st: st, env: env, to: number, action: site.ActionApplication})
}

// Media (#2591): a site serves managed-resource folders at URL prefixes on
// every environment. Mounting is a publishing act: whoever mounts must manage
// the site and be able to read the folder, and its files are then public on a
// public environment, as the grant references makes on an asset.

// MediaFolders checks a folder a caller mounts, reading the caller from ctx.
type MediaFolders interface {
	CheckFolder(ctx context.Context, m site.Mount) error
}

// MediaStore keeps a site's mounts.
type MediaStore interface {
	SetMedia(ctx context.Context, siteID string, mounts []site.Mount, actor string) error
}

// ErrNoFolder is a folder the caller cannot read or the library does not hold.
var ErrNoFolder = errors.New("no such folder")

// setMedia makes a site's mounts exactly mounts. A mount already in place
// unchanged is not checked again; each new or changed one is checked against
// the caller.
func (s *Service) setMedia(ctx context.Context, actor Actor, st *site.Site, mounts []site.Mount) error {
	if s.MediaFolders == nil || s.MediaStore == nil {
		return refused("this deployment cannot mount media: it has no managed-resource library")
	}
	if err := s.mayEdit(ctx, actor, st); err != nil {
		return err
	}
	clean, err := site.ValidateMounts(mounts)
	if err != nil {
		return refusedBy(err)
	}
	for _, m := range clean {
		if slices.ContainsFunc(st.Media, func(old site.Mount) bool { return sameMount(old, m) }) {
			continue
		}
		if err := s.MediaFolders.CheckFolder(ctx, m); err != nil {
			return &RefusedError{Err: ErrNoFolder, Msg: fmt.Sprintf(
				"media: there is no folder %q in the %s library that you can read, so it cannot be mounted at %s", m.Folder, m.Scope, m.Prefix)}
		}
	}
	if err := s.MediaStore.SetMedia(ctx, st.ID, clean, actor.Email); err != nil {
		return err //nolint:wrapcheck // read by identity
	}
	if s.Invalidate != nil {
		s.Invalidate(st.Name)
	}
	return nil
}

func sameMount(a, b site.Mount) bool {
	return a.Prefix == b.Prefix && a.Scope == b.Scope && a.ScopeID == b.ScopeID && a.Folder == b.Folder
}
