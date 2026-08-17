package callrecord

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNormalizeStatement(t *testing.T) {
	t.Parallel()

	// Reuse compares statements, so two agents that indent the same query
	// differently must compare equal, or neither ever credits the other.
	a := NormalizeStatement("SELECT  region,\n  SUM(amount)\nFROM sales.orders")
	b := NormalizeStatement("select region, sum(amount) from sales.orders")
	if a != b {
		t.Errorf("the same query written twice must normalize alike:\n%q\n%q", a, b)
	}
	if got := NormalizeStatement("   "); got != "" {
		t.Errorf("blank statement = %q, want empty", got)
	}
}

func TestValidOutcome(t *testing.T) {
	t.Parallel()

	for _, outcome := range Outcomes {
		if !ValidOutcome(outcome) {
			t.Errorf("%q is an outcome the store can derive but not a valid filter value", outcome)
		}
	}
	if ValidOutcome("promoted") {
		t.Error("an unknown outcome must not validate: a typo would filter to nothing and read as an answer")
	}
}

func TestPromotable(t *testing.T) {
	t.Parallel()

	now := time.Now()
	tests := []struct {
		name string
		rec  Record
		want bool
	}{
		{"satisfied and undecided", Record{Outcome: OutcomeSatisfied}, true},
		{"never answered anything", Record{Outcome: OutcomeRan}, false},
		{"failed", Record{Outcome: OutcomeFailed}, false},
		{"already promoted", Record{Outcome: OutcomeSatisfied, PromotedURN: "urn:li:query:x"}, false},
		{"already declined", Record{Outcome: OutcomeSatisfied, RejectedAt: &now}, false},
	}
	for _, tt := range tests {
		if got := tt.rec.Promotable(); got != tt.want {
			t.Errorf("%s: Promotable() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestFilterFromQuery(t *testing.T) {
	t.Parallel()

	q := url.Values{
		"kind":       {"sql"},
		"connection": {"acme-warehouse"},
		"outcome":    {"satisfied"},
		"target":     {"urn:li:dataset:(urn:li:dataPlatform:trino,sales.orders,PROD)"},
		"session_id": {"dps_abc"},
		"q":          {"revenue"},
		"queue":      {"promotable"},
		// The caller is deliberately not a query parameter: each surface
		// assigns it, so a hand-written one must not widen a listing.
		"user_id": {"someone-else"},
	}
	f := FilterFromQuery(q)

	if f.UserID != "" {
		t.Errorf("UserID = %q, want empty: the shared parser must never read the caller", f.UserID)
	}
	if f.Kind != KindSQL || f.Connection != "acme-warehouse" || f.Outcome != OutcomeSatisfied {
		t.Errorf("facets not parsed: %+v", f)
	}
	if f.SessionID != "dps_abc" || f.Search != "revenue" || !f.PromotableOnly {
		t.Errorf("facets not parsed: %+v", f)
	}
	if f.Limit != DefaultPerPage {
		t.Errorf("Limit = %d, want the default %d", f.Limit, DefaultPerPage)
	}
}

func TestFilterFromQueryDropsUnknownValues(t *testing.T) {
	t.Parallel()

	f := FilterFromQuery(url.Values{"kind": {"graphql"}, "outcome": {"promoted"}})
	// An unknown facet value is absent rather than passed through: a typo
	// should show the unfiltered list, not an empty one that reads as an
	// answer.
	if f.Kind != "" || f.Outcome != "" {
		t.Errorf("unknown facet values must be dropped, got %+v", f)
	}
}

func TestClampPerPage(t *testing.T) {
	t.Parallel()

	if got := ClampPerPage(0); got != DefaultPerPage {
		t.Errorf("unstated page size = %d, want %d", got, DefaultPerPage)
	}
	if got := ClampPerPage(MaxPerPage + 1); got != MaxPerPage {
		t.Errorf("oversized page = %d, want the cap %d", got, MaxPerPage)
	}
	if got := ClampPerPage(10); got != 10 {
		t.Errorf("stated page size = %d, want 10", got)
	}
}

func TestIndexText(t *testing.T) {
	t.Parallel()

	text := IndexText(Record{
		Purpose:   "Sizing Q3 revenue by region.",
		Statement: "SELECT region FROM sales.orders",
		Targets:   []string{"urn:li:dataset:(urn:li:dataPlatform:trino,sales.orders,PROD)"},
	})
	// Both halves are needed: a purpose alone does not say which table, and a
	// statement alone does not say what question it answers.
	for _, want := range []string{"Sizing Q3 revenue", "SELECT region"} {
		if !strings.Contains(text, want) {
			t.Errorf("index text %q is missing %q", text, want)
		}
	}
	// The targets are not in it: the vector and the lexical index must cover
	// the same corpus, and a statement already names the tables it reads.
	if strings.Contains(text, "urn:li:dataset") {
		t.Errorf("index text %q must not carry the target URNs", text)
	}

	api := IndexText(Record{Purpose: "Listing open orders.", Method: "GET", Path: "/v1/orders", OperationID: "listOrders"})
	if !strings.Contains(api, "GET /v1/orders") || !strings.Contains(api, "listOrders") {
		t.Errorf("an api record must be searchable by its request line, got %q", api)
	}
	if IndexText(Record{}) != "" {
		t.Error("a record with nothing to say must index as nothing rather than as blank lines")
	}
}

func TestAPITarget(t *testing.T) {
	t.Parallel()

	// The same operation against two upstreams is two endpoints, so the
	// connection is part of the target.
	if got := APITarget("acme-crm", "GET", "/v1/orders", "listOrders"); got != "api:acme-crm:listOrders" {
		t.Errorf("APITarget = %q", got)
	}
	if got := APITarget("acme-crm", "GET", "/v1/orders", ""); got != "api:acme-crm:GET /v1/orders" {
		t.Errorf("APITarget without an operation id = %q", got)
	}
	if got := APITarget("acme-crm", "", "", ""); got != "" {
		t.Errorf("a call that named no endpoint has no target, got %q", got)
	}
}
