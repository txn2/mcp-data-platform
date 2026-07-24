package apisvc

import (
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Gold handlers: the ten operations tasks require, served over the
// report-1 dataset. Filter semantics mirror the parameter descriptions in
// the spec exactly ("at or after" is inclusive, "before" is exclusive);
// the ground-truth computations in internal/apigen use the same
// comparisons, so a correctly parameterized call yields the graded answer.

var (
	regionVocab = []string{"North", "South", "East", "West"}
	tierVocab   = []string{"basic", "plus", "enterprise"}
	statusVocab = []string{"pending", "completed", "refunded", "canceled"}
)

// handleGold dispatches one gold (or deprecated near-miss) operation.
func (s *Service) handleGold(w http.ResponseWriter, r *http.Request, opID, id string) {
	switch opID {
	case "list_customers":
		s.listCustomers(w, r)
	case "get_customer":
		s.getCustomer(w, id)
	case "aggregate_customers":
		s.aggregateCustomers(w, r)
	case "update_customer":
		s.updateCustomer(w, r, id)
	case "list_customer_orders":
		s.listCustomerOrders(w, r, id)
	default:
		s.handleGoldOrders(w, r, opID, id)
	}
}

// handleGoldOrders dispatches the order-side gold operations.
func (s *Service) handleGoldOrders(w http.ResponseWriter, r *http.Request, opID, id string) {
	switch opID {
	case "list_orders", "list_orders_v1":
		s.listOrders(w, r, opID == "list_orders_v1")
	case "get_order":
		s.getOrder(w, id)
	case "aggregate_orders":
		s.aggregateOrders(w, r)
	case "create_order":
		s.createOrder(w, r)
	case "cancel_order":
		s.cancelOrder(w, id)
	default:
		writeError(w, http.StatusNotFound, "unknown operation "+opID)
	}
}

// enumParam validates an optional enum query parameter.
func enumParam(r *http.Request, name string, vocab []string) (string, error) {
	v := r.URL.Query().Get(name)
	if v != "" && !slices.Contains(vocab, v) {
		return "", badParam{name + " must be one of: " + strings.Join(vocab, ", ")}
	}
	return v, nil
}

// parseID parses a path id segment.
func parseID(raw string) (int, error) {
	id, err := strconv.Atoi(raw)
	if err != nil || id < 1 {
		return 0, badParam{"id must be a positive integer"}
	}
	return id, nil
}

// customerFilters holds the parsed list_customers filters.
type customerFilters struct {
	region, tier, name string
	after, before      time.Time
}

// parseCustomerFilters parses list_customers query parameters.
func parseCustomerFilters(r *http.Request) (customerFilters, error) {
	var f customerFilters
	var err error
	if f.region, err = enumParam(r, "region", regionVocab); err != nil {
		return f, err
	}
	if f.tier, err = enumParam(r, "tier", tierVocab); err != nil {
		return f, err
	}
	f.name = r.URL.Query().Get("name")
	if f.after, err = parseTimeParam(r, "created_after"); err != nil {
		return f, err
	}
	if f.before, err = parseTimeParam(r, "created_before"); err != nil {
		return f, err
	}
	return f, nil
}

// matches reports whether a customer passes the filters.
func (f customerFilters) matches(c *Customer) bool {
	if f.region != "" && c.Region != f.region {
		return false
	}
	if f.tier != "" && c.Tier != f.tier {
		return false
	}
	if f.name != "" && c.Name != f.name {
		return false
	}
	return inWindow(c.CreatedAt, f.after, f.before)
}

// inWindow reports whether an RFC 3339 timestamp is at or after `after`
// (inclusive, when set) and before `before` (exclusive, when set) - the
// semantics the parameter descriptions document.
func inWindow(rfc3339 string, after, before time.Time) bool {
	ts, _ := time.Parse(time.RFC3339, rfc3339)
	if !after.IsZero() && ts.Before(after) {
		return false
	}
	if !before.IsZero() && !ts.Before(before) {
		return false
	}
	return true
}

// listCustomers serves list_customers.
func (s *Service) listCustomers(w http.ResponseWriter, r *http.Request) {
	f, err := parseCustomerFilters(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.mu.Lock()
	var items []any
	for _, c := range s.st.customers {
		if f.matches(c) {
			items = append(items, *c)
		}
	}
	s.st.mu.Unlock()
	writePage(w, r, items)
}

// getCustomer serves get_customer.
func (s *Service) getCustomer(w http.ResponseWriter, rawID string) {
	id, err := parseID(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	c := s.st.customer(id)
	if c == nil {
		writeError(w, http.StatusNotFound, "no customer with id "+rawID)
		return
	}
	writeJSON(w, http.StatusOK, *c)
}

// aggregateCustomers serves aggregate_customers.
func (s *Service) aggregateCustomers(w http.ResponseWriter, r *http.Request) {
	groupBy := r.URL.Query().Get("group_by")
	if groupBy != "region" && groupBy != "tier" {
		writeError(w, http.StatusBadRequest, "group_by must be one of: region, tier")
		return
	}
	counts := map[string]int64{}
	s.st.mu.Lock()
	for _, c := range s.st.customers {
		if groupBy == "region" {
			counts[c.Region]++
		} else {
			counts[c.Tier]++
		}
	}
	s.st.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"groups": sortedGroups(counts, "count")})
}

// sortedGroups renders a counts map as sorted group objects with the
// given value field name.
func sortedGroups(counts map[string]int64, valueField string) []any {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	groups := make([]any, 0, len(keys))
	for _, k := range keys {
		groups = append(groups, map[string]any{"key": k, valueField: counts[k]})
	}
	return groups
}

// updateCustomer serves update_customer.
func (s *Service) updateCustomer(w http.ResponseWriter, r *http.Request, rawID string) {
	id, err := parseID(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	body, err := decodeCustomerUpdate(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	c := s.st.customer(id)
	if c == nil {
		writeError(w, http.StatusNotFound, "no customer with id "+rawID)
		return
	}
	if body.Region != nil {
		c.Region = *body.Region
	}
	if body.Tier != nil {
		c.Tier = *body.Tier
	}
	writeJSON(w, http.StatusOK, *c)
}

// customerUpdate is the update_customer request body.
type customerUpdate struct {
	Region *string `json:"region"`
	Tier   *string `json:"tier"`
}

// decodeCustomerUpdate decodes and validates an update_customer body.
func decodeCustomerUpdate(r *http.Request) (customerUpdate, error) {
	var body customerUpdate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return body, badParam{"invalid JSON body"}
	}
	if body.Region == nil && body.Tier == nil {
		return body, badParam{"provide region and/or tier"}
	}
	if body.Region != nil && !slices.Contains(regionVocab, *body.Region) {
		return body, badParam{"region must be one of: " + strings.Join(regionVocab, ", ")}
	}
	if body.Tier != nil && !slices.Contains(tierVocab, *body.Tier) {
		return body, badParam{"tier must be one of: " + strings.Join(tierVocab, ", ")}
	}
	return body, nil
}

// listCustomerOrders serves list_customer_orders.
func (s *Service) listCustomerOrders(w http.ResponseWriter, r *http.Request, rawID string) {
	id, err := parseID(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.mu.Lock()
	if s.st.customer(id) == nil {
		s.st.mu.Unlock()
		writeError(w, http.StatusNotFound, "no customer with id "+rawID)
		return
	}
	var items []any
	for _, o := range s.st.orders {
		if o.CustomerID == id {
			items = append(items, *o)
		}
	}
	s.st.mu.Unlock()
	writePage(w, r, items)
}

// orderFilters holds the parsed list_orders filters.
type orderFilters struct {
	status               string
	customerID           int64
	hasCustomer          bool
	minAmount, maxAmount int64
	hasMin, hasMax       bool
	after, before        time.Time
}

// parseOrderFilters parses list_orders query parameters.
func parseOrderFilters(r *http.Request) (orderFilters, error) {
	var f orderFilters
	var err error
	if f.status, err = enumParam(r, "status", statusVocab); err != nil {
		return f, err
	}
	if f.customerID, f.hasCustomer, err = parseIntParam(r, "customer_id"); err != nil {
		return f, err
	}
	if f.minAmount, f.hasMin, err = parseIntParam(r, "min_amount"); err != nil {
		return f, err
	}
	if f.maxAmount, f.hasMax, err = parseIntParam(r, "max_amount"); err != nil {
		return f, err
	}
	if f.after, err = parseTimeParam(r, "placed_after"); err != nil {
		return f, err
	}
	if f.before, err = parseTimeParam(r, "placed_before"); err != nil {
		return f, err
	}
	return f, nil
}

// matches reports whether an order passes the filters.
func (f orderFilters) matches(o *Order) bool {
	if f.status != "" && o.Status != f.status {
		return false
	}
	if f.hasCustomer && int64(o.CustomerID) != f.customerID {
		return false
	}
	if f.hasMin && o.Amount < f.minAmount {
		return false
	}
	if f.hasMax && o.Amount > f.maxAmount {
		return false
	}
	return inWindow(o.PlacedAt, f.after, f.before)
}

// listOrders serves list_orders and the deprecated list_orders_v1 (which
// has no filter parameters: filters are ignored on the v1 route, matching
// its spec).
func (s *Service) listOrders(w http.ResponseWriter, r *http.Request, v1 bool) {
	var f orderFilters
	if !v1 {
		var err error
		if f, err = parseOrderFilters(r); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	s.st.mu.Lock()
	var items []any
	for _, o := range s.st.orders {
		if v1 || f.matches(o) {
			items = append(items, *o)
		}
	}
	s.st.mu.Unlock()
	writePage(w, r, items)
}

// getOrder serves get_order.
func (s *Service) getOrder(w http.ResponseWriter, rawID string) {
	id, err := parseID(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	o := s.st.order(id)
	if o == nil {
		writeError(w, http.StatusNotFound, "no order with id "+rawID)
		return
	}
	writeJSON(w, http.StatusOK, *o)
}

// aggregateOrders serves aggregate_orders.
func (s *Service) aggregateOrders(w http.ResponseWriter, r *http.Request) {
	groupBy := r.URL.Query().Get("group_by")
	if groupBy != "status" && groupBy != "month" {
		writeError(w, http.StatusBadRequest, "group_by must be one of: status, month")
		return
	}
	metric := r.URL.Query().Get("metric")
	if metric == "" {
		metric = "count"
	}
	if metric != "count" && metric != "amount_sum" {
		writeError(w, http.StatusBadRequest, "metric must be one of: count, amount_sum")
		return
	}
	values := map[string]int64{}
	s.st.mu.Lock()
	for _, o := range s.st.orders {
		key := o.Status
		if groupBy == "month" {
			key = o.PlacedAt[:7] // RFC 3339 prefix YYYY-MM
		}
		if metric == "count" {
			values[key]++
		} else {
			values[key] += o.Amount
		}
	}
	s.st.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"groups": sortedGroups(values, "value")})
}

// createOrder serves create_order.
func (s *Service) createOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CustomerID *int   `json:"customer_id"`
		Amount     *int64 `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.CustomerID == nil || body.Amount == nil {
		writeError(w, http.StatusBadRequest, "customer_id and amount are required")
		return
	}
	if *body.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "amount must be a positive number of cents")
		return
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	if s.st.customer(*body.CustomerID) == nil {
		writeError(w, http.StatusBadRequest, "unknown customer_id")
		return
	}
	o := &Order{
		ID: s.st.nextOrderID, CustomerID: *body.CustomerID,
		PlacedAt: createdAtSentinel, Status: "pending", Amount: *body.Amount,
	}
	s.st.nextOrderID++
	s.st.orders = append(s.st.orders, o)
	writeJSON(w, http.StatusCreated, *o)
}

// cancelOrder serves cancel_order. Only pending orders can be canceled.
func (s *Service) cancelOrder(w http.ResponseWriter, rawID string) {
	id, err := parseID(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	o := s.st.order(id)
	if o == nil {
		writeError(w, http.StatusNotFound, "no order with id "+rawID)
		return
	}
	if o.Status != "pending" {
		writeError(w, http.StatusConflict, "only pending orders can be canceled; order is "+o.Status)
		return
	}
	o.Status = "canceled"
	writeJSON(w, http.StatusOK, *o)
}
