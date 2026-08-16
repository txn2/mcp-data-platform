package middleware

import (
	"bytes"
	"encoding/json"
	"maps"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Shared mechanics for the platform-owned tool arguments the platform advertises
// on tools/list and consumes on tools/call: the session handle (#792) and the
// call's purpose (#1317). Both decorate the list response only, so upstream
// toolkits (mcp-trino, mcp-datahub, mcp-s3, gateway-proxied tools) are never
// modified, and both take their argument back off the request before the handler
// or a proxied upstream server can observe it.

// injectListedToolProperty adds a platform-owned property to the input schema of
// every listed tool the include predicate selects. It replaces the Tool pointer
// in the result slice with a shallow copy carrying the augmented schema, so the
// server's shared tool registry is never mutated. A result that is not a
// tools/list result is returned untouched.
func injectListedToolProperty(result mcp.Result, name string, prop map[string]any, include func(toolName string) bool) mcp.Result {
	listResult, ok := result.(*mcp.ListToolsResult)
	if !ok || listResult == nil {
		return result
	}
	for i, tool := range listResult.Tools {
		if tool == nil || !include(tool.Name) {
			continue
		}
		injected, ok := withSchemaProperty(tool.InputSchema, name, prop)
		if !ok {
			continue
		}
		cp := *tool
		cp.InputSchema = injected
		listResult.Tools[i] = &cp
	}
	return listResult
}

// withSchemaProperty returns a copy of a tool input schema with the named
// property added to its properties. It normalizes any schema representation
// (*jsonschema.Schema, json.RawMessage, or map) via a JSON round-trip, so one
// code path covers every registration style. The second return is false when the
// schema is not a JSON object (the tool is then left unchanged).
//
// A tool that already declares the property keeps its own declaration: the
// platform advertises an argument, it does not overwrite a tool's own.
func withSchemaProperty(schema any, name string, prop map[string]any) (any, bool) {
	if schema == nil {
		return nil, false
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, false
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}
	// Only object schemas carry named properties.
	if t, _ := obj["type"].(string); t != "" && t != "object" {
		return nil, false
	}
	existing, _ := obj["properties"].(map[string]any)
	// No capacity hint: len(existing) derives from a decoded schema, which the
	// allocation-size-overflow analysis treats as untrusted; the map grows fine.
	props := make(map[string]any)
	maps.Copy(props, existing)
	if _, exists := props[name]; !exists {
		props[name] = prop
	}
	obj["properties"] = props
	return obj, true
}

// takeStringArg removes a platform-owned string argument from a tools/call
// request's arguments and returns it. present is true only when the argument
// held a string value that keep accepted; that value is removed so upstream tool
// handlers and gateway-proxied servers never see the platform-injected argument.
//
// keep lets a caller consume only values it recognizes as its own — the session
// handle uses it so a tool that legitimately defines its own session_id
// parameter still receives it. A nil keep consumes any string value.
//
// The re-encode uses a json.Number decoder so that removing the argument does
// not silently rewrite the other arguments' numbers (a large int64 ID would
// otherwise round-trip through float64 and lose precision). When nothing is
// removed, the arguments are left byte-identical.
func takeStringArg(req mcp.Request, name string, keep func(string) bool) (value string, present bool) {
	callParams := toolCallParams(req)
	if callParams == nil || len(callParams.Arguments) == 0 {
		return "", false
	}
	dec := json.NewDecoder(bytes.NewReader(callParams.Arguments))
	dec.UseNumber()
	var args map[string]any
	if err := dec.Decode(&args); err != nil {
		return "", false
	}
	v, ok := args[name]
	if !ok {
		return "", false
	}
	s, _ := v.(string)
	if keep != nil && !keep(s) {
		return "", false
	}
	delete(args, name)
	if updated, err := json.Marshal(args); err == nil {
		callParams.Arguments = updated
	}
	return s, true
}
