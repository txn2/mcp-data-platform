package middleware

import (
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
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
//     (mcp_enrichment.go); the platform cannot enumerate those keys ahead of
//     time. (It fires only for trino_/datahub_/s3_ tools today, none of which
//     declare a schema, but keeping declared schemas open makes them safe if
//     that ever changes.)
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
