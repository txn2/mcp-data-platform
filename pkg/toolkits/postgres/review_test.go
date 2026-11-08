package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/pglint"
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
	assert.Equal(t, pglint.RuleDropColumn, first["rule"])
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
	assert.Equal(t, pglint.RuleIndexLocks, f["rule"])
	assert.Equal(t, pglint.SeverityWarning, f["severity"])
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
	assert.Equal(t, pglint.RuleRenameReferenced, f["rule"])
	assert.Equal(t, []any{"order-intake", "weekly-report"}, f["scripts"])
	assert.Contains(t, f["effect"], "order-intake, weekly-report")

	tk.SetScriptReferences(func(context.Context, string, []string) (map[string][]string, error) {
		return nil, errors.New("index down")
	})
	_, renames := pglint.Lint("ALTER TABLE orders RENAME TO purchases", pglint.Target{})
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
	assert.Equal(t, pglint.RuleFromZeroUnchecked, f["rule"])
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

func TestReviewRefusesAndReportsTheTarget(t *testing.T) {
	ctx := mcpcontext.WithCaller(context.Background(), mcpcontext.Caller{UserID: "u1", Email: "dev@example.com", IsAdmin: true})
	tk, store := schemaToolkit(&fakeLedger{exists: true}, baseConfig())
	_, refusal := run(ctx, t, tk, schemaInput{Command: schemaAdd, Version: 2, Name: "sys", Up: "CREATE TABLE a (id int);\nALTER SYSTEM SET work_mem = '1GB'"})
	assert.Contains(t, refusal, "migration 2 is refused")
	assert.Contains(t, refusal, "statement 2 (ALTER SYSTEM)")
	assert.Empty(t, store.migrations, "nothing saved")

	// The down SQL is held to the same refusals (#2475).
	const canary = "down-canary-2475"
	_, refusal = run(ctx, t, tk, schemaInput{
		Command: schemaAdd, Version: 2, Name: "down", Up: "CREATE TABLE a (id int)",
		Down: "DROP TABLE a;\nALTER ROLE app PASSWORD '" + canary + "'",
	})
	assert.Contains(t, refusal, "down statement 2 (ALTER ROLE)")
	assert.NotContains(t, refusal, canary)
	assert.Empty(t, store.migrations, "nothing saved")

	tk, _ = schemaToolkit(&fakeLedger{exists: true, targetErr: errors.New("connection refused")}, baseConfig())
	out, refusal := run(ctx, t, tk, schemaInput{Command: schemaAdd, Version: 2, Name: "t", Up: "CREATE TABLE t (id uuid DEFAULT uuidv7())"})
	require.Empty(t, refusal)
	findings, _ := out["findings"].([]any)
	require.Len(t, findings, 1, "the version rules did not run, and the add says so")
	first, _ := findings[0].(map[string]any)
	assert.Equal(t, pglint.RuleTargetUnread, first["rule"])

	tk, _ = schemaToolkit(&fakeLedger{exists: true, target: pglint.Target{Major: 18}}, baseConfig())
	out, refusal = run(ctx, t, tk, schemaInput{Command: schemaAdd, Version: 2, Name: "t", Up: "CREATE TABLE t (id uuid DEFAULT uuidv7())"})
	require.Empty(t, refusal)
	assert.Empty(t, out["findings"], "18 has uuidv7")
}

func TestWithServerFindings(t *testing.T) {
	plain := errors.New("plain")
	assert.Equal(t, plain, withServerFindings(plain, nil))
	failed := &RefusedError{Code: CodeFromZeroFailed, msg: "applied from zero: syntax error"}
	assert.Equal(t, failed, withServerFindings(failed, []pglint.Finding{{Rule: "row_security_not_forced"}}))
	got := withServerFindings(failed, []pglint.Finding{{Rule: pglint.RuleNeedsNewerServer, Statement: 1, Effect: "uses uuidv7()", Safe: "gen_random_uuid()"}})
	var refused *RefusedError
	require.ErrorAs(t, got, &refused)
	assert.Equal(t, CodeFromZeroFailed, refused.Code)
	assert.Contains(t, got.Error(), "syntax error. The review found: statement 1 uses uuidv7() (instead: gen_random_uuid())")
}

func TestReviewReadsTheSchemasRowSecurityAndVirtualColumns(t *testing.T) {
	ctx := mcpcontext.WithCaller(context.Background(), mcpcontext.Caller{UserID: "u1", Email: "dev@example.com", IsAdmin: true})
	tk, _ := schemaToolkit(&fakeLedger{exists: true, target: pglint.Target{
		Major: 18, RowSecurity: map[string]bool{"notes": true}, Virtual: map[string]map[string]bool{"t": {"b": true}},
	}}, baseConfig())
	out, refusal := run(ctx, t, tk, schemaInput{
		Command: schemaAdd, Version: 2, Name: "v",
		Up: "CREATE VIEW mine AS SELECT id FROM notes;\nCREATE INDEX t_b ON t (b)",
	})
	require.Empty(t, refusal)
	findings, _ := out["findings"].([]any)
	rules := make([]string, 0, len(findings))
	for _, raw := range findings {
		f, _ := raw.(map[string]any)
		rule, _ := f["rule"].(string)
		rules = append(rules, rule)
	}
	assert.ElementsMatch(t, []string{"view_bypasses_row_security", pglint.RuleIndexVirtualColumn, pglint.RuleIndexLocks}, rules)

	target, unread := tk.lintTarget(ctx, "nope", "public")
	assert.Zero(t, target.Major)
	assert.Nil(t, unread, "an unknown connection is refused by the add, not reported here")
}
