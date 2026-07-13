package gen

import (
	"math"
	"time"
)

// Ground-truth computations. Each mirrors the reference SQL recorded on its
// task (expected_sql) exactly: integer-cent sums divided by 100 at the end, so
// sums are exact to the cent and only averages round.

// centsToUSD converts an exact cent sum to dollars.
func centsToUSD(cents int64) float64 {
	return float64(cents) / 100.0
}

// round2 rounds to the nearest cent, matching ROUND(x, 2).
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// customerByID indexes the customer dimension.
func (d *Dataset) customerByID() map[int]Customer {
	m := make(map[int]Customer, len(d.Customers))
	for _, c := range d.Customers {
		m[c.ID] = c
	}
	return m
}

// inRange reports ts in [from, to).
func inRange(ts, from, to time.Time) bool {
	return !ts.Before(from) && ts.Before(to)
}

// TotalAmountQ1USD is the units-trap truth: total order amount (any status)
// for Q1 2025, in USD.
func (d *Dataset) TotalAmountQ1USD() float64 {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	var cents int64
	for _, o := range d.Orders {
		if inRange(o.TS, from, to) {
			cents += o.Amount
		}
	}
	return centsToUSD(cents)
}

// AvgAmountEnterpriseUSD is the units-trap truth with a join: average order
// amount across enterprise-tier customers' orders, in USD, rounded to cents.
func (d *Dataset) AvgAmountEnterpriseUSD() float64 {
	byID := d.customerByID()
	var cents, n int64
	for _, o := range d.Orders {
		if byID[o.CustomerID].Tier == "enterprise" {
			cents += o.Amount
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return round2(float64(cents) / float64(n) / 100.0)
}

// enterpriseOrderCount supports the generator's invariant check.
func (d *Dataset) enterpriseOrderCount() int {
	byID := d.customerByID()
	n := 0
	for _, o := range d.Orders {
		if byID[o.CustomerID].Tier == "enterprise" {
			n++
		}
	}
	return n
}

// netCentsByRegion sums policy net revenue (amount - discount, completed only)
// per region over [from, to).
func (d *Dataset) netCentsByRegion(from, to time.Time) map[string]int64 {
	byID := d.customerByID()
	sums := map[string]int64{}
	for _, o := range d.Orders {
		if o.Status == "completed" && inRange(o.TS, from, to) {
			sums[byID[o.CustomerID].Region] += o.Amount - o.Discount
		}
	}
	return sums
}

// year2025 returns the calendar-2025 range.
func year2025() (time.Time, time.Time) {
	return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

// NetEastMarchUSD is the policy-trap truth: net revenue for East, March 2025.
func (d *Dataset) NetEastMarchUSD() float64 {
	from := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	return centsToUSD(d.netCentsByRegion(from, to)["East"])
}

// NetTotal2025USD is the policy-trap truth: total net revenue for 2025.
func (d *Dataset) NetTotal2025USD() float64 {
	from, to := year2025()
	var total int64
	for _, cents := range d.netCentsByRegion(from, to) {
		total += cents
	}
	return centsToUSD(total)
}

// TopRegionNet2025 is the region with the highest policy net revenue in 2025.
func (d *Dataset) TopRegionNet2025() string {
	from, to := year2025()
	return argmax(d.netCentsByRegion(from, to))
}

// topRegionGrossAll is the plausible-but-wrong reading the trap is built
// against: highest gross amount, all statuses, 2025.
func (d *Dataset) topRegionGrossAll() string {
	byID := d.customerByID()
	from, to := year2025()
	sums := map[string]int64{}
	for _, o := range d.Orders {
		if inRange(o.TS, from, to) {
			sums[byID[o.CustomerID].Region] += o.Amount
		}
	}
	return argmax(sums)
}

// losingRegions returns every region except the policy net-revenue leader —
// the top-region task's wrong-alias set (any answer naming one is incorrect).
func (d *Dataset) losingRegions() []string {
	winner := d.TopRegionNet2025()
	losers := make([]string, 0, len(regions)-1)
	for _, r := range regions {
		if r != winner {
			losers = append(losers, r)
		}
	}
	return losers
}

// argmax returns the key with the highest value, ties broken by name order so
// the result is deterministic (the generator asserts a unique leader anyway).
func argmax(sums map[string]int64) string {
	var best string
	var bestV int64
	for _, r := range regions {
		if v := sums[r]; best == "" || v > bestV {
			best, bestV = r, v
		}
	}
	return best
}
