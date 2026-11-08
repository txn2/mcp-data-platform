package appsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
)

// TestAddSchema_ToAnExistingApplication is #2539's first criteria: a schema
// is added to an application that has none, as a part, on a connection the
// caller reaches, with the migration version it already has adopted and a
// run role whose password is sealed bound to the application, connection,
// schema and role.
func TestAddSchema_ToAnExistingApplication(t *testing.T) {
	svc, store, _, _, schema := fixture()
	roles, ok := svc.Roles.(*fakeRoles)
	require.True(t, ok)
	ctx := context.Background()
	_, err := svc.Create(ctx, jane, application.Application{Name: "shop"})
	require.NoError(t, err)
	schema.applied, schema.pending = []int{1, 2, 3}, nil

	a, err := svc.AddPart(ctx, jane, "shop", application.KindSchema, "orders/shop")
	require.NoError(t, err)
	assert.Equal(t, "orders", a.Connection)
	assert.Equal(t, "shop", a.Schema)
	assert.Equal(t, 3, a.SchemaAdoptedVersion, "the migrations applied before it was added are adopted")
	assert.Contains(t, a.Parts, application.Part{Kind: application.KindSchema, ID: "orders/shop", AddedBy: jane.Email})
	assert.Equal(t, []string{"orders/shop"}, roles.created)
	rr := store.runRoles["app_shop"]
	assert.Equal(t, "shop_run", rr.Role)
	assert.Equal(t, "sealed["+RunRoleBinding("app_shop", "orders", "shop", "shop_run")+"]", rr.SealedPassword)
	assert.NotContains(t, rr.SealedPassword, "pw-shop", "the password is stored sealed only")

	_, err = svc.AddPart(ctx, jane, "shop", application.KindSchema, "orders/other")
	require.ErrorContains(t, err, `holds schema orders/shop already`)
	again, err := svc.AddPart(ctx, jane, "shop", application.KindSchema, "shop")
	require.NoError(t, err, "adding the schema it holds is no change")
	assert.Equal(t, "shop", again.Schema)
	assert.Len(t, roles.created, 1)

	_, err = svc.Create(ctx, joe, application.Application{Name: "rival"})
	require.NoError(t, err)
	_, err = svc.AddPart(ctx, joe, "rival", application.KindSchema, "orders/shop")
	require.ErrorIs(t, err, application.ErrSchemaHeld)
	require.ErrorContains(t, err, `application "shop"`, "the refusal names the application holding it")
	_, err = svc.AddPart(ctx, joe, "shop", application.KindSchema, "orders/x")
	require.ErrorIs(t, err, application.ErrNotAllowed, "a schema is added by the owner or an administrator")
}

// TestAddSchema_ChoosesTheConnection: with one connection the caller
// reaches, the schema alone names the part; with several the refusal lists
// them; a connection the caller does not reach is refused naming the ones
// they do; with none the refusal says so.
func TestAddSchema_ChoosesTheConnection(t *testing.T) {
	svc, _, _, _, _ := fixture()
	ctx := context.Background()
	for _, name := range []string{"one", "two", "three", "four"} {
		_, err := svc.Create(ctx, jane, application.Application{Name: name})
		require.NoError(t, err)
	}
	a, err := svc.AddPart(ctx, jane, "one", application.KindSchema, "solo")
	require.NoError(t, err)
	assert.Equal(t, "orders", a.Connection, "the only connection the caller reaches")

	svc.Reach = reachOnly("orders", "ledger")
	_, err = svc.AddPart(ctx, jane, "two", application.KindSchema, "duo")
	require.ErrorContains(t, err, "several application database connections")
	require.ErrorContains(t, err, "ledger, orders")
	_, err = svc.AddPart(ctx, jane, "two", application.KindSchema, "warehouse/duo")
	require.ErrorContains(t, err, `"warehouse" is not an application database connection (kind appdb) you reach`)
	require.ErrorContains(t, err, "ledger, orders")
	a, err = svc.AddPart(ctx, jane, "two", application.KindSchema, "ledger/duo")
	require.NoError(t, err)
	assert.Equal(t, "ledger", a.Connection)

	svc.Reach = reachOnly()
	_, err = svc.AddPart(ctx, jane, "three", application.KindSchema, "trio")
	require.ErrorContains(t, err, "you reach no application database connection")
	svc.Reach = nil
	_, err = svc.AddPart(ctx, jane, "three", application.KindSchema, "trio")
	require.ErrorContains(t, err, "no application database connection to hold a schema on")
	svc.Reach = reachOnly("orders")
	_, err = svc.AddPart(ctx, jane, "four", application.KindSchema, "orders/")
	require.ErrorContains(t, err, "part_id names the schema")
}

// TestAddSchema_Refusals: no encryption key, a connection user that cannot
// create roles, and a failed write each refuse, and the failed write drops
// the role it made.
func TestAddSchema_Refusals(t *testing.T) {
	svc, store, _, _, _ := fixture()
	roles, ok := svc.Roles.(*fakeRoles)
	require.True(t, ok)
	ctx := context.Background()
	_, err := svc.Create(ctx, jane, application.Application{Name: "shop"})
	require.NoError(t, err)

	svc.Sealer = fakeSealer{disabled: true}
	_, err = svc.AddPart(ctx, jane, "shop", application.KindSchema, "orders/shop")
	require.ErrorContains(t, err, "ENCRYPTION_KEY")
	svc.Sealer = fakeSealer{}

	roles.failCreate = errors.New("appdb: the user of connection \"orders\" cannot create roles ... ALTER ROLE app CREATEROLE")
	_, err = svc.AddPart(ctx, jane, "shop", application.KindSchema, "orders/shop")
	require.ErrorContains(t, err, "CREATEROLE")
	roles.failCreate = nil

	store.failSet = errors.New("down")
	_, err = svc.AddPart(ctx, jane, "shop", application.KindSchema, "orders/shop")
	require.Error(t, err)
	assert.Equal(t, []string{"orders/shop"}, roles.dropped, "the role made for a record never written is dropped")
	store.failSet = nil

	svc.Roles = nil
	_, err = svc.AddPart(ctx, jane, "shop", application.KindSchema, "orders/shop")
	require.ErrorContains(t, err, "no application database connection kind")
}

// TestCreateWithASchema_GoesThroughAdd: a schema named at create is added as
// add_part adds one, and a refused schema leaves no application behind.
func TestCreateWithASchema_GoesThroughAdd(t *testing.T) {
	svc, store, _, _, _ := fixture()
	ctx := context.Background()
	a, err := svc.Create(ctx, jane, application.Application{Name: "shop", Connection: "orders", Schema: "shop"})
	require.NoError(t, err)
	assert.Equal(t, "shop_run", store.runRoles[a.ID].Role)

	svc.Sealer = fakeSealer{disabled: true}
	_, err = svc.Create(ctx, jane, application.Application{Name: "nokey", Connection: "orders", Schema: "nokey"})
	require.ErrorContains(t, err, "ENCRYPTION_KEY")
	_, err = svc.Find(ctx, "nokey")
	require.ErrorIs(t, err, application.ErrNotFound, "the refused application is not kept")
}

// TestRepairRunRole: an application holding a schema from before run roles
// gets one by adding the schema again.
func TestRepairRunRole(t *testing.T) {
	svc, store, _, _, _ := fixture()
	ctx := context.Background()
	_, err := svc.Create(ctx, jane, application.Application{Name: "shop"})
	require.NoError(t, err)
	store.apps["app_shop"].Connection, store.apps["app_shop"].Schema = "orders", "shop"
	st, err := svc.StatusOf(ctx, "shop")
	require.NoError(t, err)
	require.NotNil(t, st.Schema)
	assert.Contains(t, st.Schema.Note, "no run role")

	_, err = svc.AddPart(ctx, jane, "shop", application.KindSchema, "orders/shop")
	require.NoError(t, err)
	assert.Equal(t, "shop_run", store.runRoles["app_shop"].Role)
}

// TestRemoveSchema: an administrator takes the schema off a released
// application, its owner off one never released, and its run role is
// dropped; deleting an application drops it too.
func TestRemoveSchema(t *testing.T) {
	svc, store, _, _, _ := released(t)
	roles, ok := svc.Roles.(*fakeRoles)
	require.True(t, ok)
	ctx := context.Background()
	_, err := svc.Release(ctx, admin, "shop", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"orders/shop"}, roles.granted, "a release grants the run role its write role")

	_, err = svc.RemovePart(ctx, jane, "shop", application.KindSchema, "orders/shop")
	require.ErrorIs(t, err, application.ErrNotAllowed, "the schema of a released application is an administrator's to take out")
	_, err = svc.RemovePart(ctx, admin, "shop", application.KindSchema, "orders/other")
	require.ErrorIs(t, err, application.ErrNoPart)
	roles.failDrop = errors.New("down")
	_, err = svc.RemovePart(ctx, admin, "shop", application.KindSchema, "orders/shop")
	require.ErrorContains(t, err, "dropping the application's run role")
	roles.failDrop = nil
	a, err := svc.RemovePart(ctx, admin, "shop", application.KindSchema, "shop")
	require.NoError(t, err)
	assert.Empty(t, a.Schema)
	assert.Equal(t, []string{"orders/shop"}, roles.dropped)
	_, ok = store.runRoles["app_shop"]
	assert.False(t, ok, "the stored password goes with the role")

	_, err = svc.Create(ctx, joe, application.Application{Name: "gone", Connection: "orders", Schema: "gone"})
	require.NoError(t, err)
	require.NoError(t, svc.Delete(ctx, joe, "gone"))
	assert.Contains(t, roles.dropped, "orders/gone")
	_, err = svc.Create(ctx, joe, application.Application{Name: "stuck", Connection: "orders", Schema: "stuck"})
	require.NoError(t, err)
	roles.failDrop = errors.New("down")
	require.ErrorContains(t, svc.Delete(ctx, joe, "stuck"), "dropping the application's run role")
}

// TestRollback_NeverBelowTheAdoptedVersion: migrations applied before the
// schema was added were applied by no release, and a rollback to a release
// from before it stops at them.
func TestRollback_NeverBelowTheAdoptedVersion(t *testing.T) {
	svc, _, scripts, _, schema := fixture()
	ctx := context.Background()
	_, err := svc.Create(ctx, jane, application.Application{Name: "shop"})
	require.NoError(t, err)
	_, err = svc.AddPart(ctx, jane, "shop", application.KindScript, scriptA)
	require.NoError(t, err)
	_, err = svc.Release(ctx, admin, "shop", "before the schema")
	require.NoError(t, err)

	schema.applied, schema.pending = []int{1, 2}, []int{3}
	_, err = svc.AddPart(ctx, jane, "shop", application.KindSchema, "orders/shop")
	require.NoError(t, err)
	scripts.scripts[scriptA].Version = 2
	rel, err := svc.Release(ctx, admin, "shop", "with the schema")
	require.NoError(t, err)
	assert.Equal(t, []int{3}, rel.Changes.Applied, "a release applies nothing already applied")
	assert.Equal(t, 3, rel.MigrationVersion, "and records the schema's version")

	st, err := svc.StatusOf(ctx, "shop")
	require.NoError(t, err)
	require.NotNil(t, st.Schema)
	assert.Equal(t, 3, st.Schema.ReleasedVersion)
	assert.Equal(t, 2, st.Schema.AdoptedVersion)
	assert.Equal(t, "shop_run", st.Schema.RunRole)
	assert.Contains(t, st.Schema.Note, "reaches no table until a migration defines shop_write")

	back, err := svc.Rollback(ctx, admin, "shop", 1)
	require.NoError(t, err)
	assert.Equal(t, []int{3}, back.Changes.Reverted, "only the migration a release applied is reverted")

	svc.Members = func(context.Context, string, string, string) bool { return true }
	st, err = svc.StatusOf(ctx, "shop")
	require.NoError(t, err)
	assert.Equal(t, "shop_write", st.Schema.WriteRole)
	assert.Empty(t, st.Schema.Note)
}

// TestAddScript_RefusedWhenItUsesAnotherApplicationsSchema: a script whose
// source reads a table in a schema another application holds is not added
// (#2539); one reading its own application's schema, a computed table, or
// another connection is.
func TestAddScript_RefusedWhenItUsesAnotherApplicationsSchema(t *testing.T) {
	svc, _, scripts, _, _ := fixture()
	ctx := context.Background()
	_, err := svc.Create(ctx, joe, application.Application{Name: "billing", Connection: "orders", Schema: "billing"})
	require.NoError(t, err)
	_, err = svc.Create(ctx, jane, application.Application{Name: "shop", Connection: "orders", Schema: "shop"})
	require.NoError(t, err)

	scripts.sources = map[string]string{scriptA: `
def main():
    """Reads."""
    platform.query("SELECT * FROM shop.orders o JOIN billing.invoices i ON i.o = o.id", connection = "orders")
`}
	_, err = svc.AddPart(ctx, jane, "shop", application.KindScript, scriptA)
	require.ErrorContains(t, err, `uses billing.invoices, in schema billing on connection orders, which belongs to application "billing"`)

	scripts.sources[scriptA] = `
def main():
    """Reads its own, a computed table and another connection."""
    platform.query("SELECT * FROM shop.orders", connection = "orders")
    platform.query("SELECT * FROM " + run.params["t"], connection = "orders")
    platform.query("SELECT * FROM billing.invoices", connection = "warehouse")
`
	svc.HasConnection = func(name string) bool { return name == "orders" }
	_, err = svc.AddPart(ctx, jane, "shop", application.KindScript, scriptA)
	require.NoError(t, err)
}
