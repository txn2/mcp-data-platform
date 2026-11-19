package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

// fakeRoles answers the run-role statements: whether the user may create
// roles, which roles exist, which are members of which, and the statements
// executed.
type fakeRoles struct {
	canCreate bool
	exists    map[string]bool
	members   map[string]bool
	executed  []string
	rowErr    error
	execErr   map[string]error
}

func (f *fakeRoles) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if f.rowErr != nil {
		return fakeRow{err: f.rowErr}
	}
	var v bool
	switch sql {
	case canCreateRoleSQL:
		v = f.canCreate
	case roleExistsSQL:
		v = f.exists[fmt.Sprint(args[0])]
	case memberOfSQL:
		v = f.members[fmt.Sprint(args[0])+">"+fmt.Sprint(args[1])]
	case schemaExistsSQL:
		v = f.exists["schema:"+fmt.Sprint(args[0])]
	}
	return fakeRow{rows: &fakeRows{rows: [][]any{{v}}}}
}

func (f *fakeRoles) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	for marker, err := range f.execErr {
		if strings.Contains(sql, marker) {
			return pgconn.CommandTag{}, err
		}
	}
	f.executed = append(f.executed, sql)
	if strings.Contains(sql, "CREATE ROLE ") {
		name := strings.Trim(strings.Fields(sql)[5], `"`)
		f.exists[name] = true
	}
	return pgconn.NewCommandTag("OK"), nil
}

// roleToolkitFake is a toolkit serving "db" whose run-role statements f
// answers.
func roleToolkitFake(f *fakeRoles) (*Toolkit, *conn) {
	cfg := baseConfig()
	cfg.Database, cfg.User = "orders", "orders_app"
	tk := fakeToolkit(cfg, &fakeTx{})
	c := tk.connections["db"]
	c.roles = &rolePools{pools: map[string]*pgxpool.Pool{}, q: f}
	return tk, c
}

// TestCreateRunRole is #2539's run role: refused without CREATEROLE naming
// the statement, refused over a role of the same name, and otherwise created
// with a SCRAM verifier (never the password) and granted the write role the
// schema defines.
func TestCreateRunRole(t *testing.T) {
	ctx := context.Background()
	f := &fakeRoles{exists: map[string]bool{"shop_write": true}, members: map[string]bool{}}
	tk, c := roleToolkitFake(f)

	_, _, err := tk.CreateRunRole(ctx, "db", "shop")
	var refused *RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, codeCreateRoleRequired, refused.Code)
	assert.Contains(t, err.Error(), `ALTER ROLE "orders_app" CREATEROLE`)

	f.canCreate = true
	role, password, err := tk.CreateRunRole(ctx, "db", "shop")
	require.NoError(t, err)
	assert.Equal(t, c.runRoleName("shop"), role)
	assert.True(t, strings.HasPrefix(role, "shop_run_"))
	assert.GreaterOrEqual(t, len(password), 40)
	require.Len(t, f.executed, 3, "a schema not created yet has nothing to grant on")
	assert.Contains(t, f.executed[0], "PASSWORD 'SCRAM-SHA-256$4096:")
	assert.NotContains(t, f.executed[0], password, "the statement carries the verifier, never the password")
	assert.Contains(t, f.executed[0], "NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS")
	assert.Contains(t, f.executed[1], `ALTER ROLE "`+role+`" SET search_path TO "shop"`, "its statements name their own schema's tables without it")
	assert.Contains(t, f.executed[2], `GRANT "shop_write" TO "`+role+`"`)

	_, _, err = tk.CreateRunRole(ctx, "db", "shop")
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, codeRoleExists, refused.Code, "a role of the same name is refused")

	f.exists["other_write"] = true
	f.execErr = map[string]error{`GRANT "other_write"`: errors.New("grant failed")}
	_, _, err = tk.CreateRunRole(ctx, "db", "other")
	require.Error(t, err)
	assert.Contains(t, f.executed[len(f.executed)-1], "DROP ROLE IF EXISTS", "a role not granted is dropped again")
	f.execErr = map[string]error{"CREATE ROLE": errors.New("no")}
	_, _, err = tk.CreateRunRole(ctx, "db", "third")
	require.Error(t, err)

	f.rowErr = errors.New("down")
	_, _, err = tk.CreateRunRole(ctx, "db", "fourth")
	require.Error(t, err)
	_, _, err = tk.CreateRunRole(ctx, "nope", "shop")
	require.Error(t, err)
	_, _, err = tk.CreateRunRole(ctx, "db", "Bad Schema")
	require.Error(t, err)
}

// TestGrantDropAndMembership grants the write role only when both roles
// exist and the run role is not a member yet, drops a run role and what it
// was granted (a missing one is no error), and reads membership.
func TestGrantDropAndMembership(t *testing.T) {
	ctx := context.Background()
	f := &fakeRoles{exists: map[string]bool{}, members: map[string]bool{}}
	tk, c := roleToolkitFake(f)
	run := c.runRoleName("shop")

	granted, err := tk.GrantRunRole(ctx, "db", "shop")
	require.NoError(t, err)
	assert.Empty(t, granted, "no run role yet")
	f.exists[run] = true
	granted, err = tk.GrantRunRole(ctx, "db", "shop")
	require.NoError(t, err)
	assert.Empty(t, granted, "no write role yet")
	f.exists["shop_write"] = true
	f.members[run+">shop_write"] = true
	granted, err = tk.GrantRunRole(ctx, "db", "shop")
	require.NoError(t, err)
	assert.Empty(t, granted, "already a member")

	is, err := tk.IsMember(ctx, "db", run, "shop_write")
	require.NoError(t, err)
	assert.True(t, is)
	is, err = tk.IsMember(ctx, "db", run, "absent")
	require.NoError(t, err)
	assert.False(t, is)
	_, err = tk.IsMember(ctx, "nope", run, "shop_write")
	require.ErrorIs(t, err, ErrConnectionNotFound)

	dropFrom := len(f.executed)
	require.NoError(t, tk.DropRunRole(ctx, "db", "shop"))
	assert.Equal(t, []string{
		`/* mcp-data-platform */ GRANT "` + run + `" TO CURRENT_USER WITH INHERIT TRUE, SET TRUE`,
		`/* mcp-data-platform */ DROP OWNED BY "` + run + `"`,
		`/* mcp-data-platform */ DROP ROLE IF EXISTS "` + run + `"`,
	}, f.executed[dropFrom:], "the connection's user takes the role's privileges before DROP OWNED BY needs them (#2717)")
	f.exists[run] = false
	before := len(f.executed)
	require.NoError(t, tk.DropRunRole(ctx, "db", "shop"), "a role already gone")
	assert.Len(t, f.executed, before)

	f.exists[run] = true
	f.execErr = map[string]error{"TO CURRENT_USER": errors.New("no")}
	require.Error(t, tk.DropRunRole(ctx, "db", "shop"))
	f.execErr = map[string]error{"DROP OWNED": errors.New("no")}
	require.Error(t, tk.DropRunRole(ctx, "db", "shop"))
	f.execErr = map[string]error{"DROP ROLE": errors.New("no")}
	require.Error(t, tk.DropRunRole(ctx, "db", "shop"))
	f.rowErr = errors.New("down")
	require.Error(t, tk.DropRunRole(ctx, "db", "shop"))
	_, err = tk.GrantRunRole(ctx, "db", "shop")
	require.Error(t, err)
	_, err = tk.IsMember(ctx, "db", run, "shop_write")
	require.Error(t, err)
	require.Error(t, tk.DropRunRole(ctx, "nope", "shop"))
	_, err = tk.GrantRunRole(ctx, "nope", "shop")
	require.Error(t, err)
}

// TestRunRolePool: a statement of an application holding a schema on the
// connection runs on its run role's pool, built once; one of an application
// holding none runs as the persona's role or the connection's own.
func TestRunRolePool(t *testing.T) {
	ctx := context.Background()
	cfg := baseConfig()
	cfg.ConnectionName, cfg.Host, cfg.Database = "db", "127.0.0.1", "orders"
	tk := NewMulti(MultiConfig{DefaultName: "db", Instances: map[string]Config{"db": cfg}})
	t.Cleanup(func() { _ = tk.Close() })
	c, ok := tk.lookup("db")
	require.True(t, ok)

	_, ok, err := tk.runRolePool(ctx, c, "app_1", "")
	require.NoError(t, err)
	assert.False(t, ok, "no applications installed")
	tk.SetApplications(
		func(context.Context, string, string) (string, error) { return "shop", nil },
		func(_ context.Context, _, app, _ string) (string, string, error) {
			switch app {
			case "app_1":
				return "shop_run_x", "pw", nil
			case "broken":
				return "", "", errors.New("down")
			}
			return "", "", nil
		})
	p, err := c.identityPool(ctx, Identity{Application: "app_1"})
	require.NoError(t, err)
	assert.Equal(t, "shop_run_x", p.Config().ConnConfig.User)
	again, err := c.identityPool(ctx, Identity{Application: "app_1"})
	require.NoError(t, err)
	assert.Same(t, p, again, "built once")
	own, err := c.identityPool(ctx, Identity{Application: "app_none"})
	require.NoError(t, err)
	assert.Same(t, c.pool, own, "an application holding no schema here runs as the connection's own")
	_, err = c.identityPool(ctx, Identity{Application: "broken"})
	require.Error(t, err)

	c.forgetRole("shop_run_x")
	fresh, err := c.identityPool(ctx, Identity{Application: "app_1"})
	require.NoError(t, err)
	assert.NotSame(t, p, fresh, "a forgotten role's pool is built again")
	c.forgetRole("absent")

	tk.SetApplications(nil, nil)
	assert.Nil(t, tk.applications())
	c.roles = nil
	_, err = c.rolePool("k", "r", "p")
	require.ErrorIs(t, err, errNoRolePools)
	c.forgetRole("x")
}

// schemaLedger is a fakeLedger that also answers which schemas the ledger
// records.
type schemaLedger struct {
	*fakeLedger
	schemas []string
	err     error
}

func (l *schemaLedger) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if strings.Contains(sql, "SELECT DISTINCT schema_name") {
		if l.err != nil {
			return nil, l.err
		}
		rows := make([][]any, 0, len(l.schemas))
		for _, s := range l.schemas {
			rows = append(rows, []any{s})
		}
		return &fakeRows{rows: rows}, nil
	}
	return l.fakeLedger.Query(ctx, sql, args...)
}

// TestSchemaGate is #2539 in manage_schema: add, plan, apply and down on a
// schema no application holds are refused naming add_part, and so is the
// portal's plan; status and the report name the holder and every unheld
// schema with migrations, kept or in the ledger.
func TestSchemaGate(t *testing.T) {
	ctx := context.Background()
	ledger := &schemaLedger{fakeLedger: &fakeLedger{exists: true}, schemas: []string{"loose", "shop"}}
	tk, store := schemaToolkit(&fakeLedger{exists: true}, baseConfig())
	tk.connections["shop"].ledgerQ = ledger
	store.migrations = []Migration{{Connection: "shop", Schema: "kept", Version: 1, Name: "k", Up: "SELECT 1"}}
	holders := map[string]string{"shop": "storefront"}
	tk.SetApplications(
		func(_ context.Context, _, schema string) (string, error) {
			if schema == "broken" {
				return "", errors.New("down")
			}
			return holders[schema], nil
		},
		func(context.Context, string, string, string) (string, string, error) { return "", "", nil })

	for _, cmd := range []string{schemaAdd, schemaPlan, schemaApply, schemaDown} {
		_, refusal := run(ctx, t, tk, schemaInput{Command: cmd, Schema: "loose", Version: 1, Name: "x", Up: "SELECT 1"})
		assert.Contains(t, refusal, "belongs to no application", cmd)
		assert.Contains(t, refusal, "manage_application add_part", cmd)
		assert.Contains(t, refusal, "shop/loose", cmd)
	}
	_, err := tk.PendingPlan(ctx, "shop", "loose")
	var refused *RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, codeSchemaNotHeld, refused.Code)
	_, refusal := run(ctx, t, tk, schemaInput{Command: schemaAdd, Schema: "broken", Version: 1, Name: "x", Up: "SELECT 1"})
	assert.Contains(t, refusal, "reading which application holds schema")

	out, refusal := run(ctx, t, tk, schemaInput{Command: schemaStatus, Schema: "shop"})
	require.Empty(t, refusal)
	assert.Equal(t, "storefront", out["application"])
	unheld := asJSONString(t, out["unheld_schemas"])
	assert.Contains(t, unheld, `"schema":"kept"`)
	assert.Contains(t, unheld, `"schema":"loose"`)
	assert.NotContains(t, unheld, `"schema":"shop"`)
	assert.Contains(t, unheld, "part_id=shop/loose")

	report, err := tk.Report(ctx, "shop", "shop")
	require.NoError(t, err)
	assert.Equal(t, "storefront", report.Application)
	require.NotNil(t, report.UnheldSchemas)
	assert.Len(t, *report.UnheldSchemas, 2)

	ledger.err = errors.New("down")
	_, err = tk.Report(ctx, "shop", "shop")
	require.Error(t, err)
	_, refusal = run(ctx, t, tk, schemaInput{Command: schemaStatus, Schema: "shop"})
	assert.Contains(t, refusal, "reading the ledger")
	ledger.err = nil
	holders["shop"] = ""
	_, err = tk.Report(ctx, "shop", "broken")
	require.Error(t, err)

	ledger.exists = false
	names, err := tk.migratedSchemas(ctx, "shop")
	require.NoError(t, err)
	assert.Equal(t, []string{"kept"}, names, "no ledger yet: the kept migrations alone")
	store.err = errors.New("down")
	_, err = tk.migratedSchemas(ctx, "shop")
	require.Error(t, err)
	_, err = tk.migratedSchemas(ctx, "nope")
	require.ErrorIs(t, err, ErrConnectionNotFound)

	tk.SetApplications(nil, nil)
	none, err := tk.unheldSchemas(ctx, "shop")
	require.NoError(t, err)
	assert.Empty(t, none)
	_, err = tk.Report(ctx, "shop", "shop")
	require.Error(t, err, "the store is down")
	store.err = nil
	report, err = tk.Report(ctx, "shop", "shop")
	require.NoError(t, err)
	assert.Nil(t, report.UnheldSchemas, "absent when no schema is governed")
}

func asJSONString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// TestApplyGrantsTheRunRole: apply on a schema an application holds grants
// its run role the write role the migrations defined, and says so.
func TestApplyGrantsTheRunRole(t *testing.T) {
	tk, _ := schemaToolkit(&fakeLedger{exists: true}, baseConfig())
	c := tk.connections["shop"]
	f := &fakeRoles{exists: map[string]bool{c.runRoleName(DefaultSchema): true, DefaultSchema + "_write": true}, members: map[string]bool{}}
	c.roles = &rolePools{pools: map[string]*pgxpool.Pool{}, q: f}
	tk.SetApplications(
		func(context.Context, string, string) (string, error) { return "shop", nil },
		func(context.Context, string, string, string) (string, string, error) { return "", "", nil })
	admin := mcpcontext.WithCaller(context.Background(), mcpcontext.Caller{UserID: "a", IsAdmin: true})
	out, refusal := run(admin, t, tk, schemaInput{Command: schemaApply})
	require.Empty(t, refusal)
	assert.Equal(t, DefaultSchema+"_write to "+c.runRoleName(DefaultSchema), out["granted"])
}

// TestGrantRunRole_TheSchemasGrantsFromThePlatform is #2709's run role: a
// schema whose migrations define no write role has its run role granted
// USAGE and DML on that schema alone, once the schema exists.
func TestGrantRunRole_TheSchemasGrantsFromThePlatform(t *testing.T) {
	ctx := context.Background()
	f := &fakeRoles{exists: map[string]bool{}, members: map[string]bool{}}
	tk, c := roleToolkitFake(f)
	run := c.runRoleName("orders_dev")
	f.exists[run] = true

	granted, err := tk.GrantRunRole(ctx, "db", "orders_dev")
	require.NoError(t, err)
	assert.Empty(t, granted)
	assert.Equal(t, []string{`/* mcp-data-platform */ ALTER ROLE "` + run + `" SET search_path TO "orders_dev"`}, f.executed,
		"a schema not created yet has nothing to grant on")

	f.exists["schema:orders_dev"], f.executed = true, nil
	_, err = tk.GrantRunRole(ctx, "db", "orders_dev")
	require.NoError(t, err)
	require.Len(t, f.executed, 5)
	for _, stmt := range f.executed[1:] {
		assert.Contains(t, stmt, `"orders_dev"`)
		assert.Contains(t, stmt, `TO "`+run+`"`)
	}
	assert.Contains(t, f.executed[2], "GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA")

	f.execErr = map[string]error{"ON ALL SEQUENCES": errors.New("no")}
	_, err = tk.GrantRunRole(ctx, "db", "orders_dev")
	require.Error(t, err)
	f.execErr, f.rowErr = nil, nil
	f.execErr = map[string]error{"SET search_path": errors.New("no")}
	_, err = tk.GrantRunRole(ctx, "db", "orders_dev")
	require.Error(t, err)
}

// buildSession is a held connection whose transaction answers whether the
// schema exists and what it reaches (#2709).
type buildSession struct {
	*fakeSession
	exists  bool
	reaches []string
	tx      *fakeMigTx
}

func (b *buildSession) Begin(context.Context) (pgx.Tx, error) {
	rows := make([][]any, 0, len(b.reaches))
	for _, r := range b.reaches {
		rows = append(rows, []any{r})
	}
	b.tx = &fakeMigTx{fakeTx: &fakeTx{statements: map[string]fakeStatement{
		schemaExistsSQL: {rows: [][]any{{b.exists}}},
		reachesOutSQL:   {rows: rows},
	}}}
	return b.tx, nil
}

// TestBuildSchema builds a schema in one transaction and commits it only
// when nothing in it reaches the schema it must stay apart from; one that
// exists is never built over (#2709).
func TestBuildSchema(t *testing.T) {
	ctx := context.Background()
	b := &buildSession{fakeSession: &fakeSession{}}
	tk := liveToolkit(b.fakeSession, baseConfig())
	tk.connections["shop"].openSession = func(context.Context) (session, error) { return b, nil }

	require.NoError(t, tk.BuildSchema(ctx, "shop", "orders_dev", "CREATE TABLE t (id int);", []string{"orders"}))
	assert.True(t, b.tx.committed)
	assert.Equal(t, []string{"CREATE TABLE t (id int);"}, b.tx.scripts)
	assert.Contains(t, strings.Join(b.tx.execs, "\n"), "CREATE SCHEMA IF NOT EXISTS orders_dev")

	b.reaches = []string{"view leak"}
	err := tk.BuildSchema(ctx, "shop", "orders_dev", "CREATE VIEW leak AS SELECT 1;", []string{"orders"})
	var refused *RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, codeSchemaReachesOut, refused.Code)
	assert.Contains(t, err.Error(), "through view leak")
	assert.False(t, b.tx.committed, "a refused build keeps nothing")

	b.reaches, b.exists = nil, true
	err = tk.BuildSchema(ctx, "shop", "orders_dev", "SELECT 1;", []string{"orders"})
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, codeSchemaExists, refused.Code)

	b.exists = false
	require.NoError(t, tk.BuildSchema(ctx, "shop", "orders_dev", "SELECT 1;", nil), "nothing to stay apart from checks nothing")
	require.Error(t, tk.BuildSchema(ctx, "nope", "orders_dev", "SELECT 1;", nil))
	require.Error(t, tk.BuildSchema(ctx, "shop", "Bad Schema", "SELECT 1;", nil))
	tk.connections["shop"].openSession = func(context.Context) (session, error) { return nil, errors.New("no pool") }
	require.Error(t, tk.BuildSchema(ctx, "shop", "orders_dev", "SELECT 1;", nil))
}
