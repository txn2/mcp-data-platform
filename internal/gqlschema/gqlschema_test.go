package gqlschema

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

// loadFixture loads a hand-authored schema. The two fixtures are the
// whole proof that namespace descent works: a namespaced schema shaped
// like an ERP's, and a flat one shaped like a metadata service's.
// Neither is a vendor's schema.
func loadFixture(t *testing.T, name string) *Schema {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name + ".graphql") //nolint:gosec // a fixture path this test builds
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	s, err := Load(string(raw))
	if err != nil {
		t.Fatalf("loading fixture: %v", err)
	}
	return s
}

func TestLoadRefusesEmptyAndMalformedSchemas(t *testing.T) {
	if _, err := Load("   \n"); err == nil {
		t.Error("an empty schema loaded; want a refusal")
	}
	if _, err := Load("type Query { field: "); err == nil {
		t.Error("a truncated schema loaded; want a refusal")
	}
}

func TestHashIdentifiesTheSchemaText(t *testing.T) {
	a := loadFixture(t, "flat")
	b := loadFixture(t, "flat")
	if a.Hash() != b.Hash() {
		t.Error("the same schema text produced two hashes")
	}
	c := loadFixture(t, "namespaced")
	if a.Hash() == c.Hash() {
		t.Error("two different schemas produced one hash")
	}
	if a.SDL() == "" {
		t.Error("SDL is empty")
	}
}

func TestOperationsDescendsANamespacedSchema(t *testing.T) {
	ops := Operations(loadFixture(t, "namespaced"), 0)
	got := map[string]Operation{}
	for _, op := range ops {
		got[op.ID] = op
	}
	// The entity-level verbs are the operations: descent passes through
	// the package and the entity, both of which take no arguments and
	// return a singular object, and stops at the verb that takes one.
	for _, want := range []string{
		"query:masterData.product.query",
		"query:masterData.product.read",
		"query:masterData.product.lookups",
		"query:masterData.site.query",
		"mutation:sales.salesOrder.create",
		"mutation:sales.salesOrder.delete",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("descent did not reach %s; it produced %v", want, ids(ops))
		}
	}
	// A package or an entity is a namespace, never an operation of its own.
	for _, absent := range []string{"query:masterData", "query:masterData.product"} {
		if _, ok := got[absent]; ok {
			t.Errorf("%s was indexed as an operation; it is a namespace", absent)
		}
	}
	op := got["query:masterData.product.query"]
	if op.Kind != OperationQuery {
		t.Errorf("kind = %q; want QUERY", op.Kind)
	}
	if op.PolicyPath() != "/masterData/product/query" {
		t.Errorf("policy path = %q", op.PolicyPath())
	}
	if op.ReturnType != "ProductConnection" {
		t.Errorf("return type = %q", op.ReturnType)
	}
	if len(op.Arguments) != 6 {
		t.Errorf("arguments = %d; want the 6 the fixture declares", len(op.Arguments))
	}
	if !strings.Contains(op.Description, "Master data package") ||
		!strings.Contains(op.Description, "List products") {
		t.Errorf("descriptions were not collected along the path: %q", op.Description)
	}
}

func TestOperationsStopsAtARelayConnection(t *testing.T) {
	ops := Operations(loadFixture(t, "namespaced"), 5)
	// ProductConnection carries edges and pageInfo, so it is where
	// results are rather than a namespace, even with depth to spare.
	for _, op := range ops {
		if strings.Contains(op.Dotted(), ".edges") || strings.Contains(op.Dotted(), ".pageInfo") {
			t.Errorf("descent walked into a Relay connection: %s", op.ID)
		}
	}
}

func TestOperationsRespectsTheDepthCap(t *testing.T) {
	ops := Operations(loadFixture(t, "namespaced"), 2)
	found := false
	for _, op := range ops {
		if len(op.Path) > 2 {
			t.Errorf("%s has %d segments; the cap was 2", op.ID, len(op.Path))
		}
		if op.ID == "query:masterData.product" {
			found = true
		}
	}
	if !found {
		t.Errorf("at the cap the entity itself must be the operation; got %v", ids(ops))
	}
}

func TestOperationsIndexesAFlatSchemasRootFields(t *testing.T) {
	ops := Operations(loadFixture(t, "flat"), 0)
	got := ids(ops)
	for _, want := range []string{"query:search", "query:dataset", "mutation:updateDataset"} {
		if !contains(got, want) {
			t.Errorf("%s missing from %v", want, got)
		}
	}
}

func TestOperationsEmitsTheHolderRatherThanEachScalarField(t *testing.T) {
	// `me` takes no arguments and returns an object, so descent enters
	// it; `identity` likewise. Its fields are plain scalars, which are a
	// record's columns rather than units of work, so the operation is
	// the object that holds them.
	ops := ids(Operations(loadFixture(t, "flat"), 0))
	if !contains(ops, "query:me.identity") {
		t.Errorf("the scalar-holding object was not indexed: %v", ops)
	}
	for _, absent := range []string{"query:me.identity.urn", "query:me.identity.email"} {
		if contains(ops, absent) {
			t.Errorf("%s was indexed; a no-argument scalar field is not an operation", absent)
		}
	}
	// A root scalar has no holder below the root, so it stays an
	// operation of its own.
	if !contains(ids(Operations(loadFixture(t, "namespaced"), 0)), "query:health") {
		t.Error("a root-level scalar field must still be an operation")
	}
}

func TestOperationsOnANilSchema(t *testing.T) {
	if ops := Operations(nil, 3); ops != nil {
		t.Errorf("a nil schema produced %v", ops)
	}
}

func TestDeprecationSurvivesTheRoundTrip(t *testing.T) {
	s := loadFixture(t, "namespaced")
	detail, err := Describe(s, mustLookup(t, s, "query:masterData.product.read"), 0)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	for _, f := range detail.ReturnShape {
		if f.Name == "legacyName" && !f.Deprecated {
			t.Error("legacyName lost its @deprecated marker")
		}
	}
}

func TestLookupAcceptsAnIDWithOrWithoutItsKindPrefix(t *testing.T) {
	ops := Operations(loadFixture(t, "flat"), 0)
	if _, ok := Lookup(ops, "query:search"); !ok {
		t.Error("the full id did not resolve")
	}
	if op, ok := Lookup(ops, "search"); !ok || op.ID != "query:search" {
		t.Error("the bare dotted id did not resolve to its single operation")
	}
	if _, ok := Lookup(ops, "nothing.here"); ok {
		t.Error("an unknown id resolved")
	}
}

func TestDescribeRendersARunnableSkeletonForEveryOperation(t *testing.T) {
	for _, fixture := range []string{"namespaced", "flat"} {
		s := loadFixture(t, fixture)
		for _, op := range Operations(s, 0) {
			detail, err := Describe(s, op, 0)
			if err != nil {
				t.Fatalf("%s: describe %s: %v", fixture, op.ID, err)
			}
			doc, err := Parse(detail.Skeleton, "")
			if err != nil {
				t.Fatalf("%s: skeleton for %s did not parse: %v\n%s", fixture, op.ID, err, detail.Skeleton)
			}
			if errs := doc.Validate(s); len(errs) > 0 {
				t.Errorf("%s: skeleton for %s does not validate: %v\n%s", fixture, op.ID, errs, detail.Skeleton)
			}
			var vars map[string]any
			if err := json.Unmarshal([]byte(detail.Variables), &vars); err != nil {
				t.Errorf("%s: variables stub for %s is not a JSON object: %v", fixture, op.ID, err)
			}
		}
	}
}

func TestDescribeReachesDataThroughARelayConnection(t *testing.T) {
	s := loadFixture(t, "namespaced")
	detail, err := Describe(s, mustLookup(t, s, "query:masterData.product.query"), 0)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	// The default depth exists so a paged operation's skeleton reaches
	// the node's own fields rather than stopping at the connection.
	for _, want := range []string{"edges", "node", "name", "pageInfo", "endCursor"} {
		if !strings.Contains(detail.Skeleton, want) {
			t.Errorf("skeleton is missing %q:\n%s", want, detail.Skeleton)
		}
	}
}

func TestDescribeExpandsInputObjectsAndStubsRequiredArguments(t *testing.T) {
	s := loadFixture(t, "namespaced")
	detail, err := Describe(s, mustLookup(t, s, "mutation:sales.salesOrder.create"), 0)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if len(detail.InputTypes) != 1 || detail.InputTypes[0].Name != "SalesOrderInput" {
		t.Fatalf("input types = %+v; want SalesOrderInput expanded", detail.InputTypes)
	}
	var vars map[string]any
	if err := json.Unmarshal([]byte(detail.Variables), &vars); err != nil {
		t.Fatalf("variables: %v", err)
	}
	data, ok := vars["data"].(map[string]any)
	if !ok {
		t.Fatalf("the required argument is missing from the stub: %s", detail.Variables)
	}
	if _, ok := data["number"]; !ok {
		t.Errorf("a required input field is missing from the stub: %s", detail.Variables)
	}
	if _, ok := data["note"]; ok {
		t.Errorf("an input field with a default is in the stub; leaving it out is what applies the default: %s", detail.Variables)
	}
	if _, ok := data["customer"].(map[string]any); !ok {
		t.Errorf("a nested required input object was not expanded: %s", detail.Variables)
	}
}

func TestDescribeRendersAUnionAsInlineFragments(t *testing.T) {
	s := loadFixture(t, "flat")
	detail, err := Describe(s, mustLookup(t, s, "query:search"), 0)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if !strings.Contains(detail.Skeleton, "... on Dataset") || !strings.Contains(detail.Skeleton, "__typename") {
		t.Errorf("a union member is not selectable from this skeleton:\n%s", detail.Skeleton)
	}
}

func TestDescribeRefusesAnUnknownOperation(t *testing.T) {
	s := loadFixture(t, "flat")
	_, err := Describe(s, Operation{ID: "query:nope", Kind: OperationQuery, Path: []string{"nope"}}, 0)
	if !errors.Is(err, ErrOperationNotFound) {
		t.Errorf("err = %v; want ErrOperationNotFound", err)
	}
	if _, err := Describe(nil, Operation{}, 0); !errors.Is(err, ErrOperationNotFound) {
		t.Errorf("a nil schema gave %v", err)
	}
}

func TestIndexTextCarriesThePathArgumentsAndReturnVocabulary(t *testing.T) {
	s := loadFixture(t, "namespaced")
	text := IndexText(s, mustLookup(t, s, "query:masterData.product.query"))
	for _, want := range []string{"masterData.product.query", "List products", "filter: String", "ProductConnection", "name"} {
		if !strings.Contains(text, want) {
			t.Errorf("index text is missing %q:\n%s", want, text)
		}
	}
	if len(text) > maxIndexTextChars {
		t.Errorf("index text is %d chars; the bound is %d", len(text), maxIndexTextChars)
	}
}

func TestSearchFieldsCarryWhatALexicalQueryMatchesOn(t *testing.T) {
	s := loadFixture(t, "namespaced")
	fields := SearchFields(mustLookup(t, s, "query:masterData.product.read"))
	if !contains(fields, "masterData.product.read") {
		t.Errorf("the dotted id is not a search field: %v", fields)
	}
	if !contains(fields, "_id") {
		t.Errorf("argument names are not search fields: %v", fields)
	}
}

func ids(ops []Operation) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, op.ID)
	}
	return out
}

func contains(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}

func mustLookup(t *testing.T, s *Schema, id string) Operation {
	t.Helper()
	op, ok := Lookup(Operations(s, 0), id)
	if !ok {
		t.Fatalf("fixture does not define %s", id)
	}
	return op
}

// TestPlaceholderShapesEveryScalarFamily covers the variables stub's
// per-type placeholder: a caller edits what is there, so a wrong shape
// costs them a round trip.
func TestPlaceholderShapesEveryScalarFamily(t *testing.T) {
	const sdl = `
	scalar Money
	enum Color { RED GREEN }
	input Inner { deep: String! }
	input Outer {
	  count: Int!
	  ratio: Float!
	  flag: Boolean!
	  label: String!
	  price: Money!
	  color: Color!
	  tags: [String!]!
	  inner: Inner!
	  optional: String
	}
	type Query { go(in: Outer!, plain: ID!): String }
	`
	s, err := Load(sdl)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	detail, err := Describe(s, mustLookup(t, s, "query:go"), 0)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	var vars map[string]any
	if err := json.Unmarshal([]byte(detail.Variables), &vars); err != nil {
		t.Fatalf("variables: %v", err)
	}
	in, ok := vars["in"].(map[string]any)
	if !ok {
		t.Fatalf("the input object was not stubbed: %s", detail.Variables)
	}
	checks := map[string]any{
		"count": float64(0), "ratio": float64(0), "flag": false,
		"label": "", "price": "", "color": "RED",
	}
	for key, want := range checks {
		if got := in[key]; got != want {
			t.Errorf("%s placeholder = %#v; want %#v", key, got, want)
		}
	}
	if list, ok := in["tags"].([]any); !ok || len(list) != 1 {
		t.Errorf("a list placeholder should carry one element of its own shape: %#v", in["tags"])
	}
	if _, ok := in["inner"].(map[string]any); !ok {
		t.Errorf("a nested input object was not expanded: %#v", in["inner"])
	}
	if _, ok := in["optional"]; ok {
		t.Errorf("an optional field is in the stub; leaving it out is what makes the argument absent")
	}
	if vars["plain"] != "" {
		t.Errorf("an ID placeholder = %#v; want the empty string", vars["plain"])
	}
}

// TestSkeletonOmitsAFieldItCannotSelect proves the rule the skeleton
// exists for: it never renders a document that does not validate.
func TestSkeletonOmitsAFieldItCannotSelect(t *testing.T) {
	const sdl = `
	type Query { thing(id: ID!): Thing }
	type Thing {
	  plain: String
	  needsArg(key: String!): String
	  optionalArg(key: String): String
	}
	`
	s, err := Load(sdl)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	detail, err := Describe(s, mustLookup(t, s, "query:thing"), 0)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if strings.Contains(detail.Skeleton, "needsArg") {
		t.Errorf("a field with a required argument was selected:\n%s", detail.Skeleton)
	}
	if !strings.Contains(detail.Skeleton, "optionalArg") {
		t.Errorf("a field whose arguments are all optional is selectable:\n%s", detail.Skeleton)
	}
	doc, err := Parse(detail.Skeleton, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if errs := doc.Validate(s); len(errs) > 0 {
		t.Errorf("skeleton does not validate: %v", errs)
	}
}

// TestSelectionFallsBackToTypenameWhenNothingIsSelectable keeps the
// skeleton parseable for an object every one of whose fields needs an
// argument: an empty selection set is a syntax error.
func TestSelectionFallsBackToTypenameWhenNothingIsSelectable(t *testing.T) {
	const sdl = `
	type Query { thing(id: ID!): Thing }
	type Thing { onlyWithArg(key: String!): String }
	`
	s, err := Load(sdl)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	detail, err := Describe(s, mustLookup(t, s, "query:thing"), 0)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if !strings.Contains(detail.Skeleton, "__typename") {
		t.Errorf("want a __typename fallback:\n%s", detail.Skeleton)
	}
	doc, err := Parse(detail.Skeleton, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if errs := doc.Validate(s); len(errs) > 0 {
		t.Errorf("skeleton does not validate: %v", errs)
	}
}

// TestDescentTerminatesOnASelfReferentialSchema proves the branch guard:
// a type that reaches itself would otherwise recurse until the depth cap
// on every path through it.
func TestDescentTerminatesOnASelfReferentialSchema(t *testing.T) {
	const sdl = `
	type Query { node: Node }
	type Node { self: Node, name: String }
	`
	s, err := Load(sdl)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ops := Operations(s, 6)
	for _, op := range ops {
		if len(op.Path) > 6 {
			t.Fatalf("descent did not terminate: %s", op.ID)
		}
	}
	detail, err := Describe(s, ops[0], 6)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if _, err := Parse(detail.Skeleton, ""); err != nil {
		t.Errorf("skeleton for a cyclic type did not parse: %v\n%s", err, detail.Skeleton)
	}
}

// TestIndexTextOfAnOperationTheSchemaLost is the state a caller reaches
// by holding an operation from a schema that has since been replaced.
func TestIndexTextOfAnOperationTheSchemaLost(t *testing.T) {
	s := loadFixture(t, "flat")
	gone := Operation{ID: "query:gone", Kind: OperationQuery, Path: []string{"gone"}}
	if text := IndexText(s, gone); !strings.Contains(text, "gone") {
		t.Errorf("index text = %q; the operation is still named", text)
	}
	if got := returnLeafNames(nil, gone); got != "" {
		t.Errorf("returnLeafNames on a nil schema = %q", got)
	}
}
