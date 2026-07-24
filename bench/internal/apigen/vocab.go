package apigen

import (
	"slices"
	"strings"
)

// Distractor vocabulary. Family and resource names are realistic business
// systems so retrieval faces plausible competition, and the near-miss pack
// puts deliberate semantic neighbors of the gold surface (purchase-orders,
// order-templates, archived-orders, invoices, leads, contacts) into every
// tier including the smallest.

// nearMissKeys are the distractor resources seeded as semantic neighbors
// of the gold customers/orders surface. They occupy the first tier-0
// slots, so distractor proximity is constant across tiers and volume is
// the only scaling variable.
var nearMissKeys = []string{
	"procurement/purchase-orders",
	"commerce/order-templates",
	"commerce/archived-orders",
	"billing/invoices",
	"crm/leads",
	"crm/contacts",
}

// family groups a name with its resource nouns and its two
// family-flavored row fields.
type family struct {
	name      string
	resources []string
	extras    [2]Field
}

// families is the full distractor vocabulary in deterministic order.
// Resource keys (family/plural) are unique; the same noun may recur under
// different families, as it does in real API estates.
var families = []family{
	{"crm", []string{"leads", "contacts", "segments", "pipelines", "referrals", "touchpoints", "opportunities", "personas", "mailing-lists", "call-logs", "meetings", "activity-notes"},
		[2]Field{{Name: "owner", Type: "string", Desc: "Assigned account owner"}, {Name: "source", Type: "string", Desc: "Acquisition source"}}},
	{"commerce", []string{"order-templates", "archived-orders", "carts", "promotions", "coupons", "storefronts", "checkouts", "wishlists", "gift-cards", "price-lists", "bundles", "fulfillments"},
		[2]Field{{Name: "channel", Type: "string", Desc: "Sales channel"}, {Name: "currency", Type: "string", Desc: "ISO 4217 currency code"}}},
	{"procurement", []string{"purchase-orders", "suppliers", "requisitions", "quotes", "approvals", "receipts", "tenders", "bid-requests", "vendor-ratings", "purchase-returns", "delivery-schedules", "sourcing-events"},
		[2]Field{{Name: "buyer", Type: "string", Desc: "Responsible buyer"}, {Name: "cost_center", Type: "string", Desc: "Charged cost center"}}},
	{"billing", []string{"invoices", "payments", "credit-notes", "statements", "disputes", "payment-methods", "billing-accounts", "charges", "adjustments", "tax-rates", "dunning-notices", "ledgers"},
		[2]Field{{Name: "amount_due", Type: "integer", Desc: "Outstanding amount in " + centsDesc}, {Name: "due_date", Type: "date-time", Desc: "Payment due date, UTC"}}},
	{"inventory", []string{"items", "stock-levels", "reservations", "adjustments", "transfers", "cycle-counts", "bins", "lots", "serials", "replenishments", "backorders", "kits"},
		[2]Field{{Name: "sku", Type: "string", Desc: "Stock keeping unit"}, {Name: "quantity", Type: "integer", Desc: "Unit count on hand"}}},
	{"shipping", []string{"shipments", "carriers", "tracking-events", "labels", "manifests", "rates", "zones", "pickups", "deliveries", "returns-labels", "customs-forms", "freight-quotes"},
		[2]Field{{Name: "tracking_number", Type: "string", Desc: "Carrier tracking number"}, {Name: "carrier_code", Type: "string", Desc: "Carrier identifier"}}},
	{"marketing", []string{"campaigns", "audiences", "creatives", "ab-tests", "landing-pages", "email-templates", "social-posts", "utm-links", "newsletters", "sponsorships", "surveys", "banners"},
		[2]Field{{Name: "budget", Type: "integer", Desc: "Allocated budget in " + centsDesc}, {Name: "audience_size", Type: "integer", Desc: "Targeted audience count"}}},
	{"support", []string{"tickets", "agents", "queues", "macros", "satisfaction-surveys", "escalations", "knowledge-articles", "chat-sessions", "callbacks", "sla-policies", "canned-responses", "incident-reports"},
		[2]Field{{Name: "priority", Type: "string", Desc: "Priority level"}, {Name: "assignee", Type: "string", Desc: "Assigned agent"}}},
	{"hr", []string{"employees", "departments", "positions", "candidates", "interviews", "onboardings", "leave-requests", "performance-reviews", "benefits", "timesheets", "org-units", "certifications"},
		[2]Field{{Name: "manager", Type: "string", Desc: "Reporting manager"}, {Name: "location", Type: "string", Desc: "Office location"}}},
	{"payroll", []string{"pay-runs", "payslips", "deductions", "bonuses", "reimbursements", "tax-withholdings", "direct-deposits", "garnishments", "pay-schedules", "overtime-entries", "stipends", "year-end-forms"},
		[2]Field{{Name: "gross_amount", Type: "integer", Desc: "Gross amount in " + centsDesc}, {Name: "pay_period", Type: "string", Desc: "Pay period identifier"}}},
	{"analytics", []string{"dashboards", "reports", "metrics", "funnels", "cohorts", "alerts", "exports", "data-sources", "saved-queries", "annotations", "snapshots", "goals"},
		[2]Field{{Name: "refresh_interval", Type: "integer", Desc: "Refresh interval in seconds"}, {Name: "owner_team", Type: "string", Desc: "Owning team"}}},
	{"notifications", []string{"channels", "broadcasts", "digests", "push-tokens", "preferences", "delivery-logs", "bounces", "suppression-lists", "sms-messages", "in-app-banners", "topics", "schedules"},
		[2]Field{{Name: "channel_type", Type: "string", Desc: "Delivery channel type"}, {Name: "delivered", Type: "boolean", Desc: "Whether delivery succeeded"}}},
	{"webhooks", []string{"endpoints", "deliveries", "secrets", "event-types", "retries", "dead-letters", "payload-logs", "signing-keys", "filters", "transformations", "replay-jobs", "health-checks"},
		[2]Field{{Name: "target_url", Type: "string", Desc: "Destination URL"}, {Name: "last_status_code", Type: "integer", Desc: "HTTP status of the last delivery"}}},
	{"audit", []string{"log-entries", "access-reviews", "retention-policies", "export-jobs", "anomalies", "sessions", "sign-ins", "permission-changes", "data-access-logs", "config-changes", "review-cycles", "attestations"},
		[2]Field{{Name: "actor", Type: "string", Desc: "Acting principal"}, {Name: "ip_address", Type: "string", Desc: "Source IP address"}}},
	{"catalog", []string{"products", "categories", "brands", "attributes", "variants", "media-assets", "size-charts", "collections", "reviews", "questions", "spec-sheets", "barcodes"},
		[2]Field{{Name: "visibility", Type: "string", Desc: "Storefront visibility"}, {Name: "position", Type: "integer", Desc: "Sort position"}}},
	{"pricing", []string{"price-books", "discounts", "surcharges", "currencies", "exchange-rates", "price-rules", "markdowns", "cost-models", "margin-targets", "rebates", "price-approvals", "floor-prices"},
		[2]Field{{Name: "unit_price", Type: "integer", Desc: "Unit price in " + centsDesc}, {Name: "effective_from", Type: "date-time", Desc: "Effective start, UTC"}}},
	{"returns", []string{"return-requests", "rmas", "inspections", "restocking-fees", "return-reasons", "exchanges", "credit-memos", "disposition-codes", "return-labels", "warranty-claims", "damage-reports", "return-policies"},
		[2]Field{{Name: "reason_code", Type: "string", Desc: "Return reason code"}, {Name: "resolved", Type: "boolean", Desc: "Whether the case is resolved"}}},
	{"loyalty", []string{"programs", "members", "points-ledgers", "rewards", "redemptions", "tiers", "referral-codes", "badges", "streaks", "partner-offers", "gift-points", "expirations"},
		[2]Field{{Name: "points_balance", Type: "integer", Desc: "Current points balance"}, {Name: "enrolled_at", Type: "date-time", Desc: "Enrollment timestamp, UTC"}}},
	{"fleet", []string{"vehicles", "drivers", "routes", "maintenance-logs", "fuel-entries", "trip-logs", "incidents", "registrations", "insurance-policies", "telematics-devices", "work-orders", "depots"},
		[2]Field{{Name: "vehicle_vin", Type: "string", Desc: "Vehicle identification number"}, {Name: "odometer", Type: "integer", Desc: "Odometer reading in miles"}}},
	{"warehouse", []string{"zones", "docks", "pick-lists", "put-away-tasks", "pack-stations", "cycle-tasks", "labor-shifts", "equipment", "safety-checks", "slotting-plans", "receiving-logs", "capacity-reports"},
		[2]Field{{Name: "site", Type: "string", Desc: "Warehouse site code"}, {Name: "aisle", Type: "string", Desc: "Aisle identifier"}}},
	{"contracts", []string{"agreements", "clauses", "renewals", "amendments", "signatures", "obligations", "milestones", "counterparties", "templates", "negotiations", "terminations", "redlines"},
		[2]Field{{Name: "counterparty", Type: "string", Desc: "Contracting counterparty"}, {Name: "expires_at", Type: "date-time", Desc: "Expiration timestamp, UTC"}}},
	{"compliance", []string{"policies", "controls", "assessments", "findings", "remediations", "frameworks", "evidence-items", "exceptions", "trainings", "risk-registers", "waivers", "breach-reports"},
		[2]Field{{Name: "severity", Type: "string", Desc: "Severity rating"}, {Name: "due_by", Type: "date-time", Desc: "Remediation due date, UTC"}}},
	{"projects", []string{"initiatives", "tasks", "sprints", "backlogs", "epics", "workstreams", "deliverables", "dependencies", "retrospectives", "status-reports", "resource-plans", "risk-items"},
		[2]Field{{Name: "project_code", Type: "string", Desc: "Project code"}, {Name: "percent_complete", Type: "integer", Desc: "Completion percentage"}}},
	{"assets", []string{"devices", "licenses", "leases", "depreciations", "assignments", "disposals", "warranties", "locations", "asset-tags", "valuations", "maintenance-schedules", "loaners"},
		[2]Field{{Name: "serial_number", Type: "string", Desc: "Manufacturer serial number"}, {Name: "purchase_price", Type: "integer", Desc: "Purchase price in " + centsDesc}}},
	{"vendors", []string{"onboarding-requests", "scorecards", "compliance-docs", "payment-terms", "risk-ratings", "engagements", "performance-reports", "diversity-records", "insurance-certs", "contact-points", "spend-summaries", "offboardings"},
		[2]Field{{Name: "vendor_code", Type: "string", Desc: "Vendor code"}, {Name: "rating", Type: "integer", Desc: "Composite score, 1 to 100"}}},
	{"subscriptions", []string{"plans", "subscribers", "trials", "upgrades", "downgrades", "cancellations", "usage-records", "entitlements", "add-ons", "billing-cycles", "churn-events", "pauses"},
		[2]Field{{Name: "plan_code", Type: "string", Desc: "Subscribed plan code"}, {Name: "mrr", Type: "integer", Desc: "Monthly recurring revenue in " + centsDesc}}},
	{"events", []string{"venues", "sessions", "registrations", "attendees", "speakers", "sponsors", "booths", "agendas", "recordings", "feedback-forms", "waitlists", "badge-prints"},
		[2]Field{{Name: "capacity", Type: "integer", Desc: "Maximum attendance"}, {Name: "starts_at", Type: "date-time", Desc: "Start timestamp, UTC"}}},
	{"documents", []string{"folders", "files", "versions", "shares", "comments", "doc-templates", "retention-rules", "watermarks", "redactions", "access-grants", "scan-jobs", "archives"},
		[2]Field{{Name: "mime_type", Type: "string", Desc: "Detected media type"}, {Name: "size_bytes", Type: "integer", Desc: "Content size in bytes"}}},
	{"training", []string{"courses", "modules", "enrollments", "quizzes", "instructors", "completion-records", "learning-paths", "workshops", "skill-matrices", "syllabi", "credits", "practice-labs"},
		[2]Field{{Name: "duration_minutes", Type: "integer", Desc: "Duration in minutes"}, {Name: "passing_score", Type: "integer", Desc: "Minimum passing score"}}},
	{"facilities", []string{"buildings", "floors", "rooms", "desks", "bookings", "visitors", "access-cards", "work-requests", "janitorial-logs", "energy-readings", "floor-plans", "parking-permits"},
		[2]Field{{Name: "building_code", Type: "string", Desc: "Building code"}, {Name: "floor_number", Type: "integer", Desc: "Floor number"}}},
}

// distractorResources orders the distractor pool: the near-miss pack
// first (tier 0 by position), then the remaining vocabulary row by row
// across families so every tier draws from many families. The pool is
// truncated to the tier-2 cut.
func distractorResources() []Resource {
	nearMiss := map[string]bool{}
	for _, k := range nearMissKeys {
		nearMiss[k] = true
	}
	out := make([]Resource, 0, tierResourceCuts[Tier2])
	for _, key := range nearMissKeys {
		out = append(out, resourceByKey(key))
	}
	for round := 0; round < maxResourcesPerFamily(); round++ {
		for _, f := range families {
			if round >= len(f.resources) {
				continue
			}
			r := Resource{Family: f.name, Plural: f.resources[round], Fields: f.extras[:]}
			if nearMiss[r.Key()] || len(out) >= tierResourceCuts[Tier2] {
				continue
			}
			out = append(out, r)
		}
	}
	return out
}

// resourceByKey resolves a family/plural key against the vocabulary.
func resourceByKey(key string) Resource {
	fam, plural, _ := strings.Cut(key, "/")
	for _, f := range families {
		if f.name == fam && slices.Contains(f.resources, plural) {
			return Resource{Family: fam, Plural: plural, Fields: f.extras[:]}
		}
	}
	panic("apigen: near-miss key not in vocabulary: " + key)
}

// maxResourcesPerFamily returns the longest family resource list.
func maxResourcesPerFamily() int {
	m := 0
	for _, f := range families {
		m = max(m, len(f.resources))
	}
	return m
}

// baseDistractorFields is the shared distractor row schema; family extras
// are appended.
func baseDistractorFields() []Field {
	return []Field{
		{Name: "id", Type: "integer", Desc: "Unique record identifier"},
		{Name: "name", Type: "string", Desc: "Display name"},
		{Name: "status", Type: "string", Desc: "Lifecycle status: active, archived, or draft"},
		{Name: "created_at", Type: "date-time", Desc: "Creation timestamp, UTC"},
	}
}

// opID builds a catalog-unique operation id from kind, family, and plural.
func opID(kind, fam, plural string) string {
	return kind + "_" + fam + "_" + strings.ReplaceAll(plural, "-", "_")
}

// resourceOperations builds the standard seven-operation set for one
// distractor resource. Update opsPerResource when the set changes.
func resourceOperations(r Resource, tier int) []Operation {
	fields := append(baseDistractorFields(), r.Fields...)
	writable := fields[1:] // id is server-assigned
	base := "/" + r.Family + "/" + r.Plural
	label := r.Family + " " + strings.ReplaceAll(r.Plural, "-", " ")
	listParams := append([]Param{
		{Name: "status", In: "query", Type: "string", Desc: "Filter by lifecycle status: active, archived, or draft"},
		{Name: "created_after", In: "query", Type: "string", Desc: "Only records created at or after this ISO 8601 timestamp"},
	}, pagingParams()...)
	return []Operation{
		{ID: opID(KindList, r.Family, r.Plural), Kind: KindList, Method: "get", Path: base,
			Summary: "List " + label + " with optional filters and cursor pagination.",
			Tag:     r.Family, Tier: tier, Resource: r.Key(), Params: listParams, Response: fields},
		{ID: opID(KindCreate, r.Family, r.Plural), Kind: KindCreate, Method: "post", Path: base,
			Summary: "Create a new record in " + label + ".",
			Tag:     r.Family, Tier: tier, Resource: r.Key(), Request: writable, Response: fields},
		{ID: opID(KindGet, r.Family, r.Plural), Kind: KindGet, Method: "get", Path: base + "/{id}",
			Summary: "Fetch one " + label + " record by id.",
			Tag:     r.Family, Tier: tier, Resource: r.Key(), Params: []Param{idPathParam("Record identifier")}, Response: fields},
		{ID: opID(KindUpdate, r.Family, r.Plural), Kind: KindUpdate, Method: "patch", Path: base + "/{id}",
			Summary: "Update an existing " + label + " record.",
			Tag:     r.Family, Tier: tier, Resource: r.Key(), Params: []Param{idPathParam("Record identifier")}, Request: writable, Response: fields},
		{ID: opID(KindDelete, r.Family, r.Plural), Kind: KindDelete, Method: "delete", Path: base + "/{id}",
			Summary: "Delete a " + label + " record by id.",
			Tag:     r.Family, Tier: tier, Resource: r.Key(), Params: []Param{idPathParam("Record identifier")}},
		{ID: opID(KindSearch, r.Family, r.Plural), Kind: KindSearch, Method: "post", Path: base + ":search",
			Summary: "Search " + label + " with a free-text query.",
			Tag:     r.Family, Tier: tier, Resource: r.Key(),
			Request:  []Field{{Name: "query", Type: "string", Desc: "Free-text search expression"}},
			Response: fields},
		{ID: opID(KindAggregate, r.Family, r.Plural), Kind: KindAggregate, Method: "get", Path: base + ":aggregate",
			Summary: "Count " + label + " records grouped by a dimension.",
			Tag:     r.Family, Tier: tier, Resource: r.Key(),
			Params: []Param{{Name: "group_by", In: "query", Type: "string", Desc: "Dimension to group by: status", Required: true}},
			Response: []Field{
				{Name: "key", Type: "string", Desc: "Group value"},
				{Name: "count", Type: "integer", Desc: "Number of records in the group"},
			}},
	}
}
