package appsvc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appprepare"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// schemaEnvFixture is an application, shop, holding schema orders on
// connection orders, with one environment, prod, that has applied
// migrations 1 and 2 (#2709).
// schemaEnv is the fixture's service and the fakes it records through.
type schemaEnv struct {
	svc    *Service
	store  *memStore
	schema *fakeSchema
	roles  *fakeRoles
}

func schemaEnvFixture(t *testing.T) schemaEnv {
	t.Helper()
	svc, store, _, _, schema := fixture()
	store.vars = map[string][]application.Variable{}
	svc.Environments = store
	roles := &fakeRoles{}
	svc.Roles = roles
	ctx := context.Background()
	_, err := svc.Create(ctx, jane, application.Application{Name: "shop", Environments: []application.Environment{{Name: "prod"}}})
	require.NoError(t, err)
	schema.applied, schema.pending = []int{1, 2}, []int{}
	_, err = svc.AddPart(ctx, jane, "shop", application.KindSchema, "orders/orders")
	require.NoError(t, err)
	_, err = svc.AddPart(ctx, jane, "shop", application.KindScript, scriptA)
	require.NoError(t, err)
	return schemaEnv{svc: svc, store: store, schema: schema, roles: roles}
}

// An environment added with no schema named gets <schema>_<name>, built from
// the last environment's structure, recorded at its migration version, with a
// run role of its own sealed to that schema (#2709).
func TestAddEnvironment_BuildsItsOwnSchema(t *testing.T) {
	ctx := context.Background()
	fx := schemaEnvFixture(t)
	svc := fx.svc
	store := fx.store
	schema := fx.schema
	roles := fx.roles

	a, err := svc.AddEnvironment(ctx, admin, "shop", application.Environment{Name: "dev"}, "")
	require.NoError(t, err)
	require.Equal(t, "dev", a.Environments[0].Name)
	assert.Equal(t, "orders_dev", a.Environments[0].Schema)
	assert.Equal(t, []string{"orders>orders_dev"}, roles.cloned)
	assert.Contains(t, schema.calls, "baseline orders_dev 2", "recorded as having applied what prod had")
	assert.Contains(t, roles.created, "orders/orders_dev")
	rr, err := store.RunRoleIn(ctx, a.ID, "dev")
	require.NoError(t, err)
	assert.Equal(t, "orders_dev_run", rr.Role)
	assert.Equal(t, "sealed["+RunRoleBinding(a.ID, "orders", "orders_dev", "orders_dev_run")+"]", rr.SealedPassword,
		"sealed to its own schema and role")
	prod, err := store.RunRoleIn(ctx, a.ID, "prod")
	require.NoError(t, err)
	assert.NotEqual(t, rr.Role, prod.Role, "prod keeps the application's run role")

	a, err = svc.AddEnvironment(ctx, admin, "shop", application.Environment{Name: "qa-2", Schema: "orders_qa"}, "dev")
	require.NoError(t, err)
	assert.Equal(t, "orders_qa", a.Environments[1].Schema, "a schema named is the one built")
	assert.Equal(t, "orders>orders_qa", roles.cloned[1], "built from the last environment's schema")
	assert.Equal(t, "orders_qa_2", DefaultSchema("orders", "qa-2"))
}

func TestAddEnvironment_SchemaRefusals(t *testing.T) {
	ctx := context.Background()
	fx := schemaEnvFixture(t)
	svc := fx.svc
	store := fx.store
	roles := fx.roles
	add := func(e application.Environment) error {
		_, err := svc.AddEnvironment(ctx, admin, "shop", e, "")
		return err
	}
	require.ErrorContains(t, add(application.Environment{Name: "dev", Schema: "Orders Dev"}), "a lowercase identifier")
	require.ErrorContains(t, add(application.Environment{Name: "dev", Schema: strings.Repeat("o", 51)}), "is 51 bytes")
	require.ErrorContains(t, add(application.Environment{Name: "dev", Schema: "orders"}), "is environment prod's")
	other := &application.Application{Name: "billing", Connection: "orders", Schema: "orders_dev"}
	store.apps["app_billing"] = other
	other.ID = "app_billing"
	require.ErrorIs(t, add(application.Environment{Name: "dev"}), application.ErrSchemaHeld)
	delete(store.apps, "app_billing")

	roles.failClone = errors.New("schema orders_dev would reach schema orders through view open_orders")
	err := add(application.Environment{Name: "dev"})
	require.ErrorContains(t, err, "environment dev was not added: building schema orders_dev from orders: schema orders_dev would reach schema orders")
	roles.failClone = nil

	roles.failCreate = errors.New("no CREATEROLE")
	require.ErrorContains(t, add(application.Environment{Name: "dev"}), "schema orders_dev was built and is left for an administrator to drop")
	roles.failCreate = nil

	svc.Sealer = fakeSealer{disabled: true}
	require.ErrorContains(t, add(application.Environment{Name: "dev"}), "no encryption key")
	svc.Sealer = fakeSealer{}

	svc.Preparer = &countingPreparer{counts: appprepare.Counts{Scripts: 2, Migrations: 1}}
	require.ErrorContains(t, add(application.Environment{Name: "dev"}),
		"2 scripts, 0 libraries and 1 pending migrations name schema orders or its roles")
	svc.Preparer = &countingPreparer{err: errors.New("down")}
	require.ErrorContains(t, add(application.Environment{Name: "dev"}), "down")

	plain, err := svc.Create(ctx, jane, application.Application{Name: "plain"})
	require.NoError(t, err)
	_, err = svc.AddEnvironment(ctx, admin, plain.Name, application.Environment{Name: "dev", Schema: "x"}, "")
	require.ErrorContains(t, err, "holds no schema")
	a, err := svc.AddEnvironment(ctx, admin, plain.Name, application.Environment{Name: "dev"}, "")
	require.NoError(t, err)
	assert.Empty(t, a.Environments[0].Schema, "an application with no schema gives its environments none")
}

// A release applies migrations to the first environment's schema, a promote
// to the target's up to the version the release recorded, before the target
// moves, and a failure there refuses the promote and moves nothing (#2709).
func TestMigrationsApplyPerEnvironment(t *testing.T) {
	ctx := context.Background()
	fx := schemaEnvFixture(t)
	svc := fx.svc
	schema := fx.schema
	roles := fx.roles
	_, err := svc.AddEnvironment(ctx, admin, "shop", application.Environment{Name: "dev"}, "")
	require.NoError(t, err)

	schema.pending = []int{3}
	schema.calls = nil
	rel, err := svc.Release(ctx, admin, "shop", "with migration 3")
	require.NoError(t, err)
	assert.Equal(t, []string{"apply orders_dev -1"}, schema.calls, "a release applies to the first environment's schema")
	assert.Equal(t, []int{3}, rel.Changes.Applied)
	assert.Contains(t, roles.granted, "orders/orders_dev")

	schema.failRelease = errors.New("migration 3 failed in orders")
	_, err = svc.Promote(ctx, admin, "shop", "dev")
	require.ErrorContains(t, err, "nothing was promoted: migration 3 failed")
	a, _ := svc.Find(ctx, "shop")
	assert.Zero(t, a.Last().Release, "prod is unmoved")
	schema.failRelease = nil

	schema.calls = nil
	p, err := svc.Promote(ctx, admin, "shop", "dev")
	require.NoError(t, err)
	assert.Equal(t, []string{"apply orders " + "3"}, schema.calls, "the target's schema applies what the release recorded")
	assert.Equal(t, []int{3}, p.Applied)
}

// A rollback reverts migrations in a schema only its environment runs in,
// and leaves one environments share alone, as before #2709.
func TestRollbackRevertsOnlyAnOwnSchema(t *testing.T) {
	ctx := context.Background()
	fx := schemaEnvFixture(t)
	svc := fx.svc
	store := fx.store
	schema := fx.schema
	_, err := svc.AddEnvironment(ctx, admin, "shop", application.Environment{Name: "dev"}, "")
	require.NoError(t, err)
	schema.pending = []int{3}
	_, err = svc.Release(ctx, admin, "shop", "first")
	require.NoError(t, err)
	schema.pending = []int{4}
	_, err = svc.Release(ctx, admin, "shop", "second")
	require.NoError(t, err)

	schema.calls = nil
	_, err = svc.RollbackIn(ctx, admin, "shop", "dev", 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"rollback orders_dev 3"}, schema.calls, "dev's own schema goes back to what release 1 recorded")

	a := store.apps[mustID(t, svc, "shop")]
	a.Environments[0].Schema = ""
	assert.False(t, exclusive(a, 0), "two environments in the application's schema share it")
	schema.calls = nil
	_, err = svc.RollbackIn(ctx, admin, "shop", "dev", 0)
	require.ErrorContains(t, err, "no earlier release")
	assert.Empty(t, schema.calls)
}

func mustID(t *testing.T, svc *Service, ref string) string {
	t.Helper()
	a, err := svc.Find(context.Background(), ref)
	require.NoError(t, err)
	return a.ID
}

// Removing an environment drops the run role of a schema no other
// environment runs in and keeps the schema (#2709).
func TestRemoveEnvironment_DropsItsRunRole(t *testing.T) {
	ctx := context.Background()
	fx := schemaEnvFixture(t)
	svc := fx.svc
	roles := fx.roles
	_, err := svc.AddEnvironment(ctx, admin, "shop", application.Environment{Name: "dev"}, "")
	require.NoError(t, err)
	_, err = svc.RemoveEnvironment(ctx, admin, "shop", "dev")
	require.NoError(t, err)
	assert.Equal(t, []string{"orders/orders_dev"}, roles.dropped)

	roles.failDrop = errors.New("down")
	_, err = svc.AddEnvironment(ctx, admin, "shop", application.Environment{Name: "qa", Schema: "orders_qa"}, "")
	require.NoError(t, err)
	_, err = svc.RemoveEnvironment(ctx, admin, "shop", "qa")
	require.NoError(t, err, "a role that cannot be dropped is logged, not a failed removal")
}

// countingPreparer answers Named with counts and records plans and saves.
type countingPreparer struct {
	counts appprepare.Counts
	err    error
	saved  []string
}

func (c *countingPreparer) Named(context.Context, *application.Application) (appprepare.Counts, error) {
	return c.counts, c.err
}

func (c *countingPreparer) Plan(_ context.Context, a *application.Application, _ scriptsave.Caller) (*appprepare.Plan, error) {
	return &appprepare.Plan{Application: a.Name, Files: []appprepare.File{}, Counts: c.counts}, c.err
}

func (c *countingPreparer) Save(_ context.Context, a *application.Application, _ scriptsave.Caller, author script.Author) (*appprepare.Plan, error) {
	c.saved = append(c.saved, a.Name+" by "+author.Email)
	return &appprepare.Plan{Application: a.Name, Files: []appprepare.File{}, Saved: true}, c.err
}

// Prepare is read by the owner and administrators and saved by an
// administrator; a gate's refusal is a refusal (#2709).
func TestPrepare(t *testing.T) {
	ctx := context.Background()
	fx := schemaEnvFixture(t)
	svc := fx.svc
	_, err := svc.Prepare(ctx, jane, "shop", false)
	require.ErrorContains(t, err, "cannot prepare applications")

	pr := &countingPreparer{counts: appprepare.Counts{Scripts: 1}}
	svc.Preparer = pr
	plan, err := svc.Prepare(ctx, jane, "shop", false)
	require.NoError(t, err)
	assert.Equal(t, 1, plan.Counts.Scripts)
	_, err = svc.Prepare(ctx, joe, "shop", false)
	require.ErrorIs(t, err, application.ErrNotAllowed)
	_, err = svc.Prepare(ctx, jane, "shop", true)
	require.ErrorIs(t, err, application.ErrNotAllowed, "saving is an administrator's")
	plan, err = svc.Prepare(ctx, admin, "shop", true)
	require.NoError(t, err)
	assert.True(t, plan.Saved)
	assert.Equal(t, []string{"shop by " + admin.Email}, pr.saved)

	pr.err = appprepare.ErrNotReady
	_, err = svc.Prepare(ctx, admin, "shop", true)
	var refusal *RefusedError
	require.ErrorAs(t, err, &refusal)
	_, err = svc.Prepare(ctx, admin, "nope", false)
	require.ErrorIs(t, err, application.ErrNotFound)
}

// The application's page shows each environment's schema, where its
// migrations stand there, and a schema two environments share (#2709).
func TestStatus_EachEnvironmentsSchema(t *testing.T) {
	ctx := context.Background()
	fx := schemaEnvFixture(t)
	_, err := fx.svc.AddEnvironment(ctx, admin, "shop", application.Environment{Name: "dev"}, "")
	require.NoError(t, err)
	fx.svc.Preparer = &countingPreparer{counts: appprepare.Counts{Scripts: 1}}
	st, err := fx.svc.StatusOf(ctx, "shop")
	require.NoError(t, err)
	require.Len(t, st.Schema.Environments, 2)
	assert.Equal(t, EnvironmentSchema{Environment: "dev", Schema: "orders_dev", MigrationVersion: 2, Pending: []int{}}, st.Schema.Environments[0])
	assert.Equal(t, "orders", st.Schema.Environments[1].Schema)
	assert.Equal(t, 1, st.Prepare.Scripts, "the status counts what still names the schema")

	a := fx.store.apps[mustID(t, fx.svc, "shop")]
	a.Environments[0].Schema = ""
	st, err = fx.svc.StatusOf(ctx, "shop")
	require.NoError(t, err)
	assert.Equal(t, "prod", st.Schema.Environments[0].Shared)
}

// An agent's move into an environment that requires approval is refused
// with APPROVAL_REQUIRED; a person's is not (#2709).
func TestApproved(t *testing.T) {
	a := &application.Application{Name: "shop"}
	gated := &application.Environment{Name: "prod", RequiresApproval: true}
	require.NoError(t, approved(application.Actor{}, a, gated), "a person carries it out")
	require.NoError(t, approved(application.Actor{Agent: true}, a, &application.Environment{Name: "dev"}))
	err := approved(application.Actor{Agent: true}, a, gated)
	require.ErrorIs(t, err, application.ErrApprovalRequired)
	assert.Contains(t, err.Error(), "APPROVAL_REQUIRED: environment prod of application shop requires approval")
}
