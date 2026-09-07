// Package gqlschema holds the GraphQL schema machinery the platform's
// graphql connection kind is built on: turning an introspection result
// into a schema the platform can keep, walking that schema into the
// operation units an agent discovers and calls, rendering a runnable
// skeleton for one of them, and deciding whether a document a model
// wrote may be executed.
//
// It knows nothing about connections, HTTP, personas or tools. It takes
// schema text and document text and answers questions about them, which
// is what makes the namespace descent testable against a hand-authored
// schema rather than against whatever upstream a deployment happens to
// point at.
package gqlschema

import (
	"encoding/json"
	"errors"
	"fmt"
)

// IntrospectionQuery is the document sent to an upstream to read its
// schema. It is the canonical introspection query at the compatibility
// level every GraphQL server implements: no `isRepeatable` on
// directives, no `specifiedByURL` on scalars, no `includeDeprecated`
// argument on `args`. Those fields postdate servers still in
// production, and a schema read is the one call that must succeed
// before a connection is usable at all.
//
// The cost of that floor is that directive repeatability is not
// recorded, so a document repeating a custom directive on one element
// is refused under strict validation. Such a document passes under
// schema_validation: warn.
const IntrospectionQuery = `query IntrospectionQuery {
  __schema {
    queryType { name }
    mutationType { name }
    subscriptionType { name }
    types { ...FullType }
    directives { name description locations args { ...InputValue } }
  }
}
fragment FullType on __Type {
  kind
  name
  description
  fields(includeDeprecated: true) {
    name
    description
    args { ...InputValue }
    type { ...TypeRef }
    isDeprecated
    deprecationReason
  }
  inputFields { ...InputValue }
  interfaces { ...TypeRef }
  enumValues(includeDeprecated: true) {
    name
    description
    isDeprecated
    deprecationReason
  }
  possibleTypes { ...TypeRef }
}
fragment InputValue on __InputValue {
  name
  description
  type { ...TypeRef }
  defaultValue
}
fragment TypeRef on __Type {
  kind
  name
  ofType {
    kind
    name
    ofType {
      kind
      name
      ofType {
        kind
        name
        ofType {
          kind
          name
          ofType {
            kind
            name
            ofType {
              kind
              name
            }
          }
        }
      }
    }
  }
}`

// ErrNoIntrospection reports an introspection response that carried no
// __schema. Every upstream that disables introspection answers this
// way, so the connection reports a named cause rather than an empty
// operation index.
var ErrNoIntrospection = errors.New("gqlschema: response carried no __schema (introspection may be disabled upstream)")

// introspection type-kind discriminators, as the __TypeKind enum spells
// them on the wire.
const (
	kindNonNull     = "NON_NULL"
	kindList        = "LIST"
	kindObject      = "OBJECT"
	kindInterface   = "INTERFACE"
	kindUnion       = "UNION"
	kindEnum        = "ENUM"
	kindInputObject = "INPUT_OBJECT"
	kindScalar      = "SCALAR"
)

// typeRef is a possibly-wrapped reference to a named type: the
// LIST/NON_NULL chain the introspection result nests around a name.
type typeRef struct {
	Kind   string   `json:"kind"`
	Name   string   `json:"name"`
	OfType *typeRef `json:"ofType"`
}

// inputValue is an argument or input-object field.
type inputValue struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Type         *typeRef `json:"type"`
	DefaultValue *string  `json:"defaultValue"`
}

// introspectedField is one field on an object or interface type.
type introspectedField struct {
	Name              string       `json:"name"`
	Description       string       `json:"description"`
	Args              []inputValue `json:"args"`
	Type              *typeRef     `json:"type"`
	IsDeprecated      bool         `json:"isDeprecated"`
	DeprecationReason *string      `json:"deprecationReason"`
}

// introspectedEnumValue is one member of an enum type.
type introspectedEnumValue struct {
	Name              string  `json:"name"`
	Description       string  `json:"description"`
	IsDeprecated      bool    `json:"isDeprecated"`
	DeprecationReason *string `json:"deprecationReason"`
}

// introspectedType is one entry of __schema.types.
type introspectedType struct {
	Kind        string                  `json:"kind"`
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Fields      []introspectedField     `json:"fields"`
	InputFields []inputValue            `json:"inputFields"`
	Interfaces  []typeRef               `json:"interfaces"`
	EnumValues  []introspectedEnumValue `json:"enumValues"`
	Types       []typeRef               `json:"possibleTypes"`
}

// introspectedDirective is one entry of __schema.directives.
type introspectedDirective struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Locations   []string     `json:"locations"`
	Args        []inputValue `json:"args"`
}

// namedRef names a root operation type.
type namedRef struct {
	Name string `json:"name"`
}

// introspectedSchema is the __schema object itself.
type introspectedSchema struct {
	QueryType        *namedRef               `json:"queryType"`
	MutationType     *namedRef               `json:"mutationType"`
	SubscriptionType *namedRef               `json:"subscriptionType"`
	Types            []introspectedType      `json:"types"`
	Directives       []introspectedDirective `json:"directives"`
}

// introspectionEnvelope accepts the three shapes an introspection
// result arrives in: the GraphQL response as sent by a server
// (`{"data":{"__schema":...}}`), the data object alone
// (`{"__schema":...}`), and a saved schema object. An operator pasting
// a schema into the admin upload route has usually saved one of the
// first two from a browser tool, so all three are read rather than
// refused on a nesting level.
type introspectionEnvelope struct {
	Data *struct {
		Schema *introspectedSchema `json:"__schema"`
	} `json:"data"`
	Schema *introspectedSchema `json:"__schema"`
}

// SDLFromIntrospection converts an introspection result to SDL. The
// SDL, not the JSON, is what a connection stores: it is what the parser
// loads, it is what an operator can read when diagnosing a refused
// document, and it is the one form both the introspection path and the
// admin upload path normalize to.
//
// Returns ErrNoIntrospection when the payload carries no __schema at
// any of the three nesting levels.
func SDLFromIntrospection(payload []byte) (string, error) {
	var env introspectionEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return "", fmt.Errorf("gqlschema: parsing introspection result: %w", err)
	}
	schema := env.Schema
	if schema == nil && env.Data != nil {
		schema = env.Data.Schema
	}
	if schema == nil || len(schema.Types) == 0 {
		return "", ErrNoIntrospection
	}
	return renderSDL(schema)
}
