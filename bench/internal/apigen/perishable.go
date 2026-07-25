package apigen

import "fmt"

// The perishable-knowledge study's catalog (#1054). One surface carries
// all three volatility classes over a single credential:
//
//	perishable  monitors are provisioned or not, and how many
//	durable     the granularity parameter is silently ignored, until a
//	            vendor release honors it
//	eternal     daily unique counts never sum to a period unique
//
// The gold customers-and-orders surface and the tier-0 near-miss
// distractor pack come along unchanged: base tasks have to be real work
// (pagination, chained lookups, aggregate reasoning), and discovery
// difficulty is held constant at its easy setting because it is a
// controlled variable here, not a manipulated one.

// insightsTag is the OpenAPI tag and path root of the study surface.
const insightsTag = "insights"

// Verification classes. The study's primary dependent variable is the
// agent's decision to spend a verification action rather than trust the
// stored belief, so operations are classified by what calling one means,
// not by what its result happens to reveal. The line is whether the call's
// own question is about the perishable state or presupposes it.
const (
	// VerifyDirect operations ask about the state: whether any monitors
	// exist, or whether a particular one does. Calling one is a check of
	// the belief. The primary measure is defined over exactly this set.
	VerifyDirect = "direct"
	// VerifyIncidental operations presuppose the state and ask something
	// conditioned on it. Their outcome does reveal the state (a trend
	// call against an unprovisioned monitor 404s), but an agent that
	// trusts a stale "monitors exist" belief reaches that 404 by relying
	// on the belief, not by testing it; scoring it as verification would
	// count a truster as a verifier. Carried as a pre-registered
	// sensitivity analysis, never folded into the primary measure.
	VerifyIncidental = "incidental"
)

// verificationClasses classifies every operation whose outcome depends on
// the perishable state. Operations outside the listening area (the
// corroboration surface, the gold surface) are not in the map: they do not
// depend on the state at all.
var verificationClasses = map[string]string{
	"list_monitors":      VerifyDirect,
	"get_monitor":        VerifyDirect,
	"list_monitor_trend": VerifyIncidental,
}

// VerificationOps returns the operation ids in one verification class, in
// catalog order. Exported as data so the primary measure and its
// sensitivity analysis are read from one committed classification rather
// than reimplemented per analysis.
func VerificationOps(class string) []string {
	var out []string
	for _, op := range insightsOperations() {
		if verificationClasses[op.ID] == class {
			out = append(out, op.ID)
		}
	}
	return out
}

// monitorFields is the monitor object schema.
func monitorFields() []Field {
	return []Field{
		{Name: "id", Type: "integer", Desc: "Unique monitor identifier"},
		{Name: "name", Type: "string", Desc: "Monitor display name"},
		{Name: "keywords", Type: "string", Desc: "Comma-separated terms the monitor tracks"},
		{Name: "workspace_id", Type: "integer", Desc: "Identifier of the workspace the monitor belongs to"},
		{Name: "created_at", Type: "date-time", Desc: "Monitor creation timestamp, UTC"},
	}
}

// windowParams are the required reporting-window parameters every series
// operation takes.
func windowParams() []Param {
	return []Param{
		{Name: "start_date", In: "query", Type: "string", Desc: "First day of the reporting window, YYYY-MM-DD", Required: true},
		{Name: "end_date", In: "query", Type: "string", Desc: "Last day of the reporting window, inclusive, YYYY-MM-DD", Required: true},
	}
}

// insightsOperations builds the study surface.
func insightsOperations() []Operation {
	ops := insightsListeningOperations()
	return append(ops, insightsProfileOperations()...)
}

// insightsListeningOperations builds the listening area: the perishable
// monitor collection and the trend series that depends on it. Every
// operation here documents a 403, because the whole area is entitled
// separately, and every one of them serves an empty collection rather than
// an error when nothing is provisioned.
func insightsListeningOperations() []Operation {
	return []Operation{
		{
			ID: "list_workspaces", Kind: KindList, Method: "get", Path: "/insights/workspaces",
			Summary: "List the account's workspaces.",
			Tag:     insightsTag, Gold: true,
			Params: pagingParams(),
			Response: []Field{
				{Name: "id", Type: "integer", Desc: "Unique workspace identifier"},
				{Name: "name", Type: "string", Desc: "Workspace display name"},
				{Name: "created_at", Type: "date-time", Desc: "Workspace creation timestamp, UTC"},
			},
		},
		{
			ID: "list_monitors", Kind: KindList, Method: "get", Path: "/insights/monitors",
			Summary: "List the listening monitors provisioned on the account.",
			Tag:     insightsTag, Gold: true, Forbidden: true,
			Params: append([]Param{
				{Name: "workspace_id", In: "query", Type: "integer", Desc: "Restrict results to one workspace. Required on accounts with workspace scoping enabled."},
			}, pagingParams()...),
			Response: monitorFields(),
		},
		{
			ID: "get_monitor", Kind: KindGet, Method: "get", Path: "/insights/monitors/{id}",
			Summary: "Fetch one listening monitor by id.",
			Tag:     insightsTag, Gold: true, Forbidden: true,
			Params:   []Param{idPathParam("Monitor identifier")},
			Response: monitorFields(),
		},
		{
			ID: "list_monitor_trend", Kind: KindList, Method: "get", Path: "/insights/monitors/{id}/trend",
			Summary: "List one monitor's daily volume and sentiment over a reporting window.",
			Tag:     insightsTag, Gold: true, Forbidden: true,
			Params: append(append([]Param{idPathParam("Monitor identifier")}, windowParams()...), pagingParams()...),
			Response: []Field{
				{Name: "date", Type: "string", Desc: "Day of the series, YYYY-MM-DD"},
				{Name: "volume", Type: "integer", Desc: "Matching mentions on the day"},
				{Name: "sentiment_score", Type: "integer", Desc: "Net sentiment on the day, 0 (negative) to 100 (positive)"},
			},
		},
	}
}

// insightsProfileOperations builds the owned-profile area: the
// corroboration surface, and the metrics that carry the durable-contract
// and eternal-invariant behaviors. It is entitled separately from
// listening and is populated in every world.
func insightsProfileOperations() []Operation {
	metricFields := []Field{
		{Name: "date", Type: "string", Desc: "First day of the bucket, YYYY-MM-DD"},
		{Name: "impressions", Type: "integer", Desc: "Times content was displayed in the bucket"},
		{Name: "engagements", Type: "integer", Desc: "Interactions with content in the bucket"},
		{Name: "unique_reach", Type: "integer", Desc: "Distinct accounts reached within the bucket"},
	}
	return []Operation{
		{
			ID: "list_profiles", Kind: KindList, Method: "get", Path: "/insights/profiles",
			Summary: "List the owned social profiles connected to the account.",
			Tag:     insightsTag, Gold: true,
			Params: pagingParams(),
			Response: []Field{
				{Name: "id", Type: "integer", Desc: "Unique profile identifier"},
				{Name: "name", Type: "string", Desc: "Profile display name"},
				{Name: "network", Type: "string", Desc: "Social network the profile belongs to"},
				{Name: "created_at", Type: "date-time", Desc: "Profile connection timestamp, UTC"},
			},
		},
		{
			ID: "list_profile_metrics", Kind: KindList, Method: "get", Path: "/insights/profiles/{id}/metrics",
			Summary: "List one owned profile's engagement metrics over a reporting window.",
			Tag:     insightsTag, Gold: true,
			Params: append(append([]Param{idPathParam("Profile identifier")}, windowParams()...),
				append([]Param{
					{Name: "granularity", In: "query", Type: "string", Desc: "Bucket size for the returned series: day (default) or week"},
				}, pagingParams()...)...),
			Response: metricFields,
		},
		{
			ID: "aggregate_profile_metrics", Kind: KindAggregate, Method: "get", Path: "/insights/profiles/{id}/metrics:aggregate",
			Summary: "Aggregate one owned profile's metrics over a reporting window.",
			Tag:     insightsTag, Gold: true,
			Params: append([]Param{idPathParam("Profile identifier")}, windowParams()...),
			Response: []Field{
				{Name: "key", Type: "string", Desc: "Metric name: impressions, engagements, or unique_reach"},
				{Name: "value", Type: "integer", Desc: "Metric value over the whole window"},
			},
		},
	}
}

// PerishableSpecName is the committed spec artifact's base name.
const PerishableSpecName = "pk"

// BuildPerishableCatalog constructs the #1054 study catalog. It is pure
// and deterministic, like BuildCatalog, and deliberately separate from it:
// the #1027 catalog and its committed specs are frozen so that study's
// archived runs stay reproducible.
func BuildPerishableCatalog() *Catalog {
	resources := distractorResources()[:tierResourceCuts[Tier0]]
	c := &Catalog{Resources: resources}
	c.Operations = append(c.Operations, goldOperations()...)
	c.Operations = append(c.Operations, deprecatedOrdersV1())
	c.Operations = append(c.Operations, insightsOperations()...)
	for _, r := range resources {
		c.Operations = append(c.Operations, resourceOperations(r, Tier0)...)
	}
	c.assertPerishableInvariants()
	return c
}

// assertPerishableInvariants panics on study-catalog construction bugs.
// The catalog is built from constant data, so a violation is a code defect
// its own tests catch, never a runtime roll of the dice.
func (c *Catalog) assertPerishableInvariants() {
	wantOps := len(goldOperations()) + 1 + len(insightsOperations()) + tierResourceCuts[Tier0]*opsPerResource
	if len(c.Operations) != wantOps {
		panic(fmt.Sprintf("apigen: perishable catalog has %d operations, want %d", len(c.Operations), wantOps))
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
		if op.Tier != Tier0 {
			panic("apigen: perishable catalog operation " + op.ID + " is not tier 0")
		}
	}
	for id := range verificationClasses {
		if !seenOp[id] {
			panic("apigen: classified operation " + id + " missing from the catalog")
		}
	}
	for _, class := range []string{VerifyDirect, VerifyIncidental} {
		if len(VerificationOps(class)) == 0 {
			panic("apigen: no operation classified " + class)
		}
	}
}
