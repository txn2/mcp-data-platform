package gqlschema

import (
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

// OperationKind distinguishes the two root types a schema exposes for
// execution. It is also the method an operation is authorized under, so
// the values are the ones a persona rule names.
type OperationKind string

const (
	// OperationQuery is a read. Persona rules name it as the method
	// QUERY.
	OperationQuery OperationKind = "QUERY"
	// OperationMutation is a write. Persona rules name it as the method
	// MUTATION.
	OperationMutation OperationKind = "MUTATION"
)

// DefaultNamespaceDepth caps how many segments a dotted operation id
// may have. Three reaches the entity-level verb of a namespaced schema
// (package, entity, verb) without indexing every leaf of a deep object
// graph.
const DefaultNamespaceDepth = 3

// Argument is one argument an operation takes.
type Argument struct {
	// Name is the argument name as the document must spell it.
	Name string `json:"name"`
	// Type is the rendered GraphQL type ("String", "ProductFilter!",
	// "[ID!]").
	Type string `json:"type"`
	// Description is the argument's own description, when the schema
	// carries one.
	Description string `json:"description,omitempty"`
	// Required reports a non-null type with no default: a document
	// omitting it does not validate.
	Required bool `json:"required,omitempty"`
	// DefaultValue is the literal the schema declares, or "" for none.
	DefaultValue string `json:"default,omitempty"`
}

// Operation is one unit of work an agent discovers and calls: a field
// on a root type, or a field reached by descending through fields that
// are namespaces rather than work.
type Operation struct {
	// ID is the kind-prefixed dotted identifier
	// ("query:x3MasterData.product.query"). It is what
	// graphql_discover reports and what an embedding row is keyed on.
	ID string
	// Kind is QUERY or MUTATION.
	Kind OperationKind
	// Path is the dotted id's segments, root field first.
	Path []string
	// Description is the descriptions collected along the path, nearest
	// last, so the operation's own description reads at the end.
	Description string
	// Arguments are the arguments the terminal field takes.
	Arguments []Argument
	// ReturnType is the named type at the bottom of the terminal
	// field's type, with its list and non-null wrappers rendered.
	ReturnType string
	// Deprecated reports the terminal field carrying @deprecated.
	Deprecated bool
}

// Dotted returns the id without its kind prefix.
func (o Operation) Dotted() string { return strings.Join(o.Path, ".") }

// PolicyPath is the path a persona rule matches this operation under:
// the dotted id with dots as slashes, leading slash included. Together
// with Kind as the method it puts a GraphQL operation into the same
// (connection, method, path) space the API route rules already
// evaluate.
func (o Operation) PolicyPath() string { return "/" + strings.Join(o.Path, "/") }

// Operations walks a schema into the operations it exposes. maxDepth
// caps the number of segments in a dotted id; zero or less means
// DefaultNamespaceDepth.
//
// The walk descends through a field only when the field is a namespace
// rather than work: it takes no arguments, and it returns a singular
// object type that is not a Relay connection. Everything else is
// terminal and becomes an operation. A flat schema therefore indexes
// its root fields, and a namespaced one indexes the verbs underneath
// its packages.
//
// Results are ordered by id so two walks of one schema produce the same
// index.
func Operations(s *Schema, maxDepth int) []Operation {
	if s == nil || s.ast == nil {
		return nil
	}
	if maxDepth <= 0 {
		maxDepth = DefaultNamespaceDepth
	}
	w := &walker{schema: s.ast, maxDepth: maxDepth}
	w.walk(s.ast.Query, branch{kind: OperationQuery})
	w.walk(s.ast.Mutation, branch{kind: OperationMutation})
	sort.Slice(w.out, func(i, j int) bool { return w.out[i].ID < w.out[j].ID })
	return w.out
}

// walker carries the schema and the depth cap across the recursion.
type walker struct {
	schema   *ast.Schema
	maxDepth int
	out      []Operation
}

// walk visits every field of def, emitting the terminal ones as
// operations and descending into the rest. path is the segments already
// taken, descs the descriptions collected along them, seen the type
// names on the current branch (which stops a self-referential schema
// from recursing forever), and parent the field whose type is def.
//
// A field that takes no arguments and yields only scalar data is not
// emitted: below the root it is a field of its parent's record, not a
// unit of work, and indexing it would fill the index with one operation
// per column. The object holding such fields is emitted instead, and
// its return shape is where those fields are read.
func (w *walker) walk(def *ast.Definition, br branch) {
	if def == nil {
		return
	}
	holdsData := false
	for _, f := range def.Fields {
		if strings.HasPrefix(f.Name, "__") {
			continue
		}
		if w.visit(f, br) {
			holdsData = true
		}
	}
	if holdsData && br.parent != nil {
		w.out = append(w.out, w.operation(br.parent, br.kind, br.path, br.descs))
	}
}

// branch is one path through the schema: which root it started at, the
// segments and descriptions taken so far, the type names already on it
// (which stops a self-referential schema recursing forever), and the
// field whose type the walk is currently inside.
type branch struct {
	kind   OperationKind
	path   []string
	descs  []string
	seen   map[string]bool
	parent *ast.FieldDefinition
}

// visit handles one field: descend into it, emit it, or report that it is
// data its parent holds rather than a unit of work.
func (w *walker) visit(f *ast.FieldDefinition, br branch) (isData bool) {
	next := append(append([]string{}, br.path...), f.Name)
	nextDescs := br.descs
	if f.Description != "" {
		nextDescs = append(append([]string{}, br.descs...), f.Description)
	}
	if target := w.namespaceTarget(f, len(next), br.seen); target != nil {
		w.walk(target, branch{
			kind: br.kind, path: next, descs: nextDescs,
			seen: copyWithType(br.seen, target.Name), parent: f,
		})
		return false
	}
	if len(br.path) > 0 && len(f.Arguments) == 0 && w.yieldsScalars(f.Type) {
		return true
	}
	w.out = append(w.out, w.operation(f, br.kind, next, nextDescs))
	return false
}

// yieldsScalars reports a type whose values are read directly rather
// than selected from: a scalar or enum, at any list depth.
func (w *walker) yieldsScalars(t *ast.Type) bool {
	if t == nil {
		return false
	}
	def := w.schema.Types[t.Name()]
	return def != nil && def.IsLeafType()
}

// namespaceTarget returns the type to descend into when a field is a
// namespace rather than work, and nil when the field is terminal. A
// field is a namespace when it takes no arguments, returns a singular
// object type that is not a Relay connection, that type has fields of
// its own, the type is not already on this branch, and the depth cap
// leaves room for at least one more segment.
func (w *walker) namespaceTarget(f *ast.FieldDefinition, depth int, seen map[string]bool) *ast.Definition {
	if len(f.Arguments) > 0 || depth >= w.maxDepth || f.Type == nil || f.Type.Elem != nil {
		return nil
	}
	target := w.schema.Types[f.Type.Name()]
	if target == nil || target.Kind != ast.Object || len(target.Fields) == 0 {
		return nil
	}
	if seen[target.Name] || isRelayConnection(target) {
		return nil
	}
	return target
}

// operation builds the terminal operation for a field.
func (*walker) operation(f *ast.FieldDefinition, kind OperationKind, path, descs []string) Operation {
	op := Operation{
		ID:          strings.ToLower(string(kind)) + ":" + strings.Join(path, "."),
		Kind:        kind,
		Path:        path,
		Description: strings.Join(descs, " — "),
		Deprecated:  f.Directives.ForName("deprecated") != nil,
	}
	if f.Type != nil {
		op.ReturnType = f.Type.String()
	}
	for _, a := range f.Arguments {
		op.Arguments = append(op.Arguments, argument(a))
	}
	return op
}

// argument renders one argument definition.
func argument(a *ast.ArgumentDefinition) Argument {
	arg := Argument{Name: a.Name, Description: a.Description}
	if a.Type != nil {
		arg.Type = a.Type.String()
		arg.Required = a.Type.NonNull && a.DefaultValue == nil
	}
	if a.DefaultValue != nil {
		arg.DefaultValue = a.DefaultValue.String()
	}
	return arg
}

// isRelayConnection reports a type that pages its own results, by the
// shape the Relay connection specification gives it rather than by its
// name. Such a type is where results are, not a namespace to descend
// through, so the field returning it is the operation.
//
// Name is deliberately not consulted: Sage X3 names an entity's
// namespace `<Entity>_Connection` while giving it verb fields and no
// edges, and a name test would stop the walk one segment above every
// operation that schema has.
func isRelayConnection(def *ast.Definition) bool {
	hasEdges, hasPageInfo := false, false
	for _, f := range def.Fields {
		switch f.Name {
		case "edges":
			hasEdges = true
		case "pageInfo":
			hasPageInfo = true
		}
	}
	return hasEdges || hasPageInfo
}

// copyWithType returns seen plus one more type name, without mutating
// the caller's map: two sibling fields must not share a branch's
// cycle guard.
func copyWithType(seen map[string]bool, name string) map[string]bool {
	out := make(map[string]bool, len(seen)+1)
	for k := range seen {
		out[k] = true
	}
	out[name] = true
	return out
}
