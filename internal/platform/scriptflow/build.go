package scriptflow

import (
	"cmp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
)

// build turns the walk into the graph.
func (a *analyzer) build(g *Graph) {
	g.Truncated = a.truncated
	hasTable := map[string]bool{}
	saves := []string{}
	for _, s := range a.order {
		for _, n := range a.nodes(s) {
			g.Nodes = append(g.Nodes, n)
			if n.Kind == KindTable {
				hasTable[s.id()] = true
			}
			if n.Kind == KindSaveState {
				saves = append(saves, n.ID)
			}
		}
	}
	g.Groups = a.buildGroups()
	g.Edges = reduce(a.dataEdges(hasTable))
	if a.readState || len(saves) > 0 {
		state := Node{
			ID: StateNodeID, Role: RoleInput, Kind: KindState, Title: "run.state",
			Subtitle: "what the last run saved", Line: a.stateLine, EndLine: a.stateLine,
			Detail: []string{}, Loops: []string{},
		}
		g.Nodes = append([]Node{state}, g.Nodes...)
		for _, id := range saves {
			g.Edges = append(g.Edges, Edge{From: id, To: StateNodeID, Via: []string{}, Kind: EdgeState})
		}
	}
	g.Params = a.buildParams()
}

type edgeKey struct{ from, to string }

// dataEdges is every value passed from one step (or run.state) to another,
// with the functions it passed through. A value read from an export that
// registers a table is drawn from the table.
func (a *analyzer) dataEdges(hasTable map[string]bool) map[edgeKey]map[string]bool {
	edges := edgeSet{}
	for _, s := range a.order {
		for k, via := range s.inputs {
			if _, isParam := paramOrigin(k); isParam || k == s.id() {
				continue
			}
			if hasTable[k] {
				k += tableSuffix
			}
			edges.add(k, s.id(), via)
		}
		if hasTable[s.id()] {
			edges.add(s.id(), s.id()+tableSuffix, nil)
		}
	}
	return edges
}

// edgeSet is the edges found, each with the functions its value passed through.
type edgeSet map[edgeKey]map[string]bool

func (e edgeSet) add(from, to string, via map[string]bool) {
	k := edgeKey{from, to}
	if e[k] == nil {
		e[k] = map[string]bool{}
	}
	for v := range via {
		e[k][v] = true
	}
}

// reduce drops every edge a longer path already implies (transitive
// reduction), which is what makes a real script's diagram readable: without
// it every step is wired to every step before it that fed anything it used.
func reduce(edges map[edgeKey]map[string]bool) []Edge {
	succ := map[string][]string{}
	for k := range edges {
		succ[k.from] = append(succ[k.from], k.to)
	}
	keys := make([]edgeKey, 0, len(edges))
	for k := range edges {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].from != keys[j].from {
			return keys[i].from < keys[j].from
		}
		return keys[i].to < keys[j].to
	})
	out := make([]Edge, 0, len(keys))
	for _, k := range keys {
		if k.from == k.to || reachableWithout(succ, k) {
			continue
		}
		via := make([]string, 0, len(edges[k]))
		for v := range edges[k] {
			via = append(via, v+"()")
		}
		sort.Strings(via)
		out = append(out, Edge{From: k.from, To: k.to, Via: via, Kind: EdgeData})
	}
	return out
}

// reachableWithout reports whether skip.to is reachable from skip.from by a
// path that does not use the edge skip itself.
func reachableWithout(succ map[string][]string, skip edgeKey) bool {
	seen := map[string]bool{}
	queue := []string{}
	for _, n := range succ[skip.from] {
		if n != skip.to {
			queue = append(queue, n)
		}
	}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		if x == skip.to {
			return true
		}
		if seen[x] {
			continue
		}
		seen[x] = true
		queue = append(queue, succ[x]...)
	}
	return false
}

// buildGroups renders the function boxes, parents before children.
func (a *analyzer) buildGroups() []Group {
	out := make([]Group, 0, len(a.groups))
	for id, gi := range a.groups {
		d := a.funcs[gi.fn]
		calls := make([]int, 0, len(gi.calledFrom))
		for l := range gi.calledFrom {
			calls = append(calls, l)
		}
		sort.Ints(calls)
		out = append(out, Group{
			ID: id, Label: a.signature(d), Caption: caption(d),
			Parent: id[:strings.LastIndex(id, "/")], DefLine: int(d.Def.Line), CalledFrom: calls,
		})
	}
	slices.SortFunc(out, func(x, y Group) int { return cmp.Compare(x.ID, y.ID) })
	return out
}

// maxSignature is the longest box label.
const maxSignature = 52

// signature is a def's name and parameter list.
func (a *analyzer) signature(d *syntax.DefStmt) string {
	if len(d.Params) == 0 {
		return d.Name.Name + "()"
	}
	parts := make([]string, 0, len(d.Params))
	for _, p := range d.Params {
		parts = append(parts, a.srcOf(p))
	}
	return clip(d.Name.Name+"("+strings.Join(parts, ", ")+")", maxSignature)
}

// maxCaption is the longest box caption.
const maxCaption = 120

// caption is the first sentence of the comment the author wrote above a def,
// or at the top of its body, or else of its docstring. A script created since
// #1938 documents every function, so its every box carries a sentence a reader
// who does not read code can follow.
func caption(d *syntax.DefStmt) string {
	if cm := d.Comments(); cm != nil {
		if s := firstSentence(cm.Before); s != "" {
			return s
		}
	}
	if len(d.Body) == 0 {
		return ""
	}
	if cm := d.Body[0].Comments(); cm != nil {
		if s := firstSentence(cm.Before); s != "" {
			return s
		}
	}
	return docSentence(d.Body[0])
}

// docSentence is the first sentence of a docstring statement, or "" when the
// statement is not one.
func docSentence(s syntax.Stmt) string {
	es, ok := s.(*syntax.ExprStmt)
	if !ok {
		return ""
	}
	lit, ok := es.X.(*syntax.Literal)
	if !ok {
		return ""
	}
	text, ok := lit.Value.(string)
	if !ok {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	comments := make([]syntax.Comment, 0, len(lines))
	for _, l := range lines {
		comments = append(comments, syntax.Comment{Text: "# " + l})
	}
	return firstSentence(comments)
}

// firstSentence joins the comment lines of one paragraph up to its first full
// stop. A rule line (# ----) or a blank comment ends the paragraph.
func firstSentence(comments []syntax.Comment) string {
	var parts []string
	for _, c := range comments {
		t := strings.TrimSpace(strings.TrimPrefix(c.Text, "#"))
		if t == "" || strings.HasPrefix(t, "---") || strings.HasPrefix(t, "===") {
			if len(parts) > 0 {
				break
			}
			continue
		}
		parts = append(parts, t)
		if strings.HasSuffix(t, ".") {
			break
		}
	}
	s := strings.Join(parts, " ")
	if i := strings.Index(s, ". "); i > 0 {
		s = s[:i+1]
	}
	return clip(s, maxCaption)
}

// buildParams lists the parameters in the order the source first reads them,
// each with the steps its value reaches.
func (a *analyzer) buildParams() []Param {
	out := make([]Param, 0, len(a.params))
	for name, line := range a.params {
		p := Param{Name: name, Line: line, Reaches: []string{}, Decides: a.decides[name]}
		for _, s := range a.order {
			if _, ok := s.inputs[paramPrefix+name]; ok {
				p.Reaches = append(p.Reaches, s.id())
			}
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(x, y Param) int {
		if c := cmp.Compare(x.Line, y.Line); c != 0 {
			return c
		}
		return cmp.Compare(x.Name, y.Name)
	})
	return out
}

// Library is what a library defines for another script to load (#1970). A
// library makes no platform calls, so its diagram is empty by definition; what
// a reader of one needs is the functions it offers and how to load them.
type Library struct {
	// Functions is every function a load can name, in source order.
	Functions []Function `json:"functions"`
	// Load is the statement that loads every one of them from this version,
	// empty when there is none to load.
	Load string `json:"load,omitempty" example:"load(\"lib:date-windows@2\", \"last_week\")"`
}

// Function is one function a library defines at its top level.
type Function struct {
	Name string `json:"name" example:"last_week"`
	// Params is each parameter as the source writes it, a default included.
	Params []string `json:"params" example:"today,days=7"`
	// Doc is the first sentence of the function's docstring.
	Doc  string `json:"doc,omitempty" example:"The seven days before today."`
	Line int    `json:"line" example:"12"`
}

// LibraryOf is what one version of a library offers a load: the functions its
// source defines and the statement that loads them from that version. A source
// that does not parse offers none; its graph already says why.
func LibraryOf(ref scriptlib.Ref, source string) *Library {
	file, err := scriptrun.Parse(source)
	if err != nil {
		return &Library{Functions: []Function{}}
	}
	lib := newAnalyzer(source, file).library()
	if len(lib.Functions) > 0 {
		args := make([]string, 0, len(lib.Functions))
		args = append(args, strconv.Quote(ref.String()))
		for _, fn := range lib.Functions {
			args = append(args, strconv.Quote(fn.Name))
		}
		lib.Load = "load(" + strings.Join(args, ", ") + ")"
	}
	return lib
}

// library lists the functions file defines at its top level that a load can
// name. A name beginning with an underscore is private to the library: load
// refuses it, so it is not offered. A test_ function is the library's test,
// run by the harness on a save, not a function a script loads.
func (a *analyzer) library() *Library {
	lib := &Library{Functions: []Function{}}
	for _, s := range a.file.Stmts {
		d, ok := s.(*syntax.DefStmt)
		if !ok || strings.HasPrefix(d.Name.Name, "_") || scriptdialect.IsTest(d) {
			continue
		}
		fn := Function{Name: d.Name.Name, Params: make([]string, 0, len(d.Params)), Line: int(d.Def.Line)}
		for _, p := range d.Params {
			fn.Params = append(fn.Params, a.paramText(p))
		}
		if len(d.Body) > 0 {
			fn.Doc = clip(docSentence(d.Body[0]), maxCaption)
		}
		lib.Functions = append(lib.Functions, fn)
	}
	return lib
}

// paramText is one parameter as the source writes it: a name, name=default,
// *args or **kwargs. A default is read through srcOf, which restores the
// closing bracket the parser leaves out of an index or slice span.
func (a *analyzer) paramText(p syntax.Expr) string {
	if b, ok := p.(*syntax.BinaryExpr); ok && b.Op == syntax.EQ {
		return a.srcOf(b.X) + "=" + a.srcOf(b.Y)
	}
	return a.srcOf(p)
}
