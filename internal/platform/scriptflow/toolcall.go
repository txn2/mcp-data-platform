package scriptflow

import (
	"slices"
	"strings"

	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow/sqlread"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow/stmtlookup"
)

// Tools whose calls the diagram reads further than the generic card.
const (
	toolAPIInvoke    = "api_invoke_endpoint"
	toolAPIExport    = "api_export"
	toolTrinoExecute = "trino_execute"
	toolTrinoQuery   = "trino_query"
)

// writeActions are the action or command values that make a generic tool call
// a write.
var writeActions = map[string]bool{
	"create": true, "update": true, "delete": true, "register": true, "unregister": true,
	"replace_content": true, "put": true, "patch": true, "move": true, "share": true,
	"handler_set": true, "handler_enable": true, "handler_disable": true, "handler_delete": true, "upload": true, "copy": true, "remove": true,
}

// writeMethods are the HTTP methods that make an API call a write. POST is not
// among them: APIs search and query with it as often as they create.
var writeMethods = map[string]bool{"PUT": true, "PATCH": true, "DELETE": true}

// toolCall renders a platform.call: the tool, and what its argument dict names.
func (c *card) toolCall(n *Node) {
	tool, toolFull := c.value(c.arg("tool", 0))
	argExpr := c.arg("args", 1)
	d := c.s.scope.dict(argExpr)
	f := dictFields(d)
	conn, connFull := c.value(f["connection"])
	if p, ok := f["purpose"]; ok {
		n.Purpose, _ = c.value(p)
	}
	// Arguments the source computes hide the connection, so the card is
	// computed even when the tool is a literal.
	n.Computed = !toolFull || (argExpr != nil && d == nil) || !connFull
	if !toolFull {
		n.Role, n.Kind, n.Title = RoleReads, KindTool, "Call "+tool
		return
	}
	if render, ok := toolCards[tool]; ok {
		render(c, n, f, conn)
		return
	}
	c.genericTool(n, f, tool)
}

// toolCards renders the tools the diagram reads further than a generic card.
var toolCards = map[string]func(*card, *Node, map[string]syntax.Expr, string){
	toolAPIInvoke:    (*card).apiInvoke,
	toolAPIExport:    (*card).apiExport,
	toolTrinoExecute: (*card).trinoExecute,
	toolTrinoQuery:   (*card).trinoQuery,
}

// onConn names the connection a card reaches, or nothing when the call names
// none.
func onConn(title, conn string) string {
	if conn == "" {
		return title
	}
	return title + " " + conn
}

func (c *card) apiInvoke(n *Node, f map[string]syntax.Expr, conn string) {
	n.Kind, n.Title = KindAPI, onConn("API", conn)
	method, _ := c.value(f["method"])
	path, _ := c.value(f["path"])
	opid, _ := c.value(f["operation_id"])
	n.API = c.apiCall(f, conn)
	switch {
	case path != "":
		n.Subtitle = strings.TrimSpace(method + " " + path)
	case opid != "":
		n.Subtitle = opid
	}
	n.Role = RoleReads
	if writeMethods[strings.ToUpper(method)] {
		n.Role = RoleWrites
	}
}

// apiCall is what an API step names, each part kept only when the source
// writes it whole: a value with a computed hole is not an address.
func (c *card) apiCall(f map[string]syntax.Expr, conn string) *APICall {
	literal := func(key string) string {
		v, full := c.value(f[key])
		if !full {
			return ""
		}
		return v
	}
	if _, full := c.value(f["connection"]); !full {
		conn = ""
	}
	return &APICall{
		Connection: conn, OperationID: literal("operation_id"),
		Method: strings.ToUpper(literal("method")), Path: literal("path"),
	}
}

func (c *card) apiExport(n *Node, f map[string]syntax.Expr, conn string) {
	n.Role, n.Kind, n.Title = RoleReads, KindAPI, onConn("API", conn)+" to file"
	method, _ := c.value(f["method"])
	path, _ := c.value(f["path"])
	n.API = c.apiCall(f, conn)
	n.Subtitle = strings.TrimSpace(method + " " + path)
	for _, k := range []string{"destination", "key", "name"} {
		if v, ok := f[k]; ok {
			vt, _ := c.value(v)
			n.Detail = append(n.Detail, vt)
		}
	}
}

func (c *card) trinoExecute(n *Node, f map[string]syntax.Expr, conn string) {
	c.writeStatement(n, f[argSQL], conn)
}

// writeStatement is the card of a statement that changes state: its verb and
// the table it writes, read from the SQL.
func (c *card) writeStatement(n *Node, sqlExpr syntax.Expr, conn string) {
	n.Role, n.Kind, n.Title = RoleWrites, KindWrite, onConn("Write", conn)
	if verb, tables, ok := c.lookupWrites(sqlExpr); ok {
		n.Subtitle = verb + " " + strings.Join(tables, ", ")
		return
	}
	sql, _ := c.value(sqlExpr)
	w, ok := sqlread.LastWrite(sql)
	if !ok {
		n.Subtitle = "SQL assembled at run time"
		n.Computed = true
		return
	}
	n.Subtitle = w.Verb + " " + w.Table
	if strings.Contains(w.Table, "{") {
		n.Computed = true
	}
}

func (c *card) trinoQuery(n *Node, f map[string]syntax.Expr, conn string) {
	n.Role, n.Kind = RoleReads, KindQuery
	if conn == "" {
		conn = defaultConnection
	}
	n.Title = "Query " + conn
	sql, sqlFull := c.value(f[argSQL])
	tables := sqlread.TablesIn(sql)
	n.Detail = shortList(tables, maxDetail)
	if sqlread.HasHole(tables) {
		n.Computed = true
	}
	if !sqlFull && len(tables) == 0 {
		n.Detail = []string{"SQL assembled at run time"}
		n.Computed = true
	}
}

// genericTool is any other tool: its name, its action, and the one argument
// that names what it acts on.
func (c *card) genericTool(n *Node, f map[string]syntax.Expr, tool string) {
	n.Kind, n.Title = KindTool, strings.ReplaceAll(tool, "_", " ")
	action := ""
	for _, k := range []string{"action", "command"} {
		if v, ok := f[k]; ok {
			action, _ = c.value(v)
		}
	}
	n.Subtitle = action
	for _, k := range []string{"asset_id", "reference", "name", "registration_id", "table_name", "uri"} {
		if v, ok := f[k]; ok {
			vt, _ := c.value(v)
			n.Detail = append(n.Detail, k+" "+clip(vt, 40))
			break
		}
	}
	n.Role = RoleReads
	if writeActions[action] || strings.HasPrefix(tool, "save_") {
		n.Role = RoleWrites
	}
}

// DataRef is what a statement step names of the data it reads and writes:
// its connection and the tables, each as the SQL writes it ("orders",
// "shop.orders"). It is not drawn; an application's diagram and the check
// that a script stays inside its application's schema read it (#2539,
// #2540).
type DataRef struct {
	// Connection is the connection the step names, empty when it names none
	// (the default connection) or computes it (Computed).
	Connection string
	Reads      []string
	Writes     []string
	// Computed is true when the connection, or a table, is only known when
	// the script runs: such a step's tables are never matched to a part.
	Computed bool
	// Write is true for a step that writes by what it is (platform.execute,
	// a transaction that is not read-only, platform.batch), whether or not
	// the tables it writes are known (#2615).
	Write bool
}

// OutputRef is what an output step names: an export's or a data refresh's
// output name, which is one asset per script (script.OutputIdentityKey), or
// the channel a notification or a post goes to. Not drawn; an application's
// diagram reads it (#2540).
type OutputRef struct {
	// Kind is the step's kind: KindExport, KindPublishData, KindNotify or
	// KindPublish.
	Kind string
	// Name is the output name or the channel, as the source writes it.
	Name string
	// Computed is true when the name is only known when the script runs.
	Computed bool
	// Portal is true for an output that lands in the portal as an asset.
	Portal bool
}

// OutputRefs is every output step of source, in source order.
func OutputRefs(source string) []OutputRef {
	out := []OutputRef{}
	for _, n := range Derive(source).Nodes {
		if n.Output != nil {
			out = append(out, *n.Output)
		}
	}
	return out
}

// DataRefs is every statement step of source with what it reads and writes,
// in source order.
func DataRefs(source string) []DataRef {
	return DataRefsWith(source, nil)
}

// DataRefsWith is DataRefs reading the names the script loads from the
// libraries libs holds (#2699).
func DataRefsWith(source string, libs Libraries) []DataRef {
	out := []DataRef{}
	for _, n := range DeriveWith(source, libs).Nodes {
		if n.Data != nil {
			out = append(out, *n.Data)
		}
	}
	return out
}

// dataRef is the reference of statements on one connection.
func (c *card) dataRef(connExpr syntax.Expr, statements ...syntax.Expr) *DataRef {
	ref := &DataRef{Reads: []string{}, Writes: []string{}}
	if connExpr != nil {
		conn, full := c.value(connExpr)
		if full {
			ref.Connection = conn
		} else {
			ref.Computed = true
		}
	}
	for _, e := range statements {
		reads, writes, computed := c.statementRefs(e)
		ref.Reads, ref.Writes = union(ref.Reads, reads), union(ref.Writes, writes)
		ref.Computed = ref.Computed || computed
	}
	return ref
}

// lookupWrites is the tables a statement picked by a lookup writes (#2623):
// when every statement the lookup can pick is one the source holds whole
// that writes, the verb they share ("writes" when they differ) and each
// table. ok is false for anything else, which is read as one statement.
func (c *card) lookupWrites(e syntax.Expr) (verb string, tables []string, ok bool) {
	if e == nil {
		return "", nil, false
	}
	exprs, ok := stmtlookup.Candidates(c.s.scope, e)
	if !ok || (len(exprs) == 1 && exprs[0] == e) {
		return "", nil, false
	}
	for _, x := range exprs {
		sql, full := c.value(x)
		w, wrote := sqlread.LastWrite(sql)
		if !full || !wrote {
			return "", nil, false
		}
		switch {
		case verb == "":
			verb = w.Verb
		case verb != w.Verb:
			verb = "writes"
		}
		tables = union(tables, []string{w.Table})
	}
	return verb, tables, true
}

// statementRefs is what one statement reads and writes. A statement given as
// a dict is read by its sql; one picked by a lookup into literals is every
// statement the lookup can pick (#2623), and computed when any of them is.
func (c *card) statementRefs(e syntax.Expr) (reads, writes []string, computed bool) {
	if d, ok := e.(*syntax.DictExpr); ok {
		e = dictFields(d)[argSQL]
	}
	if e == nil {
		return nil, nil, true
	}
	exprs, ok := stmtlookup.Candidates(c.s.scope, e)
	if !ok {
		exprs = []syntax.Expr{e}
	}
	for _, x := range exprs {
		r, w, comp := c.oneStatementRefs(x)
		reads, writes, computed = union(reads, r), union(writes, w), computed || comp
	}
	return reads, writes, computed
}

// oneStatementRefs is what one statement the source writes reads and writes.
func (c *card) oneStatementRefs(e syntax.Expr) (reads, writes []string, computed bool) {
	sql, full := c.value(e)
	for _, w := range sqlread.WritesOf(sql) {
		if !slices.Contains(writes, w.Table) {
			writes = append(writes, w.Table)
		}
	}
	for _, t := range sqlread.TablesIn(sql) {
		if !slices.Contains(writes, t) {
			reads = append(reads, t)
		}
	}
	computed = sqlread.HasHole(reads) || sqlread.HasHole(writes) || (!full && len(reads) == 0 && len(writes) == 0)
	return reads, writes, computed
}

// txStatementExprs are the statements a transaction's function runs through
// its tx parameter: each tx.query and tx.execute, and each statement of a
// tx.batch list (#2615): a list or a list comprehension (#2623), written in
// the call, bound to a name in the function, or bound in the scope the
// transaction is started from (sc), which is the function a nested one closes
// over. copies are the tables each tx.copy_in writes rows into (#2665).
func txStatementExprs(body []syntax.Stmt, tx string, sc *scope) (stmts, copies []syntax.Expr) {
	lists := stmtlookup.ListsBound(body)
	for _, stmt := range body {
		syntax.Walk(stmt, func(node syntax.Node) bool {
			call, member := txCall(node, tx)
			switch member {
			case KindQuery, memberExecute:
				stmts = append(stmts, call.Args[0])
			case memberBatch:
				batch, _ := stmtlookup.Statements(sc, callArg(call, "statements", 0), lists)
				stmts = append(stmts, batch...)
			case memberCopyIn:
				copies = append(copies, callArg(call, argTable, 0))
			}
			return true
		})
	}
	return stmts, copies
}

// memberCopyIn is the member that writes rows into a table through COPY
// (#2453): platform.copy_in(connection, table, ...) and tx.copy_in(table, ...).
const memberCopyIn = "copy_in"

// argTable is the argument copy_in names its table in.
const argTable = "table"

// addCopies adds to ref each table a copy_in writes rows into (#2665): a
// table the source holds whole is written, one it computes makes the step
// computed.
func (c *card) addCopies(ref *DataRef, tables ...syntax.Expr) {
	for _, e := range tables {
		name, full := c.value(e)
		if e == nil || !full || name == "" || sqlread.HasHole([]string{name}) {
			ref.Computed = true
			continue
		}
		ref.Writes = union(ref.Writes, []string{name})
		ref.Reads = slices.DeleteFunc(ref.Reads, func(r string) bool { return r == name })
	}
}

// txCall is node as a call of a member of the transaction parameter tx, with
// at least one argument, and the member it calls; "" when it is not one.
func txCall(node syntax.Node, tx string) (call *syntax.CallExpr, member string) {
	call, ok := node.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 {
		return nil, ""
	}
	dot, ok := call.Fn.(*syntax.DotExpr)
	if !ok {
		return nil, ""
	}
	if recv, ok := dot.X.(*syntax.Ident); !ok || recv.Name != tx {
		return nil, ""
	}
	return call, dot.Name.Name
}

// memberBatch is the member that runs a list of statements in one round trip.
const memberBatch = "batch"

// callArg is a call's argument by keyword, or by position among the
// positional arguments.
func callArg(call *syntax.CallExpr, name string, pos int) syntax.Expr {
	i := 0
	for _, a := range call.Args {
		if b, ok := a.(*syntax.BinaryExpr); ok && b.Op == syntax.EQ {
			if id, ok := b.X.(*syntax.Ident); ok && id.Name == name {
				return b.Y
			}
			continue
		}
		if i == pos {
			return a
		}
		i++
	}
	return nil
}

// union is a with each of b it lacks.
func union(a, b []string) []string {
	for _, x := range b {
		if !slices.Contains(a, x) {
			a = append(a, x)
		}
	}
	return a
}
