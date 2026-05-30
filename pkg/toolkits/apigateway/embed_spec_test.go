package apigateway

import (
	"strings"
	"testing"
)

// TestBuildOperationItems_ExtractsOperations covers the happy path:
// a two-operation spec yields one item per operation, each with a
// non-empty operation id and embed text, in stable order.
func TestBuildOperationItems_ExtractsOperations(t *testing.T) {
	t.Parallel()
	items, err := BuildOperationItems(persistedEmbedTestSpec, "default")
	if err != nil {
		t.Fatalf("BuildOperationItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d; want 2", len(items))
	}
	for _, it := range items {
		if it.OperationID == "" {
			t.Error("operation id should not be empty")
		}
		if it.Text == "" {
			t.Errorf("embed text for %q should not be empty", it.OperationID)
		}
	}
	// Stable (path, method) order: /a before /b.
	if items[0].OperationID != "alpha" || items[1].OperationID != "bravo" {
		t.Errorf("unexpected order: %q, %q", items[0].OperationID, items[1].OperationID)
	}
}

// TestBuildOperationItems_UnparseableSpecErrors covers the parse-
// failure path: malformed input surfaces a wrapped "build operation
// items" error so the caller logs the right cause.
func TestBuildOperationItems_UnparseableSpecErrors(t *testing.T) {
	t.Parallel()
	_, err := BuildOperationItems("::not yaml::", "default")
	if err == nil {
		t.Fatal("expected error on malformed spec")
	}
	if !strings.Contains(err.Error(), "build operation items") {
		t.Errorf("error should name build operation items; got %q", err)
	}
}

// TestBuildOperationItems_ZeroOperationsReturnsNil covers the
// no-operations early return: a spec that parses but has zero
// methods on any path produces no items and no error.
func TestBuildOperationItems_ZeroOperationsReturnsNil(t *testing.T) {
	t.Parallel()
	emptySpec := `openapi: 3.0.0
info: {title: t, version: "1"}
paths: {}`
	items, err := BuildOperationItems(emptySpec, "default")
	if err != nil {
		t.Fatalf("zero-op spec should not error; got %v", err)
	}
	if items != nil {
		t.Errorf("zero-op spec should return nil; got %d items", len(items))
	}
}

// TestBuildOperationItems_SynthesizesIDForMissingOperationID covers
// the path that synthesizes "METHOD path" when a spec operation has
// no explicit operationId, with an empty base path (so a per-spec
// base_path override does not invalidate the stored vectors).
func TestBuildOperationItems_SynthesizesIDForMissingOperationID(t *testing.T) {
	t.Parallel()
	items, err := BuildOperationItems(noOperationIDSpec, "default")
	if err != nil {
		t.Fatalf("BuildOperationItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d; want 1", len(items))
	}
	if !strings.Contains(items[0].OperationID, "/widgets") {
		t.Errorf("synthesized id should contain the path; got %q", items[0].OperationID)
	}
	if strings.Contains(items[0].OperationID, "/v2") {
		t.Errorf("synthesized id should use empty base path, not an override; got %q", items[0].OperationID)
	}
}
