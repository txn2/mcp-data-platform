// Package sqlprotect checks a statement a transform's covered caller sends
// against the columns the transform protects (#2634): a protected column may
// be selected as it is, and used nowhere else. An expression, a function
// argument, a filter, a join condition, a grouping, an ordering (by name or by
// position), a window, DISTINCT, a set operation other than UNION ALL, a
// rename, a column list on an alias, and a write all let a caller learn or
// copy the value the transform hides, so each is refused naming the column and
// the handler. A statement the parser cannot read is refused when it names a
// protected column at all.
//
// Names are followed through aliases, derived tables and CTEs by name: a
// column named like a protected one, anywhere a protected table is in scope,
// is treated as the protected column, which refuses a statement that merely
// shares the name rather than ever admitting one that hides it. A view is
// followed through its definition, which ViewReader supplies.
//
// The grammar is PostgreSQL's (github.com/multigres/multigres), which reads the
// statements a Trino connection and an application database are sent alike;
// Trino's own extensions it cannot read (try_cast, table functions) are
// refused for a covered caller only.
package sqlprotect

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/multigres/multigres/go/common/parser"
	"github.com/multigres/multigres/go/common/parser/ast"

	"github.com/txn2/mcp-data-platform/internal/sqltables"
)

// maxViewDepth bounds how far views are followed into the views they read.
const maxViewDepth = 4

// ViewReader returns the SQL a view is defined by, "" for a relation that is
// not a view. Its name is as the statement wrote it.
type ViewReader func(ctx context.Context, catalog, schema, name string) string

// RefusalError is a statement refused for a protected column.
type RefusalError struct {
	Column  string
	Handler string
	Why     string
}

func (e *RefusalError) Error() string {
	return fmt.Sprintf("%s is protected by %s: %s; select it as it is, or leave it out", e.Column, e.Handler, e.Why)
}

// table is one protected table: its dotted name and its protected columns,
// each with the handler protecting it.
type table struct {
	parts   []string
	columns map[string]string
}

// Check refuses sql when it uses a column protected maps
// ("catalog.schema.table.column", lowercase, to its handler) other than by
// selecting it as it is. views reads a view's definition; nil follows none.
func Check(ctx context.Context, sql string, protected map[string]string, views ViewReader) error {
	tables := tablesOf(protected)
	if len(tables) == 0 {
		return nil
	}
	return checkSQL(ctx, sql, tables, views, 0)
}

func checkSQL(ctx context.Context, sql string, tables []table, views ViewReader, depth int) error {
	stmts, err := parser.ParseSQL(sql)
	if err != nil {
		return unreadable(sql, tables)
	}
	inScope, names := scope(ctx, sql, tables, views, depth)
	if len(inScope) == 0 {
		return nil
	}
	for _, st := range stmts {
		if _, ok := st.(*ast.SelectStmt); !ok {
			col, h := first(names)
			return &RefusalError{Column: col, Handler: h, Why: "a covered caller only reads the table it is in; this statement writes or changes"}
		}
		if err := uses(st, names); err != nil {
			return err
		}
	}
	return nil
}

// sep separates the parts of a qualified name.
const sep = "."

// column is col's full name in t.
func (t table) column(col string) string {
	return strings.Join(t.parts, sep) + sep + col
}

// tablesOf groups protected columns by their table.
func tablesOf(protected map[string]string) []table {
	byTable := map[string]*table{}
	for col, handler := range protected {
		parts := strings.Split(strings.ToLower(col), sep)
		if len(parts) < 2 {
			continue
		}
		key := strings.Join(parts[:len(parts)-1], sep)
		t := byTable[key]
		if t == nil {
			t = &table{parts: parts[:len(parts)-1], columns: map[string]string{}}
			byTable[key] = t
		}
		t.columns[parts[len(parts)-1]] = handler
	}
	out := make([]table, 0, len(byTable))
	keys := make([]string, 0, len(byTable))
	for k := range byTable {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, *byTable[k])
	}
	return out
}

// unreadable refuses a statement the parser cannot read when its text names
// a protected column, and lets it through otherwise.
func unreadable(sql string, tables []table) error {
	lower := strings.ToLower(sql)
	for _, t := range tables {
		for col, h := range t.columns {
			if containsWord(lower, col) {
				return &RefusalError{
					Column: t.column(col), Handler: h,
					Why: "the statement could not be read, and it names the column",
				}
			}
		}
	}
	return nil
}

func containsWord(s, word string) bool {
	for i := 0; ; {
		j := strings.Index(s[i:], word)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(word)
		if (start == 0 || !identRune(s[start-1])) && (end == len(s) || !identRune(s[end])) {
			return true
		}
		i = end
	}
}

func identRune(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}

// scope is the protected tables a statement reads, directly or through a
// view, and the column names in scope that are treated as protected, each
// with its full name and handler. The tables a statement names are read
// lexically (internal/sqltables), the platform's one reading of them: the
// grammar loses the schema and table of a three-part name.
func scope(ctx context.Context, sql string, tables []table, views ViewReader, depth int) (hit []table, names map[string][2]string) {
	names = map[string][2]string{}
	for _, ref := range sqltables.Extract(sql) {
		hit = append(hit, matching(refParts(ref), tables, names)...)
		if views == nil || depth >= maxViewDepth {
			continue
		}
		if def := views(ctx, ref.Catalog, ref.Schema, ref.Table); def != "" {
			inner, innerNames := scope(ctx, def, tables, views, depth+1)
			hit = append(hit, inner...)
			maps.Copy(names, innerNames)
		}
	}
	return hit, names
}

// matching is the protected tables a relation's parts name, adding each one's
// columns to names.
func matching(parts []string, tables []table, names map[string][2]string) []table {
	var hit []table
	for _, t := range tables {
		if !sameRelation(parts, t.parts) {
			continue
		}
		hit = append(hit, t)
		for col, h := range t.columns {
			names[col] = [2]string{t.column(col), h}
		}
	}
	return hit
}

func refParts(ref sqltables.Ref) []string {
	var parts []string
	for _, p := range []string{ref.Catalog, ref.Schema, ref.Table} {
		if p != "" {
			parts = append(parts, strings.ToLower(strings.Trim(p, `"`)))
		}
	}
	return parts
}

// ViewUse refuses a view's definition that uses a protected column other
// than by selecting it as it is: such a view would hand a covered caller what
// it computed from the value.
func ViewUse(ctx context.Context, def string, protected map[string]string) error {
	return Check(ctx, def, protected, nil)
}

// sameRelation reports whether a relation as a statement names it is a
// protected table: its parts are the table's last parts, so a name the
// connection's default catalog and schema complete matches.
func sameRelation(ref, protected []string) bool {
	if len(ref) == 0 || len(ref) > len(protected) {
		return false
	}
	return slices.Equal(ref, protected[len(protected)-len(ref):])
}

func first(names map[string][2]string) (column, handler string) {
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "", ""
	}
	v := names[keys[0]]
	return v[0], v[1]
}

// uses refuses a protected name used other than as a plain selection.
func uses(st ast.Node, names map[string][2]string) error {
	u := &user{names: names, plain: plainTargets(st, names)}
	ast.Rewrite(st, func(c *ast.Cursor) bool {
		if u.refusal != nil {
			return false
		}
		u.visit(c.Node())
		return true
	}, nil)
	return u.refusal
}

// user walks one statement for a protected name used other than as a plain
// selection, keeping the first refusal.
type user struct {
	names   map[string][2]string
	plain   map[*ast.ColumnRef]bool
	refusal error
}

func (u *user) refuse(name, why string) {
	if u.refusal == nil {
		v := u.names[name]
		u.refusal = &RefusalError{Column: v[0], Handler: v[1], Why: why}
	}
}

func (u *user) visit(node ast.Node) {
	switch n := node.(type) {
	case *ast.ColumnRef:
		u.column(n)
	case *ast.JoinExpr:
		checkJoin(n, u.names, u.refuse)
	case *ast.SelectStmt:
		checkSelect(n, u.names, u.refuse)
	case *ast.Alias:
		u.renames(n.ColNames, "a column list on an alias can rename it")
	case *ast.CommonTableExpr:
		u.renames(n.Aliascolnames, "a column list on a CTE can rename it")
	}
}

// column refuses a protected column referenced other than as a plain
// selection.
func (u *user) column(n *ast.ColumnRef) {
	name := lastName(n)
	if name == "" {
		return
	}
	if _, ok := u.names[name]; ok && !u.plain[n] {
		u.refuse(name, "it is used in an expression, a filter, a join, a grouping, an ordering or a rename")
	}
}

// renames refuses a column list that can rename a protected column.
func (u *user) renames(cols *ast.NodeList, why string) {
	if cols != nil && len(cols.Items) > 0 {
		name, _ := firstKey(u.names)
		u.refuse(name, why)
	}
}

func firstKey(names map[string][2]string) (string, bool) {
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "", false
	}
	return keys[0], true
}

// plainTargets are the column references that select a protected column as
// it is: the whole value of a select list entry, under its own name, in a
// select without DISTINCT.
func plainTargets(st ast.Node, names map[string][2]string) map[*ast.ColumnRef]bool {
	out := map[*ast.ColumnRef]bool{}
	ast.Rewrite(st, func(c *ast.Cursor) bool {
		s, ok := c.Node().(*ast.SelectStmt)
		if ok && s.TargetList != nil && s.DistinctClause == nil {
			plainIn(s.TargetList.Items, names, out)
		}
		return true
	}, nil)
	return out
}

// plainIn marks the select list entries that are a protected column as it
// is, under its own name.
func plainIn(items []ast.Node, names map[string][2]string, out map[*ast.ColumnRef]bool) {
	for _, item := range items {
		rt, ok := item.(*ast.ResTarget)
		if !ok {
			continue
		}
		cr, ok := rt.Val.(*ast.ColumnRef)
		if !ok {
			continue
		}
		name := lastName(cr)
		if _, protected := names[name]; protected && (rt.Name == "" || strings.EqualFold(rt.Name, name)) {
			out[cr] = true
		}
	}
}

// checkJoin refuses a join that compares a protected column implicitly:
// USING it, or NATURAL with a protected table in scope.
func checkJoin(j *ast.JoinExpr, names map[string][2]string, refuse func(name, why string)) {
	if j.IsNatural {
		name, _ := firstKey(names)
		refuse(name, "a NATURAL join compares every column of the same name")
		return
	}
	if j.UsingClause == nil {
		return
	}
	for _, item := range j.UsingClause.Items {
		if s, ok := item.(*ast.String); ok {
			if _, protected := names[strings.ToLower(s.SVal)]; protected {
				refuse(strings.ToLower(s.SVal), "a join USING it compares it")
			}
		}
	}
}

// checkSelect refuses what a select does with its protected selections as a
// whole: DISTINCT, a set operation other than UNION ALL, and ordering or
// grouping by position.
func checkSelect(s *ast.SelectStmt, names map[string][2]string, refuse func(name, why string)) {
	if name, ok := comparedBySetOp(s, names); ok {
		refuse(name, "a set operation other than UNION ALL compares it")
		return
	}
	if s.DistinctClause != nil {
		if name, ok := selectsProtected(s, names); ok {
			refuse(name, "DISTINCT compares it")
			return
		}
	}
	if name, ok := byPosition(s, names); ok {
		refuse(name, "ordering or grouping by its position compares it")
	}
}

// comparedBySetOp is a protected column either side of a set operation other
// than UNION ALL selects.
func comparedBySetOp(s *ast.SelectStmt, names map[string][2]string) (string, bool) {
	if s.Op == ast.SETOP_NONE || (s.Op == ast.SETOP_UNION && s.All) {
		return "", false
	}
	return selectsProtectedEither(s, names)
}

// byPosition is a protected column a select orders or groups by its position
// in the select list.
func byPosition(s *ast.SelectStmt, names map[string][2]string) (string, bool) {
	for _, list := range []*ast.NodeList{s.SortClause, s.GroupClause} {
		if list == nil {
			continue
		}
		for _, item := range list.Items {
			n := item
			if sb, ok := item.(*ast.SortBy); ok {
				n = sb.Node
			}
			k, ok := position(n)
			if !ok {
				continue
			}
			if name, ok := targetAt(s, k, names); ok {
				return name, true
			}
		}
	}
	return "", false
}

// selectsProtected reports whether a select's list holds a protected column.
func selectsProtected(s *ast.SelectStmt, names map[string][2]string) (string, bool) {
	if s == nil {
		return "", false
	}
	if s.TargetList == nil {
		return selectsProtectedEither(s, names)
	}
	for _, item := range s.TargetList.Items {
		rt, ok := item.(*ast.ResTarget)
		if !ok {
			continue
		}
		if name, ok := refersProtected(rt.Val, names); ok {
			return name, true
		}
	}
	return "", false
}

func selectsProtectedEither(s *ast.SelectStmt, names map[string][2]string) (string, bool) {
	if name, ok := selectsProtected(s.Larg, names); ok {
		return name, true
	}
	return selectsProtected(s.Rarg, names)
}

// refersProtected reports whether an expression is, or holds, a protected
// column or a star that would select one.
func refersProtected(n ast.Node, names map[string][2]string) (string, bool) {
	found, hit := "", false
	ast.Rewrite(n, func(c *ast.Cursor) bool {
		if cr, ok := c.Node().(*ast.ColumnRef); ok {
			if name := lastName(cr); name != "" {
				if _, protected := names[name]; protected {
					found, hit = name, true
					return false
				}
			}
			if isStar(cr) {
				found, hit = firstKey(names)
				return false
			}
		}
		return true
	}, nil)
	return found, hit
}

// position is an integer constant, a reference to a select list entry.
func position(n ast.Node) (int, bool) {
	ac, ok := n.(*ast.A_Const)
	if !ok {
		return 0, false
	}
	i, ok := ac.Val.(*ast.Integer)
	if !ok {
		return 0, false
	}
	return i.IVal, true
}

// targetAt reports whether a select's k-th list entry is a protected column.
func targetAt(s *ast.SelectStmt, k int, names map[string][2]string) (string, bool) {
	if s.TargetList == nil || k < 1 || k > len(s.TargetList.Items) {
		return "", false
	}
	rt, ok := s.TargetList.Items[k-1].(*ast.ResTarget)
	if !ok {
		return "", false
	}
	return refersProtected(rt.Val, names)
}

// lastName is a column reference's column name, lowercase; "" for a star.
func lastName(cr *ast.ColumnRef) string {
	if cr.Fields == nil || len(cr.Fields.Items) == 0 {
		return ""
	}
	if s, ok := cr.Fields.Items[len(cr.Fields.Items)-1].(*ast.String); ok {
		return strings.ToLower(s.SVal)
	}
	return ""
}

func isStar(cr *ast.ColumnRef) bool {
	if cr.Fields == nil || len(cr.Fields.Items) == 0 {
		return false
	}
	_, ok := cr.Fields.Items[len(cr.Fields.Items)-1].(*ast.A_Star)
	return ok
}
