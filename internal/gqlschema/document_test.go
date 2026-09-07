package gqlschema

import (
	"errors"
	"strings"
	"testing"
)

func TestParseResolvesTheOperationToRun(t *testing.T) {
	const two = `query A { health } query B { health }`
	if _, err := Parse(two, ""); !errors.Is(err, ErrOperationNameRequired) {
		t.Errorf("a two-operation document with no name gave %v", err)
	}
	if _, err := Parse(two, "C"); !errors.Is(err, ErrOperationNotInDocument) {
		t.Errorf("an unknown operation name gave %v", err)
	}
	doc, err := Parse(two, "B")
	if err != nil {
		t.Fatalf("naming an operation: %v", err)
	}
	if doc.Operation.Name != "B" {
		t.Errorf("ran %q; want B", doc.Operation.Name)
	}
	if doc.Raw != two {
		t.Error("the document was not carried through verbatim")
	}
}

func TestParseErrorNamesTheDocumentsOperations(t *testing.T) {
	_, err := Parse(`query A { health } { health }`, "")
	if err == nil {
		t.Fatal("want a refusal")
	}
	if !strings.Contains(err.Error(), "A") || !strings.Contains(err.Error(), "unnamed") {
		t.Errorf("the refusal does not name what the document defines: %v", err)
	}
}

func TestParseRefusesWhatCannotBeRun(t *testing.T) {
	if _, err := Parse(`fragment F on Query { health }`, ""); !errors.Is(err, ErrNoOperation) {
		t.Errorf("a fragments-only document gave %v", err)
	}
	if _, err := Parse(`subscription S { ticks }`, ""); !errors.Is(err, ErrSubscriptionUnsupported) {
		t.Errorf("a subscription gave %v", err)
	}
	if _, err := Parse(`query { health`, ""); err == nil {
		t.Error("an unparseable document was accepted")
	}
}

func TestKindReportsTheMethodAPersonaRuleNames(t *testing.T) {
	q, _ := Parse(`{ health }`, "")
	if q.Kind() != OperationQuery {
		t.Errorf("kind = %q; want QUERY", q.Kind())
	}
	m, _ := Parse(`mutation { sales { salesOrder { delete(_id: "1") } } }`, "")
	if m.Kind() != OperationMutation {
		t.Errorf("kind = %q; want MUTATION", m.Kind())
	}
}

func TestValidateReportsWhatTheSchemaDoesNotAdmit(t *testing.T) {
	s := loadFixture(t, "flat")
	ok, _ := Parse(`{ dataset(urn: "x") { urn name } }`, "")
	if errs := ok.Validate(s); len(errs) != 0 {
		t.Errorf("a valid document reported %v", errs)
	}
	bad, _ := Parse(`{ dataset(urn: "x") { notAField } }`, "")
	if errs := bad.Validate(s); len(errs) == 0 {
		t.Error("an unknown field validated")
	}
	if errs := bad.Validate(nil); errs != nil {
		t.Errorf("with no schema there is nothing to validate against; got %v", errs)
	}
}

func TestHasIntrospectionSelection(t *testing.T) {
	for _, c := range []struct {
		document string
		want     bool
	}{
		{`{ __schema { types { name } } }`, true},
		{`{ __type(name: "Query") { name } }`, true},
		{`{ dataset(urn: "x") { urn } }`, false},
	} {
		doc, err := Parse(c.document, "")
		if err != nil {
			t.Fatalf("parse %q: %v", c.document, err)
		}
		if got := doc.HasIntrospectionSelection(); got != c.want {
			t.Errorf("%q reported %v; want %v", c.document, got, c.want)
		}
	}
}

func TestTopLevelFieldsResolveAliasesAndFragments(t *testing.T) {
	doc, err := Parse(`
	  query {
	    renamed: dataset(urn: "a") { urn }
	    ...Spread
	    ... on Query { search(input: {query: "x"}) { total } }
	  }
	  fragment Spread on Query { me { identity { urn } } }
	`, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := doc.TopLevelFields()
	for _, want := range []string{"dataset", "me", "search"} {
		if !contains(got, want) {
			t.Errorf("%s missing from %v — an alias or a fragment hid a field", want, got)
		}
	}
	if contains(got, "renamed") {
		t.Errorf("the alias was reported instead of the field: %v", got)
	}
}

func TestTopLevelFieldsTerminatesOnAFragmentCycle(t *testing.T) {
	doc, err := Parse(`query { ...A } fragment A on Query { ...B } fragment B on Query { ...A health }`, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := doc.TopLevelFields(); !contains(got, "health") {
		t.Errorf("fields = %v; want health reached through the cycle", got)
	}
}

func TestDepthCountsWhatTheUpstreamResolves(t *testing.T) {
	cases := []struct {
		name     string
		document string
		want     int
	}{
		{"one root field", `{ health }`, 1},
		{"three levels", `{ a { b { c } } }`, 3},
		{"an inline fragment adds no level of its own", `{ a { ... on T { b } } }`, 2},
		{"a spread is measured where it lands", `query { a { ...F } } fragment F on T { b { c } }`, 3},
		{"the deepest branch decides", `{ a { b } c { d { e } } }`, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc, err := Parse(c.document, "")
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := doc.Depth(); got != c.want {
				t.Errorf("depth = %d; want %d", got, c.want)
			}
		})
	}
}

func TestDepthTerminatesOnAFragmentCycle(t *testing.T) {
	doc, err := Parse(`query { ...A } fragment A on Q { x { ...B } } fragment B on Q { ...A }`, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := doc.Depth(); got == 0 {
		t.Error("a cyclic document measured zero depth")
	}
}

func TestInvokedPathsReduceToTheOperationNotTheRootField(t *testing.T) {
	s := loadFixture(t, "namespaced")
	ops := Operations(s, 0)
	doc, err := Parse(`query { masterData { product { query(first: 1) { totalCount } } } }`, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := doc.InvokedPaths(NewPathIndex(ops, OperationQuery))
	if len(got) != 1 || got[0] != "masterData.product.query" {
		t.Fatalf("invoked = %v; a rule that names the verb must be able to match it", got)
	}
}

func TestInvokedPathsReportsAPathTheIndexDoesNotHold(t *testing.T) {
	s := loadFixture(t, "namespaced")
	doc, _ := Parse(`query { unknownRoot { thing } }`, "")
	got := doc.InvokedPaths(NewPathIndex(Operations(s, 0), OperationQuery))
	if len(got) != 1 || got[0] != "unknownRoot" {
		t.Errorf("invoked = %v; an unindexed field must still reach the policy", got)
	}
}

func TestInvokedPathsWithAnEmptyIndexFallsBackToRootFields(t *testing.T) {
	doc, _ := Parse(`query { a { b } c }`, "")
	got := doc.InvokedPaths(NewPathIndex(nil, OperationQuery))
	if len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Errorf("invoked = %v; want the root fields", got)
	}
}

func TestPathIndexHoldsOnlyItsOwnKind(t *testing.T) {
	s := loadFixture(t, "namespaced")
	idx := NewPathIndex(Operations(s, 0), OperationMutation)
	doc, _ := Parse(`mutation { sales { salesOrder { create(data: {number: "1", customer: {code: "c"}}) { _id } } } }`, "")
	got := doc.InvokedPaths(idx)
	if len(got) != 1 || got[0] != "sales.salesOrder.create" {
		t.Errorf("invoked = %v", got)
	}
}

// TestInvokedPathsRecordsAnOperationThatIsAlsoANamespace covers the shape a
// metadata service's config root has: an object with scalar fields of its own,
// which makes it an operation, and object children that are operations in
// turn. A rule naming either level has to be able to govern a document that
// reaches the deeper one.
func TestInvokedPathsRecordsAnOperationThatIsAlsoANamespace(t *testing.T) {
	const sdl = `
	type Query { appConfig: AppConfig }
	type AppConfig { appVersion: String, authConfig: AuthConfig }
	type AuthConfig { tokenAuthEnabled: Boolean }
	`
	s, err := Load(sdl)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ops := Operations(s, 3)
	idx := NewPathIndex(ops, OperationQuery)

	doc, err := Parse(`{ appConfig { appVersion authConfig { tokenAuthEnabled } } }`, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := doc.InvokedPaths(idx)
	if !contains(got, "appConfig") || !contains(got, "appConfig.authConfig") {
		t.Fatalf("invoked = %v; both levels must reach the policy", got)
	}
	// The scalar the document selected is the recorded operation's return
	// shape, not another operation to authorize.
	if contains(got, "appConfig.appVersion") {
		t.Errorf("invoked = %v; a selected field of a recorded operation is its return shape", got)
	}
}

// TestInvokedPathsRecordsAnUnknownFieldBelowANamespace keeps the walk
// fail-closed where nothing has authorized the caller yet: a field the index
// does not hold, reached through a namespace rather than through an
// operation, still reaches the policy.
func TestInvokedPathsRecordsAnUnknownFieldBelowANamespace(t *testing.T) {
	s := loadFixture(t, "namespaced")
	idx := NewPathIndex(Operations(s, 0), OperationQuery)
	doc, err := Parse(`{ masterData { product { __typename } } }`, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := doc.InvokedPaths(idx)
	if len(got) != 1 || got[0] != "masterData.product.__typename" {
		t.Errorf("invoked = %v", got)
	}
}
