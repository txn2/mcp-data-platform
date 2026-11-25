package toolhelp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func siteSchema(withHelp bool) map[string]any {
	commands := []any{"publish", "grant", "revoke", "list"}
	if withHelp {
		commands = append(commands, "help")
	}
	return map[string]any{
		"type":     "object",
		"required": []any{"command"},
		"properties": map[string]any{
			"command": map[string]any{"type": "string", "enum": commands},
			"site":    map[string]any{"type": "string", "description": "The site, by name or id."},
			"files":   map[string]any{"type": "array", "description": "publish: the files. Required for publish."},
			"email":   map[string]any{"type": "string", "description": "grant, revoke: the person. More words here."},
			"query":   map[string]any{"type": "string", "description": "list and search: matched text."},
		},
	}
}

func callHelp(t *testing.T, schema map[string]any, args string) (mcp.Result, bool) {
	t.Helper()
	reached := false
	leaf := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		reached = true
		return &mcp.CallToolResult{}, nil
	}
	lookup := func(context.Context, string) map[string]any { return schema }
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "manage_site", Arguments: json.RawMessage(args)}}
	res, err := HelpMiddleware(lookup)(leaf)(context.Background(), methodToolsCall, req)
	if err != nil {
		t.Fatal(err)
	}
	return res, reached
}

func TestHelp_AnswersFromTheSchema(t *testing.T) {
	res, reached := callHelp(t, siteSchema(false), `{"command":"help"}`)
	if reached {
		t.Fatal("help reached the tool")
	}
	var h toolHelp
	raw, _ := res.(*mcp.CallToolResult).StructuredContent.(json.RawMessage)
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatal(err)
	}
	byCmd := map[string]commandHelp{}
	for _, c := range h.Commands {
		byCmd[c.Command] = c
	}
	if len(h.Commands) != 4 {
		t.Fatalf("commands = %d, want 4", len(h.Commands))
	}
	if p := byCmd["publish"].Parameters; len(p) != 1 || p[0].Name != "files" || p[0].Description != "publish: the files." {
		t.Errorf("publish parameters = %+v", p)
	}
	if byCmd["publish"].Example["files"] != "<array>" || byCmd["publish"].Example["command"] != "publish" {
		t.Errorf("publish example = %v", byCmd["publish"].Example)
	}
	for _, c := range []string{"grant", "revoke"} {
		if p := byCmd[c].Parameters; len(p) != 1 || p[0].Name != "email" {
			t.Errorf("%s parameters = %+v", c, p)
		}
	}
	// "list and search:" names a word that is not a command, so it is common.
	names := make([]string, 0, len(h.Common))
	for _, p := range h.Common {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "query,site" {
		t.Errorf("common = %v, want query,site", names)
	}
}

func TestHelp_PassesThrough(t *testing.T) {
	for name, tc := range map[string]struct {
		schema map[string]any
		args   string
	}{
		"the tool answers its own help": {siteSchema(true), `{"command":"help"}`},
		"another command":               {siteSchema(false), `{"command":"publish"}`},
		"no schema":                     {nil, `{"command":"help"}`},
		"no enum":                       {map[string]any{"properties": map[string]any{"command": map[string]any{"type": "string"}}}, `{"command":"help"}`},
		"no arguments":                  {siteSchema(false), ``},
		"arguments not an object":       {siteSchema(false), `[1]`},
	} {
		t.Run(name, func(t *testing.T) {
			if _, reached := callHelp(t, tc.schema, tc.args); !reached {
				t.Error("the call did not reach the tool")
			}
		})
	}
}

func TestHelp_ActionFieldAndOtherMethods(t *testing.T) {
	schema := map[string]any{"properties": map[string]any{"action": map[string]any{"type": "string", "enum": []any{"get", "put"}}}}
	if _, reached := callHelp(t, schema, `{"action":" HELP "}`); reached {
		t.Error("help on an action tool reached the tool")
	}
	reached := false
	leaf := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		reached = true
		return &mcp.CallToolResult{}, nil
	}
	_, _ = HelpMiddleware(nil)(leaf)(context.Background(), methodToolsCall, nil)
	_, _ = HelpMiddleware(func(context.Context, string) map[string]any { return schema })(leaf)(context.Background(), "tools/list", nil)
	if !reached {
		t.Error("a non-call or a nil lookup did not pass through")
	}
}

func TestFirstSentence(t *testing.T) {
	if got := firstSentence("One. Two."); got != "One." {
		t.Errorf("firstSentence = %q", got)
	}
	if got := firstSentence(strings.Repeat("a", 300)); len(got) != helpDescriptionLimit+3 {
		t.Errorf("firstSentence did not bound: %d", len(got))
	}
}
