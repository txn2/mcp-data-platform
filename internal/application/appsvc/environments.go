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
// /endpoints/<name> and every handler naming no environment run. Every
// environment uses the application's schema, so its migrations move with the
// first environment: a release applies them and a rollback there reverts
// them; a promote, and a rollback in any later environment, leave the schema
// alone.

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

// first reports whether env is where releases land.
func first(a *application.Application, env string) bool {
	return len(a.Environments) == 0 || env == "" && len(a.Environments) == 1 || a.Environments[0].Name == env
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
	if err := s.releaseReadable(ctx, a, src.Release); err != nil {
		return nil, err
	}
	out := &Promoted{From: src.Name, To: dst.Name, Release: src.Release, Replaced: dst.Release}
	move := application.Move{Environment: dst.Name, Number: src.Release, Expect: dst.Release, By: actor.Email}
	if err := store.SetEnvironmentRelease(ctx, a.ID, move); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
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

// releaseReadable refuses a release whose pins can no longer be read.
func (s *Service) releaseReadable(ctx context.Context, a *application.Application, number int) error {
	rel, err := s.Store.Release(ctx, a.ID, number)
	if err != nil {
		return err //nolint:wrapcheck // read by identity
	}
	return s.pinsReadable(ctx, rel.Pins)
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
	if err := store.SetEnvironmentRelease(ctx, a.ID, application.Move{Environment: e.Name, Number: to, Expect: e.Release, By: actor.Email}); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
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
	e.UpdatedBy = actor.Email
	if err := store.AddEnvironment(ctx, a.ID, e, after); err != nil {
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
	if _, _, err := environment(a, env); err != nil {
		return nil, err
	}
	if a.Last().Name == env {
		return nil, refused("%s is application %q's last environment, which /endpoints serves, and cannot be removed", env, a.Name)
	}
	if err := store.RemoveEnvironment(ctx, a.ID, env); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
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
