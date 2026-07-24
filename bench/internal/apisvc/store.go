// Package apisvc is the fixture HTTP service for the API-connection
// architecture study (#1027). One in-memory server serves every operation
// in the full tier-2 catalog: gold customers/orders over the report-1
// dataset, and generated rows for every distractor resource so a called
// distractor answers coherently instead of leaking a 404. State is a pure
// function of the fixed seeds until mutated; the harness control plane
// under /_bench/ (excluded from the specs) resets state between attempts
// and dumps it for mutation grading.
package apisvc

import (
	"maps"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
)

// createdAtSentinel is the placed_at / created_at stamped on rows created
// during an episode. A constant (not wall clock) so post-run state dumps
// are reproducible.
const createdAtSentinel = "2026-01-01T00:00:00Z"

// Customer is the mutable service-side customer row.
type Customer struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Region    string `json:"region"`
	Tier      string `json:"tier"`
	CreatedAt string `json:"created_at"`
}

// Order is the mutable service-side order row.
type Order struct {
	ID         int    `json:"id"`
	CustomerID int    `json:"customer_id"`
	PlacedAt   string `json:"placed_at"`
	Status     string `json:"status"`
	Amount     int64  `json:"amount"`
	Discount   int64  `json:"discount"`
}

// RequestLogEntry is one access-log record for the harness's failure
// taxonomy (which endpoints were actually hit, with what outcome).
type RequestLogEntry struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Status      int    `json:"status"`
	OperationID string `json:"operation_id,omitempty"`
}

// store is the mutable fixture state. All access is mutex-guarded; the
// dataset is small enough that a single lock is not a bottleneck for a
// one-agent episode.
type store struct {
	mu          sync.Mutex
	customers   []*Customer
	orders      []*Order
	nextOrderID int
	distractors map[string][]apigen.Row
	requests    []RequestLogEntry
}

// newStore seeds a store from the generated state.
func newStore(s *apigen.State) *store {
	st := &store{}
	st.seed(s)
	return st
}

// seed (re)builds the mutable state from the immutable generated state.
func (st *store) seed(s *apigen.State) {
	st.customers = make([]*Customer, 0, len(s.Dataset.Customers))
	for _, c := range s.Dataset.Customers {
		st.customers = append(st.customers, &Customer{
			ID: c.ID, Name: c.Name, Region: c.Region, Tier: c.Tier,
			CreatedAt: c.CreatedAt.Format(time.RFC3339),
		})
	}
	st.orders = make([]*Order, 0, len(s.Dataset.Orders))
	maxID := 0
	for _, o := range s.Dataset.Orders {
		st.orders = append(st.orders, &Order{
			ID: o.ID, CustomerID: o.CustomerID, PlacedAt: o.TS.Format(time.RFC3339),
			Status: o.Status, Amount: o.Amount, Discount: o.Discount,
		})
		maxID = max(maxID, o.ID)
	}
	st.nextOrderID = maxID + 1
	st.distractors = make(map[string][]apigen.Row, len(s.Distractors))
	for key, rows := range s.Distractors {
		copied := make([]apigen.Row, 0, len(rows))
		for _, r := range rows {
			row := make(apigen.Row, len(r))
			maps.Copy(row, r)
			copied = append(copied, row)
		}
		st.distractors[key] = copied
	}
	st.requests = nil
}

// customer returns the customer with the given id, or nil. Callers hold
// the lock.
func (st *store) customer(id int) *Customer {
	for _, c := range st.customers {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// order returns the order with the given id, or nil. Callers hold the
// lock.
func (st *store) order(id int) *Order {
	for _, o := range st.orders {
		if o.ID == id {
			return o
		}
	}
	return nil
}

// logRequest appends one access-log record. Callers do NOT hold the lock.
func (st *store) logRequest(e RequestLogEntry) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.requests = append(st.requests, e)
}
