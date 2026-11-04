package appschema

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// kit is a fake toolkit holding a ledger.
type kit struct {
	applied   []int
	drift     bool
	planErr   error
	failAt    int // version Apply fails at; 0 never
	revertErr error
	statusErr error
}

func (k *kit) Status(_ context.Context, _, _ string, ms []postgreskit.Migration) (*postgreskit.LedgerStatus, error) {
	if k.statusErr != nil {
		return nil, k.statusErr
	}
	st := &postgreskit.LedgerStatus{}
	for _, v := range k.applied {
		st.Applied = append(st.Applied, postgreskit.Applied{Version: v})
	}
	for _, m := range ms {
		if !slices.Contains(k.applied, m.Version) {
			st.Pending = append(st.Pending, m)
		}
	}
	if k.drift {
		st.Drift = []postgreskit.Drift{{Version: 1, Reason: "edited after it was applied"}}
	}
	return st, nil
}

func (k *kit) PlanMigrations(context.Context, string, string, []postgreskit.Migration) (*postgreskit.Plan, error) {
	return &postgreskit.Plan{}, k.planErr
}

func (k *kit) Apply(_ context.Context, o postgreskit.ApplyOptions, ms []postgreskit.Migration) ([]postgreskit.Migration, error) {
	done := []postgreskit.Migration{}
	for _, m := range ms {
		if slices.Contains(k.applied, m.Version) {
			continue
		}
		if m.Version == k.failAt {
			o.OnDone(m, 0, errors.New("relation missing"))
			return done, errors.New("relation missing")
		}
		k.applied = append(k.applied, m.Version)
		o.OnDone(m, 0, nil)
		done = append(done, m)
	}
	return done, nil
}

func (k *kit) Revert(_ context.Context, o postgreskit.ApplyOptions, ms []postgreskit.Migration, to int) ([]postgreskit.Migration, error) {
	if k.revertErr != nil {
		return nil, k.revertErr
	}
	done := []postgreskit.Migration{}
	for _, m := range slices.Backward(ms) {
		if m.Version > to && slices.Contains(k.applied, m.Version) {
			k.applied = slices.DeleteFunc(k.applied, func(v int) bool { return v == m.Version })
			o.OnDone(m, 0, nil)
			done = append(done, m)
		}
	}
	return done, nil
}

type store struct {
	ms     []postgreskit.Migration
	events []postgreskit.MigrationEvent
}

func (s *store) List(context.Context, string, string) ([]postgreskit.Migration, error) {
	return s.ms, nil
}

func (s *store) Record(_ context.Context, e postgreskit.MigrationEvent) error {
	s.events = append(s.events, e)
	return nil
}

func migs(withDown bool, versions ...int) []postgreskit.Migration {
	out := make([]postgreskit.Migration, 0, len(versions))
	for _, v := range versions {
		m := postgreskit.Migration{Version: v, Up: "CREATE TABLE t" + strconv.Itoa(v) + " ()"}
		if withDown {
			m.Down = "DROP TABLE x"
		}
		out = append(out, m)
	}
	return out
}

func TestNoKit(t *testing.T) {
	for _, s := range []*Schema{{}, {Kit: func() Kit { return nil }}} {
		_, err := s.State(context.Background(), "c", "s")
		require.ErrorIs(t, err, ErrNoKit)
		_, err = s.Release(context.Background(), "c", "s", "a")
		require.ErrorIs(t, err, ErrNoKit)
		_, err = s.Rollback(context.Background(), "c", "s", "a", 0)
		require.ErrorIs(t, err, ErrNoKit)
	}
}

func TestReleaseAndState(t *testing.T) {
	k, st := &kit{applied: []int{1}}, &store{ms: migs(true, 1, 2, 3)}
	s := &Schema{Kit: func() Kit { return k }, Store: st}
	ctx := context.Background()
	state, err := s.State(ctx, "c", "s")
	require.NoError(t, err)
	assert.Equal(t, 1, state.Applied)
	assert.Equal(t, []int{2, 3}, state.Pending)

	applied, err := s.Release(ctx, "c", "s", "admin")
	require.NoError(t, err)
	assert.Equal(t, []int{2, 3}, applied.Versions)
	assert.Equal(t, 3, applied.Head)
	require.Len(t, st.events, 2)
	assert.Equal(t, postgreskit.EventApplied, st.events[0].Event)
	assert.Equal(t, "application release", st.events[0].Detail)

	applied, err = s.Release(ctx, "c", "s", "admin")
	require.NoError(t, err)
	assert.Empty(t, applied.Versions, "nothing pending applies nothing")
	assert.Equal(t, 3, applied.Head)

	k.drift = true
	_, err = s.Release(ctx, "c", "s", "admin")
	require.ErrorContains(t, err, "resolve it with manage_schema")
	k.drift, k.statusErr = false, errors.New("down")
	_, err = s.State(ctx, "c", "s")
	require.ErrorContains(t, err, "ledger")
}

func TestRelease_FailureChangesNothing(t *testing.T) {
	ctx := context.Background()
	k, st := &kit{planErr: errors.New("syntax error")}, &store{ms: migs(true, 1)}
	s := &Schema{Kit: func() Kit { return k }, Store: st}
	_, err := s.Release(ctx, "c", "s", "admin")
	var failed *FailedError
	require.ErrorAs(t, err, &failed)
	assert.Empty(t, k.applied, "a plan that fails applies nothing")

	k = &kit{failAt: 2}
	s.Kit = func() Kit { return k }
	st.ms = migs(true, 1, 2)
	_, err = s.Release(ctx, "c", "s", "admin")
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, []int{1}, failed.Undone, "what the release applied before the failure is reverted")
	assert.Empty(t, failed.Left)
	assert.Empty(t, k.applied)
	assert.NotContains(t, failed.Error(), "stay applied")

	k = &kit{failAt: 2}
	s.Kit = func() Kit { return k }
	st.ms = migs(false, 1, 2)
	_, err = s.Release(ctx, "c", "s", "admin")
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, []int{1}, failed.Left, "a migration with no down migration stays applied, and the error says so")
	assert.Contains(t, failed.Error(), "stay applied")
	require.ErrorContains(t, errors.Unwrap(failed), "relation missing")

	k = &kit{failAt: 2, revertErr: errors.New("revert broke")}
	s.Kit = func() Kit { return k }
	st.ms = migs(true, 1, 2)
	_, err = s.Release(ctx, "c", "s", "admin")
	require.ErrorAs(t, err, &failed)
	assert.ErrorContains(t, failed, "revert broke")

	k = &kit{failAt: 1}
	s.Kit = func() Kit { return k }
	_, err = s.Release(ctx, "c", "s", "admin")
	require.ErrorAs(t, err, &failed)
	assert.Empty(t, failed.Left, "a first migration that fails leaves nothing applied")
}

func TestRollback(t *testing.T) {
	ctx := context.Background()
	k, st := &kit{applied: []int{1, 2, 3}}, &store{ms: migs(true, 1, 2, 3)}
	s := &Schema{Kit: func() Kit { return k }, Store: st}
	r, err := s.Rollback(ctx, "c", "s", "admin", 3)
	require.NoError(t, err)
	assert.Empty(t, r.Versions, "nothing above the target")

	r, err = s.Rollback(ctx, "c", "s", "admin", 1)
	require.NoError(t, err)
	assert.Equal(t, []int{3, 2}, r.Versions)
	assert.Equal(t, 1, r.Head)

	k.applied = []int{1, 2, 3}
	st.ms = migs(false, 1, 2, 3)
	r, err = s.Rollback(ctx, "c", "s", "admin", 1)
	require.NoError(t, err)
	assert.Empty(t, r.Versions)
	assert.Equal(t, "migrations 2, 3 have no down migration, so no migration was reverted", r.Kept)

	st.ms = migs(true, 1, 2, 3)
	k.revertErr = errors.New("locked")
	_, err = s.Rollback(ctx, "c", "s", "admin", 1)
	require.ErrorContains(t, err, "locked")
	k.revertErr, k.drift = nil, true
	_, err = s.Rollback(ctx, "c", "s", "admin", 1)
	require.Error(t, err)
}
