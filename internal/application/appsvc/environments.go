package appsvc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/application"
)

// Environments (#2696): a release lands in an application's first
// environment, a promote copies an environment's release to the next, and a
// rollback moves one environment back. The last environment is what
// /endpoints/<name> and every handler naming no environment run. Each
// environment runs in a schema of its own (envschema.go, #2709): a release
// applies the migrations to the first environment's schema, a promote to the
// target's, and a rollback reverts them in a schema only its environment
// runs in.

// errNoEnvironments is an environment operation on a deployment wired with
// no environment store.
var errNoEnvironments = errors.New("this deployment keeps no application environments")

// landing is where a release lands: the first environment and the release
// it runs, or the application's current release when it has none listed.
func landing(a *application.Application) (env string, running int) {
	if len(a.Environments) == 0 {
		return "", a.CurrentRelease
	}
	return a.Environments[0].Name, a.Environments[0].Release
}

// releaseOf is release number of the application, an empty one for 0.
func (s *Service) releaseOf(ctx context.Context, a *application.Application, number int) (*application.Release, error) {
	if number == 0 {
		return &application.Release{Pins: []application.Pin{}}, nil
	}
	return s.Store.Release(ctx, a.ID, number) //nolint:wrapcheck // the store's errors are read by identity
}

func (s *Service) envStore() (application.EnvironmentStore, error) {
	if s.Environments == nil {
		return nil, errNoEnvironments
	}
	return s.Environments, nil
}

// environment is the application's environment named name ("" for the
// last), refused naming the ones it has.
func environment(a *application.Application, name string) (*application.Environment, int, error) {
	e, i, ok := a.EnvironmentNamed(name)
	if !ok {
		return nil, -1, refused("application %q has no environment %q; it has %s", a.Name, name, envNames(a))
	}
	return e, i, nil
}

func envNames(a *application.Application) string {
	names := make([]string, 0, len(a.Environments))
	for _, e := range a.Environments {
		names = append(names, e.Name)
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// Promoted is what a promote did.
type Promoted struct {
	From    string `json:"from" example:"dev"`
	To      string `json:"to" example:"prod"`
	Release int    `json:"release" example:"8"`
	// Replaced is the release the target ran before, 0 for none.
	Replaced int `json:"replaced" example:"6"`
	// Applied is the migrations the promote applied to the target's schema
	// (#2709), [] for none.
	Applied []int `json:"applied"`
}

// NextEnvironment is the environment a promote from env moves into.
func NextEnvironment(a *application.Application, env string) (*application.Environment, error) {
	_, i, err := environment(a, env)
	if err != nil {
		return nil, err
	}
	if i == len(a.Environments)-1 {
		return nil, refused("%s is application %q's last environment; there is nothing to promote it to", a.Environments[i].Name, a.Name)
	}
	return &a.Environments[i+1], nil
}

// Promote makes the environment after from run from's release.
func (s *Service) Promote(ctx context.Context, actor application.Actor, ref, from string) (*Promoted, error) {
	store, a, err := s.adminEnvironments(ctx, actor, ref)
	if err != nil {
		return nil, err
	}
	src, dst, err := promotion(a, from)
	if err != nil {
		return nil, err
	}
	if err := approved(actor, a, dst); err != nil {
		return nil, err
	}
	rel, err := s.releaseReadable(ctx, a, src.Release)
	if err != nil {
		return nil, err
	}
	out := &Promoted{From: src.Name, To: dst.Name, Release: src.Release, Replaced: dst.Release, Applied: []int{}}
	// The target's schema applies what the release recorded before the
	// environment moves (#2709); a migration that fails there refuses the
	// promote and leaves the target as it was.
	if a.Schema != "" {
		applied, err := s.applyToEnvironment(ctx, actor, a, dst, rel.MigrationVersion)
		if err != nil {
			return nil, fmt.Errorf("nothing was promoted: %w", err)
		}
		out.Applied = applied.Versions
	}
	move := application.Move{Environment: dst.Name, Number: src.Release, Expect: dst.Release, By: actor.Email}
	if err := store.SetEnvironmentRelease(ctx, a.ID, move); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	// The site parts calling the target move with it (#2709), to the site
	// release the source ran this release with.
	if err := s.applySitePins(ctx, actor, pinsCalling(a, rel.Pins, dst.Name)); err != nil {
		return nil, err
	}
	return out, nil
}

// adminEnvironments is the environment store and the application ref names,
// for an administrator.
func (s *Service) adminEnvironments(ctx context.Context, actor application.Actor, ref string) (application.EnvironmentStore, *application.Application, error) {
	if !actor.Admin {
		return nil, nil, errNeedsAdmin
	}
	store, err := s.envStore()
	if err != nil {
		return nil, nil, err
	}
	a, err := s.Find(ctx, ref)
	if err != nil {
		return nil, nil, err
	}
	return store, a, nil
}

// promotion is the environment a promote moves from and the one it moves
// into, refused when there is nothing to move.
func promotion(a *application.Application, from string) (src, dst *application.Environment, err error) {
	if src, _, err = environment(a, from); err != nil {
		return nil, nil, err
	}
	if dst, err = NextEnvironment(a, src.Name); err != nil {
		return nil, nil, err
	}
	if src.Release == 0 {
		return nil, nil, refused("%s runs no release yet; release application %q first", src.Name, a.Name)
	}
	if src.Release == dst.Release {
		return nil, nil, refused("%s already runs release %d", dst.Name, dst.Release)
	}
	return src, dst, nil
}

// releaseReadable is release number, refused when its pins can no longer be
// read.
func (s *Service) releaseReadable(ctx context.Context, a *application.Application, number int) (*application.Release, error) {
	rel, err := s.Store.Release(ctx, a.ID, number)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if err := s.pinsReadable(ctx, rel.Pins); err != nil {
		return nil, err
	}
	return rel, nil
}

// RollbackIn moves one environment back to an earlier release: to, or the
// release before the one it runs when to is 0. In the last environment it is
// Rollback, recorded as a release; in any other it points the environment at
// the earlier release and records nothing new.
func (s *Service) RollbackIn(ctx context.Context, actor application.Actor, ref, env string, to int) (*application.Release, error) {
	if !actor.Admin {
		return nil, errNeedsAdmin
	}
	a, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	if e, _, ok := a.EnvironmentNamed(env); ok {
		if err := approved(actor, a, e); err != nil {
			return nil, err
		}
	}
	if lastOrOnly(a, env) {
		return s.Rollback(ctx, actor, ref, to)
	}
	e, to, err := rollbackIn(a, env, to)
	if err != nil {
		return nil, err
	}
	return s.pointAt(ctx, actor, a, e, to)
}

// lastOrOnly reports whether env is the application's last environment, or
// it lists none.
func lastOrOnly(a *application.Application, env string) bool {
	return env == "" || len(a.Environments) == 0 || a.Last().Name == env
}

// pointAt makes e run release to, refused when its pins can no longer be
// read.
func (s *Service) pointAt(ctx context.Context, actor application.Actor, a *application.Application, e *application.Environment, to int) (*application.Release, error) {
	target, err := s.Store.Release(ctx, a.ID, to)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if err := s.pinsReadable(ctx, target.Pins); err != nil {
		return nil, err
	}
	store, err := s.envStore()
	if err != nil {
		return nil, err
	}
	current, err := s.releaseOf(ctx, a, e.Release)
	if err != nil {
		return nil, err
	}
	_, i, _ := a.EnvironmentNamed(e.Name)
	if _, _, err := s.revertEnvironment(ctx, actor, a, i, target.MigrationVersion); err != nil {
		return nil, fmt.Errorf("nothing was rolled back: %w", err)
	}
	if err := store.SetEnvironmentRelease(ctx, a.ID, application.Move{Environment: e.Name, Number: to, Expect: e.Release, By: actor.Email}); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	// Only the site parts calling this environment return with it (#2709).
	if err := s.applySitePins(ctx, actor, pinsCalling(a, rollbackSitePins(a, current, target), e.Name)); err != nil {
		return nil, err
	}
	return target, nil
}

// rollbackIn is the environment a rollback moves and the release it moves
// to: to, or the one before the release it runs.
func rollbackIn(a *application.Application, env string, to int) (*application.Environment, int, error) {
	e, _, err := environment(a, env)
	if err != nil {
		return nil, 0, err
	}
	if to == 0 {
		to = e.Release - 1
	}
	if to <= 0 || to >= e.Release {
		return nil, 0, refused("%s has no earlier release to roll back to (it runs release %d)", e.Name, e.Release)
	}
	return e, to, nil
}

// AddEnvironment adds an environment after the one named after ("" puts it
// first). It runs no release until one is promoted or released to it.
func (s *Service) AddEnvironment(ctx context.Context, actor application.Actor, ref string, e application.Environment, after string) (*application.Application, error) {
	if !actor.Admin {
		return nil, errEnvNeedsAdmin
	}
	store, err := s.envStore()
	if err != nil {
		return nil, err
	}
	if err := application.CheckEnvironment(e); err != nil {
		return nil, refused("%s", err.Error())
	}
	a, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	if after != "" {
		if _, _, err := environment(a, after); err != nil {
			return nil, err
		}
		if a.Last().Name == after {
			return nil, refused("%s is application %q's last environment, which /endpoints serves; add the new one before it", after, a.Name)
		}
	}
	if _, _, taken := a.EnvironmentNamed(e.Name); taken {
		return nil, refused("application %q already has an environment %q", a.Name, e.Name)
	}
	if err := s.newEnvironmentSchema(ctx, actor, a, &e); err != nil {
		return nil, err
	}
	e.UpdatedBy = actor.Email
	if err := store.AddEnvironment(ctx, a.ID, e, after); err != nil {
		if e.Held != nil {
			s.dropRunRole(ctx, a.Connection, e.Schema)
			err = fmt.Errorf("schema %s was built and is left for an administrator to drop: %w", e.Schema, err)
		}
		if errors.Is(err, application.ErrNameTaken) {
			return nil, refused("application %q already has an environment %q", a.Name, e.Name)
		}
		return nil, err //nolint:wrapcheck // read by identity
	}
	return s.Find(ctx, a.ID)
}

// errEnvNeedsAdmin refuses a change to an application's environments by
// someone who is not an administrator.
var errEnvNeedsAdmin = fmt.Errorf("changing an application's environments is for administrators: %w", application.ErrNotAllowed)

// RemoveEnvironment removes an environment other than the last, with its
// variables.
func (s *Service) RemoveEnvironment(ctx context.Context, actor application.Actor, ref, env string) (*application.Application, error) {
	if !actor.Admin {
		return nil, errEnvNeedsAdmin
	}
	store, err := s.envStore()
	if err != nil {
		return nil, err
	}
	a, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	removed, _, err := environment(a, env)
	if err != nil {
		return nil, err
	}
	if a.Last().Name == env {
		return nil, refused("%s is application %q's last environment, which /endpoints serves, and cannot be removed", env, a.Name)
	}
	if part := callerOf(a, env); part != "" {
		kept := ""
		if schema := envSchema(a, removed); schema != a.Schema {
			kept = fmt.Sprintf(". Its schema %s is kept until an administrator drops it", schema)
		}
		return nil, refused("%s can't be removed: site part %s calls it. Remove that site part first%s", env, part, kept)
	}
	if err := store.RemoveEnvironment(ctx, a.ID, env); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	s.forgetEnvironmentSchema(ctx, a, removed)
	return s.Find(ctx, a.ID)
}

// SetEnvironmentApproval changes whether an agent's promote or rollback into
// env waits on a person.
func (s *Service) SetEnvironmentApproval(ctx context.Context, actor application.Actor, ref, env string, requires bool) (*application.Application, error) {
	if !actor.Admin {
		return nil, errEnvNeedsAdmin
	}
	store, err := s.envStore()
	if err != nil {
		return nil, err
	}
	a, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	if _, _, err := environment(a, env); err != nil {
		return nil, err
	}
	if err := store.SetEnvironmentApproval(ctx, a.ID, env, requires, actor.Email); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	return s.Find(ctx, a.ID)
}

// errVarsNotAllowed refuses reading or changing variables to anyone but the
// owner and administrators.
var errVarsNotAllowed = fmt.Errorf("an application's variables are its owner's and administrators': %w", application.ErrNotAllowed)

// Variables is the application's variables, for its owner and
// administrators.
func (s *Service) Variables(ctx context.Context, actor application.Actor, ref string) ([]application.Variable, error) {
	store, err := s.envStore()
	if err != nil {
		return nil, err
	}
	a, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !a.MayEdit(actor) {
		return nil, errVarsNotAllowed
	}
	return store.Variables(ctx, a.ID) //nolint:wrapcheck // read by identity
}

// SetVariable writes NAME=value in env, "" for the application-wide
// default. The next run reads it; no release is needed.
func (s *Service) SetVariable(ctx context.Context, actor application.Actor, ref string, v application.Variable) error {
	store, a, err := s.variableTarget(ctx, actor, ref, v.Environment)
	if err != nil {
		return err
	}
	if err := application.CheckVariable(v); err != nil {
		return refused("%s", err.Error())
	}
	v.UpdatedBy = actor.Email
	return store.SetVariable(ctx, a.ID, v) //nolint:wrapcheck // read by identity
}

// DeleteVariable removes NAME from env, "" for the default.
func (s *Service) DeleteVariable(ctx context.Context, actor application.Actor, ref, env, name string) error {
	store, a, err := s.variableTarget(ctx, actor, ref, env)
	if err != nil {
		return err
	}
	return store.DeleteVariable(ctx, a.ID, env, name) //nolint:wrapcheck // read by identity
}

func (s *Service) variableTarget(ctx context.Context, actor application.Actor, ref, env string) (application.EnvironmentStore, *application.Application, error) {
	store, err := s.envStore()
	if err != nil {
		return nil, nil, err
	}
	a, err := s.Find(ctx, ref)
	if err != nil {
		return nil, nil, err
	}
	if !a.MayEdit(actor) {
		return nil, nil, errVarsNotAllowed
	}
	if env != "" && !slices.ContainsFunc(a.Environments, func(e application.Environment) bool { return e.Name == env }) {
		return nil, nil, refused("application %q has no environment %q; it has %s", a.Name, env, envNames(a))
	}
	return store, a, nil
}

// checkEnvironments refuses the environments an application is created with
// when a name is malformed or repeated; none named gives it one, prod, so
// its releases go live as they always have.
func checkEnvironments(envs []application.Environment) error {
	seen := map[string]bool{}
	for i := range envs {
		if err := application.CheckEnvironment(envs[i]); err != nil {
			return refused("%s", err.Error())
		}
		if seen[envs[i].Name] {
			return refused("environment %q is named twice", envs[i].Name)
		}
		seen[envs[i].Name] = true
		envs[i].Release = 0
	}
	return nil
}

// siteCalls refuses a site part whose environment names no environment of
// the application: a site environment's endpoints run in the application
// environment of its own name (#2709), and there would be none to run.
func siteCalls(a *application.Application, partID string) error {
	_, env, ok := application.ParseSitePartID(partID)
	if !ok || len(a.Environments) == 0 {
		return nil
	}
	if _, _, found := a.EnvironmentNamed(env); found {
		return nil
	}
	return refused("site part %s would call environment %s of application %q, which it does not have (it has %s); add environment %s first", partID, env, a.Name, envNames(a), env)
}

// callerOf is the site part whose endpoints run in env, "" for none.
func callerOf(a *application.Application, env string) string {
	for _, p := range a.Parts {
		if p.Kind != application.KindSite {
			continue
		}
		if _, e, ok := application.ParseSitePartID(p.ID); ok && e == env {
			return p.ID
		}
	}
	return ""
}

// pinsCalling is the pins a move into env applies (#2709): every pin but a
// site part calling another environment. An application listing no
// environments moves every site part, as before environments.
func pinsCalling(a *application.Application, pins []application.Pin, env string) []application.Pin {
	if len(a.Environments) == 0 {
		return pins
	}
	out := make([]application.Pin, 0, len(pins))
	for _, p := range pins {
		if p.Kind == application.KindSite {
			if _, e, ok := application.ParseSitePartID(p.PartID); !ok || e != env {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// pinnedIn is the version each site's part calling env is pinned at, by site.
func pinnedIn(pins []application.Pin, env string) map[string]int {
	out := map[string]int{}
	for _, p := range pins {
		if name, e, ok := application.ParseSitePartID(p.PartID); p.Kind == application.KindSite && ok && e == env {
			out[name] = p.Version
		}
	}
	return out
}

// lastName is the application's last environment, "" for none.
func lastName(a *application.Application) string {
	if l := a.Last(); l != nil {
		return l.Name
	}
	return ""
}

// approved refuses an agent's move into an environment that requires
// approval (#2706, #2709). It is the one check every surface reaches: the
// tool, the portal API a platform-admin connection or a managed script calls,
// and anything added later.
func approved(actor application.Actor, a *application.Application, target *application.Environment) error {
	if !actor.Agent || !target.RequiresApproval {
		return nil
	}
	return fmt.Errorf("%w: environment %s of application %s requires approval, so an agent does not move it: a person "+
		"promotes or rolls it back on the application's page in the portal (Applications > %s > Environments)",
		application.ErrApprovalRequired, target.Name, a.Name, a.Name)
}
