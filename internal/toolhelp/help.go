package toolhelp

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/toolwrite"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
)

// helpCommand is the value of a multi-command tool's command or action that
// asks the tool what it takes (#2815). It is the draft write barrier's help
// verb, which reads on every tool this answers (#2827).
const helpCommand = toolwrite.HelpVerb

// helpDescriptionLimit bounds each parameter's description in a help answer:
// the first sentence, cut here, which is enough to choose by.
const helpDescriptionLimit = 200

// commandFields are the argument names a multi-command tool chooses its
// operation by, in the order they are looked for.
var commandFields = []string{"command", "action"}

// HelpMiddleware answers `help` on every tool that takes a command or an
// action (#2815), from the tool's advertised input schema, so the answer is
// what the tool accepts and cannot drift from it. A tool whose command enum
// lists help itself (manage_script) answers its own and is passed through, as
// is a call to any tool whose schema the lookup cannot read or that chooses
// no command by an enum.
//
// The answer lists each command with the parameters whose description names
// it (the schemas write "publish: ..." or "grant, revoke: ..."), the
// parameters every command shares, and an example call per command.
//
// Placement: inner to authorization and the gates, so a persona that may not
// call the tool is refused as for any other command, and inner to the error
// contract.
func HelpMiddleware(lookup SchemaLookup) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodToolsCall || lookup == nil {
				return next(ctx, method, req)
			}
			tool, raw := callNameAndArgs(req)
			field, ok := asksForHelp(raw)
			if !ok {
				return next(ctx, method, req)
			}
			help, ok := buildHelp(tool, field, lookup(ctx, tool))
			if !ok {
				return next(ctx, method, req)
			}
			return helpResult(help)
		}
	}
}

// asksForHelp reports which command field of a call's arguments says help.
func asksForHelp(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var args map[string]any
	if json.Unmarshal(raw, &args) != nil {
		return "", false
	}
	for _, f := range commandFields {
		if v, _ := args[f].(string); strings.EqualFold(strings.TrimSpace(v), helpCommand) {
			return f, true
		}
	}
	return "", false
}

// toolHelp is the answer to help.
type toolHelp struct {
	Tool     string          `json:"tool"`
	Field    string          `json:"field"`
	Commands []commandHelp   `json:"commands"`
	Common   []parameterHelp `json:"common_parameters"`
	Note     string          `json:"note"`
}

// commandHelp is one command with what it takes.
type commandHelp struct {
	Command    string          `json:"command"`
	Parameters []parameterHelp `json:"parameters"`
	Example    map[string]any  `json:"example"`
}

// parameterHelp is one parameter.
type parameterHelp struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

// parametersByCommand sorts a tool's parameters (other than the command field)
// into those whose description names commands and those common to all
// (#2826). A named parameter is listed under each command it names, required
// there only when the schema or its description says that command needs it; a
// common parameter is required only when the schema says so.
func parametersByCommand(props map[string]any, required map[string]bool, field string, commands []string) (perCommand map[string][]parameterHelp, common []parameterHelp) {
	perCommand = map[string][]parameterHelp{}
	for _, name := range sortedKeys(props) {
		if name == field {
			continue
		}
		prop, _ := props[name].(map[string]any)
		desc, _ := prop["description"].(string)
		p := parameterHelp{Name: name, Type: typeOf(prop), Description: firstSentence(desc)}
		use := useOf(desc, commands)
		if len(use.named) == 0 {
			p.Required = required[name]
			common = append(common, p)
			continue
		}
		for _, c := range use.named {
			pc := p
			pc.Required = requiredFor(use, required[name], c)
			perCommand[c] = append(perCommand[c], pc)
		}
	}
	return perCommand, common
}

// buildHelp builds the help answer for a tool whose command field is an enum
// that does not already list help. ok is false otherwise.
func buildHelp(tool, field string, schema map[string]any) (toolHelp, bool) {
	props, _ := schema["properties"].(map[string]any)
	commands := enumStrings(asMap(props[field]))
	if len(commands) == 0 || slices.Contains(commands, helpCommand) {
		return toolHelp{}, false
	}
	perCommand, common := parametersByCommand(props, stringSet(schema["required"]), field, commands)
	h := toolHelp{
		Tool: tool, Field: field, Common: common,
		Note: "Parameters are listed under each " + field + " whose description names it; common parameters apply to " +
			"every " + field + ". Send " + field + " with one of these values and the parameters it lists.",
	}
	if h.Common == nil {
		h.Common = []parameterHelp{}
	}
	for _, c := range commands {
		params := perCommand[c]
		if params == nil {
			params = []parameterHelp{}
		}
		h.Commands = append(h.Commands, commandHelp{Command: c, Parameters: params, Example: commandExample(field, c, params, common)})
	}
	return h, true
}

// commandExample is one call of a command: the command and every required
// parameter, with a placeholder naming its type.
func commandExample(field, command string, params, common []parameterHelp) map[string]any {
	ex := map[string]any{field: command}
	for _, p := range append(slices.Clone(common), params...) {
		if p.Required {
			ex[p.Name] = "<" + p.Type + ">"
		}
	}
	return ex
}

// firstSentence is a description's first sentence, bounded.
func firstSentence(desc string) string {
	if i := strings.Index(desc, ". "); i >= 0 {
		desc = desc[:i+1]
	}
	if r := []rune(desc); len(r) > helpDescriptionLimit {
		desc = string(r[:helpDescriptionLimit]) + "..."
	}
	return desc
}

// helpResult is the help answer as a tool result, text and structured.
func helpResult(h toolHelp) (mcp.Result, error) {
	sort.SliceStable(h.Common, func(i, j int) bool { return h.Common[i].Required && !h.Common[j].Required })
	b, err := wirejson.Marshal(h)
	if err != nil {
		return nil, err //nolint:wrapcheck // a fixed struct of strings and maps
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(b)}},
		StructuredContent: json.RawMessage(b),
	}, nil
}
