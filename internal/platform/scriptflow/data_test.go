package scriptflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDataRefs is what each statement step names of its data (#2539, #2540):
// its connection and the tables it reads and writes, marked computed when
// either is only known when the script runs.
func TestDataRefs(t *testing.T) {
	refs := DataRefs(`
def place(tx):
    """Places the order."""
    tx.execute("INSERT INTO shop.orders (id) SELECT id FROM shop.carts", params = {})
    tx.query("SELECT count(*) FROM shop.items")

platform.query("SELECT * FROM shop.orders o JOIN billing.invoices i ON i.o = o.id", connection = "appdb")
platform.query("SELECT 1 FROM hive.s.t")
platform.execute("UPDATE shop.stock SET n = n - 1", connection = "appdb")
platform.query("SELECT * FROM shop.orders", connection = run.params["c"])
platform.query("SELECT * FROM " + run.params["t"], connection = "appdb")
platform.transaction("appdb", place)
platform.batch("appdb", ["DELETE FROM shop.holds", {"sql": "INSERT INTO audit.log VALUES (1)"}])
`)
	require.Len(t, refs, 7)
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{"shop.orders", "billing.invoices"}, Writes: []string{}}, refs[0])
	assert.Equal(t, DataRef{Reads: []string{"hive.s.t"}, Writes: []string{}}, refs[1], "no connection named: the default one")
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{}, Writes: []string{"shop.stock"}}, refs[2])
	assert.True(t, refs[3].Computed, "a computed connection")
	assert.Empty(t, refs[3].Connection)
	assert.True(t, refs[4].Computed, "a computed table")
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{"shop.carts", "shop.items"}, Writes: []string{"shop.orders"}}, refs[5],
		"a transaction's statements through tx")
	assert.Equal(t, DataRef{Connection: "appdb", Reads: []string{}, Writes: []string{"shop.holds", "audit.log"}}, refs[6])

	assert.Equal(t, "public", SchemaOf("orders"))
	assert.Equal(t, "shop", SchemaOf("shop.orders"))
	assert.Equal(t, "shop", SchemaOf(`"shop"."orders"`))
	assert.Equal(t, "s", SchemaOf("hive.s.t"))
	assert.Empty(t, DataRefs("not starlark ("))
}

// TestOutputRefs is what each output step names (#2540).
func TestOutputRefs(t *testing.T) {
	refs := OutputRefs(`
def main():
    """Writes."""
    platform.export("daily", [{"a": 1}], "csv")
    platform.export("x-" + run.params["d"], [{"a": 1}], "csv")
    platform.export("drop", [{"a": 1}], "csv", destination = "acme-drop")
    platform.publish_data("dash", {"n": 1})
    platform.notify("ops", "Done", "body")
    platform.publish("team", "report")
`)
	require.Len(t, refs, 6)
	assert.Equal(t, OutputRef{Kind: KindExport, Name: "daily", Portal: true}, refs[0])
	assert.True(t, refs[1].Computed)
	assert.False(t, refs[2].Portal, "a bucket destination is not an asset")
	assert.Equal(t, OutputRef{Kind: KindPublishData, Name: "dash", Portal: true}, refs[3])
	assert.Equal(t, OutputRef{Kind: KindNotify, Name: "ops"}, refs[4])
	assert.Equal(t, KindPublish, refs[5].Kind)
	assert.Equal(t, "team", refs[5].Name)
}
