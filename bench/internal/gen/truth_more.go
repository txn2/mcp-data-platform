package gen

import "time"

// Additional range-based ground-truth helpers shared by the expanded S3 trap
// tasks. Each mirrors the reference SQL recorded on its task: integer-cent sums
// divided by 100 at the end (exact to the cent), averages rounded to cents.

// NetUSD sums policy net revenue (amount - discount, completed only) over
// [from, to), in USD. The window is a parameter because a study that plants a
// wrong reporting window needs the figure under that window computed from the
// same rows the correct one is.
func (d *Dataset) NetUSD(from, to time.Time) float64 {
	var cents int64
	for _, c := range d.netCentsByRegion(from, to) {
		cents += c
	}
	return centsToUSD(cents)
}

// netRegionUSD sums policy net revenue for one region over [from, to), in USD.
func (d *Dataset) netRegionUSD(region string, from, to time.Time) float64 {
	return centsToUSD(d.netCentsByRegion(from, to)[region])
}

// grossCompletedUSD sums gross amount over completed orders in [from, to), in
// USD — the daily_region_revenue index's definition (used by freshness tasks
// whose correct answer must come from orders, not the stale index).
func (d *Dataset) grossCompletedUSD(from, to time.Time) float64 {
	var cents int64
	for _, o := range d.Orders {
		if o.Status == "completed" && inRange(o.TS, from, to) {
			cents += o.Amount
		}
	}
	return centsToUSD(cents)
}

// TotalAmountAllUSD sums gross amount over ALL orders (any status), all time,
// in USD — the units-trap total.
func (d *Dataset) TotalAmountAllUSD() float64 {
	var cents int64
	for _, o := range d.Orders {
		cents += o.Amount
	}
	return centsToUSD(cents)
}

// CompletedGrossUSD sums gross amount over completed orders, all time, in USD.
func (d *Dataset) CompletedGrossUSD() float64 {
	return centsToUSD(d.CompletedAmountCents())
}

// NetRegion2025USD is calendar-year-2025 policy net revenue for one region.
func (d *Dataset) NetRegion2025USD(region string) float64 {
	from, to := year2025()
	return d.netRegionUSD(region, from, to)
}

// fiscalQuarter2025 returns the [from, to) range for fiscal quarter q of fiscal
// year 2025. Q1 starts at the fiscal year start (February) and each quarter is
// three calendar months.
func fiscalQuarter2025(q int) (time.Time, time.Time) {
	yearStart, _ := fiscalYear2025()
	from := yearStart.AddDate(0, (q-1)*3, 0)
	return from, from.AddDate(0, 3, 0)
}

// FiscalQuarter2025NetUSD is policy net revenue for fiscal quarter q of FY2025.
func (d *Dataset) FiscalQuarter2025NetUSD(q int) float64 {
	from, to := fiscalQuarter2025(q)
	return d.NetUSD(from, to)
}

// FiscalYear2025Region is FY2025 policy net revenue for one region.
func (d *Dataset) FiscalYear2025Region(region string) float64 {
	from, to := fiscalYear2025()
	return d.netRegionUSD(region, from, to)
}

// FiscalYear2025CompletedCount counts completed orders in the FY2025 window (a
// fiscal-boundary question with no revenue policy involved).
func (d *Dataset) FiscalYear2025CompletedCount() int {
	return d.CompletedOrderCount(fiscalYear2025())
}

// DecemberGrossUSD, Q4GrossUSD, NovDecGrossUSD, and FullYearGrossUSD are the
// freshness-cutoff truths: gross completed revenue over windows the stale index
// cannot answer (December falls after the 2025-11-30 cutoff).

// Q4GrossUSD is Q4 2025 (Oct–Dec) gross completed revenue in USD.
func (d *Dataset) Q4GrossUSD() float64 {
	from := time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return d.grossCompletedUSD(from, to)
}

// NovDecGrossUSD is Nov–Dec 2025 gross completed revenue in USD (spans the
// index cutoff).
func (d *Dataset) NovDecGrossUSD() float64 {
	from := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return d.grossCompletedUSD(from, to)
}

// FullYearGrossUSD is calendar-2025 gross completed revenue in USD (the index
// would under-report it by the post-cutoff month).
func (d *Dataset) FullYearGrossUSD() float64 {
	from, to := year2025()
	return d.grossCompletedUSD(from, to)
}

// KeyAccountCompletedCount counts completed orders by key-account customers.
func (d *Dataset) KeyAccountCompletedCount() int {
	ids := d.keyAccountCustomerIDs()
	n := 0
	for _, o := range d.Orders {
		if o.Status == "completed" && ids[o.CustomerID] {
			n++
		}
	}
	return n
}

// KeyAccountAvgUSD is the average order amount (any status) across key-account
// customers' orders, in USD rounded to cents.
func (d *Dataset) KeyAccountAvgUSD() float64 {
	ids := d.keyAccountCustomerIDs()
	var cents, n int64
	for _, o := range d.Orders {
		if ids[o.CustomerID] {
			cents += o.Amount
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return round2(float64(cents) / float64(n) / 100.0)
}

// KeyAccountRegionCount counts key-account customers in one region.
func (d *Dataset) KeyAccountRegionCount(region string) int {
	tierSet := map[string]bool{}
	for _, t := range keyAccountTiers {
		tierSet[t] = true
	}
	n := 0
	for _, c := range d.Customers {
		if tierSet[c.Tier] && c.Region == region {
			n++
		}
	}
	return n
}
