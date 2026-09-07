package graphql

import (
	"strings"
	"testing"
)

func TestPolicyHidesAnOperationTheCallerCannotRun(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", nil)
	// The one-line read-only persona: deny the MUTATION method with no
	// path named. A path glob would not do it — filepath.Match's `*` does
	// not cross a separator and `**` is not recursive — so the method is
	// what a rule of this shape names.
	tk.SetRoutePolicy(denyPolicy{deny: func(method, _ string) bool { return method == "MUTATION" }})

	out := callDiscover(t, tk, DiscoverInput{Connection: "gql"})
	for _, op := range out.Operations {
		if op.Kind == "MUTATION" {
			t.Errorf("%s is listed to a persona that cannot run it", op.OperationID)
		}
	}
	if len(out.Operations) == 0 {
		t.Error("every operation was hidden")
	}
	// And the one it hides cannot be reached by naming it either.
	msg := refuseDiscover(t, tk, DiscoverInput{Connection: "gql", OperationID: "mutation:sales.salesOrder.create"})
	if !strings.Contains(msg, "not found") {
		t.Errorf("msg = %q", msg)
	}
}

func TestPolicyRefusesADocumentThatInvokesADeniedOperation(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{}}`)
	tk := newToolkit(t, u, "namespaced", nil)
	tk.SetRoutePolicy(denyPolicy{deny: func(method, _ string) bool { return method == "MUTATION" }})

	msg := refuseQuery(t, tk, QueryInput{
		Connection: "gql",
		Query:      `mutation { sales { salesOrder { delete(_id: "1") } } }`,
	})
	if !strings.Contains(msg, "MUTATION /sales/salesOrder/delete") {
		t.Errorf("msg = %q; the refusal names what was denied", msg)
	}
	if len(u.calls()) != 0 {
		t.Error("a denied document reached the endpoint")
	}
}

func TestPolicyIsCheckedAgainstTheOperationNotTheRootField(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{}}`)
	tk := newToolkit(t, u, "namespaced", nil)
	// A rule that names the entity's verb must be able to match it: the
	// document's root field is only the package.
	tk.SetRoutePolicy(denyPolicy{deny: func(_, path string) bool {
		return path == "/masterData/product/read"
	}})

	msg := refuseQuery(t, tk, QueryInput{
		Connection: "gql",
		Query:      `{ masterData { product { read(_id: "1") { _id } } } }`,
	})
	if !strings.Contains(msg, "/masterData/product/read") {
		t.Errorf("msg = %q", msg)
	}
	// A sibling verb under the same package is unaffected.
	callQuery(t, tk, QueryInput{
		Connection: "gql",
		Query:      `{ masterData { product { lookups(field: "name") } } }`,
	})
}

func TestAMixedDocumentIsRefusedWhenAnyPartIsDenied(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{}}`)
	tk := newToolkit(t, u, "namespaced", nil)
	tk.SetRoutePolicy(denyPolicy{deny: func(_, path string) bool {
		return path == "/masterData/site/read"
	}})

	msg := refuseQuery(t, tk, QueryInput{
		Connection: "gql",
		Query: `{
		  masterData {
		    product { read(_id: "1") { _id } }
		    site { read(_id: "2") { _id } }
		  }
		}`,
	})
	if !strings.Contains(msg, "/masterData/site/read") {
		t.Errorf("msg = %q", msg)
	}
	// The permitted half is not sent on its own: the caller asked for one
	// answer and must not be handed a different one silently.
	if len(u.calls()) != 0 {
		t.Error("a partially-denied document was sent")
	}
}

func TestAliasesAndFragmentsDoNotHideADeniedOperation(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{}}`)
	tk := newToolkit(t, u, "namespaced", nil)
	tk.SetRoutePolicy(denyPolicy{deny: func(_, path string) bool {
		return path == "/masterData/product/read"
	}})

	for _, document := range []string{
		`{ masterData { product { renamed: read(_id: "1") { _id } } } }`,
		`{ masterData { product { ...F } } } fragment F on Product_Connection { read(_id: "1") { _id } }`,
		`{ masterData { ... on MasterDataPackage { product { read(_id: "1") { _id } } } } }`,
	} {
		if msg := refuseQuery(t, tk, QueryInput{Connection: "gql", Query: document}); msg == "" {
			t.Errorf("this document reached the endpoint: %s", document)
		}
	}
}

func TestReadOnlyRefusesEveryMutationForEveryPersona(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{}}`)
	tk := newToolkit(t, u, "namespaced", map[string]any{"read_only": true})
	// No route policy at all: read_only is the connection's own rule.
	msg := refuseQuery(t, tk, QueryInput{
		Connection: "gql",
		Query:      `mutation { sales { salesOrder { delete(_id: "1") } } }`,
	})
	if !strings.Contains(msg, "read_only") {
		t.Errorf("msg = %q", msg)
	}
	// A query on the same connection still runs.
	callQuery(t, tk, QueryInput{Connection: "gql", Query: `{ health }`})
}

func TestWithNoPolicyTheConnectionGateIsTheOnlyOne(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{}}`)
	tk := newToolkit(t, u, "namespaced", nil)
	out := callDiscover(t, tk, DiscoverInput{Connection: "gql"})
	if len(out.Operations) < 6 {
		t.Errorf("operations = %d; a nil policy hides nothing", len(out.Operations))
	}
	callQuery(t, tk, QueryInput{Connection: "gql", Query: `mutation { sales { salesOrder { delete(_id: "1") } } }`})
}

func TestThePolicyIsAskedWithTheOperationsOwnCoordinates(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{}}`)
	policy := &allowPolicy{}
	tk := newToolkit(t, u, "namespaced", nil)
	tk.SetRoutePolicy(policy)

	callQuery(t, tk, QueryInput{
		Connection: "gql",
		Query:      `{ masterData { product { lookups(field: "name") } } }`,
	})

	var found bool
	for _, asked := range policy.asked {
		if asked == [3]string{"gql", "QUERY", "/masterData/product/lookups"} {
			found = true
		}
	}
	if !found {
		t.Errorf("the policy was asked %v", policy.asked)
	}
}
