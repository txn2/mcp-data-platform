package gqlschema

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"
	"github.com/vektah/gqlparser/v2/validator/rules"
)

// Errors a document is refused with before it is ever sent upstream.
var (
	// ErrOperationNameRequired reports a document defining more than one
	// operation with no operation_name naming which to run. GraphQL
	// leaves that undefined, so the platform refuses rather than
	// picking.
	ErrOperationNameRequired = errors.New("gqlschema: document defines more than one operation, so operation_name is required")
	// ErrOperationNotInDocument reports an operation_name the document
	// does not define.
	ErrOperationNotInDocument = errors.New("gqlschema: document defines no operation with that name")
	// ErrNoOperation reports a document with no executable operation
	// (fragments only).
	ErrNoOperation = errors.New("gqlschema: document defines no operation to execute")
	// ErrSubscriptionUnsupported reports a subscription. A subscription
	// is a long-lived stream over a transport this kind does not open.
	ErrSubscriptionUnsupported = errors.New("gqlschema: subscriptions are not supported")
	// ErrIntrospectionSelection reports a document reading the schema
	// rather than the data. The stored schema is what graphql_discover
	// serves, so an agent reaching for __schema is steered there instead
	// of spending a call and a context window on the raw result.
	ErrIntrospectionSelection = errors.New("gqlschema: introspection selections (__schema, __type) are not executed through this tool, so call graphql_discover for the schema")
)

// Document is a parsed GraphQL document with the operation to execute
// already resolved.
type Document struct {
	// Raw is the document text as the caller wrote it. It is what goes
	// on the wire: the platform never re-prints a caller's document,
	// so a formatting quirk is never mistaken for a semantic change.
	Raw string
	// Operation is the operation that will execute.
	Operation *ast.OperationDefinition
	// fragments are the document's fragment definitions, needed to
	// resolve spreads when reducing to top-level fields and measuring
	// depth.
	fragments ast.FragmentDefinitionList
}

// Parse parses a document and resolves which of its operations will
// execute. operationName may be empty for a single-operation document.
func Parse(raw, operationName string) (*Document, error) {
	doc, err := parser.ParseQuery(&ast.Source{Name: "request.graphql", Input: raw})
	if err != nil {
		return nil, fmt.Errorf("gqlschema: parsing document: %w", err)
	}
	op, err := resolveOperation(doc.Operations, operationName)
	if err != nil {
		return nil, err
	}
	if op.Operation == ast.Subscription {
		return nil, ErrSubscriptionUnsupported
	}
	return &Document{Raw: raw, Operation: op, fragments: doc.Fragments}, nil
}

// resolveOperation picks the operation to execute, refusing the two
// ambiguous cases rather than guessing at them.
func resolveOperation(ops ast.OperationList, name string) (*ast.OperationDefinition, error) {
	if len(ops) == 0 {
		return nil, ErrNoOperation
	}
	if name == "" {
		if len(ops) > 1 {
			return nil, fmt.Errorf("gqlschema: %w (this document defines %s)",
				ErrOperationNameRequired, strings.Join(operationNames(ops), ", "))
		}
		return ops[0], nil
	}
	for _, op := range ops {
		if op.Name == name {
			return op, nil
		}
	}
	return nil, fmt.Errorf("gqlschema: %w: %q (this document defines %s)",
		ErrOperationNotInDocument, name, strings.Join(operationNames(ops), ", "))
}

// operationNames lists a document's operation names for an error
// message, naming an anonymous operation as such.
func operationNames(ops ast.OperationList) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		if op.Name == "" {
			out = append(out, "an unnamed operation")
			continue
		}
		out = append(out, op.Name)
	}
	return out
}

// Kind reports the operation kind, in the vocabulary a persona rule
// names as the method.
func (d *Document) Kind() OperationKind {
	if d.Operation != nil && d.Operation.Operation == ast.Mutation {
		return OperationMutation
	}
	return OperationQuery
}

// Validate checks the document against a schema, returning one message
// per violation. An empty result means the document is executable
// against this schema.
//
// The whole document is validated, not only the operation that will
// execute: an unused operation with an unknown field is still a sign
// the caller is working from a schema this connection does not serve.
func (d *Document) Validate(s *Schema) []string {
	if s == nil || s.ast == nil {
		return nil
	}
	doc, err := parser.ParseQuery(&ast.Source{Name: "request.graphql", Input: d.Raw})
	if err != nil {
		return []string{err.Error()}
	}
	errs := validator.ValidateWithRules(s.ast, doc, rules.NewDefaultRules())
	if len(errs) == 0 {
		return nil
	}
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Message)
	}
	return out
}

// HasIntrospectionSelection reports the executed operation selecting
// __schema or __type at its root.
func (d *Document) HasIntrospectionSelection() bool {
	for _, name := range d.TopLevelFields() {
		if name == "__schema" || name == "__type" {
			return true
		}
	}
	return false
}

// TopLevelFields returns the field names the executed operation selects
// at its root, sorted and deduplicated. Aliases are resolved to the
// underlying field name and fragment spreads are expanded, so a caller
// cannot reach a denied operation by renaming it or by hiding it in a
// fragment.
func (d *Document) TopLevelFields() []string {
	if d.Operation == nil {
		return nil
	}
	found := map[string]bool{}
	d.collectFields(d.Operation.SelectionSet, found, map[string]bool{})
	out := make([]string, 0, len(found))
	for name := range found {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// collectFields walks a selection set one level deep, following
// fragment spreads and inline fragments to the fields they contribute.
// seen guards against a fragment cycle; the parser accepts one and the
// validator is what refuses it, so this walk cannot assume it is
// absent.
func (d *Document) collectFields(set ast.SelectionSet, found, seen map[string]bool) {
	for _, sel := range set {
		switch s := sel.(type) {
		case *ast.Field:
			found[s.Name] = true
		case *ast.InlineFragment:
			d.collectFields(s.SelectionSet, found, seen)
		case *ast.FragmentSpread:
			if seen[s.Name] {
				continue
			}
			seen[s.Name] = true
			if def := d.fragments.ForName(s.Name); def != nil {
				d.collectFields(def.SelectionSet, found, seen)
			}
		}
	}
}

// Depth measures the executed operation's deepest selection, counting
// the root fields as depth 1 and expanding fragment spreads inline —
// the depth an upstream resolves, not the depth the document is written
// at.
func (d *Document) Depth() int {
	if d.Operation == nil {
		return 0
	}
	return d.depthOf(d.Operation.SelectionSet, map[string]bool{})
}

// depthOf returns the deepest path through a selection set.
func (d *Document) depthOf(set ast.SelectionSet, seen map[string]bool) int {
	deepest := 0
	for _, sel := range set {
		if n := d.selectionDepth(sel, seen); n > deepest {
			deepest = n
		}
	}
	return deepest
}

// selectionDepth returns the depth one selection contributes. An inline
// fragment and a spread add no level of their own: they are notation
// for the fields inside them.
func (d *Document) selectionDepth(sel ast.Selection, seen map[string]bool) int {
	switch s := sel.(type) {
	case *ast.Field:
		return 1 + d.depthOf(s.SelectionSet, seen)
	case *ast.InlineFragment:
		return d.depthOf(s.SelectionSet, seen)
	case *ast.FragmentSpread:
		if seen[s.Name] {
			return 0
		}
		branch := copyWithType(seen, s.Name)
		if def := d.fragments.ForName(s.Name); def != nil {
			return d.depthOf(def.SelectionSet, branch)
		}
	}
	return 0
}

// PathIndex is the operation index a document is reduced against: the
// dotted paths that are operations, and the paths that are namespaces
// on the way to one. It is what turns "the fields this document
// selects" into "the operations this document invokes", which is the
// unit a persona rule names.
type PathIndex struct {
	ops      map[string]bool
	prefixes map[string]bool
}

// NewPathIndex builds the index for one operation kind. Only that
// kind's operations are in it, because a query document can never
// invoke a mutation.
func NewPathIndex(ops []Operation, kind OperationKind) PathIndex {
	idx := PathIndex{ops: map[string]bool{}, prefixes: map[string]bool{}}
	for _, op := range ops {
		if op.Kind != kind {
			continue
		}
		idx.ops[op.Dotted()] = true
		for i := 1; i < len(op.Path); i++ {
			idx.prefixes[strings.Join(op.Path[:i], ".")] = true
		}
	}
	return idx
}

// InvokedPaths reduces the executed operation to the dotted paths it
// invokes, sorted. The walk descends through a namespace field and
// stops at the operation underneath it, so a document reaching
// `masterData { product { query { ... } } }` is authorized as
// `masterData.product.query` rather than as its root field — which is
// the difference between a rule that names an entity's verb and a rule
// that can only name a whole package.
//
// A field that is an operation and also a namespace (an object with
// scalar fields of its own and object children that are operations in
// turn) is recorded and descended into, so a rule naming either level
// governs a document that reaches the deeper one.
//
// Below a recorded operation, a path the index does not hold is that
// operation's return shape and is not recorded: authorizing it would
// require a rule per selected field. Above one — at a namespace, or at
// the root — such a path is recorded as it stands, because nothing has
// authorized it yet and an index that has not been built must still
// reach the policy.
//
// Aliases are ignored (the walk reads field names) and fragments are
// expanded, so a caller cannot reach a denied operation by renaming it
// or by hiding it in a fragment.
func (d *Document) InvokedPaths(idx PathIndex) []string {
	if d.Operation == nil {
		return nil
	}
	found := map[string]bool{}
	d.walkInvoked(d.Operation.SelectionSet, walkState{idx: idx, found: found, seen: map[string]bool{}})
	out := make([]string, 0, len(found))
	for p := range found {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// walkState carries one branch of the invocation walk: where it is, what
// it has found, which fragments it has expanded, and whether an
// operation above it has already been recorded.
type walkState struct {
	idx     PathIndex
	path    []string
	found   map[string]bool
	seen    map[string]bool
	covered bool
}

// walkInvoked descends one selection set, recording the operations it
// reaches.
func (d *Document) walkInvoked(set ast.SelectionSet, st walkState) {
	for _, sel := range set {
		switch s := sel.(type) {
		case *ast.Field:
			d.walkInvokedField(s, st)
		case *ast.InlineFragment:
			d.walkInvoked(s.SelectionSet, st)
		case *ast.FragmentSpread:
			if st.seen[s.Name] {
				continue
			}
			st.seen[s.Name] = true
			if def := d.fragments.ForName(s.Name); def != nil {
				d.walkInvoked(def.SelectionSet, st)
			}
		}
	}
}

// walkInvokedField applies the rule above to one field.
func (d *Document) walkInvokedField(f *ast.Field, st walkState) {
	next := append(append([]string{}, st.path...), f.Name)
	dotted := strings.Join(next, ".")
	isOp, isNamespace := st.idx.ops[dotted], st.idx.prefixes[dotted]
	switch {
	case isOp:
		st.found[dotted] = true
		if isNamespace {
			d.walkInvoked(f.SelectionSet, walkState{
				idx: st.idx, path: next, found: st.found, seen: st.seen, covered: true,
			})
		}
	case isNamespace:
		d.walkInvoked(f.SelectionSet, walkState{
			idx: st.idx, path: next, found: st.found, seen: st.seen, covered: st.covered,
		})
	case !st.covered:
		st.found[dotted] = true
	}
}
