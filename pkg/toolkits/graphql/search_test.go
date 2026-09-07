package graphql

import (
	"context"
	"testing"
)

func TestSearchOperationsRanksAcrossEveryConnection(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", nil)
	if err := tk.AddConnection("second", map[string]any{"endpoint_url": u.server.URL}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := tk.SetSchema(context.Background(), "second", fixtureSDL(t, "flat")); err != nil {
		t.Fatalf("schema: %v", err)
	}

	got := tk.SearchOperations(context.Background(), "dataset", 10)

	if len(got) == 0 {
		t.Fatal("nothing was federated")
	}
	seen := map[string]bool{}
	for _, r := range got {
		seen[r.Connection] = true
		if r.Score <= 0 {
			t.Errorf("%s carries no relevance signal", r.Operation.ID)
		}
	}
	if !seen["second"] {
		t.Errorf("the second connection was not searched: %+v", got)
	}
}

func TestSearchOperationsIsScopedByTheRoutePolicy(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", nil)
	tk.SetRoutePolicy(denyPolicy{deny: func(method, _ string) bool { return method == "MUTATION" }})

	for _, r := range tk.SearchOperations(context.Background(), "sales order", 10) {
		if r.Operation.Kind == "MUTATION" {
			t.Errorf("%s reached a federated search for a persona that cannot run it", r.Operation.ID)
		}
	}
}

func TestSearchOperationsOnAnEmptyQueryOrNoVisibleOperations(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", nil)
	if got := tk.SearchOperations(context.Background(), "   ", 10); got != nil {
		t.Errorf("an empty query returned %+v", got)
	}
	tk.SetRoutePolicy(denyPolicy{deny: func(string, string) bool { return true }})
	if got := tk.SearchOperations(context.Background(), "product", 10); got != nil {
		t.Errorf("a persona that can run nothing got %+v", got)
	}
}

func TestSearchOperationsUsesTheSemanticPathWhenIndexed(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", nil)
	indexed(t, tk)
	got := tk.SearchOperations(context.Background(), "purchase requisition", 3)
	if len(got) == 0 || len(got) > 3 {
		t.Fatalf("results = %d; want a bounded, non-empty intent match", len(got))
	}
}

func TestSearchOperationsAppliesADefaultLimit(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", nil)
	if got := tk.SearchOperations(context.Background(), "query", 0); len(got) == 0 {
		t.Error("a zero limit returned nothing")
	}
}
