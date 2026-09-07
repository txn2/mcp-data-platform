package gqlschema

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

// DefaultSelectionDepth is how deep a rendered selection set goes. Four
// is what a Relay connection needs to reach data — connection, edges,
// node, then the node's own scalar fields — so the default skeleton for
// a paged operation is runnable rather than a stub the caller has to
// finish.
const DefaultSelectionDepth = 4

// maxUnionMembers caps how many inline fragments a union's selection
// renders. A union of dozens of entity types (a search result) would
// otherwise render a skeleton longer than the schema excerpt around it.
const maxUnionMembers = 5

// ErrOperationNotFound reports an operation id that no field of the
// schema answers to.
var ErrOperationNotFound = errors.New("gqlschema: operation not found")

// InputType is an input-object type an argument references, expanded so
// the caller can fill the argument in without a second lookup.
type InputType struct {
	// Name is the type name as an argument's type names it.
	Name string `json:"name"`
	// Description is the type's own description.
	Description string `json:"description,omitempty"`
	// Fields are the input object's fields, in schema order.
	Fields []Argument `json:"fields,omitempty"`
}

// FieldNode is one node of an operation's return shape.
type FieldNode struct {
	// Name is the field name, or the inline-fragment condition
	// ("... on Dataset") for a union or interface member.
	Name string `json:"name"`
	// Type is the rendered field type.
	Type string `json:"type"`
	// Description is the field's description, when it has one.
	Description string `json:"description,omitempty"`
	// Deprecated reports the field carrying @deprecated.
	Deprecated bool `json:"deprecated,omitempty"`
	// Fields are the node's own sub-fields, empty at a leaf or at the
	// depth limit.
	Fields []FieldNode `json:"fields,omitempty"`
}

// Detail is everything graphql_discover reports about one operation:
// what it takes, what it returns, and a document that runs.
type Detail struct {
	// Operation is the indexed operation this describes.
	Operation Operation
	// InputTypes are the input-object types the arguments reference,
	// expanded one level and deduplicated.
	InputTypes []InputType
	// ReturnShape is the return type's field tree, depth-limited.
	ReturnShape []FieldNode
	// Skeleton is a document that validates against this schema and
	// calls this operation, with every argument bound to a variable.
	Skeleton string
	// Variables is a JSON object literal carrying one entry per
	// argument, ready to edit and send as the variables argument.
	Variables string
}

// Lookup finds an operation by id. The kind prefix is optional: an
// agent that read "query:search" back from a ranked list passes it
// whole, and one that typed "search" from a description gets the same
// operation as long as only one kind defines it.
func Lookup(ops []Operation, id string) (Operation, bool) {
	for _, op := range ops {
		if op.ID == id {
			return op, true
		}
	}
	var found Operation
	matches := 0
	for _, op := range ops {
		if op.Dotted() == id {
			found = op
			matches++
		}
	}
	if matches == 1 {
		return found, true
	}
	return Operation{}, false
}

// Describe builds the full description of one operation. selectionDepth
// caps the rendered return shape and skeleton selection; zero or less
// means DefaultSelectionDepth.
func Describe(s *Schema, op Operation, selectionDepth int) (Detail, error) {
	if s == nil || s.ast == nil {
		return Detail{}, ErrOperationNotFound
	}
	if selectionDepth <= 0 {
		selectionDepth = DefaultSelectionDepth
	}
	field, err := resolveField(s.ast, op)
	if err != nil {
		return Detail{}, err
	}
	r := &renderer{schema: s.ast, maxDepth: selectionDepth}
	detail := Detail{
		Operation:   op,
		InputTypes:  r.inputTypes(field),
		ReturnShape: r.shape(field.Type, 0, nil),
		Variables:   renderVariables(s.ast, field),
	}
	detail.Skeleton = r.skeleton(op, field)
	return detail, nil
}

// resolveField walks an operation's path from its root type down to the
// field the operation names.
func resolveField(schema *ast.Schema, op Operation) (*ast.FieldDefinition, error) {
	def := schema.Query
	if op.Kind == OperationMutation {
		def = schema.Mutation
	}
	var field *ast.FieldDefinition
	for _, seg := range op.Path {
		if def == nil {
			return nil, fmt.Errorf("gqlschema: %w: %s", ErrOperationNotFound, op.ID)
		}
		field = def.Fields.ForName(seg)
		if field == nil {
			return nil, fmt.Errorf("gqlschema: %w: %s", ErrOperationNotFound, op.ID)
		}
		def = schema.Types[field.Type.Name()]
	}
	if field == nil {
		return nil, fmt.Errorf("gqlschema: %w: %s", ErrOperationNotFound, op.ID)
	}
	return field, nil
}

// renderer carries the schema and the depth cap across the selection
// walk.
type renderer struct {
	schema   *ast.Schema
	maxDepth int
}

// inputTypes expands the input-object types the field's arguments
// reference, one level deep, deduplicated and in argument order.
func (r *renderer) inputTypes(field *ast.FieldDefinition) []InputType {
	var out []InputType
	seen := map[string]bool{}
	for _, a := range field.Arguments {
		if a.Type == nil {
			continue
		}
		name := a.Type.Name()
		def := r.schema.Types[name]
		if def == nil || def.Kind != ast.InputObject || seen[name] {
			continue
		}
		seen[name] = true
		it := InputType{Name: name, Description: def.Description}
		for _, f := range def.Fields {
			it.Fields = append(it.Fields, Argument{
				Name: f.Name, Type: f.Type.String(), Description: f.Description,
				Required:     f.Type.NonNull && f.DefaultValue == nil,
				DefaultValue: defaultLiteral(f.DefaultValue),
			})
		}
		out = append(out, it)
	}
	return out
}

// shape renders a type's field tree down to the depth limit. seen holds
// the type names already on this branch so a self-referential schema
// terminates.
func (r *renderer) shape(t *ast.Type, depth int, seen map[string]bool) []FieldNode {
	if t == nil || depth >= r.maxDepth {
		return nil
	}
	def := r.schema.Types[t.Name()]
	if def == nil || seen[def.Name] {
		return nil
	}
	branch := copyWithType(seen, def.Name)
	if def.Kind == ast.Union {
		return r.unionShape(def, depth, branch)
	}
	var out []FieldNode
	for _, f := range def.Fields {
		if strings.HasPrefix(f.Name, "__") || hasRequiredArgument(f) {
			continue
		}
		out = append(out, FieldNode{
			Name: f.Name, Type: f.Type.String(), Description: f.Description,
			Deprecated: f.Directives.ForName("deprecated") != nil,
			Fields:     r.shape(f.Type, depth+1, branch),
		})
	}
	return out
}

// unionShape renders a union as its possible types, each an inline
// fragment carrying that member's own fields.
func (r *renderer) unionShape(def *ast.Definition, depth int, seen map[string]bool) []FieldNode {
	out := []FieldNode{{Name: "__typename", Type: "String!"}}
	for i, name := range def.Types {
		if i >= maxUnionMembers {
			break
		}
		member := r.schema.Types[name]
		if member == nil {
			continue
		}
		out = append(out, FieldNode{
			Name:   "... on " + name,
			Type:   name,
			Fields: r.shape(ast.NamedType(name, nil), depth+1, seen),
		})
	}
	return out
}

// hasRequiredArgument reports a field that cannot be selected without
// supplying an argument. Such a field is left out of a rendered
// selection: including it would produce a document that does not
// validate, which is the one thing the skeleton exists to avoid.
func hasRequiredArgument(f *ast.FieldDefinition) bool {
	for _, a := range f.Arguments {
		if a.Type != nil && a.Type.NonNull && a.DefaultValue == nil {
			return true
		}
	}
	return false
}

// defaultLiteral renders a default value, or "" when there is none.
func defaultLiteral(v *ast.Value) string {
	if v == nil {
		return ""
	}
	return v.String()
}

// maxIndexTextChars bounds the text an operation is embedded from. An
// embedding model truncates its input anyway, and a return type with
// hundreds of fields would otherwise push the operation's own name and
// description out of the window that decides its vector.
const maxIndexTextChars = 2000

// IndexText is what an operation is embedded from and ranked
// lexically against: its dotted path, the descriptions collected along
// that path, its argument names and types, and the scalar field names
// of what it returns. The return type's leaf names are in it because
// they are the vocabulary a caller asks in — someone looking for an
// order's ship date searches for "ship date", which appears nowhere in
// the operation's own name.
func IndexText(s *Schema, op Operation) string {
	parts := []string{op.Dotted()}
	if op.Description != "" {
		parts = append(parts, op.Description)
	}
	for _, a := range op.Arguments {
		parts = append(parts, a.Name+": "+a.Type)
	}
	if op.ReturnType != "" {
		parts = append(parts, "returns "+op.ReturnType)
	}
	if leaves := returnLeafNames(s, op); leaves != "" {
		parts = append(parts, leaves)
	}
	text := strings.Join(parts, "\n")
	if len(text) > maxIndexTextChars {
		return text[:maxIndexTextChars]
	}
	return text
}

// returnLeafNames lists the scalar and enum field names reachable in
// the operation's return shape, deduplicated and in first-seen order.
func returnLeafNames(s *Schema, op Operation) string {
	if s == nil || s.ast == nil {
		return ""
	}
	field, err := resolveField(s.ast, op)
	if err != nil {
		return ""
	}
	r := &renderer{schema: s.ast, maxDepth: DefaultSelectionDepth}
	seen := map[string]bool{}
	var names []string
	var walk func(nodes []FieldNode)
	walk = func(nodes []FieldNode) {
		for _, n := range nodes {
			if len(n.Fields) > 0 {
				walk(n.Fields)
				continue
			}
			if strings.HasPrefix(n.Name, "__") || strings.HasPrefix(n.Name, "... on ") || seen[n.Name] {
				continue
			}
			seen[n.Name] = true
			names = append(names, n.Name)
		}
	}
	walk(r.shape(field.Type, 0, nil))
	return strings.Join(names, " ")
}

// SearchFields are the texts a query's tokens are matched against when
// an operation is ranked lexically.
func SearchFields(op Operation) []string {
	fields := make([]string, 0, 3+len(op.Arguments))
	fields = append(fields, op.Dotted(), op.Description, op.ReturnType)
	for _, a := range op.Arguments {
		fields = append(fields, a.Name)
	}
	return fields
}
