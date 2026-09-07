package gqlschema

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// introspect renders an introspection result for a fixture schema the
// way a server would, so the JSON path is exercised against a schema
// whose SDL the test already knows.
//
// Written by hand rather than captured from a vendor: the point is that
// the converter reads what the specification defines, not what one
// server happens to emit.
const flatIntrospection = `{
  "data": {
    "__schema": {
      "queryType": {"name": "Query"},
      "mutationType": {"name": "Mutation"},
      "subscriptionType": null,
      "types": [
        {"kind": "OBJECT", "name": "Query", "description": "The root.", "fields": [
          {"name": "dataset", "description": "Read one dataset.",
           "args": [{"name": "urn", "description": "", "type": {"kind": "NON_NULL", "name": null, "ofType": {"kind": "SCALAR", "name": "String"}}, "defaultValue": null}],
           "type": {"kind": "OBJECT", "name": "Dataset"}, "isDeprecated": false, "deprecationReason": null},
          {"name": "tags", "description": "",
           "args": [{"name": "limit", "description": "", "type": {"kind": "SCALAR", "name": "Int"}, "defaultValue": "10"}],
           "type": {"kind": "LIST", "name": null, "ofType": {"kind": "NON_NULL", "name": null, "ofType": {"kind": "SCALAR", "name": "String"}}},
           "isDeprecated": true, "deprecationReason": "use labels"}
        ]},
        {"kind": "OBJECT", "name": "Mutation", "fields": [
          {"name": "setDescription", "args": [
             {"name": "input", "type": {"kind": "NON_NULL", "name": null, "ofType": {"kind": "INPUT_OBJECT", "name": "SetInput"}}, "defaultValue": null}],
           "type": {"kind": "OBJECT", "name": "Dataset"}, "isDeprecated": false}
        ]},
        {"kind": "OBJECT", "name": "Dataset", "description": "A dataset.", "fields": [
          {"name": "urn", "type": {"kind": "NON_NULL", "name": null, "ofType": {"kind": "SCALAR", "name": "String"}}, "isDeprecated": false},
          {"name": "state", "type": {"kind": "ENUM", "name": "State"}, "isDeprecated": false}
        ]},
        {"kind": "ENUM", "name": "State", "enumValues": [
          {"name": "ACTIVE", "isDeprecated": false},
          {"name": "RETIRED", "isDeprecated": true, "deprecationReason": "gone"}
        ]},
        {"kind": "INPUT_OBJECT", "name": "SetInput", "inputFields": [
          {"name": "urn", "type": {"kind": "NON_NULL", "name": null, "ofType": {"kind": "SCALAR", "name": "String"}}, "defaultValue": null},
          {"name": "text", "type": {"kind": "SCALAR", "name": "String"}, "defaultValue": "\"\""}
        ]},
        {"kind": "SCALAR", "name": "String"},
        {"kind": "OBJECT", "name": "__Type", "fields": []}
      ],
      "directives": [
        {"name": "auth", "description": "Requires a role.", "locations": ["FIELD_DEFINITION"],
         "args": [{"name": "role", "type": {"kind": "SCALAR", "name": "String"}, "defaultValue": null}]},
        {"name": "skip", "locations": ["FIELD"], "args": []}
      ]
    }
  }
}`

func TestSDLFromIntrospectionRendersALoadableSchema(t *testing.T) {
	sdl, err := SDLFromIntrospection([]byte(flatIntrospection))
	if err != nil {
		t.Fatalf("converting: %v", err)
	}
	for _, want := range []string{
		"schema {",
		"query: Query",
		"mutation: Mutation",
		"dataset(urn: String!): Dataset",
		"tags(limit: Int = 10): [String!]",
		`@deprecated(reason: "use labels")`,
		"enum State",
		"RETIRED @deprecated",
		"input SetInput",
		`text: String = ""`,
		"directive @auth",
	} {
		if !strings.Contains(sdl, want) {
			t.Errorf("SDL is missing %q:\n%s", want, sdl)
		}
	}
	// The prelude defines these; re-emitting them would fail the load.
	if strings.Contains(sdl, "scalar String") {
		t.Error("a built-in scalar was re-emitted")
	}
	if strings.Contains(sdl, "__Type") {
		t.Error("an introspection type was emitted")
	}
	if strings.Contains(sdl, "directive @skip") {
		t.Error("a built-in directive was re-emitted")
	}
	s, err := Load(sdl)
	if err != nil {
		t.Fatalf("the rendered SDL does not load: %v\n%s", err, sdl)
	}
	if len(Operations(s, 0)) == 0 {
		t.Error("the rendered schema exposes no operations")
	}
}

func TestSDLFromIntrospectionAcceptsEveryNestingLevel(t *testing.T) {
	var full map[string]json.RawMessage
	if err := json.Unmarshal([]byte(flatIntrospection), &full); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	// The data object alone, as a browser tool saves it.
	if _, err := SDLFromIntrospection(full["data"]); err != nil {
		t.Errorf("the data object alone was refused: %v", err)
	}
	// The whole response, as a server sends it.
	if _, err := SDLFromIntrospection([]byte(flatIntrospection)); err != nil {
		t.Errorf("the full response was refused: %v", err)
	}
}

func TestSDLFromIntrospectionNamesTheDisabledCase(t *testing.T) {
	for _, payload := range []string{`{"data": {"__schema": null}}`, `{}`, `{"data":{"__schema":{"types":[]}}}`} {
		_, err := SDLFromIntrospection([]byte(payload))
		if !errors.Is(err, ErrNoIntrospection) {
			t.Errorf("%s gave %v; want ErrNoIntrospection", payload, err)
		}
	}
	if _, err := SDLFromIntrospection([]byte("not json")); err == nil {
		t.Error("a non-JSON payload was accepted")
	}
}

func TestSDLFromIntrospectionRefusesAnUnknownTypeKind(t *testing.T) {
	const payload = `{"__schema": {"queryType": {"name": "Query"}, "types": [
	  {"kind": "SOMETHING_NEW", "name": "Query", "fields": []}
	]}}`
	_, err := SDLFromIntrospection([]byte(payload))
	if err == nil || !strings.Contains(err.Error(), "SOMETHING_NEW") {
		t.Errorf("err = %v; want the offending kind named", err)
	}
}

func TestLoadAnyReadsBothFormsAnOperatorPastes(t *testing.T) {
	fromJSON, err := LoadAny([]byte(flatIntrospection))
	if err != nil {
		t.Fatalf("introspection JSON: %v", err)
	}
	fromSDL, err := LoadAny([]byte(fromJSON.SDL()))
	if err != nil {
		t.Fatalf("SDL: %v", err)
	}
	if fromSDL.Hash() != fromJSON.Hash() {
		t.Error("the two forms of one schema produced two hashes")
	}
	if _, err := LoadAny([]byte("   ")); err == nil {
		t.Error("an empty payload was accepted")
	}
	if _, err := LoadAny([]byte("type Query { ")); err == nil {
		t.Error("malformed SDL was accepted")
	}
}

func TestLoadIntrospectionSurfacesTheConversionFailure(t *testing.T) {
	if _, err := LoadIntrospection([]byte(`{}`)); !errors.Is(err, ErrNoIntrospection) {
		t.Errorf("err = %v; want ErrNoIntrospection", err)
	}
}

func TestConvertTypeSurvivesAMalformedWrapperChain(t *testing.T) {
	// A chain with no name at the bottom is not something a conforming
	// server sends, and the converter must not dereference through it.
	if got := convertType(&typeRef{Kind: kindNonNull, OfType: nil}); got.Name() != "String" {
		t.Errorf("a broken NON_NULL chain gave %v", got)
	}
	if got := convertType(nil); got.Name() != "String" {
		t.Errorf("a nil type reference gave %v", got)
	}
	if got := namedType(nil); got != "" {
		t.Errorf("namedType(nil) = %q", got)
	}
}

// TestSDLFromIntrospectionDoesNotRenderAnInterfaceAsAUnion pins the
// distinction possibleTypes hides: it carries a union's members and an
// interface's implementors, and the parser's Types field means the first
// alone. Filling it for an interface renders `interface Aspect =
// SchemaMetadata`, which is not a schema — and it is what a real metadata
// service's introspection result produced.
func TestSDLFromIntrospectionDoesNotRenderAnInterfaceAsAUnion(t *testing.T) {
	const payload = `{"__schema": {
	  "queryType": {"name": "Query"},
	  "types": [
	    {"kind": "OBJECT", "name": "Query", "fields": [
	      {"name": "aspect", "args": [], "type": {"kind": "INTERFACE", "name": "Aspect"}, "isDeprecated": false}
	    ]},
	    {"kind": "INTERFACE", "name": "Aspect",
	     "fields": [{"name": "version", "args": [], "type": {"kind": "SCALAR", "name": "Int"}, "isDeprecated": false}],
	     "possibleTypes": [{"kind": "OBJECT", "name": "SchemaMetadata"}]},
	    {"kind": "OBJECT", "name": "SchemaMetadata", "interfaces": [{"kind": "INTERFACE", "name": "Aspect"}],
	     "fields": [{"name": "version", "args": [], "type": {"kind": "SCALAR", "name": "Int"}, "isDeprecated": false}]},
	    {"kind": "UNION", "name": "Anything", "possibleTypes": [{"kind": "OBJECT", "name": "SchemaMetadata"}]}
	  ],
	  "directives": []
	}}`
	sdl, err := SDLFromIntrospection([]byte(payload))
	if err != nil {
		t.Fatalf("converting: %v", err)
	}
	if strings.Contains(sdl, "interface Aspect =") {
		t.Errorf("an interface was rendered as a union:\n%s", sdl)
	}
	if !strings.Contains(sdl, "union Anything = SchemaMetadata") {
		t.Errorf("a union lost its members:\n%s", sdl)
	}
	if !strings.Contains(sdl, "implements Aspect") {
		t.Errorf("the implementing object lost its interface:\n%s", sdl)
	}
	if _, err := Load(sdl); err != nil {
		t.Fatalf("the rendered SDL does not load: %v\n%s", err, sdl)
	}
}
