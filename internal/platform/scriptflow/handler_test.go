package scriptflow

import (
	"testing"

	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// anEndpoint is a script bound to an endpoint: no main(), a handle(req) the
// platform calls with each request (#2172).
const anEndpoint = `
def handle(req):
    rows = platform.query("SELECT id, total FROM hive.sales.orders WHERE id = ?", [req["params"]["id"]], connection="trino")
    if not rows["rows"]:
        return {"status": 404, "body": "no such order"}
    return {"status": 200, "body": rows["rows"][0]}
`

func TestDerive_AnEndpointsHandlerIsDrawn(t *testing.T) {
	g := deriveGraph(anEndpoint)
	require.True(t, g.OK, "%+v", g.Findings)
	q := nodeByTitle(t, g, "Query trino")
	assert.Equal(t, []string{"hive.sales.orders"}, q.Detail)
	assert.NotEmpty(t, g.Structure.Nodes, "the handler is drawn in the order it runs")
}

// aForm is a form script (#2176): form_spec(req) reads the plans, and
// form_submit(req, values) writes the submission.
const aForm = `
def form_spec(req):
    plans = platform.query("SELECT name FROM hive.sales.plans", connection="trino")
    return {"fields": [{"name": "plan", "type": "single_choice", "choices": plans["rows"]}]}

def form_submit(req, values):
    platform.call("api_invoke_endpoint", {"connection": "crm", "method": "POST", "path": "/v1/leads", "body": values})
    return None
`

func TestDerive_AFormsTwoHandlersAreDrawn(t *testing.T) {
	g := deriveGraph(aForm)
	require.True(t, g.OK, "%+v", g.Findings)
	q := nodeByTitle(t, g, "Query trino")
	assert.Equal(t, []string{"hive.sales.plans"}, q.Detail)
	nodeByTitle(t, g, "API crm")
	steps := 0
	for _, n := range g.Structure.Nodes {
		if n.Kind == "step" {
			steps++
		}
	}
	assert.Equal(t, 2, steps, "the spec's query and the submission's call")
	assert.Nil(t, entryCalls(parsed(t, "def form_spec(req):\n    pass\n")), "a spec handler alone is not a form")
}

// parsed parses source for entryCalls.
func parsed(t *testing.T, source string) *syntax.File {
	t.Helper()
	file, err := scriptdialect.Options.Parse("s", source, 0)
	require.NoError(t, err)
	return file
}
