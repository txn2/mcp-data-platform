package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

func TestAddRefusesAMigrationThatRemovesDataWithoutAnAgreedSummary(t *testing.T) {
	tk, store := schemaToolkit(&fakeLedger{exists: true}, baseConfig())
	ctx := mcpcontext.WithCaller(context.Background(), mcpcontext.Caller{UserID: "u1", Email: "dev@example.com", IsAdmin: true})
	in := schemaInput{Command: schemaAdd, Version: 2, Name: "drop note", Up: "ALTER TABLE orders DROP COLUMN note"}

	_, refusal := run(ctx, t, tk, in)
	assert.Contains(t, refusal, "migration 2 removes data")
	assert.Contains(t, refusal, "drops column note of orders")
	assert.Contains(t, refusal, "change_summary")
	assert.Empty(t, store.migrations, "nothing saved")

	in.ChangeSummary = "Removes the free-text note on every order."
	_, refusal = run(ctx, t, tk, in)
	assert.Contains(t, refusal, "user_agreed=true", "a summary the person has not agreed to is not enough")

	in.UserAgreed = true
	out, refusal := run(ctx, t, tk, in)
	require.Empty(t, refusal)
	findings, ok := out["findings"].([]any)
	require.True(t, ok)
	require.Len(t, findings, 1)
	first, ok := findings[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, RuleDropColumn, first["rule"])
	require.Len(t, store.events, 1)
	assert.Equal(t, EventAdded, store.events[0].Event)
	assert.Contains(t, store.events[0].Detail, "change summary agreed by dev@example.com: Removes the free-text note on every order.")
	assert.Contains(t, store.events[0].Detail, "drop_column on orders.note (statement 1)")
}

func TestAddWarnsAndSaves(t *testing.T) {
	tk, store := schemaToolkit(&fakeLedger{exists: true}, baseConfig())
	out, refusal := run(context.Background(), t, tk, schemaInput{
		Command: schemaAdd, Version: 1, Name: "index",
		Up: "CREATE INDEX orders_email ON orders (email)",
	})
	require.Empty(t, refusal)
	findings, _ := out["findings"].([]any)
	require.Len(t, findings, 1)
	f, ok := findings[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, RuleIndexLocks, f["rule"])
	assert.Equal(t, SeverityWarning, f["severity"])
	assert.NotEmpty(t, f["safe_form"])
	require.Len(t, store.migrations, 1)
	assert.Equal(t, "create_index_locks on orders (statement 1)", store.events[0].Detail)
}

func TestAddNamesTheScriptsARenameBreaks(t *testing.T) {
	tk, _ := schemaToolkit(&fakeLedger{exists: true}, baseConfig())
	var asked []string
	tk.SetScriptReferences(func(_ context.Context, connection string, names []string) (map[string][]string, error) {
		assert.Equal(t, "shop", connection)
		asked = names
		return map[string][]string{"email": {"order-intake", "weekly-report"}}, nil
	})
	out, refusal := run(context.Background(), t, tk, schemaInput{
		Command: schemaAdd, Version: 1, Name: "rename",
		Up: "ALTER TABLE orders RENAME COLUMN email TO contact_email; ALTER TABLE orders RENAME COLUMN note TO memo",
	})
	require.Empty(t, refusal)
	assert.Equal(t, []string{"email", "note"}, asked)
	findings, _ := out["findings"].([]any)
	require.Len(t, findings, 1, "a rename no script names is not reported")
	f, ok := findings[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, RuleRenameReferenced, f["rule"])
	assert.Equal(t, []any{"order-intake", "weekly-report"}, f["scripts"])
	assert.Contains(t, f["effect"], "order-intake, weekly-report")

	tk.SetScriptReferences(func(context.Context, string, []string) (map[string][]string, error) {
		return nil, errors.New("index down")
	})
	_, renames := Lint("ALTER TABLE orders RENAME TO purchases", Target{})
	assert.Empty(t, tk.renameFindings(context.Background(), "shop", renames), "a lookup failure reports nothing rather than guessing")
}

func TestAddWithoutCreateDBWarnsThatFromZeroWasNotChecked(t *testing.T) {
	tk, store := schemaToolkit(&fakeLedger{exists: true}, Config{User: "orders_app"})
	srv := newServer(false)
	tk.connections["shop"].openSession = func(context.Context) (session, error) { return srv, nil }
	out, refusal := run(context.Background(), t, tk, schemaInput{Command: schemaAdd, Version: 1, Name: "t", Up: "CREATE TABLE t (id int)"})
	require.Empty(t, refusal)
	findings, _ := out["findings"].([]any)
	require.Len(t, findings, 1)
	f, ok := findings[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, RuleFromZeroUnchecked, f["rule"])
	assert.Contains(t, f["effect"], "CREATEDB")
	assert.Len(t, store.migrations, 1)
}

func TestAddRefusesAMigrationSetThatDoesNotApplyFromZero(t *testing.T) {
	tk, store := schemaToolkit(&fakeLedger{exists: true}, baseConfig())
	tk.scratchHook = func(name string, cfg Config) (*Toolkit, error) {
		sess := &fakeSession{ledger: fakeLedger{exists: true}, txScriptErr: map[int]error{0: errors.New("relation does not exist")}}
		cfg.ConnectionName, cfg.LedgerSchema = name, DefaultLedgerSchema
		return &Toolkit{connections: map[string]*conn{name: {
			cfg: cfg, types: newTypeCache(), ledgerQ: &sess.ledger,
			openSession: func(context.Context) (session, error) { return sess, nil },
		}}}, nil
	}
	_, refusal := run(context.Background(), t, tk, schemaInput{Command: schemaAdd, Version: 1, Name: "t", Up: "ALTER TABLE missing ADD COLUMN n int"})
	assert.Contains(t, refusal, "do not apply from zero")
	assert.Empty(t, store.migrations)
}

func TestWithMigration(t *testing.T) {
	a1 := Migration{Schema: "a", Version: 1, Name: "old"}
	b1 := Migration{Schema: "b", Version: 1}
	got := withMigration([]Migration{b1, a1}, Migration{Schema: "a", Version: 1, Name: "new"})
	require.Len(t, got, 2)
	assert.Equal(t, "new", got[0].Name)
	got = withMigration(got, Migration{Schema: "a", Version: 2})
	assert.Equal(t, []int{1, 2, 1}, versionsOf(got))
}
