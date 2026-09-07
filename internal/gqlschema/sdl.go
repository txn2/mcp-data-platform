package gqlschema

import (
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
)

// builtinTypes are defined by the parser's prelude. Re-emitting them
// from an introspection result would redefine them and fail the load.
var builtinTypes = map[string]bool{
	"String": true, "Int": true, "Float": true, "Boolean": true, "ID": true,
	"__Schema": true, "__Type": true, "__TypeKind": true, "__Field": true,
	"__InputValue": true, "__EnumValue": true, "__Directive": true,
	"__DirectiveLocation": true,
}

// builtinDirectives are likewise defined by the prelude.
var builtinDirectives = map[string]bool{
	"skip": true, "include": true, "deprecated": true, "specifiedBy": true,
}

// renderSDL turns an introspection result into schema definition
// language. SDL is the form a connection stores its schema in: the
// parser loads it, an operator can read it, and the admin upload route
// normalizes an uploaded introspection result to the same thing.
func renderSDL(s *introspectedSchema) (string, error) {
	doc := &ast.SchemaDocument{Schema: ast.SchemaDefinitionList{rootDefinition(s)}}
	for _, t := range s.Types {
		if t.Name == "" || builtinTypes[t.Name] || strings.HasPrefix(t.Name, "__") {
			continue
		}
		def, err := convertDefinition(t)
		if err != nil {
			return "", err
		}
		doc.Definitions = append(doc.Definitions, def)
	}
	for _, d := range s.Directives {
		if builtinDirectives[d.Name] {
			continue
		}
		doc.Directives = append(doc.Directives, convertDirective(d))
	}
	var sb strings.Builder
	formatter.NewFormatter(&sb).FormatSchemaDocument(doc)
	return sb.String(), nil
}

// rootDefinition emits the explicit `schema { query: ... }` block. It is
// written even when the root types carry their conventional names, so
// the SDL states the entry points rather than relying on a naming
// convention the upstream may not follow.
func rootDefinition(s *introspectedSchema) *ast.SchemaDefinition {
	def := &ast.SchemaDefinition{}
	add := func(op ast.Operation, ref *namedRef) {
		if ref == nil || ref.Name == "" {
			return
		}
		def.OperationTypes = append(def.OperationTypes,
			&ast.OperationTypeDefinition{Operation: op, Type: ref.Name})
	}
	add(ast.Query, s.QueryType)
	add(ast.Mutation, s.MutationType)
	add(ast.Subscription, s.SubscriptionType)
	return def
}

// definitionKinds maps the __TypeKind enum onto the parser's kinds.
var definitionKinds = map[string]ast.DefinitionKind{
	kindObject:      ast.Object,
	kindInterface:   ast.Interface,
	kindUnion:       ast.Union,
	kindEnum:        ast.Enum,
	kindInputObject: ast.InputObject,
	kindScalar:      ast.Scalar,
}

// convertDefinition turns one introspected type into a parser
// definition. An unrecognized __TypeKind is an error rather than a
// silent skip: a type dropped from the schema turns every document that
// names it into a validation refusal with no stated cause.
func convertDefinition(t introspectedType) (*ast.Definition, error) {
	kind, ok := definitionKinds[t.Kind]
	if !ok {
		return nil, fmt.Errorf("gqlschema: type %q has unknown kind %q", t.Name, t.Kind)
	}
	def := &ast.Definition{Kind: kind, Name: t.Name, Description: t.Description}
	for _, i := range t.Interfaces {
		if n := namedType(&i); n != "" {
			def.Interfaces = append(def.Interfaces, n)
		}
	}
	// possibleTypes carries a union's members and an interface's
	// implementors, but the parser's Types field means "a union's
	// members" alone: filling it for an interface renders it as
	// `interface Aspect = SchemaMetadata`, which is not a schema.
	if kind == ast.Union {
		for _, u := range t.Types {
			if n := namedType(&u); n != "" {
				def.Types = append(def.Types, n)
			}
		}
	}
	for _, f := range t.Fields {
		def.Fields = append(def.Fields, convertField(f))
	}
	for _, f := range t.InputFields {
		def.Fields = append(def.Fields, convertInputField(f))
	}
	for _, v := range t.EnumValues {
		def.EnumValues = append(def.EnumValues, &ast.EnumValueDefinition{
			Name: v.Name, Description: v.Description,
			Directives: deprecatedDirective(v.IsDeprecated, v.DeprecationReason),
		})
	}
	return def, nil
}

// convertField turns an output field into its definition, carrying its
// arguments and its deprecation.
func convertField(f introspectedField) *ast.FieldDefinition {
	fd := &ast.FieldDefinition{
		Name: f.Name, Description: f.Description, Type: convertType(f.Type),
		Directives: deprecatedDirective(f.IsDeprecated, f.DeprecationReason),
	}
	for _, a := range f.Args {
		fd.Arguments = append(fd.Arguments, &ast.ArgumentDefinition{
			Name: a.Name, Description: a.Description,
			Type: convertType(a.Type), DefaultValue: literalValue(a.DefaultValue),
		})
	}
	return fd
}

// convertInputField turns an input-object field into its definition.
// Input fields carry a default and no arguments.
func convertInputField(f inputValue) *ast.FieldDefinition {
	return &ast.FieldDefinition{
		Name: f.Name, Description: f.Description,
		Type: convertType(f.Type), DefaultValue: literalValue(f.DefaultValue),
	}
}

// convertDirective turns an introspected directive into its definition.
// Repeatability is not carried: IntrospectionQuery does not ask for it,
// for the compatibility reason stated there.
func convertDirective(d introspectedDirective) *ast.DirectiveDefinition {
	def := &ast.DirectiveDefinition{Name: d.Name, Description: d.Description}
	for _, l := range d.Locations {
		def.Locations = append(def.Locations, ast.DirectiveLocation(l))
	}
	for _, a := range d.Args {
		def.Arguments = append(def.Arguments, &ast.ArgumentDefinition{
			Name: a.Name, Description: a.Description,
			Type: convertType(a.Type), DefaultValue: literalValue(a.DefaultValue),
		})
	}
	return def
}

// deprecatedDirective renders the @deprecated marker for a field or
// enum value, so the stored SDL keeps what the upstream said about it
// and discovery can steer away from a retired field.
func deprecatedDirective(deprecated bool, reason *string) ast.DirectiveList {
	if !deprecated {
		return nil
	}
	d := &ast.Directive{Name: "deprecated"}
	if reason != nil && *reason != "" {
		d.Arguments = ast.ArgumentList{{
			Name:  "reason",
			Value: &ast.Value{Kind: ast.StringValue, Raw: *reason},
		}}
	}
	return ast.DirectiveList{d}
}

// literalValue wraps an introspected defaultValue. Introspection
// serializes a default as GraphQL literal source ("10", `"hi"`,
// "[1, 2]", "{a: 1}", "RED"), so it is carried through verbatim rather
// than parsed and re-printed. ast.EnumValue is the kind whose String()
// emits Raw unquoted, which is what "verbatim" requires here.
func literalValue(raw *string) *ast.Value {
	if raw == nil || *raw == "" {
		return nil
	}
	return &ast.Value{Kind: ast.EnumValue, Raw: *raw}
}

// convertType unwraps the LIST/NON_NULL chain around a named type. A
// malformed chain (no name at the bottom) yields the String type rather
// than a nil dereference; a schema that reaches that state fails to
// load with the parser's own message.
func convertType(t *typeRef) *ast.Type {
	if t == nil {
		return ast.NamedType("String", nil)
	}
	switch t.Kind {
	case kindNonNull:
		inner := convertType(t.OfType)
		inner.NonNull = true
		return inner
	case kindList:
		return ast.ListType(convertType(t.OfType), nil)
	default:
		if t.Name == "" {
			return ast.NamedType("String", nil)
		}
		return ast.NamedType(t.Name, nil)
	}
}

// namedType returns the name at the bottom of a type reference's
// wrapper chain, or "" when there is none.
func namedType(t *typeRef) string {
	for t != nil {
		if t.Name != "" {
			return t.Name
		}
		t = t.OfType
	}
	return ""
}
