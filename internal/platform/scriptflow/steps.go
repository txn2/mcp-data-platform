package scriptflow

import (
	"fmt"
	"regexp"
	"strings"

	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/scriptconst"
	"github.com/txn2/mcp-data-platform/internal/sqltables"
)

// step is one platform call as the walk found it.
type step struct {
	seq     int
	member  string
	call    *syntax.CallExpr
	group   string
	line    int
	endLine int
	site    int
	wrapper string
	loops   []string
	inputs  origins
	scope   *scope
}

func (s *step) id() string { return fmt.Sprintf("op:%d", s.seq) }

// newStep records a platform call with the scope it was written in.
func (a *analyzer) newStep(f *frame, c *syntax.CallExpr, member string) *step {
	start, end := c.Span()
	return &step{
		seq: len(a.order) + 1, member: member, call: c, group: f.group,
		line: int(start.Line), endLine: int(end.Line), site: f.site, wrapper: f.wrapper,
		loops: append([]string{}, f.loops...), inputs: origins{}, scope: a.scopeOf(f),
	}
}

// card renders one step's text.
type card struct {
	a  *analyzer
	s  *step
	r  *scriptconst.Renderer
	kw map[string]syntax.Expr
	// pos is the positional arguments.
	pos []syntax.Expr
}

// value renders an argument, reporting whether the source fully determines it.
func (c *card) value(e syntax.Expr) (string, bool) {
	if e == nil {
		return "", true
	}
	return c.r.Render(e)
}

// arg is the argument passed by keyword, or at position i.
func (c *card) arg(keyword string, i int) syntax.Expr {
	if e, ok := c.kw[keyword]; ok {
		return e
	}
	if i >= 0 && i < len(c.pos) {
		return c.pos[i]
	}
	return nil
}

// nodes renders a step as its card, plus the registered table an export
// creates.
func (a *analyzer) nodes(s *step) []Node {
	c := &card{a: a, s: s, r: a.renderer(s.scope), kw: map[string]syntax.Expr{}}
	for _, arg := range s.call.Args {
		if b, ok := arg.(*syntax.BinaryExpr); ok && b.Op == syntax.EQ {
			if id, ok := b.X.(*syntax.Ident); ok {
				c.kw[id.Name] = b.Y
			}
			continue
		}
		c.pos = append(c.pos, arg)
	}
	n := Node{
		ID: s.id(), Group: s.group, Line: s.line, EndLine: s.endLine, Site: s.site,
		Wrapper: s.wrapper, Loops: s.loops, Detail: []string{},
	}
	if s.member == "export" {
		return c.export(n)
	}
	if render, ok := memberCards[s.member]; ok {
		render(c, &n)
	} else {
		n.Role, n.Kind, n.Title = RoleWrites, KindTool, "platform."+s.member
	}
	return []Node{n}
}

// memberCards renders the card of each platform member but export, which also
// draws the table it registers.
var memberCards = map[string]func(*card, *Node){
	"query":        (*card).query,
	"publish_data": (*card).publishData,
	"notify":       (*card).notify,
	"publish":      (*card).publish,
	"call":         (*card).toolCall,
	"save_state": func(_ *card, n *Node) {
		n.Role, n.Kind, n.Title, n.Subtitle = RoleOutput, KindSaveState, "Save state", "run.state of the next run"
	},
	"result": func(_ *card, n *Node) {
		n.Role, n.Kind, n.Title, n.Subtitle = RoleOutput, KindResult, "Run result", "handed back to whoever ran it"
	},
}

// maxStandIn is the longest source text a {…} stand-in on a card carries.
const maxStandIn = 26

// defaultConnection is how a query that names no connection is shown: it runs
// on the deployment's default query connection.
const defaultConnection = "default connection"

func (c *card) query(n *Node) {
	n.Role, n.Kind = RoleReads, KindQuery
	conn, full := c.value(c.kw["connection"])
	if c.kw["connection"] == nil {
		conn = defaultConnection
	}
	n.Title = "Query " + conn
	sql, sqlFull := c.value(c.arg("sql", 0))
	tables := tablesIn(sql)
	n.Detail = shortList(tables, maxDetail)
	n.Computed = !full || hasHole(tables) || (!sqlFull && len(tables) == 0)
	if !sqlFull && len(tables) == 0 {
		n.Detail = []string{"SQL assembled at run time"}
	}
}

func (c *card) export(n Node) []Node {
	n.Role, n.Kind = RoleOutput, KindExport
	name, _ := c.value(c.arg("name", 0))
	format, _ := c.value(c.arg("format", 2))
	if format == "" {
		format = "csv"
	}
	dest, full := c.value(c.kw["destination"])
	if c.kw["destination"] == nil {
		dest = "portal"
	}
	n.Title = "Export " + strings.ToUpper(format) + " to " + dest
	n.Subtitle = name
	n.Computed = !full
	if k, ok := c.kw["key"]; ok {
		kt, _ := c.value(k)
		n.Detail = append(n.Detail, kt)
	}
	if ap, ok := c.kw["append"]; ok && isTrue(ap) {
		n.Detail = append(n.Detail, "appended across batches")
	}
	reg, ok := c.kw["register"]
	if !ok {
		return []Node{n}
	}
	return []Node{n, c.table(n, reg)}
}

// table is the table an export's register= puts over the file.
func (c *card) table(export Node, reg syntax.Expr) Node {
	t := Node{
		ID: export.ID + tableSuffix, Role: RoleOutput, Kind: KindTable, Group: export.Group,
		Line: export.Line, EndLine: export.EndLine, Site: export.Site, Wrapper: export.Wrapper,
		Loops: export.Loops, Detail: []string{},
	}
	d := c.s.scope.dict(reg)
	if d == nil {
		t.Title, t.Computed = "Table on {"+clip(c.a.srcOf(reg), maxStandIn)+"}", true
		return t
	}
	f := dictFields(d)
	conn, connFull := c.value(f["connection"])
	t.Title = "Table on " + conn
	name, nameFull := c.value(f["table_name"])
	if f["table_name"] == nil {
		name, nameFull = "named after the file", true
	}
	t.Subtitle = name
	t.Computed = !connFull || !nameFull
	if fv, ok := f["follow"]; !ok || isTrue(fv) {
		t.Detail = append(t.Detail, "follows each new version of the file")
	}
	return t
}

// tableSuffix is appended to an export's id to name the table it registers.
const tableSuffix = ":table"

func (c *card) publishData(n *Node) {
	n.Role, n.Kind = RoleOutput, KindPublishData
	name, full := c.value(c.arg("name", 0))
	n.Title, n.Subtitle, n.Computed = "Refresh data on portal", name, !full
}

func (c *card) notify(n *Node) {
	n.Role, n.Kind = RoleOutput, KindNotify
	ch, full := c.value(c.arg("channel", 0))
	title, _ := c.value(c.arg("title", 1))
	n.Title, n.Subtitle, n.Computed = "Notify "+ch, title, !full
}

func (c *card) publish(n *Node) {
	n.Role, n.Kind = RoleOutput, KindPublish
	ch, full := c.value(c.arg("channel", 0))
	name, _ := c.value(c.arg("name", 1))
	n.Title, n.Subtitle, n.Computed = "Post to "+ch, name, !full
}

// maxDetail is how many tables or paths a card lists before "+ n more".
const maxDetail = 4

var (
	holeRe = regexp.MustCompile(`\{[^{}]*\}`)
	holeID = regexp.MustCompile(`zzhole(\d+)zz`)
)

// tablesIn lists the tables a rendered SQL statement reads. A computed part of
// a name is kept as {its source}, so "warehouse.public.{t}" is reported as
// that rather than dropped or guessed.
func tablesIn(sql string) []string {
	var holes []string
	masked := holeRe.ReplaceAllStringFunc(sql, func(m string) string {
		holes = append(holes, m)
		return fmt.Sprintf("zzhole%dzz", len(holes)-1)
	})
	seen := map[string]bool{}
	out := []string{}
	for _, ref := range sqltables.Extract(masked) {
		p := holeID.ReplaceAllStringFunc(ref.FullPath, func(m string) string {
			var i int
			_, _ = fmt.Sscanf(m, "zzhole%dzz", &i)
			if i < len(holes) {
				return holes[i]
			}
			return m
		})
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// hasHole reports whether any name carries a computed part.
func hasHole(names []string) bool {
	for _, n := range names {
		if strings.Contains(n, "{") {
			return true
		}
	}
	return false
}

func shortList(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return append(append([]string{}, xs[:n]...), fmt.Sprintf("+ %d more", len(xs)-n))
}

// dictFields maps a dict literal's string keys to their values.
func dictFields(d *syntax.DictExpr) map[string]syntax.Expr {
	m := map[string]syntax.Expr{}
	if d == nil {
		return m
	}
	for _, it := range d.List {
		de, ok := it.(*syntax.DictEntry)
		if !ok {
			continue
		}
		if lit, ok := de.Key.(*syntax.Literal); ok {
			if s, ok := lit.Value.(string); ok {
				m[s] = de.Value
			}
		}
	}
	return m
}

// isTrue reports whether an expression is the literal True.
func isTrue(e syntax.Expr) bool {
	id, ok := e.(*syntax.Ident)
	return ok && id.Name == "True"
}
