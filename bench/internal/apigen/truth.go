package apigen

import (
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/gen"
)

// Ground-truth computation over the gold state. Every exemplar (a customer
// or order a task names) is picked deterministically from the dataset, and
// every pick asserts the property that makes the task well-posed (a unique
// name, a unique maximum, a pending status) so a seed change that breaks a
// task fails generation instead of producing an ambiguous prompt.

// truths bundles the dataset with the derived exemplars tasks reference.
type truths struct {
	ds *gen.Dataset
	// uniqueNamed are customers whose full name occurs exactly once,
	// ordered by id. Tasks that look a customer up by name draw from
	// this list.
	uniqueNamed []gen.Customer
	// pending are pending orders ordered by id.
	pending []gen.Order
}

// newTruths derives the exemplar sets.
func newTruths(ds *gen.Dataset) *truths {
	t := &truths{ds: ds}
	nameCount := map[string]int{}
	for _, c := range ds.Customers {
		nameCount[c.Name]++
	}
	for _, c := range ds.Customers {
		if nameCount[c.Name] == 1 {
			t.uniqueNamed = append(t.uniqueNamed, c)
		}
	}
	for _, o := range ds.Orders {
		if o.Status == "pending" {
			t.pending = append(t.pending, o)
		}
	}
	if len(t.uniqueNamed) < 8 {
		panic(fmt.Sprintf("apigen: only %d uniquely named customers, need 8", len(t.uniqueNamed)))
	}
	if len(t.pending) < 8 {
		panic(fmt.Sprintf("apigen: only %d pending orders, need 8", len(t.pending)))
	}
	return t
}

// customer returns the customer with the given id.
func (t *truths) customer(id int) gen.Customer {
	for _, c := range t.ds.Customers {
		if c.ID == id {
			return c
		}
	}
	panic(fmt.Sprintf("apigen: no customer %d", id))
}

// order returns the order with the given id.
func (t *truths) order(id int) gen.Order {
	for _, o := range t.ds.Orders {
		if o.ID == id {
			return o
		}
	}
	panic(fmt.Sprintf("apigen: no order %d", id))
}

// countOrders counts orders matching the predicate.
func (t *truths) countOrders(pred func(gen.Order) bool) int {
	n := 0
	for _, o := range t.ds.Orders {
		if pred(o) {
			n++
		}
	}
	return n
}

// countCustomers counts customers matching the predicate.
func (t *truths) countCustomers(pred func(gen.Customer) bool) int {
	n := 0
	for _, c := range t.ds.Customers {
		if pred(c) {
			n++
		}
	}
	return n
}

// sumOrderAmounts sums the amount of orders matching the predicate, in
// cents.
func (t *truths) sumOrderAmounts(pred func(gen.Order) bool) int64 {
	var sum int64
	for _, o := range t.ds.Orders {
		if pred(o) {
			sum += o.Amount
		}
	}
	return sum
}

// largestOrder returns the order with the strictly largest amount among
// those matching the predicate; panics on a tie or empty match, which
// would make the referencing task ambiguous.
func (t *truths) largestOrder(pred func(gen.Order) bool) gen.Order {
	var best gen.Order
	found, tied := false, false
	for _, o := range t.ds.Orders {
		if !pred(o) {
			continue
		}
		switch {
		case !found || o.Amount > best.Amount:
			best, found, tied = o, true, false
		case o.Amount == best.Amount:
			tied = true
		}
	}
	if !found || tied {
		panic("apigen: largestOrder is empty or tied")
	}
	return best
}

// latestOrder returns the most recently placed order matching the
// predicate; panics on a tie or empty match.
func (t *truths) latestOrder(pred func(gen.Order) bool) gen.Order {
	var best gen.Order
	found, tied := false, false
	for _, o := range t.ds.Orders {
		if !pred(o) {
			continue
		}
		switch {
		case !found || o.TS.After(best.TS):
			best, found, tied = o, true, false
		case o.TS.Equal(best.TS):
			tied = true
		}
	}
	if !found || tied {
		panic("apigen: latestOrder is empty or tied")
	}
	return best
}

// customerWithOnePending returns a customer with exactly one pending
// order, plus that order. Deterministic: lowest customer id wins.
func (t *truths) customerWithOnePending() (gen.Customer, gen.Order) {
	byCustomer := map[int][]gen.Order{}
	for _, o := range t.pending {
		byCustomer[o.CustomerID] = append(byCustomer[o.CustomerID], o)
	}
	for _, c := range t.ds.Customers {
		if orders := byCustomer[c.ID]; len(orders) == 1 {
			return c, orders[0]
		}
	}
	panic("apigen: no customer with exactly one pending order")
}

// customerNotIn returns the first customer (by dataset order) whose
// region and tier differ from the given values, so a task that moves
// them there is a real state change. An empty value matches any
// customer on that dimension.
func (t *truths) customerNotIn(region, tier string) gen.Customer {
	for _, c := range t.ds.Customers {
		if c.Region != region && c.Tier != tier {
			return c
		}
	}
	panic(fmt.Sprintf("apigen: every customer is already in %s/%s", region, tier))
}

// month returns the [start, end) window of a calendar month.
func month(y int, m time.Month) (time.Time, time.Time) {
	start := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0)
}

// within reports start <= ts < end.
func within(ts, start, end time.Time) bool {
	return !ts.Before(start) && ts.Before(end)
}
