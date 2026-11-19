//go:build integration

package appdbdrift

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// cloneSource is a schema holding what an application's migrations make:
// a sequence-backed table, a foreign key, an enum, a view, a function whose
// body and search path name the schema, a trigger, an index and a comment,
// written schema-qualified as applications were before #2709.
const cloneSource = `CREATE TYPE orders.status AS ENUM ('open', 'paid');
CREATE TABLE orders.orders (id bigserial PRIMARY KEY, status orders.status NOT NULL DEFAULT 'open', total numeric(10,2));
CREATE TABLE orders.lines (id bigserial PRIMARY KEY, order_id bigint NOT NULL REFERENCES orders.orders(id), sku text);
CREATE INDEX lines_sku ON orders.lines (sku);
CREATE VIEW orders.open_orders AS SELECT id, total FROM orders.orders WHERE status = 'open';
CREATE FUNCTION orders.open_count() RETURNS bigint LANGUAGE sql SET search_path TO orders AS $$ SELECT count(*) FROM orders.orders WHERE status = 'open' $$;
CREATE FUNCTION orders.touch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.total := coalesce(NEW.total, 0); RETURN NEW; END $$;
CREATE TRIGGER orders_touch BEFORE INSERT ON orders.orders FOR EACH ROW EXECUTE FUNCTION orders.touch();
COMMENT ON TABLE orders.orders IS 'The open orders.';
INSERT INTO orders.orders (total) VALUES (10), (20);`

// TestClone_RealDB is #2709's new environment schema against PostgreSQL:
// built from the source's structure, every object retargeted at the new
// schema and reaching nothing in the source, with no row copied.
func TestClone_RealDB(t *testing.T) {
	testdb.OnAppDBTargets(t, func(t *testing.T) {
		db := testdb.AppDBTarget(t)
		tk, store := realKit(t, db)
		ctx := context.Background()
		applyOne(t, tk, store, "orders", 1, cloneSource)

		unsupported, err := Clone(ctx, Toolkit{Toolkit: tk}, "shop", "orders", "orders_dev")
		require.NoError(t, err)
		require.Empty(t, unsupported)

		target := db.TargetDB(t)
		var rows, views, fns int
		require.NoError(t, target.QueryRow(`SELECT count(*) FROM orders_dev.orders`).Scan(&rows))
		assert.Zero(t, rows, "no row is copied")
		require.NoError(t, target.QueryRow(`SELECT count(*) FROM pg_views WHERE schemaname = 'orders_dev'`).Scan(&views))
		assert.Equal(t, 1, views)
		require.NoError(t, target.QueryRow(`SELECT count(*) FROM pg_proc WHERE pronamespace = 'orders_dev'::regnamespace`).Scan(&fns))
		assert.Equal(t, 2, fns)

		_, err = target.Exec(`INSERT INTO orders_dev.orders (total) VALUES (5)`)
		require.NoError(t, err, "the sequence, the default, the enum and the trigger are the new schema's")
		var devCount, prodCount int
		require.NoError(t, target.QueryRow(`SELECT orders_dev.open_count()`).Scan(&devCount))
		require.NoError(t, target.QueryRow(`SELECT orders.open_count()`).Scan(&prodCount))
		assert.Equal(t, 1, devCount, "the new schema's function counts the new schema's rows")
		assert.Equal(t, 2, prodCount)
		var openRows int
		require.NoError(t, target.QueryRow(`SELECT count(*) FROM orders_dev.open_orders`).Scan(&openRows))
		assert.Equal(t, 1, openRows, "the view reads the new schema's table")
		var comment string
		require.NoError(t, target.QueryRow(`SELECT obj_description('orders_dev.orders'::regclass)`).Scan(&comment))
		assert.Equal(t, "The open orders.", comment)

		_, err = Clone(ctx, Toolkit{Toolkit: tk}, "shop", "orders", "orders_dev")
		var refused *postgres.RefusedError
		require.ErrorAs(t, err, &refused, "a schema that exists is never built over")
		assert.Contains(t, err.Error(), `schema "orders_dev" already exists`)
	})
}

// TestBuildSchema_RefusesWhatReachesOut_RealDB: an object of the new schema
// that would reach the schema it must stay apart from refuses the build, and
// nothing of it is kept (#2709).
func TestBuildSchema_RefusesWhatReachesOut_RealDB(t *testing.T) {
	testdb.OnAppDBTargets(t, func(t *testing.T) {
		db := testdb.AppDBTarget(t)
		tk, store := realKit(t, db)
		ctx := context.Background()
		applyOne(t, tk, store, "orders", 1, "CREATE TABLE orders.orders (id int PRIMARY KEY)")
		for name, ddl := range map[string]string{
			"view":        "CREATE VIEW leak AS SELECT id FROM orders.orders",
			"foreign key": "CREATE TABLE refs (id int REFERENCES orders.orders(id))",
			"function":    "CREATE FUNCTION f() RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER AS $$ BEGIN RETURN (SELECT count(*) FROM orders.orders); END $$",
			"path":        "CREATE FUNCTION g() RETURNS int LANGUAGE sql SET search_path TO orders AS $$ SELECT 1 $$",
		} {
			err := tk.BuildSchema(ctx, "shop", "orders_bad", ddl, []string{"orders"})
			var refused *postgres.RefusedError
			require.ErrorAs(t, err, &refused, name)
			assert.Contains(t, err.Error(), `schema "orders_bad" would reach schema orders`, name)
			var exists bool
			require.NoError(t, db.TargetDB(t).QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = 'orders_bad')`).Scan(&exists))
			assert.False(t, exists, "%s: nothing of a refused build is kept", name)
		}
	})
}
