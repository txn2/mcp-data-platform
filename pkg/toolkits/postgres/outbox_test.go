package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutboxIdentifier(t *testing.T) {
	ident, err := OutboxIdentifier("app.outbox")
	require.NoError(t, err)
	assert.Equal(t, `"app"."outbox"`, ident)
	for _, bad := range []string{"outbox", "App.outbox", `app.out"box`, "app.out box", "a.b.c", "app." + strings.Repeat("x", 60)} {
		_, err := OutboxIdentifier(bad)
		assert.Error(t, err, bad)
	}
	assert.Equal(t, "app.outbox", OutboxChannel("app", "outbox"))
}

func TestOutboxTemplate(t *testing.T) {
	up, down, err := OutboxTemplate("app", "events", []string{"orders", "billing.invoices"})
	require.NoError(t, err)
	for _, want := range []string{
		`CREATE TABLE "app"."events"`, `CREATE FUNCTION "app"."events_capture"()`, `pg_notify('app.events', '')`,
		`ON "app"."orders" FOR EACH ROW`, `ON "billing"."invoices" FOR EACH ROW`,
	} {
		assert.Contains(t, up, want)
	}
	assert.Equal(t, `DROP TRIGGER IF EXISTS "events_capture" ON "app"."orders";
DROP TRIGGER IF EXISTS "events_capture" ON "billing"."invoices";
DROP FUNCTION IF EXISTS "app"."events_capture"();
DROP TABLE IF EXISTS "app"."events";
`, down)
	require.NoError(t, checkMigrationSQL(up, true), "the template passes the checks every migration does")
	require.NoError(t, checkMigrationSQL(down, true))

	_, _, err = OutboxTemplate("app", "events", []string{"Orders"})
	require.Error(t, err)
	_, _, err = OutboxTemplate("app", "Events", nil)
	require.Error(t, err)
}

func TestFromTemplate(t *testing.T) {
	in, err := fromTemplate(schemaInput{Schema: "public", Template: OutboxTemplateName, Capture: []string{"orders"}})
	require.NoError(t, err)
	assert.Equal(t, "outbox public.outbox", in.Name)
	assert.Contains(t, in.Up, `CREATE TABLE "public"."outbox"`)
	assert.NotEmpty(t, in.Down)

	named, err := fromTemplate(schemaInput{Schema: "app", Template: OutboxTemplateName, OutboxTable: "events", Name: "events outbox"})
	require.NoError(t, err)
	assert.Equal(t, "events outbox", named.Name)
	assert.Contains(t, named.Up, `"app"."events"`)

	plain := schemaInput{Schema: "public", Up: "CREATE TABLE t (id int)"}
	same, err := fromTemplate(plain)
	require.NoError(t, err)
	assert.Equal(t, plain, same)

	for name, in := range map[string]schemaInput{
		"unknown template":      {Template: "audit"},
		"template beside SQL":   {Template: OutboxTemplateName, Up: "SELECT 1"},
		"capture with no table": {Capture: []string{"orders"}},
		"invalid capture":       {Schema: "public", Template: OutboxTemplateName, Capture: []string{"Orders"}},
	} {
		_, err := fromTemplate(in)
		assert.Error(t, err, name)
	}
}

// outboxTx is a fake transaction holding rows in the outbox public.outbox.
func outboxTx(rows [][]any) *fakeTx {
	sel, _ := outboxSQL(`"public"."outbox"`)
	return &fakeTx{statements: map[string]fakeStatement{sel: {rows: rows, tag: "SELECT"}}}
}

func TestDrainOutbox_FilesThenDeletesInOneTransaction(t *testing.T) {
	created := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	tx := outboxTx([][]any{
		{int64(1), "public.orders", "INSERT", `{"id": 7}`, created},
		{int64(2), "public.orders", "DELETE", `{"id": 6}`, created},
	})
	tk := fakeToolkit(baseConfig(), tx)
	var got []OutboxRow
	n, err := tk.DrainOutbox(context.Background(), "db", "public.outbox", 10, func(rows []OutboxRow) error { got = rows; return nil })
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, []OutboxRow{
		{ID: 1, Table: "public.orders", Op: "INSERT", Payload: `{"id": 7}`, CreatedAt: created},
		{ID: 2, Table: "public.orders", Op: "DELETE", Payload: `{"id": 6}`, CreatedAt: created},
	}, got)
	_, del := outboxSQL(`"public"."outbox"`)
	assert.Contains(t, tx.execs, del, "the filed rows are deleted")
	assert.True(t, tx.committed, "the read and the delete commit together")
	assert.Equal(t, pgx.ReadWrite, tx.opts.AccessMode)
}

func TestDrainOutbox_AFailedWriteKeepsEveryRow(t *testing.T) {
	tx := outboxTx([][]any{{int64(1), "public.orders", "INSERT", `{}`, time.Now()}})
	tk := fakeToolkit(baseConfig(), tx)
	boom := errors.New("the segment write failed")
	_, err := tk.DrainOutbox(context.Background(), "db", "public.outbox", 10, func([]OutboxRow) error { return boom })
	require.ErrorIs(t, err, boom, "the write's own failure reaches the caller")
	assert.Empty(t, tx.execs, "no row is deleted")
	assert.False(t, tx.committed)
	assert.True(t, tx.rolledBack)
}

func TestDrainOutbox_EmptyAndRefusals(t *testing.T) {
	tx := outboxTx(nil)
	tk := fakeToolkit(baseConfig(), tx)
	called := false
	n, err := tk.DrainOutbox(context.Background(), "db", "public.outbox", 10, func([]OutboxRow) error { called = true; return nil })
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.False(t, called, "write is not called with no rows")

	_, err = tk.DrainOutbox(context.Background(), "nope", "public.outbox", 10, nil)
	require.ErrorIs(t, err, ErrConnectionNotFound)
	_, err = tk.DrainOutbox(context.Background(), "db", "Outbox", 10, nil)
	require.Error(t, err)

	failing := outboxTx(nil)
	sel, _ := outboxSQL(`"public"."outbox"`)
	failing.statements[sel] = fakeStatement{execErr: &pgconn.PgError{Code: "42P01", Message: `relation "public.outbox" does not exist`}}
	_, err = fakeToolkit(baseConfig(), failing).DrainOutbox(context.Background(), "db", "public.outbox", 10, nil)
	require.ErrorContains(t, err, "42P01")

	deleting := outboxTx([][]any{{int64(1), "public.orders", "INSERT", `{}`, time.Now()}})
	deleting.execErr = &pgconn.PgError{Code: "42501", Message: "permission denied for table outbox"}
	_, err = fakeToolkit(baseConfig(), deleting).DrainOutbox(context.Background(), "db", "public.outbox", 10, func([]OutboxRow) error { return nil })
	require.ErrorContains(t, err, "42501", "a delete the role may not make fails the drain")
}

func TestCheckOutbox(t *testing.T) {
	allowed := &fakeTx{statements: map[string]fakeStatement{outboxPrivilegeSQL: {rows: [][]any{{true}}, tag: "SELECT"}}}
	require.NoError(t, fakeToolkit(baseConfig(), allowed).CheckOutbox(context.Background(), "db", "public.outbox"))
	assert.Equal(t, pgx.ReadOnly, allowed.opts.AccessMode)

	refused := &fakeTx{statements: map[string]fakeStatement{outboxPrivilegeSQL: {rows: [][]any{{false}}, tag: "SELECT"}}}
	err := fakeToolkit(baseConfig(), refused).CheckOutbox(context.Background(), "db", "public.outbox")
	require.ErrorIs(t, err, errOutboxPrivileges)

	missing := &fakeTx{statements: map[string]fakeStatement{outboxPrivilegeSQL: {execErr: &pgconn.PgError{Code: "42P01", Message: "relation does not exist"}}}}
	require.ErrorContains(t, fakeToolkit(baseConfig(), missing).CheckOutbox(context.Background(), "db", "public.outbox"), "42P01")

	require.ErrorIs(t, fakeToolkit(baseConfig(), allowed).CheckOutbox(context.Background(), "nope", "public.outbox"), ErrConnectionNotFound)
	require.Error(t, fakeToolkit(baseConfig(), allowed).CheckOutbox(context.Background(), "db", "outbox"))
}

func TestListen_UnknownConnection(t *testing.T) {
	_, err := fakeToolkit(baseConfig(), &fakeTx{}).Listen(context.Background(), "nope", []string{"ch"})
	require.ErrorIs(t, err, ErrConnectionNotFound)
}
