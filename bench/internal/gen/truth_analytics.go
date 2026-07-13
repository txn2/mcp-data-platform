package gen

import "time"

// Analytical ground-truth computations for the S2 suite (#943): exact
// aggregates over the seeded rows, each paired on its task with the reference
// SQL that computes the same value against the warehouse. S2 measures
// query-formulation accuracy, so its questions avoid the units trap by asking
// for counts, categories, temporal filters, and (where monetary) explicitly
// stating that amounts are integer US cents.

// OrderCount is the total number of orders.
func (d *Dataset) OrderCount() int { return len(d.Orders) }

// CustomerCount is the total number of customers.
func (d *Dataset) CustomerCount() int { return len(d.Customers) }

// OrdersByStatus counts orders per status.
func (d *Dataset) OrdersByStatus() map[string]int {
	m := map[string]int{}
	for _, o := range d.Orders {
		m[o.Status]++
	}
	return m
}

// CustomersByRegion counts customers per region.
func (d *Dataset) CustomersByRegion() map[string]int {
	m := map[string]int{}
	for _, c := range d.Customers {
		m[c.Region]++
	}
	return m
}

// CustomersByTier counts customers per tier.
func (d *Dataset) CustomersByTier() map[string]int {
	m := map[string]int{}
	for _, c := range d.Customers {
		m[c.Tier]++
	}
	return m
}

// OrdersByRegion counts orders per region (orders joined to customers).
func (d *Dataset) OrdersByRegion() map[string]int {
	byID := d.customerByID()
	m := map[string]int{}
	for _, o := range d.Orders {
		m[byID[o.CustomerID].Region]++
	}
	return m
}

// OrdersByTier counts orders per tier (orders joined to customers).
func (d *Dataset) OrdersByTier() map[string]int {
	byID := d.customerByID()
	m := map[string]int{}
	for _, o := range d.Orders {
		m[byID[o.CustomerID].Tier]++
	}
	return m
}

// OrdersInMonth counts orders whose timestamp falls in the given calendar month.
func (d *Dataset) OrdersInMonth(year int, month time.Month) int {
	from := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)
	n := 0
	for _, o := range d.Orders {
		if inRange(o.TS, from, to) {
			n++
		}
	}
	return n
}

// CompletedOrdersInQuarter counts completed orders in a calendar quarter.
func (d *Dataset) CompletedOrdersInQuarter(year, quarter int) int {
	startMonth := time.Month((quarter-1)*3 + 1)
	from := time.Date(year, startMonth, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 3, 0)
	n := 0
	for _, o := range d.Orders {
		if o.Status == "completed" && inRange(o.TS, from, to) {
			n++
		}
	}
	return n
}

// RegionStatusCount counts orders for a region/status pair (cross-tab cell).
func (d *Dataset) RegionStatusCount(region, status string) int {
	byID := d.customerByID()
	n := 0
	for _, o := range d.Orders {
		if o.Status == status && byID[o.CustomerID].Region == region {
			n++
		}
	}
	return n
}

// CompletedAmountCents sums gross amount over completed orders, in cents (the
// explicit-unit monetary S2 question states the column is integer cents).
func (d *Dataset) CompletedAmountCents() int64 {
	var cents int64
	for _, o := range d.Orders {
		if o.Status == "completed" {
			cents += o.Amount
		}
	}
	return cents
}

// CustomersCreatedInYear counts customers whose account was created in a year.
func (d *Dataset) CustomersCreatedInYear(year int) int {
	from := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(1, 0, 0)
	n := 0
	for _, c := range d.Customers {
		if inRange(c.CreatedAt, from, to) {
			n++
		}
	}
	return n
}

// DistinctCustomersWithOrders counts customers that placed at least one order.
func (d *Dataset) DistinctCustomersWithOrders() int {
	seen := map[int]bool{}
	for _, o := range d.Orders {
		seen[o.CustomerID] = true
	}
	return len(seen)
}

// TopByCount returns the key with the highest count, ties broken by the ordered
// dimension (regions or tiers) so the result is deterministic.
func topByCount(counts map[string]int, order []string) string {
	best := ""
	bestV := -1
	for _, k := range order {
		if counts[k] > bestV {
			best, bestV = k, counts[k]
		}
	}
	return best
}

// TopRegionByOrderCount is the region with the most orders.
func (d *Dataset) TopRegionByOrderCount() string {
	return topByCount(d.OrdersByRegion(), regions)
}

// TopTierByOrderCount is the tier with the most orders.
func (d *Dataset) TopTierByOrderCount() string {
	return topByCount(d.OrdersByTier(), tiers)
}

// TopRegionByCustomerCount is the region with the most customers.
func (d *Dataset) TopRegionByCustomerCount() string {
	return topByCount(d.CustomersByRegion(), regions)
}

// amountThresholdCents is the filter boundary for the "large orders" S2 task
// ($1,000.00). Chosen so a non-trivial but non-majority subset qualifies.
const amountThresholdCents = 100000

// OrdersAboveThreshold counts orders whose amount is at least the threshold.
func (d *Dataset) OrdersAboveThreshold() int {
	n := 0
	for _, o := range d.Orders {
		if o.Amount >= amountThresholdCents {
			n++
		}
	}
	return n
}
