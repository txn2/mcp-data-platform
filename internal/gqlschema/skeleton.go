package gqlschema

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/vektah/gqlparser/v2/ast"
)

// floatPlaceholder is the value a Float argument is stubbed with.
const floatPlaceholder = 0.0

// maxInputPlaceholderDepth caps how far a variables stub expands nested
// input objects. Three reaches the fields of an input object held by an
// input object, which is as deep as a filter or a create payload
// usually nests.
const maxInputPlaceholderDepth = 3

// skeleton renders a document that calls op. Every argument the
// terminal field takes is declared as a variable, so a caller adds an
// argument by adding a key to the variables object rather than by
// editing the document. An argument whose variable is not supplied is
// absent from the call, which is what makes the stub carrying only the
// required arguments a complete request.
func (r *renderer) skeleton(op Operation, field *ast.FieldDefinition) string {
	// The selection budget is counted from the return type, not from the
	// document root: a namespaced operation is three segments deep before
	// its first selection, and charging those to the budget would leave a
	// paged connection stopping above its own rows.
	selection := r.selectionLines(field.Type, 0, len(op.Path)+1, nil)
	var b strings.Builder
	write(&b, strings.ToLower(string(op.Kind)), " ", operationName(op), variableDeclarations(field), " {\n")
	// Every segment but the last opens a namespace block. The last opens
	// one only when the operation returns something to select from: a
	// field returning a scalar carries no selection set, and writing an
	// empty one would produce a document that does not parse.
	opened := len(op.Path)
	if len(selection) == 0 {
		opened--
	}
	for i, seg := range op.Path {
		write(&b, indentOf(i+1), seg)
		if i == len(op.Path)-1 {
			write(&b, argumentBindings(field))
			if len(selection) == 0 {
				write(&b, "\n")
				break
			}
		}
		write(&b, " {\n")
	}
	write(&b, selection...)
	for i := opened; i > 0; i-- {
		write(&b, indentOf(i), "}\n")
	}
	write(&b, "}")
	return b.String()
}

// write appends every part to the builder. strings.Builder.Write never
// fails, and one helper is what keeps that fact stated once rather than
// ignored at every call.
func write(b *strings.Builder, parts ...string) {
	for _, part := range parts {
		b.WriteString(part) //nolint:errcheck,revive // strings.Builder.WriteString never returns an error
	}
}

// operationName derives a document operation name from the dotted path:
// the segments concatenated, each after the first capitalized. GraphQL
// names admit only letters, digits and underscore, and every path
// segment is already a field name, so the result needs no escaping.
func operationName(op Operation) string {
	var b strings.Builder
	for i, seg := range op.Path {
		if i == 0 || seg == "" {
			write(&b, seg)
			continue
		}
		write(&b, string(unicode.ToUpper(rune(seg[0]))), seg[1:])
	}
	if b.Len() == 0 {
		return "Operation"
	}
	return b.String()
}

// variableDeclarations renders the variable list, or "" for a field
// with no arguments.
func variableDeclarations(field *ast.FieldDefinition) string {
	if len(field.Arguments) == 0 {
		return ""
	}
	parts := make([]string, 0, len(field.Arguments))
	for _, a := range field.Arguments {
		parts = append(parts, "$"+a.Name+": "+a.Type.String())
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// argumentBindings renders the argument list binding each argument to
// its variable, or "" for a field with no arguments.
func argumentBindings(field *ast.FieldDefinition) string {
	if len(field.Arguments) == 0 {
		return ""
	}
	parts := make([]string, 0, len(field.Arguments))
	for _, a := range field.Arguments {
		parts = append(parts, a.Name+": $"+a.Name)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// selectionLines renders a type's selection as indented lines. depth is
// how many levels below the operation's return type this is, and bounds
// the walk; indent is only how far the text is set in. Returns nil for a
// leaf type, for a type already on this branch, and at the depth limit.
func (r *renderer) selectionLines(t *ast.Type, depth, indent int, seen map[string]bool) []string {
	if t == nil || depth >= r.maxDepth {
		return nil
	}
	def := r.schema.Types[t.Name()]
	if def == nil || def.IsLeafType() || seen[def.Name] {
		return nil
	}
	branch := copyWithType(seen, def.Name)
	if def.Kind == ast.Union {
		return r.unionSelectionLines(def, depth, indent, branch)
	}
	lines := r.fieldSelectionLines(def, depth, indent, branch)
	if len(lines) == 0 {
		return []string{indentOf(indent) + "__typename\n"}
	}
	return lines
}

// fieldSelectionLines renders the selectable fields of an object or
// interface.
func (r *renderer) fieldSelectionLines(def *ast.Definition, depth, indent int, seen map[string]bool) []string {
	var lines []string
	for _, f := range def.Fields {
		if strings.HasPrefix(f.Name, "__") || hasRequiredArgument(f) {
			continue
		}
		sub := r.selectionLines(f.Type, depth+1, indent+1, seen)
		if sub == nil && !r.isLeaf(f.Type) {
			continue
		}
		if sub == nil {
			lines = append(lines, indentOf(indent)+f.Name+"\n")
			continue
		}
		lines = append(lines, indentOf(indent)+f.Name+" {\n")
		lines = append(lines, sub...)
		lines = append(lines, indentOf(indent)+"}\n")
	}
	return lines
}

// unionSelectionLines renders a union as __typename plus one inline
// fragment per possible type.
func (r *renderer) unionSelectionLines(def *ast.Definition, depth, indent int, seen map[string]bool) []string {
	lines := []string{indentOf(indent) + "__typename\n"}
	for i, name := range def.Types {
		if i >= maxUnionMembers {
			break
		}
		sub := r.selectionLines(ast.NamedType(name, nil), depth+1, indent+1, seen)
		if len(sub) == 0 {
			continue
		}
		lines = append(lines, indentOf(indent)+"... on "+name+" {\n")
		lines = append(lines, sub...)
		lines = append(lines, indentOf(indent)+"}\n")
	}
	return lines
}

// isLeaf reports a type whose fields are not selected: a scalar, an
// enum, or a type the schema does not define.
func (r *renderer) isLeaf(t *ast.Type) bool {
	if t == nil {
		return true
	}
	def := r.schema.Types[t.Name()]
	return def == nil || def.IsLeafType()
}

func indentOf(n int) string { return strings.Repeat("  ", n) }

// renderVariables builds the variables stub: a JSON object carrying one
// entry per required argument, with a placeholder of that argument's
// shape. Optional arguments are declared in the document but left out
// here, because a declared variable that is not supplied leaves its
// argument absent and the schema's own default in force. A caller adds
// an optional argument by adding its key.
func renderVariables(schema *ast.Schema, field *ast.FieldDefinition) string {
	vars := map[string]any{}
	for _, a := range field.Arguments {
		if a.Type == nil || !a.Type.NonNull || a.DefaultValue != nil {
			continue
		}
		vars[a.Name] = placeholder(schema, a.Type, 0)
	}
	out, err := json.MarshalIndent(vars, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(out)
}

// placeholder builds a value of the shape a type expects, so the stub
// is editable rather than a set of nulls the caller has to type from
// the schema.
func placeholder(schema *ast.Schema, t *ast.Type, depth int) any {
	if t == nil || depth > maxInputPlaceholderDepth {
		return nil
	}
	if t.Elem != nil {
		return []any{placeholder(schema, t.Elem, depth+1)}
	}
	def := schema.Types[t.Name()]
	if def == nil {
		return ""
	}
	switch def.Kind {
	case ast.Enum:
		if len(def.EnumValues) > 0 {
			return def.EnumValues[0].Name
		}
		return ""
	case ast.InputObject:
		return inputPlaceholder(schema, def, depth)
	default:
		return scalarPlaceholder(def.Name)
	}
}

// inputPlaceholder expands an input object into its required fields.
func inputPlaceholder(schema *ast.Schema, def *ast.Definition, depth int) map[string]any {
	out := map[string]any{}
	for _, f := range def.Fields {
		if f.Type == nil || !f.Type.NonNull || f.DefaultValue != nil {
			continue
		}
		out[f.Name] = placeholder(schema, f.Type, depth+1)
	}
	return out
}

// scalarPlaceholder returns the zero value a scalar's JSON form takes.
// A custom scalar is a string: that is how the overwhelming majority
// serialize, and a wrong placeholder is corrected by the caller where a
// missing one would have to be invented by them.
func scalarPlaceholder(name string) any {
	switch name {
	case "Int":
		return 0
	case "Float":
		return floatPlaceholder
	case "Boolean":
		return false
	default:
		return ""
	}
}
