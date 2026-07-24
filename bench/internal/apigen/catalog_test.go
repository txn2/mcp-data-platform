package apigen

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestBuildCatalogDeterministic(t *testing.T) {
	a, b := BuildCatalog(), BuildCatalog()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("BuildCatalog is not deterministic")
	}
	for tier := range tierCount {
		sa, err := a.SpecJSON(tier)
		if err != nil {
			t.Fatalf("tier %d spec: %v", tier, err)
		}
		sb, err := b.SpecJSON(tier)
		if err != nil {
			t.Fatalf("tier %d spec: %v", tier, err)
		}
		if !bytes.Equal(sa, sb) {
			t.Fatalf("tier %d spec emission is not deterministic", tier)
		}
	}
}

// TestTierSizes pins the per-tier operation counts near the issue's ~50 /
// ~500 / ~2,500 targets. Exact counts are asserted so a vocabulary edit
// that silently changes the catalog shape fails loudly.
func TestTierSizes(t *testing.T) {
	c := BuildCatalog()
	want := map[int]int{Tier0: 53, Tier1: 501, Tier2: 2503}
	for tier, n := range want {
		if got := len(c.TierOperations(tier)); got != n {
			t.Errorf("tier %d has %d operations, want %d", tier, got, n)
		}
	}
}

// TestTiersAreNested asserts every lower-tier operation appears unchanged
// in every higher tier, and gold operations are identical in all tiers.
// Tier nesting is what makes distractor volume the only scaling variable.
func TestTiersAreNested(t *testing.T) {
	c := BuildCatalog()
	for tier := range tierCount - 1 {
		lower, higher := c.TierOperations(tier), c.TierOperations(tier+1)
		byID := map[string]Operation{}
		for _, op := range higher {
			byID[op.ID] = op
		}
		for _, op := range lower {
			got, ok := byID[op.ID]
			if !ok {
				t.Fatalf("tier %d operation %s missing from tier %d", tier, op.ID, tier+1)
			}
			if !reflect.DeepEqual(op, got) {
				t.Fatalf("operation %s differs between tier %d and tier %d", op.ID, tier, tier+1)
			}
		}
	}
	gold := c.GoldOperations()
	if len(gold) != 10 {
		t.Fatalf("gold operations = %d, want 10", len(gold))
	}
	for _, op := range gold {
		if op.Tier != Tier0 {
			t.Errorf("gold operation %s has tier %d, want tier 0", op.ID, op.Tier)
		}
	}
}

// TestNearMissPackInTier0 asserts the deliberate semantic neighbors of the
// gold surface are present in the smallest catalog, including the
// deprecated v1 orders route.
func TestNearMissPackInTier0(t *testing.T) {
	c := BuildCatalog()
	t0 := map[string]Operation{}
	for _, op := range c.TierOperations(Tier0) {
		t0[op.ID] = op
	}
	for _, id := range []string{
		"list_procurement_purchase_orders",
		"list_commerce_order_templates",
		"list_commerce_archived_orders",
		"list_billing_invoices",
		"list_crm_leads",
		"list_crm_contacts",
	} {
		if _, ok := t0[id]; !ok {
			t.Errorf("near-miss operation %s not in tier 0", id)
		}
	}
	v1, ok := t0["list_orders_v1"]
	if !ok {
		t.Fatal("deprecated list_orders_v1 not in tier 0")
	}
	if !v1.Deprecated {
		t.Error("list_orders_v1 not marked deprecated")
	}
}

// TestSpecsAreValidOpenAPI parses and validates every tier's emitted spec
// with kin-openapi, the same library and version the platform's
// catalog.ParseSpec uses, so a spec the generator emits is one the
// platform's admin upload accepts.
func TestSpecsAreValidOpenAPI(t *testing.T) {
	c := BuildCatalog()
	for tier := range tierCount {
		raw, err := c.SpecJSON(tier)
		if err != nil {
			t.Fatalf("tier %d spec: %v", tier, err)
		}
		loader := &openapi3.Loader{Context: context.Background(), IsExternalRefsAllowed: false}
		doc, err := loader.LoadFromData(raw)
		if err != nil {
			t.Fatalf("tier %d spec does not parse: %v", tier, err)
		}
		if err := doc.Validate(loader.Context); err != nil {
			t.Fatalf("tier %d spec does not validate: %v", tier, err)
		}
		want := len(c.TierOperations(tier))
		got := 0
		for _, item := range doc.Paths.Map() {
			got += len(item.Operations())
		}
		if got != want {
			t.Errorf("tier %d spec has %d operations, want %d", tier, got, want)
		}
	}
}

// TestOperationTextIsUnique guards retrieval measurability: operation ids
// and summaries are unique across the full catalog, so a retrieval hit on
// the gold operation is never an accident of duplicated text.
func TestOperationTextIsUnique(t *testing.T) {
	c := BuildCatalog()
	ids, summaries := map[string]bool{}, map[string]bool{}
	for _, op := range c.Operations {
		if ids[op.ID] {
			t.Errorf("duplicate operation id %s", op.ID)
		}
		ids[op.ID] = true
		if summaries[op.Summary] {
			t.Errorf("duplicate summary %q", op.Summary)
		}
		summaries[op.Summary] = true
	}
}
