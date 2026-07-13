package gen

import "time"

// Ground-truth computations for the three phase-2 trap classes (#943):
// fiscal_calendar, freshness_cutoff, and tier_boundary. Each mirrors the
// reference SQL recorded on its task exactly, and the generator asserts the
// naive (plausible-but-wrong) reading differs from the knowledge-informed
// reading so the trap actually traps.

// fiscalYearStartMonth is the month the company fiscal year begins. Fiscal year
// N runs from this month of calendar year N through the month before it in
// N+1. The fact lives ONLY in the fiscal-calendar knowledge page (not in any
// schema, column, or dataset description), so it separates the knowledge arm
// (A2/A3, which can find the page via search) from the enrichment arm (A1,
// which cannot: enrichment surfaces column/dataset context, not policy pages).
const fiscalYearStartMonth = time.February

// freshnessCutoff is the last day the pre-aggregated daily_region_revenue index
// covers. The seed emits index rows only through this date; orders after it
// exist in the raw fact but not the index. The cutoff lives in the index's
// dataset description (enrichment-visible, so A1 gets it) and the warehouse
// knowledge page, so a naive index-only answer for a post-cutoff period
// under-reports while the raw-orders answer is correct.
var freshnessCutoff = time.Date(2025, 11, 30, 23, 59, 59, 0, time.UTC)

// keyAccountTiers is the policy definition of a "key account": any customer on
// the plus or enterprise tier. The fact lives ONLY in the tier-definitions
// knowledge page, so a naive reading that equates "key account" with the single
// top tier (enterprise) under-counts. Ordered for deterministic SQL emission.
var keyAccountTiers = []string{"plus", "enterprise"}

// fiscalYear2025 returns the fiscal-2025 range [Feb 1 2025, Feb 1 2026).
func fiscalYear2025() (time.Time, time.Time) {
	from := time.Date(2025, fiscalYearStartMonth, 1, 0, 0, 0, 0, time.UTC)
	return from, from.AddDate(1, 0, 0)
}

// FiscalYear2025NetUSD is the fiscal-calendar truth: policy net revenue
// (amount - discount, completed only) over fiscal year 2025.
func (d *Dataset) FiscalYear2025NetUSD() float64 {
	from, to := fiscalYear2025()
	var cents int64
	for _, c := range d.netCentsByRegion(from, to) {
		cents += c
	}
	return centsToUSD(cents)
}

// january2025NetCents is the difference between the calendar-2025 and
// fiscal-2025 net readings (fiscal excludes January 2025, the orders span ends
// in December so fiscal adds no January 2026). The generator asserts it is
// positive so the fiscal and calendar answers differ.
func (d *Dataset) january2025NetCents() int64 {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	var cents int64
	for _, c := range d.netCentsByRegion(from, to) {
		cents += c
	}
	return cents
}

// decemberGrossCents sums gross amount over completed December 2025 orders,
// mirroring the daily_region_revenue index definition (completed, gross of
// discounts) for the month the freshness cutoff excludes.
func (d *Dataset) decemberGrossCents() int64 {
	from := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var cents int64
	for _, o := range d.Orders {
		if o.Status == "completed" && inRange(o.TS, from, to) {
			cents += o.Amount
		}
	}
	return cents
}

// DecemberGrossUSD is the freshness-cutoff truth: December 2025 gross revenue
// (completed orders) in USD, computed from the raw fact. The index cannot
// answer it because its rows stop at the cutoff.
func (d *Dataset) DecemberGrossUSD() float64 {
	return centsToUSD(d.decemberGrossCents())
}

// keyAccountCustomerIDs is the set of customers on a key-account tier.
func (d *Dataset) keyAccountCustomerIDs() map[int]bool {
	tierSet := map[string]bool{}
	for _, t := range keyAccountTiers {
		tierSet[t] = true
	}
	ids := map[int]bool{}
	for _, c := range d.Customers {
		if tierSet[c.Tier] {
			ids[c.ID] = true
		}
	}
	return ids
}

// KeyAccountCount is the tier-boundary truth: the number of customers on a key
// account tier (plus or enterprise).
func (d *Dataset) KeyAccountCount() int {
	return len(d.keyAccountCustomerIDs())
}

// enterpriseCustomerCount is the naive reading the tier trap is built against:
// only the single top tier.
func (d *Dataset) enterpriseCustomerCount() int {
	n := 0
	for _, c := range d.Customers {
		if c.Tier == "enterprise" {
			n++
		}
	}
	return n
}

// KeyAccountOrderCount is a second tier-boundary truth: the number of orders
// placed by key-account customers (join).
func (d *Dataset) KeyAccountOrderCount() int {
	ids := d.keyAccountCustomerIDs()
	n := 0
	for _, o := range d.Orders {
		if ids[o.CustomerID] {
			n++
		}
	}
	return n
}
