package appdbdrift

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRetarget points every name the baseline qualifies with the source
// schema at the new one, in DDL, defaults, bodies and search_path settings,
// and leaves a table or column that only shares its name alone (#2709).
func TestRetarget(t *testing.T) {
	ddl := `CREATE TABLE orders.orders (id integer DEFAULT nextval('orders.orders_id_seq'::regclass), note text);
ALTER TABLE "orders".lines ADD CONSTRAINT lines_fk FOREIGN KEY (order_id) REFERENCES orders.orders(id);
CREATE VIEW orders.open AS SELECT o.id, o.orders FROM orders.orders o;
CREATE OR REPLACE FUNCTION orders.total()
 RETURNS bigint
 SET search_path TO 'orders', 'public'
AS $$ SELECT count(*) FROM orders.orders $$;
CREATE TABLE orders.reorders (id integer);
COMMENT ON TABLE orders.orders IS 'The open orders.';`
	got := Retarget(ddl, "orders", "orders_dev")
	assert.NotContains(t, got, "orders.orders ")
	assert.NotContains(t, got, `"orders".`)
	assert.Contains(t, got, "CREATE TABLE orders_dev.orders (")
	assert.Contains(t, got, "nextval('orders_dev.orders_id_seq'::regclass)")
	assert.Contains(t, got, "REFERENCES orders_dev.orders(id)")
	assert.Contains(t, got, "SELECT o.id, o.orders FROM orders_dev.orders o", "a column named like the schema stays")
	assert.Contains(t, got, "SET search_path TO 'orders_dev', 'public'")
	assert.Contains(t, got, "$$ SELECT count(*) FROM orders_dev.orders $$")
	assert.Contains(t, got, "CREATE TABLE orders_dev.reorders", "a name only ending in the schema's is not the schema")
	assert.Contains(t, got, "IS 'The open orders.'", "a sentence ending in the schema's name is not a qualifier")
	assert.Equal(t, "SET search_path TO orders_dev, orders_dev", Retarget("SET search_path TO orders, orders", "orders", "orders_dev"))
}

// cloneKit renders nothing it is asked for but records the build.
type cloneKit struct {
	readErr  error
	built    []string
	buildErr error
}

func (k *cloneKit) ReadTx(context.Context, string) (Tx, error) { return nil, k.readErr }

func (k *cloneKit) BuildSchema(_ context.Context, _, schema, ddl string, isolated []string) error {
	k.built = append(k.built, schema+"|"+ddl+"|"+isolated[0])
	return k.buildErr
}

func TestClone_RefusesWhatItCannotRead(t *testing.T) {
	k := &cloneKit{readErr: errors.New("down")}
	_, err := Clone(context.Background(), k, "db", "orders", "orders_dev")
	require.ErrorContains(t, err, "down")
	assert.Empty(t, k.built, "nothing is built from a schema that was not read")
}
