package apisvc

// Error-contract coverage: every validation branch an agent can trip
// (and the failure-taxonomy classifier keys on) responds with the
// documented status.

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"
)

func TestValidationErrorContract(t *testing.T) {
	ts, ds := newTestServer(t)
	orderID := ds.Orders[0].ID
	cases := []struct {
		name   string
		method string
		path   string
		body   map[string]any
		want   int
	}{
		{"update customer empty body", http.MethodPatch, "/crm/customers/1", map[string]any{}, http.StatusBadRequest},
		{"update customer bad region", http.MethodPatch, "/crm/customers/1", map[string]any{"region": "Central"}, http.StatusBadRequest},
		{"update customer bad tier", http.MethodPatch, "/crm/customers/1", map[string]any{"tier": "platinum"}, http.StatusBadRequest},
		{"update unknown customer", http.MethodPatch, "/crm/customers/9999", map[string]any{"region": "North"}, http.StatusNotFound},
		{"create order no body fields", http.MethodPost, "/commerce/orders", map[string]any{}, http.StatusBadRequest},
		{"create order zero amount", http.MethodPost, "/commerce/orders", map[string]any{"customer_id": 1, "amount": 0}, http.StatusBadRequest},
		{"create order unknown customer", http.MethodPost, "/commerce/orders", map[string]any{"customer_id": 9999, "amount": 100}, http.StatusBadRequest},
		{"cancel unknown order", http.MethodPost, "/commerce/orders/1:cancel", map[string]any{}, http.StatusNotFound},
		{"distractor update bad status", http.MethodPatch, "/billing/invoices/1", map[string]any{"status": "bogus"}, http.StatusBadRequest},
		{"distractor update unknown id", http.MethodPatch, "/billing/invoices/9999", map[string]any{"name": "x"}, http.StatusNotFound},
		{"distractor search empty query", http.MethodPost, "/billing/invoices:search", map[string]any{}, http.StatusBadRequest},
		{"distractor aggregate bad group", http.MethodPost, "/commerce/orders", map[string]any{"customer_id": 1, "amount": -5}, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code := doJSON(t, ts, c.method, c.path, c.body, nil); code != c.want {
				t.Errorf("status %d, want %d", code, c.want)
			}
		})
	}
	gets := []struct {
		name string
		path string
		want int
	}{
		{"get order bad id", "/commerce/orders/abc", http.StatusBadRequest},
		{"get order unknown id", "/commerce/orders/999999", http.StatusNotFound},
		{"customer orders unknown customer", "/crm/customers/9999/orders", http.StatusNotFound},
		{"customer orders bad id", "/crm/customers/abc/orders", http.StatusBadRequest},
		{"bad time filter", "/commerce/orders?placed_after=notatime", http.StatusBadRequest},
		{"bad int filter", "/commerce/orders?min_amount=lots", http.StatusBadRequest},
		{"bad page size", "/commerce/orders?page_size=0", http.StatusBadRequest},
		{"order aggregate bad group", "/commerce/orders:aggregate?group_by=region", http.StatusBadRequest},
		{"order aggregate bad metric", "/commerce/orders:aggregate?group_by=status&metric=median", http.StatusBadRequest},
		{"customer aggregate bad group", "/crm/customers:aggregate?group_by=status", http.StatusBadRequest},
		{"distractor aggregate bad group", "/billing/invoices:aggregate?group_by=name", http.StatusBadRequest},
		{"distractor list bad status", "/billing/invoices?status=bogus", http.StatusBadRequest},
		{"distractor list bad time", "/billing/invoices?created_after=notatime", http.StatusBadRequest},
		{"state dump unknown collection", "/_bench/state/nope/nothing", http.StatusNotFound},
		{"state dump distractor", "/_bench/state/billing/invoices", http.StatusOK},
		{"state dump customers", "/_bench/state/customers", http.StatusOK},
		{"unknown control route", "/_bench/nope", http.StatusNotFound},
	}
	for _, c := range gets {
		t.Run(c.name, func(t *testing.T) {
			if code := getJSON(t, ts, c.path, nil); code != c.want {
				t.Errorf("status %d, want %d", code, c.want)
			}
		})
	}
	// Distractor list with a valid created_after filter narrows results.
	all := fetchAll(t, ts, "/billing/invoices", nil)
	filtered := fetchAll(t, ts, "/billing/invoices", url.Values{"created_after": {"2026-01-01T00:00:00Z"}})
	if len(filtered) >= len(all) {
		t.Errorf("created_after filter did not narrow: %d >= %d", len(filtered), len(all))
	}
	// list_customer_orders returns the customer's orders.
	orders := fetchAll(t, ts, "/crm/customers/40/orders", nil)
	want := 0
	for _, o := range ds.Orders {
		if o.CustomerID == 40 {
			want++
		}
	}
	if len(orders) != want {
		t.Errorf("customer 40 orders = %d, want %d", len(orders), want)
	}
	// getOrder happy path.
	var order Order
	if code := getJSON(t, ts, "/commerce/orders/"+strconv.Itoa(orderID), &order); code != http.StatusOK || order.ID != orderID {
		t.Errorf("get order: code %d id %d", code, order.ID)
	}
}
