package flowrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

func deriveGraph(src string) scriptflow.Graph { return scriptflow.Derive(src) }

// nodeByTitle finds the one node whose title is title.
func nodeByTitle(t *testing.T, g scriptflow.Graph, title string) scriptflow.Node {
	t.Helper()
	for _, n := range g.Nodes {
		if n.Title == title {
			return n
		}
	}
	t.Fatalf("no node titled %q", title)
	return scriptflow.Node{}
}

const overlaySrc = `
def fetch(path):
    return platform.call("api_invoke_endpoint", {"connection": "crm", "method": "GET", "path": path})

rows = platform.query("SELECT 1", connection="warehouse")
fetch("/a")
platform.export("o", rows["rows"], format="csv", destination="resources", key="o.csv",
                register={"connection": "warehouse"})
platform.export("never", [], format="csv")
platform.save_state({"n": 1})
`

func siteOf(t *testing.T, g scriptflow.Graph, title string) []string {
	t.Helper()
	return nodeByTitle(t, g, title).CallSite
}

func TestDraw_CallsOutputsStateAndTheCallsNoCardMade(t *testing.T) {
	g := deriveGraph(overlaySrc)
	require.True(t, g.OK, "%+v", g.Findings)
	q, api, exp := siteOf(t, g, "Query warehouse"), siteOf(t, g, "API crm"), siteOf(t, g, "Export CSV to resources")
	calls := []Call{
		{CallSite: q, Tool: "trino_query", DurationMS: 40, Success: true, ResponseChars: 100},
		{CallSite: api, Tool: "api_invoke_endpoint", DurationMS: 10, Success: false, Error: "rate limited"},
		{CallSite: api, Tool: "api_invoke_endpoint", DurationMS: 12, Success: true},
		{CallSite: exp, Tool: "manage_table", DurationMS: 5, Success: true},
		{Tool: "s3_list", DurationMS: 3, Success: true},
	}
	o := Draw(g, calls, RunFacts{
		Status:     script.RunStatusSucceeded,
		Outputs:    []script.RunOutput{{Name: "o", RowCount: 7, CallSite: exp}},
		StateSaved: true,
	})
	id := func(title string) string { return nodeByTitle(t, g, title).ID }

	assert.Equal(t, 5, o.Calls)
	total := len(o.Other)
	for _, n := range o.Nodes {
		total += n.Calls
	}
	assert.Equal(t, o.Calls, total, "every audited call is on a card or in the other calls")

	assert.Equal(t, NodeRun{Calls: 1, DurationMS: 40, ResponseChars: 100, Reached: true}, o.Nodes[id("Query warehouse")])
	apiRun := o.Nodes[id("API crm")]
	assert.Equal(t, 2, apiRun.Calls)
	assert.Equal(t, int64(22), apiRun.DurationMS)
	assert.Equal(t, 1, apiRun.FailedCalls)
	assert.False(t, apiRun.Failed, "a refused call the run outlived does not fail the card")
	assert.Equal(t, NodeRun{Outputs: 1, Rows: 7, Reached: true}, o.Nodes[id("Export CSV to resources")])
	assert.Equal(t, 1, o.Nodes[id("Table on warehouse")].Calls, "the registration is the table's")
	assert.False(t, o.Nodes[id("Export CSV to portal")].Reached)
	assert.True(t, o.Nodes[id("Save state")].Reached)
	require.Len(t, o.Other, 1)
	assert.Equal(t, "s3_list", o.Other[0].Tool)
	assert.Empty(t, o.FailedNode)
}

func TestDraw_AFailedRunNamesTheCardItFailedAt(t *testing.T) {
	g := deriveGraph(overlaySrc)
	exp := nodeByTitle(t, g, "Export CSV to resources")
	backtrace := "Traceback (most recent call last):\n  script:" + exp.CallSite[0] + ": in <toplevel>\nError in export: the bucket refused the write"
	o := Draw(g, nil, RunFacts{Status: script.RunStatusFailed, Cause: "upstream", Error: backtrace})
	assert.Equal(t, exp.ID, o.FailedNode)
	assert.True(t, o.Nodes[exp.ID].Failed)
	assert.Equal(t, "Error in export: the bucket refused the write", o.Nodes[exp.ID].Error)
	assert.False(t, o.Nodes[nodeByTitle(t, g, "Save state").ID].Reached, "the steps after it never ran")

	o = Draw(g, nil, RunFacts{Status: script.RunStatusFailed, Error: "worker lost"})
	assert.Empty(t, o.FailedNode, "a failure at no card names none")
}
