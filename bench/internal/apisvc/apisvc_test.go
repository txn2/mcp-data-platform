package apisvc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
	"github.com/txn2/mcp-data-platform/bench/internal/gen"
)

// newTestServer builds the service with auth disabled plus the dataset
// its truths derive from.
func newTestServer(t *testing.T) (*httptest.Server, *gen.Dataset) {
	t.Helper()
	ts := httptest.NewServer(New(Options{}))
	t.Cleanup(ts.Close)
	return ts, gen.Generate()
}

// getJSON GETs a path and decodes the JSON body.
func getJSON(t *testing.T, ts *httptest.Server, path string, out any) int {
	t.Helper()
	res, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatalf("GET %s: decode: %v", path, err)
		}
	}
	return res.StatusCode
}

// doJSON sends a JSON body with the given method and decodes the response.
func doJSON(t *testing.T, ts *httptest.Server, method, path string, body, out any) int {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, ts.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return res.StatusCode
}

// fetchAll consumes every page of a listing, following next_cursor.
func fetchAll(t *testing.T, ts *httptest.Server, path string, params url.Values) []map[string]any {
	t.Helper()
	var items []map[string]any
	cursor := ""
	for {
		q := url.Values{}
		maps.Copy(q, params)
		q.Set("page_size", "100")
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page struct {
			Items      []map[string]any `json:"items"`
			NextCursor string           `json:"next_cursor"`
		}
		if code := getJSON(t, ts, path+"?"+q.Encode(), &page); code != http.StatusOK {
			t.Fatalf("GET %s: status %d", path, code)
		}
		items = append(items, page.Items...)
		if page.NextCursor == "" {
			return items
		}
		cursor = page.NextCursor
	}
}

// TestFilteredCountsMatchGroundTruth exercises the filter semantics the
// p2 tasks grade: a fully paginated filtered listing must count exactly
// what the ground-truth predicate counts.
func TestFilteredCountsMatchGroundTruth(t *testing.T) {
	ts, ds := newTestServer(t)
	completed100k := 0
	q2 := 0
	for _, o := range ds.Orders {
		if o.Status == "completed" && o.Amount >= 100000 {
			completed100k++
		}
		if !o.TS.Before(mustTime(t, "2025-04-01T00:00:00Z")) && o.TS.Before(mustTime(t, "2025-07-01T00:00:00Z")) {
			q2++
		}
	}
	got := fetchAll(t, ts, "/commerce/orders", url.Values{"status": {"completed"}, "min_amount": {"100000"}})
	if len(got) != completed100k {
		t.Errorf("completed+min_amount count = %d, want %d", len(got), completed100k)
	}
	got = fetchAll(t, ts, "/commerce/orders", url.Values{"placed_after": {"2025-04-01T00:00:00Z"}, "placed_before": {"2025-07-01T00:00:00Z"}})
	if len(got) != q2 {
		t.Errorf("Q2 window count = %d, want %d", len(got), q2)
	}
	west := 0
	for _, c := range ds.Customers {
		if c.Region == "West" && c.Tier == "enterprise" {
			west++
		}
	}
	if got := fetchAll(t, ts, "/crm/customers", url.Values{"region": {"West"}, "tier": {"enterprise"}}); len(got) != west {
		t.Errorf("West enterprise count = %d, want %d", len(got), west)
	}
}

// mustTime parses an RFC 3339 constant.
func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// TestAggregatesMatchGroundTruth exercises the aggregate endpoints the p1
// tasks grade.
func TestAggregatesMatchGroundTruth(t *testing.T) {
	ts, ds := newTestServer(t)
	wantRegion := map[string]int64{}
	for _, c := range ds.Customers {
		wantRegion[c.Region]++
	}
	var agg struct {
		Groups []struct {
			Key   string `json:"key"`
			Count int64  `json:"count"`
			Value int64  `json:"value"`
		} `json:"groups"`
	}
	if code := getJSON(t, ts, "/crm/customers:aggregate?group_by=region", &agg); code != http.StatusOK {
		t.Fatalf("aggregate status %d", code)
	}
	for _, g := range agg.Groups {
		if g.Count != wantRegion[g.Key] {
			t.Errorf("region %s count = %d, want %d", g.Key, g.Count, wantRegion[g.Key])
		}
	}
	wantMarch := int64(0)
	for _, o := range ds.Orders {
		if o.TS.Format("2006-01") == "2025-03" {
			wantMarch += o.Amount
		}
	}
	if code := getJSON(t, ts, "/commerce/orders:aggregate?group_by=month&metric=amount_sum", &agg); code != http.StatusOK {
		t.Fatalf("aggregate status %d", code)
	}
	found := false
	for _, g := range agg.Groups {
		if g.Key == "2025-03" {
			found = true
			if g.Value != wantMarch {
				t.Errorf("2025-03 amount_sum = %d, want %d", g.Value, wantMarch)
			}
		}
	}
	if !found {
		t.Error("no 2025-03 group in month aggregate")
	}
}

// TestMutationsAndReset exercises the p3 grading loop end to end: mutate,
// verify via the state dump, reset, verify restoration.
func TestMutationsAndReset(t *testing.T) {
	ts, ds := newTestServer(t)
	// Cancel the first pending order.
	var pending gen.Order
	for _, o := range ds.Orders {
		if o.Status == "pending" {
			pending = o
			break
		}
	}
	var order Order
	if code := doJSON(t, ts, http.MethodPost, fmt.Sprintf("/commerce/orders/%d:cancel", pending.ID), map[string]any{}, &order); code != http.StatusOK {
		t.Fatalf("cancel status %d", code)
	}
	if order.Status != "canceled" {
		t.Fatalf("cancel left status %s", order.Status)
	}
	// A second cancel conflicts.
	if code := doJSON(t, ts, http.MethodPost, fmt.Sprintf("/commerce/orders/%d:cancel", pending.ID), map[string]any{}, nil); code != http.StatusConflict {
		t.Errorf("double cancel status %d, want 409", code)
	}
	// Create an order and find it in the state dump.
	var created Order
	if code := doJSON(t, ts, http.MethodPost, "/commerce/orders", map[string]any{"customer_id": 12, "amount": 15000}, &created); code != http.StatusCreated {
		t.Fatalf("create status %d", code)
	}
	var dump struct {
		Rows []Order `json:"rows"`
	}
	if code := getJSON(t, ts, "/_bench/state/orders", &dump); code != http.StatusOK {
		t.Fatalf("state dump status %d", code)
	}
	foundCancel, foundCreate := false, false
	for _, o := range dump.Rows {
		if o.ID == pending.ID && o.Status == "canceled" {
			foundCancel = true
		}
		if o.ID == created.ID && o.CustomerID == 12 && o.Amount == 15000 && o.Status == "pending" {
			foundCreate = true
		}
	}
	if !foundCancel || !foundCreate {
		t.Fatalf("state dump missing mutations: cancel=%v create=%v", foundCancel, foundCreate)
	}
	// Update a customer and verify.
	var cust Customer
	if code := doJSON(t, ts, http.MethodPatch, "/crm/customers/1", map[string]any{"region": "South", "tier": "plus"}, &cust); code != http.StatusOK {
		t.Fatalf("update status %d", code)
	}
	if cust.Region != "South" || cust.Tier != "plus" {
		t.Fatalf("update left %s/%s", cust.Region, cust.Tier)
	}
	// Reset restores the seed state exactly.
	if code := doJSON(t, ts, http.MethodPost, "/_bench/reset", map[string]any{}, nil); code != http.StatusOK {
		t.Fatalf("reset failed")
	}
	if code := getJSON(t, ts, "/_bench/state/orders", &dump); code != http.StatusOK {
		t.Fatalf("post-reset dump status %d", code)
	}
	if len(dump.Rows) != len(ds.Orders) {
		t.Fatalf("post-reset order count %d, want %d", len(dump.Rows), len(ds.Orders))
	}
	for _, o := range dump.Rows {
		if o.ID == pending.ID && o.Status != "pending" {
			t.Fatalf("reset did not restore order %d (status %s)", o.ID, o.Status)
		}
	}
}

// TestDistractorSurface exercises the generic distractor handlers on a
// near-miss resource: all seven operations respond coherently.
func TestDistractorSurface(t *testing.T) {
	ts, _ := newTestServer(t)
	rows := fetchAll(t, ts, "/billing/invoices", nil)
	if len(rows) == 0 {
		t.Fatal("billing/invoices has no seeded rows")
	}
	if code := getJSON(t, ts, "/billing/invoices/1", nil); code != http.StatusOK {
		t.Errorf("distractor get status %d", code)
	}
	var created map[string]any
	if code := doJSON(t, ts, http.MethodPost, "/billing/invoices", map[string]any{"name": "Test invoice", "status": "draft"}, &created); code != http.StatusCreated {
		t.Errorf("distractor create status %d", code)
	}
	if code := doJSON(t, ts, http.MethodPatch, "/billing/invoices/1", map[string]any{"status": "archived"}, nil); code != http.StatusOK {
		t.Errorf("distractor update status %d", code)
	}
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/billing/invoices/2", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Errorf("distractor delete status %d", res.StatusCode)
	}
	if code := doJSON(t, ts, http.MethodPost, "/billing/invoices:search", map[string]any{"query": "invoices"}, nil); code != http.StatusOK {
		t.Errorf("distractor search status %d", code)
	}
	if code := getJSON(t, ts, "/billing/invoices:aggregate?group_by=status", nil); code != http.StatusOK {
		t.Errorf("distractor aggregate status %d", code)
	}
}

// TestAuthAndErrors covers the auth gate and the error contract.
func TestAuthAndErrors(t *testing.T) {
	ts := httptest.NewServer(New(Options{APIKey: "sekrit"}))
	t.Cleanup(ts.Close)
	res, err := http.Get(ts.URL + "/crm/customers/1")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no key status %d, want 401", res.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/crm/customers/1", nil)
	req.Header.Set("X-API-Key", "sekrit")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("with key status %d, want 200", res.StatusCode)
	}

	open, _ := newTestServer(t)
	if code := getJSON(t, open, "/nope/nothing", nil); code != http.StatusNotFound {
		t.Errorf("unknown route status %d, want 404", code)
	}
	if code := getJSON(t, open, "/crm/customers?region=Central", nil); code != http.StatusBadRequest {
		t.Errorf("bad enum status %d, want 400", code)
	}
	if code := getJSON(t, open, "/crm/customers/999999", nil); code != http.StatusNotFound {
		t.Errorf("unknown id status %d, want 404", code)
	}
	if code := getJSON(t, open, "/commerce/orders?cursor=%25bad", nil); code != http.StatusBadRequest {
		t.Errorf("bad cursor status %d, want 400", code)
	}
}

// TestRequestLog verifies the access log resolves operation ids and reset
// clears it.
func TestRequestLog(t *testing.T) {
	ts, _ := newTestServer(t)
	getJSON(t, ts, "/crm/customers/1", nil)
	var log struct {
		Requests []RequestLogEntry `json:"requests"`
	}
	if code := getJSON(t, ts, "/_bench/requests", &log); code != http.StatusOK {
		t.Fatalf("requests status %d", code)
	}
	if len(log.Requests) != 1 || log.Requests[0].OperationID != "get_customer" {
		t.Fatalf("request log = %+v, want one get_customer entry", log.Requests)
	}
	doJSON(t, ts, http.MethodPost, "/_bench/reset", map[string]any{}, nil)
	if code := getJSON(t, ts, "/_bench/requests", &log); code != http.StatusOK {
		t.Fatalf("requests status %d", code)
	}
	if len(log.Requests) != 0 {
		t.Fatalf("reset left %d request log entries", len(log.Requests))
	}
}

// TestServiceCoversFullCatalog sends one request to every catalog
// operation and asserts none returns 404-no-such-endpoint or a 5xx: the
// service serves the complete tier-2 surface.
func TestServiceCoversFullCatalog(t *testing.T) {
	ts, ds := newTestServer(t)
	c := apigen.BuildCatalog()
	// A real, initially pending order id: order ids are not 1-based (the
	// dataset starts them at 1000), and cancel_order requires pending.
	pendingID := 0
	for _, o := range ds.Orders {
		if o.Status == "pending" {
			pendingID = o.ID
			break
		}
	}
	for _, op := range c.Operations {
		code := invokeOperation(t, ts, op, pendingID)
		if code == http.StatusNotFound && op.Kind != apigen.KindGet && op.Kind != apigen.KindUpdate && op.Kind != apigen.KindDelete {
			t.Errorf("operation %s: 404", op.ID)
		}
		if code >= 500 {
			t.Errorf("operation %s: status %d", op.ID, code)
		}
	}
}

// invokeOperation sends a minimal valid request to one operation.
func invokeOperation(t *testing.T, ts *httptest.Server, op apigen.Operation, orderID int) int {
	t.Helper()
	id := "1"
	if op.Tag == "commerce" && op.Gold {
		id = strconv.Itoa(orderID)
	}
	path := strings.ReplaceAll(op.Path, "{id}", id)
	switch op.Kind {
	case apigen.KindAggregate:
		q := "status"
		if op.Gold && op.ID == "aggregate_customers" {
			q = "region"
		}
		return getJSON(t, ts, path+"?group_by="+q, nil)
	case apigen.KindSearch:
		return doJSON(t, ts, http.MethodPost, path, map[string]any{"query": "a"}, nil)
	case apigen.KindCreate:
		body := map[string]any{"name": "probe"}
		if op.ID == "create_order" {
			body = map[string]any{"customer_id": 1, "amount": 100}
		}
		return doJSON(t, ts, http.MethodPost, path, body, nil)
	case apigen.KindUpdate:
		body := map[string]any{"name": "probe"}
		if op.ID == "update_customer" {
			body = map[string]any{"region": "North"}
		}
		return doJSON(t, ts, http.MethodPatch, path, body, nil)
	case apigen.KindDelete:
		req, _ := http.NewRequest(http.MethodDelete, ts.URL+path, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	case apigen.KindCancel:
		return doJSON(t, ts, http.MethodPost, path, map[string]any{}, nil)
	default:
		return getJSON(t, ts, path, nil)
	}
}
