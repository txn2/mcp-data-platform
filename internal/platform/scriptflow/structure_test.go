package scriptflow

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/scriptcallsite"
)

const structured = `
LIMIT = 1000

# Checks the window reaches back no further than the source keeps.
def check_window(hours):
    if hours > 719:
        fail("window_hours reaches back past the 30-day limit")

# Reads every page of changes since the watermark.
def read_pages(since):
    first = platform.call("api_invoke_endpoint", {"connection": "crm", "method": "GET", "path": "/changes"})
    rows = []
    for p in range(2, 4):
        page = platform.call("api_invoke_endpoint", {"connection": "crm", "method": "GET", "path": "/changes"})
        rows.append(page)
        if len(page) == 0:
            break
    return rows

def fetch_one(path):
    return platform.call("api_invoke_endpoint", {"connection": "crm", "method": "GET", "path": path})

def main():
    check_window(run.params.get("hours", 12))
    rows = read_pages(run.state.get("since", ""))
    if len(rows) == 0:
        return {"changed": 0}
    detail = fetch_one("/detail")
    platform.export("changes", rows, format="csv")
    platform.save_state({"since": "now"})
    return {"changed": len(rows)}
`

func structNodes(s Structure, kind string) []StructNode {
	var out []StructNode
	for _, n := range s.Nodes {
		if n.Kind == kind {
			out = append(out, n)
		}
	}
	return out
}

func structEdge(s Structure, from, to string) (StructEdge, bool) {
	for _, e := range s.Edges {
		if e.From == from && e.To == to {
			return e, true
		}
	}
	return StructEdge{}, false
}

// acyclic reports whether the structure's edges form a DAG.
func acyclic(s Structure) bool {
	succ := map[string][]string{}
	for _, e := range s.Edges {
		succ[e.From] = append(succ[e.From], e.To)
	}
	state := map[string]int{}
	var visit func(string) bool
	visit = func(n string) bool {
		switch state[n] {
		case 1:
			return false
		case 2:
			return true
		}
		state[n] = 1
		for _, m := range succ[n] {
			if !visit(m) {
				return false
			}
		}
		state[n] = 2
		return true
	}
	for _, n := range s.Nodes {
		if !visit(n.ID) {
			return false
		}
	}
	return true
}

func TestStructure_OneStartExitsDecisionsAndBoxes(t *testing.T) {
	g := deriveGraph(structured)
	require.True(t, g.OK, "%+v", g.Findings)
	s := g.Structure

	starts, ends := structNodes(s, StructStart), structNodes(s, StructEnd)
	require.Len(t, starts, 1)
	require.Len(t, ends, 1)
	for _, e := range s.Edges {
		assert.NotEqual(t, starts[0].ID, e.To, "nothing runs before Start")
		assert.NotEqual(t, ends[0].ID, e.From, "nothing runs after End")
	}
	assert.True(t, acyclic(s), "the structure is a DAG: %+v", s.Edges)

	stops := structNodes(s, StructStop)
	require.Len(t, stops, 1)
	assert.Equal(t, "window_hours reaches back past the 30-day limit", stops[0].Label)
	for _, e := range s.Edges {
		assert.NotEqual(t, stops[0].ID, e.From, "a fail() ends the run")
	}

	returns := structNodes(s, StructReturn)
	require.Len(t, returns, 1, "the early return; the last return is the normal end")

	ifs := structNodes(s, StructIf)
	labels := make([]string, 0, len(ifs))
	for _, n := range ifs {
		labels = append(labels, n.Label)
	}
	assert.ElementsMatch(t, []string{"hours > 719", "len(page) == 0", "len(rows) == 0"}, labels)
	for _, n := range ifs {
		arms := map[string]bool{}
		for _, e := range s.Edges {
			if e.From == n.ID {
				arms[e.Label] = true
			}
		}
		assert.Equal(t, map[string]bool{ArmYes: true, ArmNo: true}, arms, "if %q has a yes and a no arm", n.Label)
	}

	boxes := map[string]StructBox{}
	for _, b := range s.Boxes {
		boxes[b.Label] = b
	}
	require.Contains(t, boxes, "check_window(hours)")
	require.Contains(t, boxes, "read_pages(since)")
	require.Contains(t, boxes, "for p in range(2, 4)")
	assert.Equal(t, BoxFunction, boxes["read_pages(since)"].Kind)
	assert.Equal(t, "Reads every page of changes since the watermark.", boxes["read_pages(since)"].Caption)
	loop := boxes["for p in range(2, 4)"]
	assert.Equal(t, BoxLoop, loop.Kind)
	assert.Equal(t, boxes["read_pages(since)"].ID, loop.Parent)
	assert.NotContains(t, boxes, `fetch_one(path)`, "a one-call wrapper is folded into its card")

	steps := structNodes(s, StructStep)
	require.Len(t, steps, 5, "two pages, the detail, the export and the save")
	byID := map[string]Node{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	for _, n := range steps {
		card, ok := byID[n.Step]
		require.True(t, ok, "step %s names a card of the value graph", n.ID)
		assert.Equal(t, card.CallSite, n.CallSite, "the two views agree on where the call is made")
	}
	inLoop := 0
	for _, n := range steps {
		if n.Box == loop.ID {
			inLoop++
		}
	}
	assert.Equal(t, 1, inLoop, "the paged call is drawn inside the loop")

	names := make([]string, 0, len(s.Functions))
	for _, f := range s.Functions {
		names = append(names, f.Name)
	}
	assert.Equal(t, []string{"check_window", "read_pages", "fetch_one", "main"}, names)
}

// The early return and the fail() leave the flow; the steps after the if run
// on its no arm.
func TestStructure_ArmsJoinWhereTheNextStatementIs(t *testing.T) {
	s := deriveGraph(structured).Structure
	var early StructNode
	for _, n := range s.Nodes {
		if n.Kind == StructIf && n.Label == "len(rows) == 0" {
			early = n
		}
	}
	require.NotEmpty(t, early.ID)
	ret := structNodes(s, StructReturn)[0]
	e, ok := structEdge(s, early.ID, ret.ID)
	require.True(t, ok)
	assert.Equal(t, ArmYes, e.Label)
	var next string
	for _, e := range s.Edges {
		if e.From == early.ID && e.Label == ArmNo {
			next = e.To
		}
	}
	require.NotEmpty(t, next)
	for _, n := range s.Nodes {
		if n.ID == next {
			assert.Equal(t, StructStep, n.Kind, "the no arm goes on to the detail fetch")
		}
	}
}

// A run that fails in fail() inside a helper records a backtrace whose
// innermost frames are the stop node's call site: the join the run overlay
// marks the failed node by.
func TestStructure_AFailedRunsBacktraceNamesItsStop(t *testing.T) {
	_, err := scriptrun.Run(context.Background(), scriptrun.Options{
		Source: structured, Name: "changes", Caller: &recorder{}, Exporter: &recorder{},
		Params: map[string]any{"hours": 800},
	})
	require.Error(t, err)
	site := scriptcallsite.FromBacktrace(err.Error())
	require.NotEmpty(t, site, "%v", err)

	stop := structNodes(deriveGraph(structured).Structure, StructStop)[0]
	assert.Equal(t, strings.Join(stop.CallSite, ">"), strings.Join(site, ">"))
}

func TestStructure_AComprehensionThatCallsIsALoop(t *testing.T) {
	g := deriveGraph(`
def main():
    ids = ["a", "b"]
    rows = [platform.query("SELECT 1", connection="w") for i in ids]
    platform.export("out", rows, format="csv")
`)
	require.True(t, g.OK, "%+v", g.Findings)
	s := g.Structure
	require.Len(t, s.Boxes, 1)
	assert.Equal(t, BoxLoop, s.Boxes[0].Kind)
	assert.Equal(t, "for i in ids", s.Boxes[0].Label)
	steps := structNodes(s, StructStep)
	require.Len(t, steps, 2)
	assert.Equal(t, s.Boxes[0].ID, steps[0].Box)
	assert.Empty(t, steps[1].Box)
}

func TestStructure_NoMainIsTheTopLevel(t *testing.T) {
	s := deriveGraph(threeSteps).Structure
	require.Len(t, structNodes(s, StructStart), 1)
	assert.Len(t, structNodes(s, StructStep), 3, "query, api and export; the table shares the export's call")
	assert.True(t, acyclic(s))
}

func TestStructure_AnUnparsedSourceHasEmptyLists(t *testing.T) {
	g := deriveGraph("def (")
	require.False(t, g.OK)
	assert.NotNil(t, g.Structure.Nodes)
	assert.NotNil(t, g.Structure.Edges)
	assert.NotNil(t, g.Structure.Boxes)
	assert.NotNil(t, g.Structure.Functions)
}

// A helper that calls the next one twice, many deep, is bounded like the value
// walk: the structure says it was cut short rather than walking every
// expansion.
func TestStructure_DeepFanOutIsBounded(t *testing.T) {
	const depth = 24
	lines := make([]string, 0, 4*depth+3)
	for i := range depth {
		lines = append(lines, fmt.Sprintf("def f%d():", i), fmt.Sprintf(`    """Level %d."""`, i))
		if i == depth-1 {
			lines = append(lines, `    platform.query("SELECT 1", connection="w")`)
			continue
		}
		lines = append(lines, fmt.Sprintf("    f%d()", i+1), fmt.Sprintf("    f%d()", i+1))
	}
	lines = append(lines, "def main():", `    """Starts the fan-out."""`, "    f0()")
	source := strings.Join(lines, "\n") + "\n"
	g := deriveGraph(source)
	require.True(t, g.OK, "%+v", g.Findings)
	assert.True(t, g.Structure.Truncated)
	assert.LessOrEqual(t, len(g.Structure.Nodes), maxStructNodes)
}
