package flowcompare

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow"
)

func deriveGraph(src string) scriptflow.Graph { return scriptflow.Derive(src) }

func changes(g scriptflow.Graph) map[string][]string {
	out := map[string][]string{}
	for _, n := range g.Nodes {
		if n.Change != "" {
			out[n.Change] = append(out[n.Change], n.Title)
		}
	}
	return out
}

const compareBase = `
def pull():
    rows = platform.query("SELECT * FROM hive.s.orders", connection="warehouse")
    platform.export("orders", rows["rows"], format="csv")

def report():
    rows = platform.query("SELECT count(*) FROM hive.s.orders", connection="lake")
    platform.export("summary", rows["rows"], format="csv")

pull()
report()
`

func TestCompare_AnAddedExportIsOneAddedCard(t *testing.T) {
	v2 := compareBase + `platform.export("extra", [], format="csv", destination="acme-drop", key="x.csv")
`
	g := Compare(deriveGraph(compareBase), deriveGraph(v2), 1)
	assert.Equal(t, 1, g.ComparedWith)
	assert.Equal(t, map[string][]string{scriptflow.ChangeAdded: {"Export CSV to acme-drop"}}, changes(g))
}

func TestCompare_AMovedDestinationIsOneChangedCardNamingBoth(t *testing.T) {
	v1 := `platform.export("orders", [], format="csv")
`
	v2 := `platform.export("orders", [], format="csv", destination="acme-drop", key="orders.csv")
`
	g := Compare(deriveGraph(v1), deriveGraph(v2), 1)
	require.Equal(t, map[string][]string{scriptflow.ChangeChanged: {"Export CSV to acme-drop"}}, changes(g))
	require.NotNil(t, g.Nodes[0].Was)
	assert.Equal(t, "Export CSV to portal", g.Nodes[0].Was.Title)
}

func TestCompare_ReorderingFunctionsChangesNothing(t *testing.T) {
	reordered := `
def report():
    rows = platform.query("SELECT count(*) FROM hive.s.orders", connection="lake")
    platform.export("summary", rows["rows"], format="csv")

def pull():
    rows = platform.query("SELECT * FROM hive.s.orders", connection="warehouse")
    platform.export("orders", rows["rows"], format="csv")

report()
pull()
`
	g := Compare(deriveGraph(compareBase), deriveGraph(reordered), 1)
	assert.Empty(t, changes(g))
	assert.Len(t, g.Nodes, 4)
}

func TestCompare_ARemovedStepIsCarriedOverWithItsEdges(t *testing.T) {
	v2 := `
def pull():
    rows = platform.query("SELECT * FROM hive.s.orders", connection="warehouse")
    platform.export("orders", rows["rows"], format="csv")
pull()
`
	g := Compare(deriveGraph(compareBase), deriveGraph(v2), 1)
	got := changes(g)
	assert.ElementsMatch(t, []string{"Query lake", "Export CSV to portal"}, got[scriptflow.ChangeRemoved])
	var removed []scriptflow.Edge
	for _, e := range g.Edges {
		if e.Change == scriptflow.ChangeRemoved {
			removed = append(removed, e)
		}
	}
	require.Len(t, removed, 1)
	assert.Contains(t, removed[0].From, removedPrefix)
	assert.Contains(t, removed[0].To, removedPrefix)
}

func TestCompare_AComputedValueComparesAsItsSource(t *testing.T) {
	v1 := `platform.export("o", [], format="csv", destination=run.params["d"], key="k")
`
	g := Compare(deriveGraph(v1), deriveGraph(v1), 1)
	assert.Empty(t, changes(g))
	v2 := `platform.export("o", [], format="csv", destination=run.params["target"], key="k")
`
	g = Compare(deriveGraph(v1), deriveGraph(v2), 1)
	require.Equal(t, []string{`Export CSV to {run.params["target"]}`}, changes(g)[scriptflow.ChangeChanged])
	assert.Equal(t, `Export CSV to {run.params["d"]}`, g.Nodes[0].Was.Title)
}

func TestCompare_StateAndAPIStepsMatchByWhatNamesThem(t *testing.T) {
	v1 := `
s = run.state
platform.call("api_invoke_endpoint", {"connection": "crm", "method": "GET", "path": "/a", "purpose": "Read a."})
platform.call("manage_asset", {"action": "update", "asset_id": "x"})
platform.save_state({})
`
	v2 := `
s = run.state
platform.call("api_invoke_endpoint", {"connection": "crm2", "method": "GET", "path": "/a", "purpose": "Read a."})
platform.call("manage_asset", {"action": "update", "asset_id": "y"})
platform.save_state({})
`
	g := Compare(deriveGraph(v1), deriveGraph(v2), 1)
	assert.ElementsMatch(t, []string{"API crm2", "manage asset"}, changes(g)[scriptflow.ChangeChanged])
}

// A query is named by the table it reads, so the same query moved to another
// connection is one card that changed, and the run's state is one step
// however it is read (#1908).
func TestCompare_AQueryMovedToAnotherConnectionIsOneChange(t *testing.T) {
	v1 := `
s = run.state
rows = platform.query("SELECT * FROM hive.s.orders", connection="warehouse")
platform.save_state({"n": len(rows["rows"])})
`
	v2 := `
rows = platform.query("SELECT * FROM hive.s.orders", connection="lake")
st = run.state
platform.save_state({"n": len(rows["rows"]), "was": st})
`
	g := Compare(deriveGraph(v1), deriveGraph(v2), 1)
	c := changes(g)
	assert.Equal(t, []string{"Query lake"}, c[scriptflow.ChangeChanged])
	assert.Empty(t, c[scriptflow.ChangeAdded])
	assert.Empty(t, c[scriptflow.ChangeRemoved])
}
