package flowrun

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

const helperSrc = `
# Checks the window.
def check(hours):
    n = hours * 2
    if n > 10:
        fail("too far back")
    return n

def main():
    check(run.params.get("hours", 1))
    platform.query("SELECT 1", connection="warehouse")
`

func structNode(t *testing.T, g scriptflow.Graph, kind string) scriptflow.StructNode {
	t.Helper()
	for _, n := range g.Structure.Nodes {
		if n.Kind == kind {
			return n
		}
	}
	t.Fatalf("no %s node", kind)
	return scriptflow.StructNode{}
}

// A run that failed in fail() is drawn failing at that stop, which no card
// can show.
func TestDraw_AFailInAHelperIsTheStructuresFailedNode(t *testing.T) {
	g := deriveGraph(helperSrc)
	require.True(t, g.OK, "%+v", g.Findings)
	stop := structNode(t, g, scriptflow.StructStop)
	o := Draw(g, nil, RunFacts{
		Status: script.RunStatusFailed,
		Error:  "Traceback (most recent call last):\n  s:" + stop.CallSite[0] + ": in main\n  s:" + stop.CallSite[1] + ": in check\nError in fail: fail: too far back",
	})
	assert.Equal(t, stop.ID, o.StructureFailed)
	assert.Empty(t, o.FailedNode, "no card made the failing call")
}

// A line that fails without a call on it (an error in the helper's own code)
// fails the helper's box.
func TestDraw_AFailureOnAPlainLineIsItsHelpersBox(t *testing.T) {
	g := deriveGraph(helperSrc)
	require.Len(t, g.Structure.Boxes, 1)
	box := g.Structure.Boxes[0]
	o := Draw(g, nil, RunFacts{
		Status: script.RunStatusFailed,
		Error:  "Traceback (most recent call last):\n  s:" + box.CallSite[0] + ": in main\n  s:4:11: in check\nError: unknown binary op",
	})
	assert.Equal(t, box.ID, o.StructureFailed)
}

func TestDraw_AFailedCardIsItsStructureStep(t *testing.T) {
	g := deriveGraph(helperSrc)
	step := structNode(t, g, scriptflow.StructStep)
	o := Draw(g, []Call{{CallSite: step.CallSite, Tool: "trino_query", Success: false, Error: "boom"}}, RunFacts{
		Status: script.RunStatusFailed,
		Error:  "Traceback (most recent call last):\n  s:" + step.CallSite[0] + ": in main\nError in query: boom",
	})
	assert.Equal(t, step.Step, o.FailedNode)
	assert.Equal(t, step.ID, o.StructureFailed)
}

func TestDraw_ASucceededRunFailsNothing(t *testing.T) {
	g := deriveGraph(helperSrc)
	o := Draw(g, nil, RunFacts{Status: script.RunStatusSucceeded})
	assert.Empty(t, o.StructureFailed)
	assert.False(t, o.Unplaced, "a run that made no calls is not one whose calls could not be placed")
	assert.Equal(t, []TimedCall{}, o.Timeline)
}

// A run whose calls carry no call site says so, rather than reading as a run
// that reached nothing.
func TestDraw_CallsWithNoCallSiteAreUnplaced(t *testing.T) {
	g := deriveGraph(helperSrc)
	o := Draw(g, []Call{{Tool: "trino_query", Success: true}, {Tool: "trino_query", Success: true}},
		RunFacts{Status: script.RunStatusSucceeded})
	assert.True(t, o.Unplaced)
	assert.Len(t, o.Other, 2)

	step := structNode(t, g, scriptflow.StructStep)
	o = Draw(g, []Call{{Tool: "s3_list"}, {CallSite: step.CallSite, Tool: "trino_query"}},
		RunFacts{Status: script.RunStatusSucceeded})
	assert.False(t, o.Unplaced, "one placed call is enough to draw the run")
}

// The timeline places every call at its offset from the run's start, with the
// card it was attributed to.
func TestDraw_TheTimelinePlacesEveryCall(t *testing.T) {
	g := deriveGraph(helperSrc)
	step := structNode(t, g, scriptflow.StructStep)
	start := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	calls := []Call{
		{CallSite: step.CallSite, Tool: "trino_query", DurationMS: 40, Success: true, At: start.Add(250 * time.Millisecond), ResponseChars: 9},
		{Tool: "s3_list", DurationMS: 5, Success: false, Error: "denied", At: start.Add(400 * time.Millisecond)},
	}
	o := Draw(g, calls, RunFacts{Status: script.RunStatusSucceeded, StartedAt: start, FinishedAt: start.Add(time.Second)})
	require.Len(t, o.Timeline, 2)
	assert.Equal(t, TimedCall{
		StartMS: 250, DurationMS: 40, Tool: "trino_query", Success: true, ResponseChars: 9,
		CallSite: step.CallSite, Node: step.Step,
	}, o.Timeline[0])
	assert.Equal(t, int64(400), o.Timeline[1].StartMS)
	assert.Empty(t, o.Timeline[1].Node)
	assert.Equal(t, "denied", o.Timeline[1].Error)
	assert.Equal(t, int64(1000), o.RunMS)

	// With no start on the record, the first call is the start.
	o = Draw(g, calls, RunFacts{Status: script.RunStatusRunning})
	assert.Equal(t, int64(0), o.Timeline[0].StartMS)
	assert.Equal(t, int64(150), o.Timeline[1].StartMS)
	assert.Zero(t, o.RunMS)
}
