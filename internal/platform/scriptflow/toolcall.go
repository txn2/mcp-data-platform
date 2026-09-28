package scriptflow

import (
	"regexp"
	"strings"

	"go.starlark.net/syntax"
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
	"schedule_set": true, "upload": true, "copy": true, "remove": true,
}

// writeMethods are the HTTP methods that make an API call a write. POST is not
// among them: APIs search and query with it as often as they create.
var writeMethods = map[string]bool{"PUT": true, "PATCH": true, "DELETE": true}

// dmlRe reads the statement verb and target a trino_execute writes with.
var dmlRe = regexp.MustCompile(`(?i)^\s*(INSERT\s+INTO|DELETE\s+FROM|UPDATE|MERGE\s+INTO|CREATE\s+TABLE(?:\s+IF\s+NOT\s+EXISTS)?|CREATE\s+OR\s+REPLACE\s+VIEW|CREATE\s+VIEW|DROP\s+TABLE(?:\s+IF\s+EXISTS)?|TRUNCATE\s+TABLE|ALTER\s+TABLE|CALL)\s+([^\s(]+)`)

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

func (c *card) apiExport(n *Node, f map[string]syntax.Expr, conn string) {
	n.Role, n.Kind, n.Title = RoleReads, KindAPI, onConn("API", conn)+" to file"
	method, _ := c.value(f["method"])
	path, _ := c.value(f["path"])
	n.Subtitle = strings.TrimSpace(method + " " + path)
	for _, k := range []string{"destination", "key", "name"} {
		if v, ok := f[k]; ok {
			vt, _ := c.value(v)
			n.Detail = append(n.Detail, vt)
		}
	}
}

func (c *card) trinoExecute(n *Node, f map[string]syntax.Expr, conn string) {
	c.writeStatement(n, f["sql"], conn)
}

// writeStatement is the card of a statement that changes state: its verb and
// the table it writes, read from the SQL.
func (c *card) writeStatement(n *Node, sqlExpr syntax.Expr, conn string) {
	n.Role, n.Kind, n.Title = RoleWrites, KindWrite, onConn("Write", conn)
	sql, _ := c.value(sqlExpr)
	m := dmlRe.FindStringSubmatch(sql)
	if m == nil {
		n.Subtitle = "SQL assembled at run time"
		n.Computed = true
		return
	}
	verb := strings.ToUpper(strings.Fields(m[1])[0])
	n.Subtitle = verb + " " + m[2]
	if strings.Contains(m[2], "{") {
		n.Computed = true
	}
}

func (c *card) trinoQuery(n *Node, f map[string]syntax.Expr, conn string) {
	n.Role, n.Kind = RoleReads, KindQuery
	if conn == "" {
		conn = defaultConnection
	}
	n.Title = "Query " + conn
	sql, sqlFull := c.value(f["sql"])
	tables := tablesIn(sql)
	n.Detail = shortList(tables, maxDetail)
	if hasHole(tables) {
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
