// Package scriptflow derives a managed script's flow graph from its source
// (#1906): what the script reads, what it writes, what it produces, and which
// results feed which calls. The script page draws it on its Flow tab.
//
// Nobody authors the graph. It is read from the parse tree, so a new version
// of a script has a new graph with nobody updating it, and the graph cannot
// disagree with the code. That is possible because the dialect is small
// (internal/platform/scriptdialect): no imports, no classes, no exceptions and
// no while, so every loop is a for over a value. Recursion is refused by the
// interpreter when a recursive call is made, so the walk expands a user
// function at every place it is called except inside its own expansion, where a
// run would fail.
//
// The rules the graph is built by:
//
//   - Every platform.* call is a step, except platform.progress.
//   - Edges follow values. A value carries the set of steps (and run.state) it
//     was computed from, and a step's inputs are the origins of its arguments.
//     The user functions a value passed through on the way are recorded on the
//     edge.
//   - A user function is expanded at each call site, so a helper called for
//     three purposes is three steps, each rendered with its caller's arguments.
//   - A function holding steps is one box per enclosing box, captioned with
//     its author's comment. A function whose only effect is one platform call
//     is not a box: it is folded into the step and named on it.
//   - Names are rendered from the source: literals and module constants by
//     value, string building with each computed part as {its source}.
//   - An edge a longer path already implies is removed.
//   - Parameters are not drawn. Each is listed with the steps its value reaches.
//   - A connection, destination, tool, table or target the source computes
//     marks its step computed; the graph never names a guessed one.
package scriptflow

import (
	"crypto/sha256"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
)

// Roles a step plays, which the diagram colors it by.
const (
	RoleInput  = "input"
	RoleReads  = "reads"
	RoleWrites = "writes"
	RoleOutput = "output"
)

// Kinds of step, which name what the step is.
const (
	KindQuery       = "query"
	KindAPI         = "api"
	KindTool        = "tool"
	KindWrite       = "write"
	KindExport      = "export"
	KindTable       = "table"
	KindPublishData = "publish_data"
	KindSaveState   = "save_state"
	KindNotify      = "notify"
	KindPublish     = "publish"
	KindResult      = "result"
	KindState       = "state"
)

// Edge kinds: a value passed from one step to the next, or this run saving
// state for the next run.
const (
	EdgeData  = "data"
	EdgeState = "state"
)

// StateNodeID is the id of the node standing for run.state.
const StateNodeID = "state"

// Graph is one script version's flow.
type Graph struct {
	// OK is false when the source does not parse or resolve. Findings then
	// says why, and the diagram is empty.
	OK       bool                `json:"ok"`
	Findings []scriptrun.Finding `json:"findings"`
	Nodes    []Node              `json:"nodes"`
	Edges    []Edge              `json:"edges"`
	Groups   []Group             `json:"groups"`
	Params   []Param             `json:"params"`
	// Lines is the source's line count, for the reader's scale.
	Lines int `json:"lines" example:"136"`
	// Truncated is true when the script expands into more steps than one
	// diagram draws; the graph holds the first ones.
	Truncated bool `json:"truncated"`
	// ComparedWith is the older version this graph is compared against
	// (#1908), zero when it is not a comparison.
	ComparedWith int `json:"compared_with,omitempty"`
}

// Node is one step, or the run.state input.
type Node struct {
	ID   string `json:"id" example:"op:3"`
	Role string `json:"role" example:"reads"`
	Kind string `json:"kind" example:"query"`
	// Title is the action and where it goes; Subtitle the operation, name or
	// path; Purpose the purpose sentence the author wrote on the call.
	Title    string `json:"title" example:"Query warehouse"`
	Subtitle string `json:"subtitle,omitempty"`
	Purpose  string `json:"purpose,omitempty"`
	// Detail is the tables, paths or keys the step touches.
	Detail []string `json:"detail"`
	// Computed is true when what the step reaches is only known at run time;
	// the computed part is written as {its source} in the text above.
	Computed bool `json:"computed"`
	// Group is the id of the function box the step is drawn in, empty at the
	// top level.
	Group string `json:"group,omitempty"`
	// Line and EndLine are the platform call's lines. Site is the line the
	// folded wrapper named in Wrapper is called from.
	Line    int    `json:"line" example:"42"`
	EndLine int    `json:"end_line" example:"44"`
	Site    int    `json:"site,omitempty"`
	Wrapper string `json:"wrapper,omitempty"`
	// Loops are the for statements the step repeats in, outermost first.
	Loops []string `json:"loops"`
	// CallSite is the position of every call on the stack that makes this
	// step's call, outermost first, as "line:col": what a run records on the
	// step's audited calls and outputs (#1907), so a run is drawn on the card
	// that made each call.
	CallSite []string `json:"call_site,omitempty"`
	// Change and Was mark a node of a compared graph (#1908): added, changed
	// (with what it said before) or removed (carried over from the older
	// version).
	Change string `json:"change,omitempty"`
	Was    *Was   `json:"was,omitempty"`
}

// Edge is one value passed from a step to another, or the state saved for the
// next run.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Via names the functions that reshaped the value on the way.
	Via  []string `json:"via"`
	Kind string   `json:"kind" example:"data"`
	// Change is "removed" on an edge of a compared graph that only the older
	// version had.
	Change string `json:"change,omitempty"`
}

// Group is a function box.
type Group struct {
	ID      string `json:"id" example:"/load_orders"`
	Label   string `json:"label" example:"load_orders(day)"`
	Caption string `json:"caption,omitempty"`
	// Parent is the id of the enclosing box, empty at the top level.
	Parent     string `json:"parent,omitempty"`
	DefLine    int    `json:"def_line" example:"12"`
	CalledFrom []int  `json:"called_from"`
}

// Param is one run parameter the source reads, with the steps its value
// reaches.
type Param struct {
	Name string `json:"name" example:"day"`
	// Line is where the source first reads it.
	Line int `json:"line" example:"3"`
	// Reaches lists the steps whose arguments the parameter's value reaches.
	Reaches []string `json:"reaches"`
	// Decides is true when the value is read by a condition, so it decides
	// which steps run.
	Decides bool `json:"decides"`
}

// Derive returns the flow graph of one source. The graph depends on the source
// alone, so it is computed once per distinct source and served from memory
// after that.
func Derive(source string) Graph {
	key := sha256.Sum256([]byte(source))
	if g, ok := cache.get(key); ok {
		return g
	}
	g := deriveGraph(source)
	cache.put(key, g)
	return g
}

// deriveGraph computes the graph without the cache.
func deriveGraph(source string) Graph {
	g := emptyGraph(source)
	report := scriptrun.Validate(source)
	if errs := errorsOf(report.Findings); len(errs) > 0 {
		g.Findings = errs
		return g
	}
	file, err := scriptrun.Parse(source)
	if err != nil {
		// Validate accepted what Parse refused, which the two sharing one
		// dialect should make impossible; the error is still the answer.
		g.Findings = []scriptrun.Finding{{Severity: scriptrun.SeverityError, Message: err.Error()}}
		return g
	}
	a := newAnalyzer(source, file)
	a.run()
	a.build(&g)
	g.OK = true
	return g
}

// emptyGraph is a graph with every list present and empty, so an empty graph
// serializes as [] rather than null.
func emptyGraph(source string) Graph {
	return Graph{
		Findings: []scriptrun.Finding{}, Nodes: []Node{}, Edges: []Edge{},
		Groups: []Group{}, Params: []Param{},
		Lines: strings.Count(source, "\n") + 1,
	}
}

// errorsOf keeps the findings that stop a script from running.
func errorsOf(findings []scriptrun.Finding) []scriptrun.Finding {
	out := make([]scriptrun.Finding, 0, len(findings))
	for _, f := range findings {
		if f.Severity == scriptrun.SeverityError {
			out = append(out, f)
		}
	}
	return out
}

// Change marks on a compared graph's nodes (#1908).
const (
	ChangeAdded   = "added"
	ChangeChanged = "changed"
	ChangeRemoved = "removed"
)

// Was is what a changed node said in the older version.
type Was struct {
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Purpose  string   `json:"purpose,omitempty"`
	Detail   []string `json:"detail"`
}
