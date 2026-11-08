// Package appsvc is what a person does to an application (#2411), the one
// implementation the manage_application tool and the portal routes share:
// create it, edit its record and parts, release it, roll it back, and read
// where it stands. It holds the rules: everyone signed in reads an
// application; its owner and administrators edit it; a part is added by
// someone who owns that part (a webhook source, which only administrators
// manage, by an administrator); and only administrators release and roll
// back, because a release changes the code behind a public endpoint and runs
// migrations on the target database.
package appsvc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appdiagram"
	"github.com/txn2/mcp-data-platform/internal/application/appschema"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Scripts reads scripts and their versions.
type Scripts interface {
	GetByID(ctx context.Context, id string) (*script.Script, error)
	GetVersion(ctx context.Context, scriptID string, version int) (*script.Version, error)
}

// Assets reads portal assets.
type Assets interface {
	Get(ctx context.Context, id string) (*portaldomain.Asset, error)
}

// Sources reports whether a webhook source exists.
type Sources interface {
	Exists(ctx context.Context, name string) (bool, error)
}

// Schema is what a release does to the application's schema.
type Schema interface {
	State(ctx context.Context, connection, schema string) (*appschema.State, error)
	Release(ctx context.Context, connection, schema, actor string) (*appschema.Applied, error)
	Rollback(ctx context.Context, connection, schema, actor string, to int) (*appschema.Reverted, error)
}

// Service is the application operations.
type Service struct {
	Store         application.Store
	Scripts       Scripts
	Assets        Assets
	AssetVersions portaldomain.VersionReader
	Sources       Sources
	Schema        Schema
	// HasConnection reports whether an application database connection
	// (kind appdb) is named so; nil checks nothing.
	HasConnection func(name string) bool
	// Schedules, Activity and Lister describe parts and list the ones that
	// may be added (#2527); nil leaves out what each reads.
	Schedules Schedules
	Activity  SourceActivity
	Lister    CandidateLister
	// Roles and Sealer create an application's run role when its schema is
	// added and keep its password sealed (#2539); without both no schema is
	// added. Reach lists the application database connections an actor may
	// hold a schema on; Members reports whether a role is a member of
	// another on a connection. Nil leaves out what each answers.
	Roles   Roles
	Sealer  Sealer
	Reach   func(ctx context.Context, actor application.Actor) ([]string, error)
	Members func(ctx context.Context, connection, member, role string) bool
	// Diagrams is what an application's diagram reads besides the stores
	// above (#2540).
	Diagrams DiagramDeps
	// Sites reads a site part and points its environment at a release
	// (#2518); nil adds no site.
	Sites Sites
}

// Sites is what an application does with its site parts.
type Sites interface {
	// EnvironmentOwner is the owner of the site holding the environment.
	EnvironmentOwner(ctx context.Context, site, environment string) (string, error)
	LatestRelease(ctx context.Context, site string) (int, error)
	ReleaseExists(ctx context.Context, site string, number int) bool
	ApplyPin(ctx context.Context, actorEmail, site, environment string, number int) error
}

// RefusedError is an operation refused for a reason the caller can act on.
type RefusedError struct{ Msg string }

func (e *RefusedError) Error() string { return e.Msg }

// The words a pin's failure is written with.
const (
	siteLabel = "site "
	labelSep  = ": "
)

func refused(format string, args ...any) error {
	return &RefusedError{Msg: fmt.Sprintf(format, args...)}
}

// errNeedsAdmin is the refusal of a release or rollback by someone who is not
// an administrator.
var errNeedsAdmin = fmt.Errorf("releasing and rolling back an application is for administrators, since a release "+
	"changes what its endpoints run and applies its migrations: %w", application.ErrNotAllowed)

// Find reads an application by id or name.
func (s *Service) Find(ctx context.Context, ref string) (*application.Application, error) {
	if strings.HasPrefix(ref, "app_") {
		return s.Store.Get(ctx, ref) //nolint:wrapcheck // the store's errors are read by identity
	}
	return s.Store.GetByName(ctx, ref) //nolint:wrapcheck // the store's errors are read by identity
}

// Create makes an application owned by the actor.
func (s *Service) Create(ctx context.Context, actor application.Actor, a application.Application) (*application.Application, error) {
	if actor.Email == "" {
		return nil, refused("an application belongs to a person: sign in with an account that has an email")
	}
	a.ID, a.CurrentRelease, a.Parts = "", 0, nil
	a.OwnerEmail, a.CreatedBy = actor.Email, actor.Email
	if err := a.Validate(); err != nil {
		return nil, &RefusedError{Msg: err.Error()}
	}
	if a.Connection != "" && s.HasConnection != nil && !s.HasConnection(a.Connection) {
		// Stored, a mistyped name surfaced only later, as a problem on the
		// application's page (#2491).
		return nil, refused("connection %q is not an application database connection (kind appdb) on this deployment; "+
			"name one, or leave connection and schema empty for an application with no database", a.Connection)
	}
	// A schema named at create is added as add_part adds one (#2539): the
	// application is created, then takes the schema on, and is not kept
	// when the schema is refused.
	connection, schema := a.Connection, a.Schema
	a.Connection, a.Schema = "", ""
	if err := s.Store.Create(ctx, &a); err != nil {
		return nil, err //nolint:wrapcheck // the store's errors are read by identity
	}
	if schema == "" {
		return &a, nil
	}
	held, err := s.addSchema(ctx, actor, &a, application.SchemaPartID(connection, schema))
	if err != nil {
		if delErr := s.Store.Delete(context.WithoutCancel(ctx), a.ID); delErr != nil {
			return nil, fmt.Errorf("the application the schema was refused for was not removed (%w): %w", delErr, err)
		}
		return nil, err
	}
	return held, nil
}

// Update writes the description and, for an administrator, the owner.
func (s *Service) Update(ctx context.Context, actor application.Actor, ref, description, owner string) (*application.Application, error) {
	a, err := s.editable(ctx, actor, ref)
	if err != nil {
		return nil, err
	}
	a.Description = description
	if owner != "" && owner != a.OwnerEmail {
		if !actor.Admin {
			return nil, fmt.Errorf("an administrator moves an application to another owner: %w", application.ErrNotAllowed)
		}
		a.OwnerEmail = owner
	}
	if err := a.Validate(); err != nil {
		return nil, &RefusedError{Msg: err.Error()}
	}
	if err := s.Store.Update(ctx, a); err != nil {
		return nil, err //nolint:wrapcheck // the store's errors are read by identity
	}
	return a, nil
}

// Delete removes an application; its parts stay and run their newest
// versions again.
func (s *Service) Delete(ctx context.Context, actor application.Actor, ref string) error {
	a, err := s.editable(ctx, actor, ref)
	if err != nil {
		return err
	}
	// Deleting a released application puts each pinned part's newest version
	// live, which is what a release does.
	if a.CurrentRelease > 0 && !actor.Admin {
		return fmt.Errorf("deleting a released application changes what it serves, which is for administrators: %w", application.ErrNotAllowed)
	}
	// Its run role goes with it; the schema and its data stay (#2539).
	if a.Schema != "" && s.Roles != nil {
		if err := s.Roles.DropRunRole(ctx, a.Connection, a.Schema); err != nil {
			return fmt.Errorf("dropping the application's run role: %w", err)
		}
	}
	return s.Store.Delete(ctx, a.ID) //nolint:wrapcheck // the store's errors are read by identity
}

func (s *Service) editable(ctx context.Context, actor application.Actor, ref string) (*application.Application, error) {
	a, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !a.MayEdit(actor) {
		return nil, fmt.Errorf("application %q is edited by its owner and administrators: %w", a.Name, application.ErrNotAllowed)
	}
	return a, nil
}

// AddPart adds a part the actor owns.
func (s *Service) AddPart(ctx context.Context, actor application.Actor, ref, kind, id string) (*application.Application, error) {
	a, err := s.editable(ctx, actor, ref)
	if err != nil {
		return nil, err
	}
	if !application.ValidKind(kind) {
		return nil, refused("kind %q: one of script, asset, source, schema, site", kind)
	}
	if kind == application.KindSchema {
		return s.addSchema(ctx, actor, a, id)
	}
	if err := s.mayAdd(ctx, actor, kind, id); err != nil {
		return nil, err
	}
	if kind == application.KindScript {
		if err := s.staysInside(ctx, a, id); err != nil {
			return nil, err
		}
	}
	if err := s.Store.AddPart(ctx, a.ID, application.Part{Kind: kind, ID: id, AddedBy: actor.Email}); err != nil {
		if errors.Is(err, application.ErrPartTaken) {
			return nil, s.taken(ctx, kind, id)
		}
		return nil, err //nolint:wrapcheck // the store's errors are read by identity
	}
	return s.Store.Get(ctx, a.ID) //nolint:wrapcheck // the store's errors are read by identity
}

// taken names the application holding a part.
func (s *Service) taken(ctx context.Context, kind, id string) error {
	if other, err := s.Store.Owning(ctx, kind, id); err == nil {
		return fmt.Errorf("part %s %s belongs to application %q: %w", kind, id, other.Name, application.ErrPartTaken)
	}
	return application.ErrPartTaken
}

// mayAdd checks that the part exists and that the actor owns it.
func (s *Service) mayAdd(ctx context.Context, actor application.Actor, kind, id string) error {
	switch kind {
	case application.KindScript:
		return s.mayAddScript(ctx, actor, id)
	case application.KindAsset:
		return s.mayAddAsset(ctx, actor, id)
	case application.KindSource:
		return s.mayAddSource(ctx, actor, id)
	case application.KindSite:
		return s.mayAddSite(ctx, actor, id)
	}
	return nil
}

func (s *Service) mayAddSite(ctx context.Context, actor application.Actor, id string) error {
	name, env, ok := application.ParseSitePartID(id)
	if !ok {
		return refused("site part %q: \"<site>/<environment>\"", id)
	}
	if s.Sites == nil {
		return refused("this deployment has no sites")
	}
	owner, err := s.Sites.EnvironmentOwner(ctx, name, env)
	if err != nil {
		return refused("site part %q: %s", id, err.Error())
	}
	if !actor.Admin && !strings.EqualFold(owner, actor.Email) {
		return fmt.Errorf("site %q is added to an application by its owner or an administrator: %w", name, application.ErrNotAllowed)
	}
	return nil
}

// applySitePins points each site part's environment at the release pins
// hold. An application release or rollback is an administrator's, so it
// stands in for the environment's own approval.
func (s *Service) applySitePins(ctx context.Context, actor application.Actor, pins []application.Pin) error {
	for _, p := range pins {
		if p.Kind != application.KindSite || s.Sites == nil {
			continue
		}
		name, env, ok := application.ParseSitePartID(p.PartID)
		if !ok {
			continue
		}
		if err := s.Sites.ApplyPin(ctx, actor.Email, name, env, p.Version); err != nil {
			return fmt.Errorf("the release was recorded, but site %s was not pointed at release %d, which releasing again does: %w", p.PartID, p.Version, err)
		}
	}
	return nil
}

func (s *Service) mayAddScript(ctx context.Context, actor application.Actor, id string) error {
	sc, err := s.script(ctx, id)
	if err != nil {
		return err
	}
	if !actor.Admin && !sc.OwnedBy(actor.Email) {
		return fmt.Errorf("script %q is added to an application by its owner or an administrator: %w", sc.Name, application.ErrNotAllowed)
	}
	return nil
}

func (s *Service) mayAddAsset(ctx context.Context, actor application.Actor, id string) error {
	as, err := s.asset(ctx, id)
	if err != nil {
		return err
	}
	if !actor.Admin && !strings.EqualFold(as.OwnerEmail, actor.Email) {
		return fmt.Errorf("asset %q is added to an application by its owner or an administrator: %w", as.Name, application.ErrNotAllowed)
	}
	return nil
}

func (s *Service) mayAddSource(ctx context.Context, actor application.Actor, name string) error {
	if !actor.Admin {
		return fmt.Errorf("webhook sources are managed by administrators, who add them to an application: %w", application.ErrNotAllowed)
	}
	if s.Sources == nil {
		return refused("this deployment has no webhook sources")
	}
	ok, err := s.Sources.Exists(ctx, name)
	if err != nil {
		return fmt.Errorf("reading webhook source %q: %w", name, err)
	}
	if !ok {
		return refused("no webhook source named %q", name)
	}
	return nil
}

func (s *Service) script(ctx context.Context, id string) (*script.Script, error) {
	if _, err := uuid.Parse(id); err != nil || s.Scripts == nil {
		return nil, refused("no script with id %q", id)
	}
	sc, err := s.Scripts.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading script %s: %w", id, err)
	}
	if sc == nil {
		return nil, refused("no script with id %q", id)
	}
	return sc, nil
}

func (s *Service) asset(ctx context.Context, id string) (*portaldomain.Asset, error) {
	if s.Assets == nil {
		return nil, refused("no asset with id %q", id)
	}
	as, err := s.Assets.Get(ctx, id)
	if err != nil || as == nil || as.DeletedAt != nil {
		return nil, refused("no asset with id %q", id)
	}
	return as, nil
}

// RemovePart removes a part; it runs or serves its newest version again.
func (s *Service) RemovePart(ctx context.Context, actor application.Actor, ref, kind, id string) (*application.Application, error) {
	a, err := s.editable(ctx, actor, ref)
	if err != nil {
		return nil, err
	}
	// Removing a part the current release pins puts its newest version live,
	// which is what a release does.
	if !actor.Admin && a.CurrentRelease > 0 {
		cur, err := s.Store.Release(ctx, a.ID, a.CurrentRelease)
		if err != nil {
			return nil, err //nolint:wrapcheck // the store's errors are read by identity
		}
		if cur.PinOf(kind, id) > 0 || kind == application.KindSchema {
			return nil, fmt.Errorf("removing a part the current release pins changes what the application serves, "+
				"which is for administrators: %w", application.ErrNotAllowed)
		}
	}
	if kind == application.KindSchema {
		return s.removeSchema(ctx, a, id)
	}
	if err := s.Store.RemovePart(ctx, a.ID, kind, id); err != nil {
		return nil, err //nolint:wrapcheck // the store's errors are read by identity
	}
	return s.Store.Get(ctx, a.ID) //nolint:wrapcheck // the store's errors are read by identity
}

// Status is where an application stands: its record, its current release,
// and what a release now would change.
type Status struct {
	Application *application.Application `json:"application"`
	Current     *application.Release     `json:"current_release"`
	// Pending is the parts whose newest version differs from the current
	// release's pin.
	Pending []application.PinChange `json:"pending"`
	// Migrations is where the schema's migrations stand; nil for an
	// application with no schema.
	Migrations *appschema.State `json:"migrations,omitempty"`
	// Schema is the schema part (#2539): its migration version, the one the
	// current release recorded, and its run role; nil for none.
	Schema *SchemaView `json:"schema,omitempty"`
	// Problems are parts a release would refuse (deleted, or unreadable),
	// and what the application's diagram finds (#2540).
	Problems []string `json:"problems"`
	// Findings are the diagram's findings, each saying whether a release
	// refuses it.
	Findings []appdiagram.Finding `json:"findings"`
}

// StatusOf reads where an application stands.
func (s *Service) StatusOf(ctx context.Context, ref string) (*Status, error) {
	a, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	st := &Status{Application: a, Pending: []application.PinChange{}, Problems: []string{}}
	if a.CurrentRelease > 0 {
		if st.Current, err = s.Store.Release(ctx, a.ID, a.CurrentRelease); err != nil {
			return nil, err //nolint:wrapcheck // the store's errors are read by identity
		}
	}
	pins, problems := s.newestPins(ctx, a)
	st.Problems = problems
	var prev []application.Pin
	if st.Current != nil {
		prev = st.Current.Pins
	}
	st.Pending = application.Diff(prev, pins)
	if a.Schema != "" && s.Schema != nil {
		if m, err := s.Schema.State(ctx, a.Connection, a.Schema); err == nil {
			st.Migrations = m
		} else {
			st.Problems = append(st.Problems, "schema "+a.Connection+"/"+a.Schema+": "+err.Error())
		}
	}
	st.Schema = s.SchemaOf(ctx, a, st.Current)
	st.Findings = s.findings(ctx, a)
	if st.Findings == nil {
		st.Findings = []appdiagram.Finding{}
	}
	st.Problems = append(st.Problems, appdiagram.Problems(&appdiagram.Diagram{Findings: st.Findings})...)
	return st, nil
}

// newestPins pins each script and asset part at its newest version, naming
// the parts that cannot be pinned.
func (s *Service) newestPins(ctx context.Context, a *application.Application) (pins []application.Pin, problems []string) {
	pins, problems = []application.Pin{}, []string{}
	for _, p := range a.Parts {
		pin, problem := s.newestPin(ctx, p)
		if problem != "" {
			problems = append(problems, problem)
		} else if pin.Version > 0 {
			pins = append(pins, pin)
		}
	}
	return pins, problems
}

// newestPin is one part pinned at its newest version, or why it cannot be;
// a webhook source pins nothing.
func (s *Service) newestPin(ctx context.Context, p application.Part) (pin application.Pin, problem string) {
	pin = application.Pin{Kind: p.Kind, PartID: p.ID}
	switch p.Kind {
	case application.KindScript:
		sc, err := s.script(ctx, p.ID)
		if err != nil {
			return pin, "script " + p.ID + labelSep + err.Error()
		}
		pin.Version = sc.Version
	case application.KindAsset:
		as, err := s.asset(ctx, p.ID)
		if err != nil {
			return pin, "asset " + p.ID + labelSep + err.Error()
		}
		pin.Version = as.CurrentVersion
	case application.KindSite:
		name, env, ok := application.ParseSitePartID(p.ID)
		if !ok || s.Sites == nil {
			return pin, siteLabel + p.ID + " cannot be read"
		}
		if _, err := s.Sites.EnvironmentOwner(ctx, name, env); err != nil {
			return pin, siteLabel + p.ID + labelSep + err.Error()
		}
		n, err := s.Sites.LatestRelease(ctx, name)
		if err != nil {
			return pin, siteLabel + p.ID + labelSep + err.Error()
		}
		if n == 0 {
			return pin, siteLabel + p.ID + " has no release to pin; publish one first"
		}
		pin.Version = n
	case application.KindSource:
		if s.Sources == nil {
			return pin, ""
		}
		if ok, err := s.Sources.Exists(ctx, p.ID); err != nil || !ok {
			return pin, "webhook source " + p.ID + " no longer exists"
		}
	}
	return pin, ""
}

// Release applies the application's pending migrations and pins every script
// and asset part at its newest version. A part that cannot be pinned, or a
// migration that fails, refuses the release and changes nothing.
func (s *Service) Release(ctx context.Context, actor application.Actor, ref, note string) (*application.Release, error) {
	return s.ReleaseAs(ctx, actor, ref, note, application.ActionRelease)
}

// ReleaseAs is Release recorded as action: an import's release is recorded
// as one.
func (s *Service) ReleaseAs(ctx context.Context, actor application.Actor, ref, note, action string) (*application.Release, error) {
	if !actor.Admin {
		return nil, errNeedsAdmin
	}
	a, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	pins, problems := s.newestPins(ctx, a)
	if len(problems) > 0 {
		return nil, refused("nothing was released: %s", strings.Join(problems, "; "))
	}
	// A part loading one of the application's libraries at a version other
	// than the one this release pins is refused; the other findings are
	// recorded on the release (#2540).
	found := s.findings(ctx, a)
	if err := blocking(found); err != nil {
		return nil, err
	}
	prev, err := s.current(ctx, a)
	if err != nil {
		return nil, err
	}
	rel := application.Release{Action: action, Note: note, CreatedBy: actor.Email, Pins: pins}
	rel.MigrationVersion = prev.MigrationVersion
	rel.Changes.Applied = []int{}
	if a.Schema != "" {
		applied, err := s.Schema.Release(ctx, a.Connection, a.Schema, actorName(actor, a))
		if err != nil {
			return nil, fmt.Errorf("nothing was released: %w", err)
		}
		rel.Changes.Applied, rel.MigrationVersion = applied.Versions, applied.Head
		s.grantRunRole(ctx, a)
	}
	rel.Changes.Parts = application.Diff(prev.Pins, pins)
	rel.Changes.Findings = appdiagram.Problems(&appdiagram.Diagram{Findings: found})
	recorded, err := s.Store.Record(ctx, a.ID, application.Recorded{Release: rel, After: a.CurrentRelease})
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if err := s.applySitePins(ctx, actor, pins); err != nil {
		return nil, err
	}
	return recorded, nil
}

// current is the application's current release, an empty one before the
// first.
func (s *Service) current(ctx context.Context, a *application.Application) (*application.Release, error) {
	if a.CurrentRelease == 0 {
		return &application.Release{Pins: []application.Pin{}}, nil
	}
	return s.Store.Release(ctx, a.ID, a.CurrentRelease) //nolint:wrapcheck // the store's errors are read by identity
}

// actorName is who a release's migrations are recorded as applied by.
func actorName(actor application.Actor, a *application.Application) string {
	return actor.Email + " (application " + a.Name + ")"
}

// Rollback restores the pins of an earlier release — to, or the release
// before the current one when to is 0 — and reverts the migrations applied
// since when each has a down migration; the rollback records which it
// reverted, or why it reverted none.
func (s *Service) Rollback(ctx context.Context, actor application.Actor, ref string, to int) (*application.Release, error) {
	if !actor.Admin {
		return nil, errNeedsAdmin
	}
	a, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	target, err := s.rollbackTarget(ctx, a, to)
	if err != nil {
		return nil, err
	}
	prev, err := s.current(ctx, a)
	if err != nil {
		return nil, err
	}
	if err := s.pinsReadable(ctx, target.Pins); err != nil {
		return nil, err
	}
	rel := application.Release{
		Action: application.ActionRollback, Restored: target.Number, CreatedBy: actor.Email,
		Pins: target.Pins, MigrationVersion: prev.MigrationVersion,
	}
	rel.Changes.Reverted = []int{}
	if a.Schema != "" {
		// Migrations applied before the schema was added were applied by no
		// release, and no rollback reverts them (#2539).
		to := max(target.MigrationVersion, a.SchemaAdoptedVersion)
		reverted, err := s.Schema.Rollback(ctx, a.Connection, a.Schema, actorName(actor, a), to)
		if err != nil {
			return nil, fmt.Errorf("nothing was rolled back: %w", err)
		}
		rel.Changes.Reverted, rel.Changes.MigrationsKept, rel.MigrationVersion = reverted.Versions, reverted.Kept, reverted.Head
	}
	rel.Changes.Parts = application.Diff(prev.Pins, target.Pins)
	recorded, err := s.Store.Record(ctx, a.ID, application.Recorded{Release: rel, After: a.CurrentRelease})
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if err := s.applySitePins(ctx, actor, target.Pins); err != nil {
		return nil, err
	}
	return recorded, nil
}

// rollbackTarget is the release a rollback restores: to, or the one before
// the current release when to is 0.
func (s *Service) rollbackTarget(ctx context.Context, a *application.Application, to int) (*application.Release, error) {
	if to == 0 {
		to = a.CurrentRelease - 1
	}
	if to <= 0 || to >= a.CurrentRelease {
		return nil, refused("application %q has no earlier release to roll back to (current release %d)", a.Name, a.CurrentRelease)
	}
	return s.Store.Release(ctx, a.ID, to) //nolint:wrapcheck // the store's errors are read by identity
}

// pinsReadable refuses pins whose version can no longer be read.
func (s *Service) pinsReadable(ctx context.Context, pins []application.Pin) error {
	for _, p := range pins {
		if !s.readable(ctx, p) {
			return refused("nothing was rolled back: %s %s version %d cannot be read", p.Kind, p.PartID, p.Version)
		}
	}
	return nil
}

// readable reports whether a pinned version can still be read.
func (s *Service) readable(ctx context.Context, p application.Pin) bool {
	switch p.Kind {
	case application.KindScript:
		if s.Scripts == nil {
			return false
		}
		v, err := s.Scripts.GetVersion(ctx, p.PartID, p.Version)
		return err == nil && v != nil
	case application.KindSite:
		name, _, ok := application.ParseSitePartID(p.PartID)
		return ok && s.Sites != nil && s.Sites.ReleaseExists(ctx, name, p.Version)
	case application.KindAsset:
		if s.AssetVersions == nil {
			return false
		}
		v, err := s.AssetVersions.GetByVersion(ctx, p.PartID, p.Version)
		return err == nil && v != nil
	}
	return true
}
