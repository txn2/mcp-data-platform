// Package appschema is what an application's release does to its
// application database schema (#2411): apply the pending migrations so that
// a release with a failing migration changes nothing, and revert to an
// earlier release's migration version when every migration between has a
// down migration. It drives the appdb kind through the toolkit's exported
// methods and records each step in the migration history manage_schema
// keeps.
package appschema

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// Kit is the part of the appdb toolkit a release uses.
type Kit interface {
	Status(ctx context.Context, connection, schema string, migrations []postgreskit.Migration) (*postgreskit.LedgerStatus, error)
	PlanMigrations(ctx context.Context, connection, schema string, pending []postgreskit.Migration) (*postgreskit.Plan, error)
	Apply(ctx context.Context, o postgreskit.ApplyOptions, migrations []postgreskit.Migration) ([]postgreskit.Migration, error)
	Revert(ctx context.Context, o postgreskit.ApplyOptions, migrations []postgreskit.Migration, toVersion int) ([]postgreskit.Migration, error)
}

// Store is the platform's record of the migrations and their history.
type Store interface {
	List(ctx context.Context, connection, schema string) ([]postgreskit.Migration, error)
	Record(ctx context.Context, e postgreskit.MigrationEvent) error
}

// ErrNoKit is a release of an application with a schema on a deployment with
// no application database kind.
var ErrNoKit = errors.New("this deployment has no application database connection kind")

// Schema applies and reverts an application's migrations.
type Schema struct {
	// Kit is the live toolkit, nil when none is registered.
	Kit   func() Kit
	Store Store
}

func (s *Schema) kit() (Kit, error) {
	if s.Kit == nil {
		return nil, ErrNoKit
	}
	k := s.Kit()
	if k == nil {
		return nil, ErrNoKit
	}
	return k, nil
}

// State is where a schema's migrations stand.
type State struct {
	// Applied is the highest applied version, 0 for none.
	Applied int `json:"applied"`
	// Pending is the versions a release would apply.
	Pending []int `json:"pending"`
}

// State reads where the schema's migrations stand. Drift between the target's
// ledger and the platform's migrations is an error.
func (s *Schema) State(ctx context.Context, connection, schema string) (*State, error) {
	k, err := s.kit()
	if err != nil {
		return nil, err
	}
	_, st, err := s.read(ctx, k, connection, schema)
	if err != nil {
		return nil, err
	}
	return &State{Applied: highest(st.Applied), Pending: versions(st.Pending)}, nil
}

func (s *Schema) read(ctx context.Context, k Kit, connection, schema string) ([]postgreskit.Migration, *postgreskit.LedgerStatus, error) {
	ms, err := s.Store.List(ctx, connection, schema)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the schema's migrations: %w", err)
	}
	st, err := k.Status(ctx, connection, schema, ms)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the schema's ledger: %w", err)
	}
	if len(st.Drift) > 0 {
		return nil, nil, fmt.Errorf("version %d: %s; resolve it with manage_schema before releasing", st.Drift[0].Version, st.Drift[0].Reason)
	}
	return ms, st, nil
}

func highest(applied []postgreskit.Applied) int {
	top := 0
	for _, a := range applied {
		top = max(top, a.Version)
	}
	return top
}

func versions(ms []postgreskit.Migration) []int {
	out := make([]int, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Version)
	}
	return out
}

// FailedError is a release whose migrations did not all apply. Undone is the
// versions applied before the failure and reverted again; Left is any that
// could not be reverted and stay applied.
type FailedError struct {
	Err    error
	Undone []int
	Left   []int
}

func (e *FailedError) Error() string {
	msg := "the release's migrations did not apply: " + e.Err.Error()
	if len(e.Left) > 0 {
		msg += fmt.Sprintf("; migrations %v applied before the failure have no down migration and stay applied", e.Left)
	}
	return msg
}

func (e *FailedError) Unwrap() error { return e.Err }

// Applied is what a release did to the schema.
type Applied struct {
	// Versions is the migrations applied, in order.
	Versions []int
	// Head is the highest applied version after the release.
	Head int
}

// Release applies every pending migration. The pending migrations that run in
// a transaction are first run together in one transaction that is rolled
// back, so one that fails is found before anything changes. Should a
// migration still fail during the apply, the ones this release applied are
// reverted with their down migrations.
func (s *Schema) Release(ctx context.Context, connection, schema, actor string) (*Applied, error) {
	k, err := s.kit()
	if err != nil {
		return nil, err
	}
	ms, st, err := s.read(ctx, k, connection, schema)
	if err != nil {
		return nil, err
	}
	head := highest(st.Applied)
	if len(st.Pending) == 0 {
		return &Applied{Versions: []int{}, Head: head}, nil
	}
	if _, err := k.PlanMigrations(ctx, connection, schema, st.Pending); err != nil {
		return nil, &FailedError{Err: err, Undone: []int{}, Left: []int{}}
	}
	o := s.options(ctx, connection, schema, actor, postgreskit.EventApplied)
	done, err := k.Apply(ctx, o, ms)
	if err != nil {
		return nil, s.undo(ctx, k, target{connection: connection, schema: schema, actor: actor, before: head}, done, err)
	}
	for _, m := range done {
		head = max(head, m.Version)
	}
	return &Applied{Versions: versions(done), Head: head}, nil
}

// target is the schema an apply acts on, who acts, and the version applied
// before it began.
type target struct {
	connection, schema, actor string
	before                    int
}

// undo reverts what a failed apply did, down to at.before.
func (s *Schema) undo(ctx context.Context, k Kit, at target, done []postgreskit.Migration, cause error) error {
	connection, schema, actor, before := at.connection, at.schema, at.actor, at.before
	failed := &FailedError{Err: cause, Undone: []int{}, Left: versions(done)}
	if len(done) == 0 {
		failed.Left = []int{}
		return failed
	}
	if missing := withoutDown(done); len(missing) > 0 {
		return failed
	}
	ms, err := s.Store.List(context.WithoutCancel(ctx), connection, schema)
	if err != nil {
		return failed
	}
	o := s.options(ctx, connection, schema, actor, postgreskit.EventReverted)
	reverted, revertErr := k.Revert(context.WithoutCancel(ctx), o, ms, before)
	failed.Undone = versions(reverted)
	failed.Left = slices.DeleteFunc(failed.Left, func(v int) bool { return slices.Contains(failed.Undone, v) })
	if revertErr != nil {
		failed.Err = fmt.Errorf("the apply failed (%w), and reverting what it applied failed too: %w", cause, revertErr)
	}
	return failed
}

func withoutDown(ms []postgreskit.Migration) []int {
	out := []int{}
	for _, m := range ms {
		if m.Down == "" {
			out = append(out, m.Version)
		}
	}
	return out
}

// Reverted is what a rollback did to the schema.
type Reverted struct {
	Versions []int
	// Head is the highest applied version after the rollback.
	Head int
	// Kept says why migrations stayed applied, empty when none did.
	Kept string
}

// Rollback reverts every applied migration above to when each has a down
// migration. When any lacks one nothing is reverted and Kept says which.
func (s *Schema) Rollback(ctx context.Context, connection, schema, actor string, to int) (*Reverted, error) {
	k, err := s.kit()
	if err != nil {
		return nil, err
	}
	ms, st, err := s.read(ctx, k, connection, schema)
	if err != nil {
		return nil, err
	}
	head := highest(st.Applied)
	above := []postgreskit.Migration{}
	for _, m := range ms {
		if m.Version > to && slices.ContainsFunc(st.Applied, func(a postgreskit.Applied) bool { return a.Version == m.Version }) {
			above = append(above, m)
		}
	}
	if len(above) == 0 {
		return &Reverted{Versions: []int{}, Head: head}, nil
	}
	if missing := withoutDown(above); len(missing) > 0 {
		return &Reverted{Versions: []int{}, Head: head, Kept: keptReason(missing)}, nil
	}
	o := s.options(ctx, connection, schema, actor, postgreskit.EventReverted)
	reverted, err := k.Revert(ctx, o, ms, to)
	if err != nil {
		return nil, fmt.Errorf("reverting migrations %v: %w", versions(reverted), err)
	}
	return &Reverted{Versions: versions(reverted), Head: to}, nil
}

func keptReason(missing []int) string {
	names := make([]string, len(missing))
	for i, v := range missing {
		names[i] = strconv.Itoa(v)
	}
	if len(missing) == 1 {
		return "migration " + names[0] + " has no down migration, so no migration was reverted"
	}
	return "migrations " + strings.Join(names, ", ") + " have no down migration, so no migration was reverted"
}

// options are the apply options a release runs with: each step recorded in the
// schema's history under the actor.
func (s *Schema) options(ctx context.Context, connection, schema, actor, event string) postgreskit.ApplyOptions {
	return postgreskit.ApplyOptions{
		Connection: connection, Schema: schema, Actor: actor,
		OnDone: func(m postgreskit.Migration, took time.Duration, err error) {
			e := postgreskit.MigrationEvent{
				Connection: connection, Schema: schema, Version: m.Version, Event: event,
				Actor: actor, Checksum: m.Checksum, DurationMS: took.Milliseconds(), Detail: "application release",
			}
			if err != nil {
				e.Event, e.Detail = postgreskit.EventFailed, event+" in an application release: "+err.Error()
			}
			_ = s.Store.Record(context.WithoutCancel(ctx), e)
		},
	}
}
