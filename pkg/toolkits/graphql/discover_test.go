package graphql

import (
	"context"
	"strings"
	"testing"
)

func callDiscover(t *testing.T, tk *Toolkit, in DiscoverInput) *DiscoverOutput {
	t.Helper()
	res, _, err := tk.handleDiscover(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("handleDiscover: %v", err)
	}
	if msg := errorMessage(res); msg != "" {
		t.Fatalf("graphql_discover refused the call: %s", msg)
	}
	var out DiscoverOutput
	decodeResult(t, res, &out)
	return &out
}

func refuseDiscover(t *testing.T, tk *Toolkit, in DiscoverInput) string {
	t.Helper()
	res, _, err := tk.handleDiscover(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("handleDiscover: %v", err)
	}
	msg := errorMessage(res)
	if msg == "" {
		t.Fatal("the call was not refused")
	}
	return msg
}

func TestDiscoverListsOperationsAndNamesTheSchemaVersion(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", nil)

	out := callDiscover(t, tk, DiscoverInput{Connection: "gql"})

	if out.Level != DiscoverLevelOperations {
		t.Errorf("level = %q", out.Level)
	}
	if out.SchemaHash == "" || out.SchemaFetchedAt == "" {
		t.Errorf("out = %+v; a caller comparing two answers needs the version", out)
	}
	if len(out.Operations) < 6 {
		t.Fatalf("operations = %d", len(out.Operations))
	}
	if out.MatchedLexical != nil {
		t.Error("an unranked list reported a relevance boundary")
	}
	var found *RankedOperation
	for i, op := range out.Operations {
		if op.OperationID == "query:masterData.product.query" {
			found = &out.Operations[i]
		}
		if op.Score != nil {
			t.Errorf("%s carries a score in an unranked list", op.OperationID)
		}
	}
	if found == nil {
		t.Fatal("the entity verb was not listed")
	}
	if found.Kind != "QUERY" || found.Path != "/masterData/product/query" {
		t.Errorf("summary = %+v; these are the coordinates a persona rule names", found)
	}
	if len(found.Arguments) == 0 || !strings.Contains(found.Arguments[0], ":") {
		t.Errorf("arguments = %v; want them rendered name: Type", found.Arguments)
	}
	if !strings.Contains(out.Next, "operation_id") {
		t.Errorf("next = %q", out.Next)
	}
}

func TestDiscoverRanksLexicallyAndReportsTheBoundary(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", nil)

	out := callDiscover(t, tk, DiscoverInput{Connection: "gql", Query: "sales order create"})

	if len(out.Operations) != 1 || out.Operations[0].OperationID != "mutation:sales.salesOrder.create" {
		t.Fatalf("operations = %+v", out.Operations)
	}
	if out.MatchedLexical == nil || *out.MatchedLexical != 1 {
		t.Errorf("matched = %v", out.MatchedLexical)
	}
	if out.ShownSemantic == nil || *out.ShownSemantic != 0 {
		t.Errorf("shown = %v", out.ShownSemantic)
	}
	if out.Operations[0].LexicalMatch == nil || !*out.Operations[0].LexicalMatch {
		t.Error("a filtered row must report its lexical match")
	}
}

func TestDiscoverSaysWhenNothingMatched(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", nil)
	out := callDiscover(t, tk, DiscoverInput{Connection: "gql", Query: "zzz nothing"})
	if len(out.Operations) != 0 {
		t.Fatalf("operations = %+v", out.Operations)
	}
	if !strings.Contains(out.Note, "no operation matched") {
		t.Errorf("note = %q", out.Note)
	}
}

func TestDiscoverAppliesTheLimit(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", nil)
	out := callDiscover(t, tk, DiscoverInput{Connection: "gql", Limit: 2})
	if len(out.Operations) != 2 {
		t.Errorf("operations = %d; want the limit applied", len(out.Operations))
	}
}

func TestDiscoverReturnsARunnableSkeletonForOneOperation(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", nil)

	out := callDiscover(t, tk, DiscoverInput{
		Connection: "gql", OperationID: "query:masterData.product.query",
	})

	if out.Level != DiscoverLevelOperation || out.Operation == nil {
		t.Fatalf("out = %+v", out)
	}
	op := out.Operation
	if len(op.ArgumentDetails) != 6 {
		t.Errorf("argument details = %d", len(op.ArgumentDetails))
	}
	if len(op.ReturnShape) == 0 {
		t.Error("no return shape")
	}
	if !strings.Contains(op.Skeleton, "edges") || !strings.Contains(op.Skeleton, "node") {
		t.Errorf("the skeleton does not reach the rows of a paged connection:\n%s", op.Skeleton)
	}
	if op.Variables == "" {
		t.Error("no variables stub")
	}
	// The skeleton is the load-bearing piece: it has to run.
	u.respond = answer(`{"data":{"masterData":{"product":{"query":{"edges":[]}}}}}`)
	result := callQuery(t, tk, QueryInput{Connection: "gql", Query: op.Skeleton})
	if result.UpstreamError {
		t.Errorf("the skeleton did not run: %+v", result.Errors)
	}
}

func TestDiscoverExpandsAnInputObjectForAMutation(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", nil)
	out := callDiscover(t, tk, DiscoverInput{
		Connection: "gql", OperationID: "mutation:sales.salesOrder.create", Depth: 2,
	})
	if out.Operation == nil || len(out.Operation.InputTypes) != 1 {
		t.Fatalf("input types = %+v", out.Operation)
	}
	if out.Operation.InputTypes[0].Name != "SalesOrderInput" {
		t.Errorf("input type = %q", out.Operation.InputTypes[0].Name)
	}
}

func TestDiscoverRefusesWhatItCannotAnswer(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", nil)
	cases := []struct {
		name, want string
		in         DiscoverInput
	}{
		{"no connection", "connection is required", DiscoverInput{}},
		{"unknown connection", "not found", DiscoverInput{Connection: "nope"}},
		{"unknown ranking", "invalid ranking", DiscoverInput{Connection: "gql", Ranking: "vibes"}},
		{"unknown operation", "not found", DiscoverInput{Connection: "gql", OperationID: "query:nope"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if msg := refuseDiscover(t, tk, c.in); !strings.Contains(msg, c.want) {
				t.Errorf("msg = %q; want %q", msg, c.want)
			}
		})
	}
}

func TestDiscoverOnAConnectionWithNoSchemaNamesTheCause(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "", nil)
	if err := tk.RefreshSchema(context.Background(), "gql"); err == nil {
		t.Fatal("the fixture endpoint must refuse introspection here")
	}
	msg := refuseDiscover(t, tk, DiscoverInput{Connection: "gql"})
	if !strings.Contains(msg, "introspection is not allowed") {
		t.Errorf("msg = %q; a caller is told the cause, not shown an empty schema", msg)
	}
	if !strings.Contains(msg, "Admin > Connections") {
		t.Errorf("msg = %q; want the way out named", msg)
	}
}

func TestDiscoverAcceptsAnOperationIDWithoutItsKindPrefix(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	out := callDiscover(t, tk, DiscoverInput{Connection: "gql", OperationID: "search"})
	if out.Operation == nil || out.Operation.OperationID != "query:search" {
		t.Errorf("out = %+v", out.Operation)
	}
}
