//go:build integration

package appdbdrift

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/migrationstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// realKit is the postgres toolkit serving the target as "shop", keeping its
// migrations in the platform's database.
func realKit(t *testing.T, db testdb.AppDB) (*postgres.Toolkit, *migrationstore.Store) {
	t.Helper()
	multi, err := postgres.ParseMultiConfig("shop", map[string]map[string]any{"shop": db.Target})
	require.NoError(t, err)
	tk := postgres.NewMulti(multi)
	tk.SetConnectGuard(postgres.OwnDatabaseGuard(db.Platform))
	store := migrationstore.New(db.Platform)
	tk.SetMigrationStore(store)
	t.Cleanup(func() { _ = tk.Close() })
	return tk, store
}

func applyOne(t *testing.T, tk *postgres.Toolkit, store *migrationstore.Store, schema string, version int, up string) {
	t.Helper()
	m := postgres.Migration{
		Connection: "shop", Schema: schema, Version: version, Name: "m", Up: up,
		Transactional: true, Checksum: postgres.ChecksumOf(up, "", true),
	}
	_, err := store.Put(context.Background(), m)
	require.NoError(t, err)
	all, err := store.List(context.Background(), "shop", schema)
	require.NoError(t, err)
	_, err = tk.Apply(context.Background(), postgres.ApplyOptions{Connection: "shop", Schema: schema, Actor: "test"}, all)
	require.NoError(t, err)
}

// TestDrift_RealDB is #2406's first criterion against PostgreSQL: a column
// added by hand outside migrations is reported naming its table and column,
// and dropping it leaves nothing to report. A pending migration is not
// drift.
func TestDrift_RealDB(t *testing.T) {
	testdb.OnAppDBTargets(t, func(t *testing.T) {
		db := testdb.AppDBTarget(t)
		tk, store := realKit(t, db)
		ctx := context.Background()
		applyOne(t, tk, store, "public", 1, "CREATE TABLE orders (id bigint PRIMARY KEY, total numeric(10,2) NOT NULL)")

		res := Check(ctx, tk, store, "shop", "public")
		require.True(t, res.Checked, res.Reason)
		assert.Empty(t, res.Differences)
		assert.Equal(t, 1, res.AppliedVersion)

		target := db.TargetDB(t)
		_, err := target.Exec(`ALTER TABLE orders ADD COLUMN coupon text`)
		require.NoError(t, err)
		res = Check(ctx, tk, store, "shop", "public")
		require.True(t, res.Checked, res.Reason)
		assert.Equal(t, []Difference{{Kind: "column", Object: "orders.coupon", Change: ChangeAdded}}, res.Differences)

		pending := "ALTER TABLE orders ADD COLUMN note text"
		_, err = store.Put(ctx, postgres.Migration{
			Connection: "shop", Schema: "public", Version: 2, Name: "note", Up: pending,
			Transactional: true, Checksum: postgres.ChecksumOf(pending, "", true),
		})
		require.NoError(t, err)
		res = Check(ctx, tk, store, "shop", "public")
		assert.Equal(t, []Difference{{Kind: "column", Object: "orders.coupon", Change: ChangeAdded}}, res.Differences,
			"a pending migration is built only once it is applied")

		_, err = target.Exec(`ALTER TABLE orders DROP COLUMN coupon`)
		require.NoError(t, err)
		res = Check(ctx, tk, store, "shop", "public")
		require.True(t, res.Checked, res.Reason)
		assert.Empty(t, res.Differences)

		none := Check(ctx, tk, store, "shop", "billing")
		assert.False(t, none.Checked)
		assert.Contains(t, none.Reason, "adopt")
	})
}

// legacySchema is a schema built by hand, with every kind of object a
// baseline writes.
const legacySchema = `
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE SCHEMA legacy;
CREATE TYPE legacy.status AS ENUM ('new', 'paid', 'shipped');
CREATE DOMAIN legacy.money AS numeric(12,2) NOT NULL DEFAULT 0 CONSTRAINT money_positive CHECK (VALUE >= 0);
CREATE SEQUENCE legacy.invoice_no AS integer START WITH 1000 INCREMENT BY 1;
CREATE TABLE legacy.customers (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  email text COLLATE "C" NOT NULL UNIQUE,
  name text NOT NULL,
  name_lower text GENERATED ALWAYS AS (lower(name)) STORED,
  created_at timestamptz NOT NULL DEFAULT now()
) WITH (fillfactor = 90);
CREATE TABLE legacy.orders (
  id bigserial PRIMARY KEY,
  customer_id bigint NOT NULL REFERENCES legacy.customers (id) ON DELETE CASCADE,
  status legacy.status NOT NULL DEFAULT 'new',
  total legacy.money,
  invoice integer DEFAULT nextval('legacy.invoice_no'),
  placed_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT total_cap CHECK (total < 1000000)
);
ALTER SEQUENCE legacy.invoice_no OWNED BY legacy.orders.invoice;
CREATE INDEX orders_customer_idx ON legacy.orders (customer_id) WHERE status <> 'shipped';
CREATE TABLE legacy.events (id bigint NOT NULL, at date NOT NULL, body jsonb) PARTITION BY RANGE (at);
CREATE TABLE legacy.events_2026 PARTITION OF legacy.events FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');
CREATE INDEX events_body_idx ON legacy.events USING gin (body);
CREATE FUNCTION legacy.touch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.placed_at := now(); RETURN NEW; END $$;
CREATE TRIGGER orders_touch BEFORE UPDATE ON legacy.orders FOR EACH ROW EXECUTE FUNCTION legacy.touch();
CREATE VIEW legacy.open_orders WITH (security_invoker = true) AS SELECT id, customer_id, total FROM legacy.orders WHERE status <> 'shipped';
CREATE MATERIALIZED VIEW legacy.daily AS SELECT date_trunc('day', placed_at) AS day, count(*) AS n FROM legacy.orders GROUP BY 1;
CREATE UNIQUE INDEX daily_day ON legacy.daily (day);
CREATE FUNCTION legacy.order_count(c bigint) RETURNS bigint LANGUAGE sql STABLE BEGIN ATOMIC SELECT count(*) FROM legacy.orders WHERE customer_id = c; END;
ALTER TABLE legacy.orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE legacy.orders FORCE ROW LEVEL SECURITY;
CREATE POLICY own_orders ON legacy.orders AS PERMISSIVE FOR SELECT TO PUBLIC USING (customer_id = current_setting('app.customer', true)::bigint);
COMMENT ON TABLE legacy.customers IS 'Who buys';
COMMENT ON COLUMN legacy.customers.email IS 'Unique, case-sensitive';
`

// TestAdopt_RealDB is #2406's second criterion against PostgreSQL: adopting a
// schema built by hand writes a baseline marked applied, a throwaway database
// built from it reports no drift against the original, and adopting it again
// is refused. An object a baseline cannot write refuses the adoption and
// writes nothing.
func TestAdopt_RealDB(t *testing.T) {
	testdb.OnAppDBTargets(t, func(t *testing.T) {
		db := testdb.AppDBTarget(t)
		tk, store := realKit(t, db)
		ctx := context.Background()
		target := db.TargetDB(t)
		_, err := target.Exec(legacySchema)
		require.NoError(t, err)

		res, err := Adopt(ctx, Toolkit{tk}, store, Target{Connection: "shop", Schema: "legacy", Actor: "admin@example.com"})
		require.NoError(t, err)
		require.Empty(t, res.Unsupported)
		require.Empty(t, res.Differences, "the baseline, built from zero, matches:\n%s", res.Up)
		assert.True(t, res.Adopted)

		migrations, err := store.List(ctx, "shop", "legacy")
		require.NoError(t, err)
		require.Len(t, migrations, 1)
		st, err := tk.Status(ctx, "shop", "legacy", migrations)
		require.NoError(t, err)
		require.Len(t, st.Applied, 1, "the baseline is marked applied")
		assert.Empty(t, st.Pending)

		check := Check(ctx, tk, store, "shop", "legacy")
		require.True(t, check.Checked, check.Reason)
		assert.Empty(t, check.Differences)

		_, err = Adopt(ctx, Toolkit{tk}, store, Target{Connection: "shop", Schema: "legacy", Actor: "admin@example.com"})
		require.ErrorIs(t, err, ErrManaged)

		_, err = target.Exec(`CREATE SCHEMA ruled; CREATE TABLE ruled.t (id int); CREATE TABLE ruled.log (id int);
			CREATE RULE t_log AS ON INSERT TO ruled.t DO ALSO INSERT INTO ruled.log VALUES (NEW.id)`)
		require.NoError(t, err)
		refused, err := Adopt(ctx, Toolkit{tk}, store, Target{Connection: "shop", Schema: "ruled", Actor: "admin@example.com"})
		require.NoError(t, err)
		assert.False(t, refused.Adopted)
		assert.Equal(t, []Unsupported{{Kind: "rule", Name: "t_log"}}, refused.Unsupported)
		none, err := store.List(ctx, "shop", "ruled")
		require.NoError(t, err)
		assert.Empty(t, none, "a refused adoption saves nothing")
	})
}
