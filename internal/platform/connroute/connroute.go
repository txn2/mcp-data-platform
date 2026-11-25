// Package connroute answers a call that names a connection its tool does not
// serve (#2812), in one copy for every toolkit.
//
// Agents send a connection to the wrong tool: an application database's name
// to trino_query, an API connection to graphql_query. Each kind refuses an
// unknown name in its own words ("unknown connection: \"apps\""), and none of
// them says the name exists under another kind. This layer reads a refused
// call's connection argument, finds the kind that does serve that name, and
// adds to the refusal which kind it is and the tools that take it.
package connroute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// methodToolsCall is the one method this layer reads.
const methodToolsCall = "tools/call"

// connectionArg is the argument a tool names its connection by.
const connectionArg = "connection"

// kindAppDB is the application database kind, which no agent SQL tool
// reaches: scripts do, through platform.query.
const kindAppDB = "appdb"

// kindNames is how a kind is named to an agent; a kind not listed is named by
// its identifier.
var kindNames = map[string]string{
	"trino":   "Trino",
	"s3":      "S3",
	"api":     "API",
	"graphql": "GraphQL",
	"mcp":     "MCP gateway",
	kindAppDB: "application database",
}

// Deps is what the layer reads, all read per call so a connection added at run
// time is known.
type Deps struct {
	// Connections lists every configured connection with its kind.
	Connections func() []knowledge.ConnectionInfo
	// ToolKind is the kind of the toolkit registering a tool, "" when no
	// toolkit does.
	ToolKind func(tool string) string
	// KindTools lists the tools the toolkits of a kind register.
	KindTools func(kind string) []string
}

// Middleware adds the routing sentence to a refused call whose connection
// argument names a connection of another kind than the tool's. A tool whose
// kind has no connections of its own (manage_table, which takes a Trino
// connection from the portal toolkit) is left alone, as is any call whose
// connection is served by the tool's kind: its refusal is about something
// else.
func Middleware(d Deps) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil || method != methodToolsCall || d.Connections == nil || d.ToolKind == nil {
				return result, err
			}
			return d.route(req, result), nil
		}
	}
}

// route is Middleware's rewrite of one result.
func (d Deps) route(req mcp.Request, result mcp.Result) mcp.Result {
	ctr, ok := result.(*mcp.CallToolResult)
	if !ok || ctr == nil || !ctr.IsError {
		return result
	}
	tool, conn := callConnection(req)
	if conn == "" {
		return result
	}
	toolKind := d.ToolKind(tool)
	if toolKind == "" {
		return result
	}
	owner, ok := d.ownerKind(conn, toolKind)
	if !ok {
		return result
	}
	sentence := d.sentence(conn, owner)
	if pe, isPE := errors.AsType[*middleware.PlatformError](ctr.GetError()); isPE {
		rebuilt := *pe
		rebuilt.Message = strings.TrimSpace(pe.Message) + " " + sentence
		return middleware.BuildErrorResult(&rebuilt)
	}
	ctr.Content = append(ctr.Content, &mcp.TextContent{Text: sentence})
	return ctr
}

// ownerKind is the kind that serves the connection name, when it is another
// kind than the tool's and the tool's kind has connections of its own.
func (d Deps) ownerKind(conn, toolKind string) (string, bool) {
	toolKindHasConnections := false
	owner := ""
	for _, c := range d.Connections() {
		if c.Kind == toolKind {
			toolKindHasConnections = true
			if c.Name == conn || c.Bound == conn {
				return "", false
			}
			continue
		}
		if owner == "" && (c.Name == conn || c.Bound == conn) {
			owner = c.Kind
		}
	}
	return owner, toolKindHasConnections && owner != ""
}

// KindName is how a connection kind is named to an agent: the refusal here and
// the baseline instructions' connection line (#2812) read one list.
func KindName(kind string) string {
	if name := kindNames[kind]; name != "" {
		return name
	}
	return kind
}

// ScriptRoute is the way a kind is reached outside its tools, "" when it has
// none: an application database is reached through platform.query in a
// managed script.
func ScriptRoute(kind string) string {
	if kind == kindAppDB {
		return "platform.query in a managed script"
	}
	return ""
}

// sentence names the connection's kind and the tools that take it.
func (d Deps) sentence(conn, kind string) string {
	name := KindName(kind)
	var tools []string
	if d.KindTools != nil {
		tools = slices.Clone(d.KindTools(kind))
		sort.Strings(tools)
	}
	var via []string
	if len(tools) > 0 {
		via = append(via, "use "+strings.Join(tools, ", "))
	}
	if r := ScriptRoute(kind); r != "" {
		via = append(via, r)
	}
	if len(via) == 0 {
		return fmt.Sprintf("%q is %s %s connection, which this tool does not serve.", conn, article(name), name)
	}
	return fmt.Sprintf("%q is %s %s connection, which this tool does not serve: %s.", conn, article(name), name, strings.Join(via, ", or "))
}

// article is "an" before a name that opens with a vowel, else "a".
func article(name string) string {
	if name != "" && strings.ContainsRune("aeiouAEIOU", rune(name[0])) {
		return "an"
	}
	return "a"
}

// callConnection reads a tools/call request's tool name and connection.
func callConnection(req mcp.Request) (tool, conn string) {
	if req == nil {
		return "", ""
	}
	params, _ := req.GetParams().(*mcp.CallToolParamsRaw)
	if params == nil {
		return "", ""
	}
	var args map[string]any
	if json.Unmarshal(params.Arguments, &args) != nil {
		return params.Name, ""
	}
	c, _ := args[connectionArg].(string)
	return params.Name, strings.TrimSpace(c)
}
