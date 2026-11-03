package whadmin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whpgstate"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// fakeDatabase records the connections it was asked to check and refuses
// while refuse is set.
type fakeDatabase struct {
	checked []string
	refuse  error
	state   whpgstate.State
}

func (f *fakeDatabase) Check(_ context.Context, src whsource.Source) error {
	f.checked = append(f.checked, src.Config.Postgres.Connection)
	return f.refuse
}

func (f *fakeDatabase) Listener(context.Context, string) (whpgstate.State, error) {
	return f.state, nil
}

func postgresInput() whsource.Source {
	return whsource.Source{
		Name: "orders-outbox", Enabled: true, Connection: "scratch", CreatedBy: "admin@example.com",
		Config: whsource.Config{Input: whsource.InputPostgres, Postgres: &whsource.Postgres{
			Connection: "orders", Mode: whsource.PostgresOutbox, Table: "public.outbox",
		}},
	}
}

func TestCreateAndUpdate_CheckTheDatabase(t *testing.T) {
	r := newRig()
	db := &fakeDatabase{refuse: errBoom}
	r.svc.deps.Database = db

	_, err := r.svc.Create(ctx, postgresInput())
	require.ErrorIs(t, err, whsource.ErrInvalid, "a database the listener cannot read is refused")
	assert.ErrorContains(t, err, "the source's database could not be read: boom")
	assert.Zero(t, r.tables.created, "nothing is built for a refused source")

	_, err = r.svc.Create(ctx, input())
	require.NoError(t, err, "an http source is never checked")

	db.refuse = nil
	src, err := r.svc.Create(ctx, postgresInput())
	require.NoError(t, err)
	assert.Equal(t, whsource.InputPostgres, src.Config.Input)

	cfg := postgresInput().Config
	cfg.Postgres.Connection = "billing"
	db.refuse = errBoom
	_, err = r.svc.Update(ctx, "orders-outbox", Update{Config: cfg})
	require.ErrorIs(t, err, whsource.ErrInvalid)
	db.refuse = nil
	src, err = r.svc.Update(ctx, "orders-outbox", Update{Config: cfg})
	require.NoError(t, err)
	assert.Equal(t, "billing", src.Config.Postgres.Connection)
	assert.Equal(t, []string{"orders", "orders", "billing", "billing"}, db.checked)
}

func TestListener(t *testing.T) {
	r := newRig()
	got, err := r.svc.Listener(ctx, "orders-outbox")
	require.NoError(t, err)
	assert.Equal(t, whpgstate.State{}, got, "no database reports no listener")

	r.svc.deps.Database = &fakeDatabase{state: whpgstate.State{Replica: "platform-0", Delivered: 4}}
	got, err = r.svc.Listener(ctx, "orders-outbox")
	require.NoError(t, err)
	assert.Equal(t, "platform-0", got.Replica)
}
