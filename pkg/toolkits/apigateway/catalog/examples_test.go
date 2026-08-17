package catalog

import (
	"context"
	"errors"
	"testing"
)

// An endpoint has no catalog entity of its own, so what a proven API call
// becomes is an example on the endpoint (#1321). The in-memory store is what a
// deployment running the gateway from file configuration alone gets.
func TestMemoryExampleStoreKeepsOnePerName(t *testing.T) {
	t.Parallel()

	store := NewMemoryExampleStore()
	ctx := context.Background()

	first, err := store.SaveExample(ctx, Example{
		Connection: "acme", OperationID: "listOrders", Name: "Listing open orders.",
		Method: "GET", Path: "/v1/orders",
	})
	if err != nil {
		t.Fatalf("SaveExample: %v", err)
	}

	// Promoting the same purpose twice refreshes the example rather than
	// accumulating near-duplicates the reader has to choose between.
	second, err := store.SaveExample(ctx, Example{
		Connection: "acme", OperationID: "listOrders", Name: "  Listing open orders.  ",
		Method: "GET", Path: "/v1/orders?status=open",
	})
	if err != nil {
		t.Fatalf("SaveExample: %v", err)
	}
	if first != second {
		t.Errorf("second save = %q, want the same example %q refreshed", second, first)
	}

	got, err := store.ListExamples(ctx, "acme", "listOrders")
	if err != nil {
		t.Fatalf("ListExamples: %v", err)
	}
	if len(got) != 1 || got[0].Path != "/v1/orders?status=open" {
		t.Fatalf("examples = %+v", got)
	}
}

func TestMemoryExampleStoreScopesByConnection(t *testing.T) {
	t.Parallel()

	store := NewMemoryExampleStore()
	ctx := context.Background()
	if _, err := store.SaveExample(ctx, Example{
		Connection: "acme", OperationID: "listOrders", Name: "Listing open orders.",
	}); err != nil {
		t.Fatalf("SaveExample: %v", err)
	}

	// A spec is shared; evidence is not. An example promoted against one
	// upstream says nothing about another.
	got, err := store.ListExamples(ctx, "acme-staging", "listOrders")
	if err != nil {
		t.Fatalf("ListExamples: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("examples = %+v, want none for another connection", got)
	}
}

func TestExampleNeedsAnEndpointToBelongTo(t *testing.T) {
	t.Parallel()

	store := NewMemoryExampleStore()
	for _, ex := range []Example{
		{OperationID: "listOrders", Name: "Listing."},
		{Connection: "acme", OperationID: "listOrders", Name: "   "},
	} {
		if _, err := store.SaveExample(context.Background(), ex); !errors.Is(err, ErrInvalidExample) {
			t.Errorf("SaveExample(%+v) = %v, want ErrInvalidExample", ex, err)
		}
	}
}

func TestMemoryExampleStoreBoundsWhatAnEndpointCarries(t *testing.T) {
	t.Parallel()

	store := NewMemoryExampleStore()
	ctx := context.Background()
	for i := range maxExamplesPerEndpoint + 3 {
		if _, err := store.SaveExample(ctx, Example{
			Connection: "acme", OperationID: "listOrders",
			Name: string(rune('a'+i)) + " purpose",
		}); err != nil {
			t.Fatalf("SaveExample: %v", err)
		}
	}

	// The examples are read into an agent's context beside the endpoint's
	// schema, which is already the largest thing that tool returns.
	got, err := store.ListExamples(ctx, "acme", "listOrders")
	if err != nil {
		t.Fatalf("ListExamples: %v", err)
	}
	if len(got) != maxExamplesPerEndpoint {
		t.Errorf("examples = %d, want the cap %d", len(got), maxExamplesPerEndpoint)
	}
}
