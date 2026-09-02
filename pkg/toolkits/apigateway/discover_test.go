package apigateway

import (
	"context"
	"strings"
	"testing"
)

// The depth api_discover answers at is decided by the arguments (#1592):
// the tests below pin each level's shape, its next sentence, and the
// answers at the edges (a spec the catalog lacks, a catalog-less
// connection asked for an operation, a denied operation).

func TestDiscover_BareCallOnMultiSpecIsTheSpecsLevel(t *testing.T) {
	tk := twoSpecConn(t)
	res, payload, err := tk.handleDiscover(context.Background(), nil, DiscoverInput{Connection: "c"})
	if err != nil || res.IsError {
		t.Fatalf("bare call: err=%v body=%s", err, textContent(res))
	}
	out, _ := payload.(DiscoverOutput)
	if out.Level != DiscoverLevelSpecs || len(out.Specs) != 2 || len(out.Operations) != 0 || out.Operation != nil {
		t.Fatalf("expected the specs level with two summaries: %+v", out)
	}
	if !strings.Contains(out.Next, "spec=<name>") || !strings.Contains(out.Next, "query=<text>") {
		t.Errorf("the specs level should name spec and query as the next arguments: %q", out.Next)
	}
	if !strings.Contains(out.Note, "2 component specs") {
		t.Errorf("the specs level should count the specs: %q", out.Note)
	}
}

func TestDiscover_QueryWithoutSpecRanksAcrossEverySpec(t *testing.T) {
	tk := twoSpecConn(t)
	res, payload, err := tk.handleDiscover(context.Background(), nil, DiscoverInput{Connection: "c", Query: "list"})
	if err != nil || res.IsError {
		t.Fatalf("query without spec: err=%v body=%s", err, textContent(res))
	}
	out, _ := payload.(DiscoverOutput)
	if out.Level != DiscoverLevelOperations || len(out.Specs) != 0 {
		t.Fatalf("a query on a multi-spec catalog is the operations level, not the specs gate: %+v", out)
	}
	specs := map[string]bool{}
	for _, op := range out.Operations {
		specs[op.Spec] = true
	}
	if !specs["orders"] || !specs["users"] {
		t.Errorf("expected operations from both specs, got %+v", out.Operations)
	}
	if !strings.Contains(out.Next, "operation_id=<id>") {
		t.Errorf("the operations level should name operation_id as the next argument: %q", out.Next)
	}
}

func TestDiscover_SpecNarrowsToOneSection(t *testing.T) {
	tk := twoSpecConn(t)
	_, payload, _ := tk.handleDiscover(context.Background(), nil, DiscoverInput{Connection: "c", Spec: "orders"})
	out, _ := payload.(DiscoverOutput)
	if out.Level != DiscoverLevelOperations || len(out.Operations) != 1 || out.Operations[0].Spec != "orders" {
		t.Fatalf("expected the orders spec's one operation: %+v", out)
	}
}

func TestDiscover_UnknownSpecIsRefusedNamingTheSpecs(t *testing.T) {
	tk := twoSpecConn(t)
	for _, in := range []DiscoverInput{
		{Connection: "c", Spec: "billing"},
		{Connection: "c", Spec: "billing", OperationID: "listOrders"},
	} {
		res, _, _ := tk.handleDiscover(context.Background(), nil, in)
		if !res.IsError {
			t.Fatalf("spec %q is not in the catalog and should be refused: %s", in.Spec, textContent(res))
		}
		body := textContent(res)
		if !strings.Contains(body, "billing") || !strings.Contains(body, "orders, users") {
			t.Errorf("the refusal should name the spec asked for and the specs the catalog has: %s", body)
		}
	}
}

func TestDiscover_OperationLevelNamesTheInvokeCall(t *testing.T) {
	tk := twoSpecConn(t)
	res, payload, _ := tk.handleDiscover(context.Background(), nil, DiscoverInput{Connection: "c", OperationID: "listOrders"})
	if res.IsError {
		t.Fatalf("operation level: %s", textContent(res))
	}
	out, _ := payload.(DiscoverOutput)
	if out.Level != DiscoverLevelOperation || out.Operation == nil || out.Operation.OperationID != "listOrders" {
		t.Fatalf("expected the operation level for listOrders: %+v", out)
	}
	for _, want := range []string{"api_invoke_endpoint", `connection="c"`, `operation_id="listOrders"`, "path_params"} {
		if !strings.Contains(out.Next, want) {
			t.Errorf("next should carry %q: %q", want, out.Next)
		}
	}
	if strings.Contains(out.Next, "spec=") {
		t.Errorf("no spec was needed here, so next should not repeat one: %q", out.Next)
	}

	_, payload, _ = tk.handleDiscover(context.Background(), nil, DiscoverInput{Connection: "c", OperationID: "listOrders", Spec: "orders"})
	out, _ = payload.(DiscoverOutput)
	if !strings.Contains(out.Next, `spec="orders"`) {
		t.Errorf("a spec the caller needed is repeated for invoke: %q", out.Next)
	}
}

func TestDiscover_NoCatalogAnswersWithTheInvokeNote(t *testing.T) {
	tk := New("test")
	if err := tk.AddConnection("bare", map[string]any{"base_url": "https://x"}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	for _, in := range []DiscoverInput{
		{Connection: "bare"},
		{Connection: "bare", Spec: "any"},
		{Connection: "bare", Query: "orders"},
	} {
		res, payload, _ := tk.handleDiscover(context.Background(), nil, in)
		if res.IsError {
			t.Fatalf("%+v: a catalog-less connection is not an error: %s", in, textContent(res))
		}
		out, _ := payload.(DiscoverOutput)
		if out.Level != DiscoverLevelOperations || len(out.Operations) != 0 || len(out.Specs) != 0 {
			t.Errorf("%+v: expected an empty operations level: %+v", in, out)
		}
		if !strings.Contains(out.Note, "no catalog configured") || !strings.Contains(out.Note, "api_invoke_endpoint with method+path") {
			t.Errorf("%+v: the note should tell the caller to invoke by method and path: %q", in, out.Note)
		}
	}
	res, _, _ := tk.handleDiscover(context.Background(), nil, DiscoverInput{Connection: "bare", OperationID: "getThing"})
	if !res.IsError || !strings.Contains(textContent(res), "no catalog configured") {
		t.Errorf("an operation_id on a catalog-less connection cannot resolve and says why: %s", textContent(res))
	}
}

func TestDiscover_DeniedOperationIsNotFoundAtEveryLevel(t *testing.T) {
	tk := twoSpecConn(t)
	tk.SetRoutePolicy(denyPathPolicy{denied: "/orders"})

	_, payload, _ := tk.handleDiscover(context.Background(), nil, DiscoverInput{Connection: "c", Spec: "orders"})
	out, _ := payload.(DiscoverOutput)
	if len(out.Operations) != 0 || !strings.Contains(out.Note, `spec "orders" has no operations this caller may invoke`) {
		t.Errorf("the operations level hides the denied operation and says so: %+v", out)
	}

	res, _, _ := tk.handleDiscover(context.Background(), nil, DiscoverInput{Connection: "c", OperationID: "listOrders"})
	if !res.IsError || !strings.Contains(textContent(res), "not found") {
		t.Errorf("the operation level reports a denied operation as not found: %s", textContent(res))
	}
	res, _, _ = tk.handleDiscover(context.Background(), nil, DiscoverInput{Connection: "c", OperationID: "listUsers"})
	if res.IsError {
		t.Errorf("an allowed operation is still served: %s", textContent(res))
	}
}

// denyPathPolicy refuses one path and allows everything else.
type denyPathPolicy struct{ denied string }

func (p denyPathPolicy) Allow(_ context.Context, _, _, path, _ string) (allowed bool, reason string) {
	if path == p.denied {
		return false, "denied by test policy"
	}
	return true, ""
}

func TestNoOperationsNote(t *testing.T) {
	cases := []struct {
		in   DiscoverInput
		want string
	}{
		{DiscoverInput{Query: "refund", Spec: "orders"}, `no operations in spec "orders" match query "refund"`},
		{DiscoverInput{Query: "refund"}, `no operations match query "refund"`},
		{DiscoverInput{Spec: "orders"}, `spec "orders" has no operations this caller may invoke`},
		{DiscoverInput{}, "this connection has no operations this caller may invoke"},
	}
	for _, tc := range cases {
		if got := noOperationsNote(tc.in); got != tc.want {
			t.Errorf("%+v: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestJoinNotes(t *testing.T) {
	if got := joinNotes("", "b"); got != "b" {
		t.Errorf("empty first: %q", got)
	}
	if got := joinNotes("a", ""); got != "a" {
		t.Errorf("empty second: %q", got)
	}
	if got := joinNotes("a", "b"); got != "a; b" {
		t.Errorf("both: %q", got)
	}
}
