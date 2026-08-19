package middleware

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrorEnvelopeProperty returns the JSON Schema for the machine-readable error
// object the platform packs under the "error" key of a failed tool result's
// structuredContent (see BuildErrorResult and MCPErrorContractMiddleware in
// error_contract.go / mcp_error_contract.go). It is the single shared fragment
// every schema-declaring tool references, so the {code, category, message, hint}
// shape is documented identically across the surface. The four documented fields
// are optional (a hint is not always present) and the object is left open, since
// the platform prefers admitting an unforeseen field over rejecting a real error.
func ErrorEnvelopeProperty() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Description: "Machine-readable error envelope; present only when the call failed. " +
			"Branch on error.code / error.category rather than parsing the message prose.",
		Properties: map[string]*jsonschema.Schema{
			"code":     stringProp("stable snake_case error code (e.g. not_found, unauthorized)"),
			"category": stringProp("error category the caller can branch on (e.g. client_input, internal)"),
			"message":  stringProp("human-readable failure message"),
			"hint":     stringProp("corrective guidance for the caller, when available"),
		},
		PropertyOrder: []string{"code", "category", "message", "hint"},
	}
}

// stringProp is a string-typed schema property with the given description.
func stringProp(description string) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Description: description}
}

// OpenToolOutputSchema adapts a struct-derived output schema to the platform's
// tool-result contract and returns it for inline use in mcp.Tool.OutputSchema.
//
// A declared output schema advertises a tool's success body, but a real tool
// result is polymorphic and may carry more (or less) than that body:
//
//   - On failure, MCPErrorContractMiddleware replaces the body with the shared
//     {error:{code,category,message,hint}} envelope (see error_contract.go), so
//     no success-body field is guaranteed present on every result.
//   - The semantic-enrichment middleware may mirror open-ended
//     semantic/memory/knowledge keys into structuredContent
//     (mcp_enrichment.go), and the call reference appends call_reference to
//     every data call; the platform cannot enumerate those keys ahead of time.
//
// Third-party toolkits do not build their schemas through this function, so
// MCPOutputSchemaMiddleware applies the same opening to every schema a
// tools/list response advertises (#1381).
//
// To keep a spec-faithful client from rejecting a valid result, this opens the
// schema: additionalProperties is allowed (admits injected keys), no property is
// required (a body-less error envelope validates), and the shared error envelope
// is declared under the "error" key so a client can branch on result.error.
// Nested objects keep the strict schemas jsonschema.For derives for them, since
// only the top level of structuredContent receives middleware-injected keys.
func OpenToolOutputSchema(base *jsonschema.Schema) *jsonschema.Schema {
	if base == nil {
		base = &jsonschema.Schema{Type: "object"}
	}
	// An empty schema value ({}) means "true": any additional top-level property
	// is permitted. This is what admits the error envelope and any future
	// enrichment key.
	base.AdditionalProperties = &jsonschema.Schema{}
	base.Required = nil
	if base.Properties == nil {
		base.Properties = map[string]*jsonschema.Schema{}
	}
	if _, ok := base.Properties[errorEnvelopeKey]; !ok {
		base.Properties[errorEnvelopeKey] = ErrorEnvelopeProperty()
		base.PropertyOrder = append(base.PropertyOrder, errorEnvelopeKey)
	}
	return base
}

// MustOutputSchema derives an open tool output schema (see OpenToolOutputSchema)
// from the success-body type T. It panics if T cannot be reflected into a JSON
// Schema, which is a programming error surfaced at package initialization and
// covered by tests, mirroring regexp.MustCompile.
func MustOutputSchema[T any]() *jsonschema.Schema {
	base, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("middleware.MustOutputSchema: %v", err))
	}
	return OpenToolOutputSchema(base)
}

// MCPOutputSchemaMiddleware opens the top level of every output schema a
// tools/list response advertises, so the schema admits the keys the platform
// adds to a tool's structuredContent after the tool's own handler has returned
// (#1381).
//
// The platform reserves the top level of every tool's structured output: the
// error contract replaces a failed call's body with the {error} envelope, the
// call reference appends call_reference to a data call, and semantic enrichment
// mirrors its context blocks in. A toolkit that registers a typed handler with
// no explicit OutputSchema (mcp-trino did before v1.4.0) gets one inferred
// from its Go struct, and jsonschema-go closes every struct-derived object with
// additionalProperties: false and a required list. A client that validates
// structuredContent against the advertised schema then discards every such
// result, successes for the keys the platform added and failures for the
// envelope that replaced the body. The advertised contract is therefore the
// same one OpenToolOutputSchema gives the platform-owned tools: the top level is
// open, nothing is required, and the error envelope is documented under
// "error". Nested objects keep the strict schemas the toolkit declared, since
// only the top level receives platform keys.
//
// The SDK still validates a handler's own structured output against the schema
// it inferred, inside the handler wrapper and before any middleware runs, so a
// toolkit's own contract is enforced unchanged; only what the server promises
// the client changes. The decorator replaces the Tool pointer in the list with
// a copy, so the server's registry is never mutated. A tool that declares a
// non-object output schema, or none, is left as it is.
func MCPOutputSchemaMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil || method != methodToolsList {
				return result, err
			}
			return openListedOutputSchemas(result), nil
		}
	}
}

// openListedOutputSchemas rewrites each listed tool's advertised output schema
// through openOutputSchema. A result that is not a tools/list result is
// returned untouched.
func openListedOutputSchemas(result mcp.Result) mcp.Result {
	listResult, ok := result.(*mcp.ListToolsResult)
	if !ok || listResult == nil {
		return result
	}
	for i, tool := range listResult.Tools {
		if tool == nil || tool.OutputSchema == nil {
			continue
		}
		opened, ok := openOutputSchema(tool.OutputSchema)
		if !ok {
			continue
		}
		cp := *tool
		cp.OutputSchema = opened
		listResult.Tools[i] = &cp
	}
	return listResult
}

// openOutputSchema returns a copy of an object output schema with its top level
// opened to the platform's keys: additionalProperties allowed, no required
// property, and the shared error envelope declared under "error" when the tool
// did not declare that key itself. It normalizes any schema representation
// (*jsonschema.Schema, json.RawMessage, or map) through a JSON round-trip, so
// one code path covers every registration style. The second return is false
// when the schema is not a JSON object schema; such a tool is left unchanged.
func openOutputSchema(schema any) (any, bool) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, false
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, false
	}
	if t, _ := obj["type"].(string); t != "" && t != "object" {
		return nil, false
	}
	obj["additionalProperties"] = true
	delete(obj, "required")
	props, _ := obj["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
	}
	if _, declared := props[errorEnvelopeKey]; !declared {
		envelope, err := json.Marshal(ErrorEnvelopeProperty())
		if err != nil {
			return nil, false
		}
		var envelopeObj map[string]any
		if err := json.Unmarshal(envelope, &envelopeObj); err != nil {
			return nil, false
		}
		props[errorEnvelopeKey] = envelopeObj
	}
	obj["properties"] = props
	return obj, true
}
