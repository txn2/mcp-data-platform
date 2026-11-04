package postgres

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RunMaintenance runs one ANALYZE, VACUUM or REINDEX outside a transaction
// under its lock timeout (#2410), and nothing else.
func TestRunMaintenance(t *testing.T) {
	sess := &fakeSession{}
	tk := liveToolkit(sess, baseConfig())
	require.NoError(t, tk.RunMaintenance(context.Background(), "shop", `VACUUM (ANALYZE) "orders"`, time.Second, 0))
	require.Len(t, sess.execs, 2)
	assert.Contains(t, sess.execs[0], "lock_timeout")
	assert.Equal(t, `VACUUM (ANALYZE) "orders"`, sess.execs[1])
	assert.Empty(t, sess.txs, "no transaction is opened")
	assert.True(t, sess.released)

	for _, stmt := range []string{"DROP TABLE orders", "CREATE INDEX i ON orders (id)", ""} {
		err := tk.RunMaintenance(context.Background(), "shop", stmt, time.Second, 0)
		var refused *RefusedError
		require.ErrorAs(t, err, &refused, stmt)
	}
	require.Error(t, tk.RunMaintenance(context.Background(), "nope", "ANALYZE", time.Second, 0))

	failing := &fakeSession{execErr: errors.New("lock timeout")}
	require.Error(t, liveToolkit(failing, baseConfig()).RunMaintenance(context.Background(), "shop", "analyze", time.Second, 0))
	assert.Len(t, failing.execs, 1, "a failed setting runs no statement")

	broken := liveToolkit(&fakeSession{}, baseConfig())
	broken.connections["shop"].openSession = func(context.Context) (session, error) { return nil, errors.New("no pool") }
	require.Error(t, broken.RunMaintenance(context.Background(), "shop", "ANALYZE", time.Second, 0))
}

func TestConnectionRolesAndApplyPersonas(t *testing.T) {
	cfg := baseConfig()
	cfg.User = "orders_app"
	cfg.PersonaRoles = map[string]string{"analyst": "orders_reader", "builder": "orders_app"}
	cfg.SchemaApplyPersonas = []string{"builder"}
	tk := liveToolkit(&fakeSession{}, cfg)
	assert.Equal(t, []string{"orders_app", "orders_reader"}, tk.ConnectionRoles("shop"))
	assert.Nil(t, tk.ConnectionRoles("nope"))
	assert.Equal(t, []string{"builder"}, tk.ApplyPersonas("shop"))
	assert.Nil(t, tk.ApplyPersonas("nope"))
}

// MarkOnly writes a baseline's ledger row without running it (#2406).
func TestApplyMarkOnly(t *testing.T) {
	sess := &fakeSession{ledger: fakeLedger{exists: true}}
	o := applyOpts()
	o.MarkOnly = true
	done, err := liveToolkit(sess, baseConfig()).Apply(context.Background(), o, []Migration{migration(1, "CREATE TABLE a (id int)", "", true)})
	require.NoError(t, err)
	assert.Equal(t, []int{1}, versionsOf(done))
	assert.Empty(t, sess.txs, "the baseline's SQL never runs")
	assert.NotContains(t, strings.Join(sess.scripts, "\n"), "CREATE TABLE a")
	assert.Contains(t, strings.Join(sess.execs, "\n"), "INSERT INTO mcp_schema.migrations")
}

// status and describe add what SetSchemaChecks installs (#2406, #2409).
func TestSchemaChecks(t *testing.T) {
	ledger := &fakeLedger{exists: true, applied: applied(1)}
	tk, store := schemaToolkit(ledger, baseConfig())
	store.migrations = []Migration{{Connection: "shop", Schema: DefaultSchema, Version: 1, Name: "m", Up: "up", Transactional: true, Checksum: ChecksumOf("up", "", true)}}
	ctx := context.Background()
	status, _ := run(ctx, t, tk, schemaInput{Command: schemaStatus})
	assert.NotContains(t, status, "schema_drift", "nothing is added without a check")

	var asked string
	tk.SetSchemaChecks(func(_ context.Context, connection, schema string) any {
		asked = connection + "/" + schema
		return map[string]any{"checked": true, "differences": []any{}}
	}, nil)
	status, _ = run(ctx, t, tk, schemaInput{Command: schemaStatus})
	assert.Equal(t, map[string]any{"checked": true, "differences": []any{}}, status["schema_drift"])
	assert.Equal(t, "shop/public", asked)
}

// DescribeBuilt builds the migrations' template, describes a clone of it,
// and drops the clone however the describe ends (#2406).
func TestDescribeBuilt(t *testing.T) {
	srv := newServer(true)
	tk, built := testServerToolkit(srv, baseConfig())
	scratch := tk.scratchHook
	tk.scratchHook = func(name string, cfg Config) (*Toolkit, error) {
		child, err := scratch(name, cfg)
		if err == nil {
			child.connections[name].begin = func(context.Context, pgx.TxOptions) (pgx.Tx, error) {
				return nil, errors.New("describe refused")
			}
		}
		return child, err
	}
	ms := []Migration{migration(1, "CREATE TABLE a (id int)", "", true)}
	_, err := tk.DescribeBuilt(context.Background(), "shop", DefaultSchema, ms)
	require.ErrorContains(t, err, "describe refused")
	assert.Len(t, built, 2, "the template was built and a clone made of it")
	assert.Empty(t, srv.has(clonePrefix), "the clone is dropped")

	_, err = tk.DescribeBuilt(context.Background(), "nope", DefaultSchema, ms)
	require.Error(t, err)
}

// The same configuration again keeps the live pool, so a call that looked the
// connection up is not failed by the announcement of a save it already took
// on; a changed configuration replaces it.
func TestUpdateConnectionKeepsThePoolForTheSameConfig(t *testing.T) {
	cfg := map[string]any{"host": "db.invalid", "port": 5432, "database": "orders", "user": "app", "password": "pw", "sslmode": "disable"}
	tk := NewMulti(MultiConfig{})
	t.Cleanup(func() { _ = tk.Close() })
	require.NoError(t, tk.AddConnection("shop", cfg))
	first := tk.connections["shop"]
	require.NoError(t, tk.UpdateConnection("shop", cfg))
	assert.Same(t, first, tk.connections["shop"], "the same configuration keeps the live pool")

	changed := maps.Clone(cfg)
	changed["database"] = "orders2"
	require.NoError(t, tk.UpdateConnection("shop", changed))
	assert.NotSame(t, first, tk.connections["shop"], "a changed configuration replaces it")
}
