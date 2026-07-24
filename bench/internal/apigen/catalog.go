// Package apigen is the deterministic fixture generator for the
// API-connection architecture study (issue #1027). One fixed-seed catalog
// model emits every study artifact — the OpenAPI specs at the three
// catalog-size tiers, the fixture service's routing table and seeded state,
// and the task set whose ground truths are computed from that state — so
// spec, behavior, and truth derive from a single source and cannot drift
// apart. Regeneration is byte-identical; a test diffs the committed
// artifacts against a fresh run (the bench/internal/gen pattern).
package apigen

import "fmt"

// Tier indices. Tiers are nested: every operation in tier N is present in
// tier N+1, gold operations are byte-identical in all tiers, and the only
// variable along the scaling axis is distractor volume.
const (
	Tier0 = iota // ~50 operations: gold + near-miss distractor pack
	Tier1        // ~500 operations
	Tier2        // ~2,500 operations
	tierCount
)

// tierResourceCuts is the number of distractor resources included at each
// tier. Each distractor resource contributes opsPerResource operations;
// gold operations and the deprecated near-miss are present in every tier.
var tierResourceCuts = [tierCount]int{6, 70, 356}

// opsPerResource is the standard operation set every distractor resource
// exposes: list, create, get, update, delete, search, aggregate.
const opsPerResource = 7

// TierNames returns the tier slugs in order ("t0", "t1", "t2").
func TierNames() []string {
	return []string{"t0", "t1", "t2"}
}

// Field is one object property in a request or response schema.
type Field struct {
	Name string
	Type string // "string", "integer", "boolean", or "date-time"
	Desc string
}

// Param is one operation parameter.
type Param struct {
	Name     string
	In       string // "query" or "path"
	Type     string // "string" or "integer"
	Desc     string
	Required bool
}

// Operation kinds. Kind drives both the spec emitter's response envelope
// (list responses page with a cursor, aggregates return groups, everything
// else returns a single object) and the fixture service's handler dispatch.
const (
	KindList      = "list"
	KindGet       = "get"
	KindCreate    = "create"
	KindUpdate    = "update"
	KindDelete    = "delete"
	KindSearch    = "search"
	KindAggregate = "aggregate"
	KindCancel    = "cancel"
)

// Operation is one API operation in the catalog.
type Operation struct {
	ID         string // operationId, unique across the catalog
	Kind       string // one of the Kind* constants
	Method     string // lowercase: get, post, patch, put, delete
	Path       string
	Summary    string
	Tag        string
	Tier       int  // minimum tier the operation appears in
	Gold       bool // true for the operations tasks require
	Deprecated bool
	Params     []Param
	Request    []Field // request body object properties; nil = no body
	Response   []Field // response item object properties
	// Resource is the distractor resource the operation belongs to
	// (family/plural), empty for gold and the deprecated near-miss.
	Resource string
}

// Resource is one distractor resource family member. The fixture service
// serves generated rows for every distractor resource so a called
// distractor answers coherently instead of leaking a 404.
type Resource struct {
	Family   string
	Plural   string
	Singular string
	Fields   []Field // row schema beyond the shared base fields
}

// Key returns the catalog-unique resource key "family/plural".
func (r Resource) Key() string { return r.Family + "/" + r.Plural }

// Catalog is the generated operation catalog all artifacts derive from.
type Catalog struct {
	// Operations holds every operation in deterministic order: gold,
	// then the deprecated near-miss, then distractor resources in tier
	// order.
	Operations []Operation
	// Resources holds the distractor resources in tier order (the first
	// tierResourceCuts[t] entries are tier t's distractor set).
	Resources []Resource
}

// TierOperations returns the operations visible at the given tier.
func (c *Catalog) TierOperations(tier int) []Operation {
	var out []Operation
	for _, op := range c.Operations {
		if op.Tier <= tier {
			out = append(out, op)
		}
	}
	return out
}

// GoldOperations returns the gold operations (identical in every tier).
func (c *Catalog) GoldOperations() []Operation {
	var out []Operation
	for _, op := range c.Operations {
		if op.Gold {
			out = append(out, op)
		}
	}
	return out
}

// BuildCatalog constructs the catalog. It is pure and deterministic: the
// vocabulary is constant data and tier membership is positional.
func BuildCatalog() *Catalog {
	resources := distractorResources()
	c := &Catalog{Resources: resources}
	c.Operations = append(c.Operations, goldOperations()...)
	c.Operations = append(c.Operations, deprecatedOrdersV1())
	for i, r := range resources {
		c.Operations = append(c.Operations, resourceOperations(r, tierFor(i))...)
	}
	c.assertInvariants()
	return c
}

// tierFor maps a distractor resource's position to its minimum tier.
func tierFor(index int) int {
	for t, cut := range tierResourceCuts {
		if index < cut {
			return t
		}
	}
	panic(fmt.Sprintf("apigen: resource index %d beyond tier cuts", index))
}

// assertInvariants panics on catalog construction bugs. The vocabulary is
// constant, so a violation is a code defect caught by the generator's own
// tests, never a runtime roll of the dice.
func (c *Catalog) assertInvariants() {
	if len(c.Resources) != tierResourceCuts[Tier2] {
		panic(fmt.Sprintf("apigen: %d distractor resources, want %d", len(c.Resources), tierResourceCuts[Tier2]))
	}
	wantOps := len(goldOperations()) + 1 + tierResourceCuts[Tier2]*opsPerResource
	if len(c.Operations) != wantOps {
		panic(fmt.Sprintf("apigen: %d operations, want %d", len(c.Operations), wantOps))
	}
	seenOp := map[string]bool{}
	seenRoute := map[string]bool{}
	for _, op := range c.Operations {
		if seenOp[op.ID] {
			panic("apigen: duplicate operation id " + op.ID)
		}
		seenOp[op.ID] = true
		route := op.Method + " " + op.Path
		if seenRoute[route] {
			panic("apigen: duplicate route " + route)
		}
		seenRoute[route] = true
	}
	for _, key := range nearMissKeys {
		found := false
		for i := 0; i < tierResourceCuts[Tier0]; i++ {
			if c.Resources[i].Key() == key {
				found = true
				break
			}
		}
		if !found {
			panic("apigen: near-miss resource " + key + " not in tier 0")
		}
	}
}
