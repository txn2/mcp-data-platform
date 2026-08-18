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

	cases := []struct {
		name        string
		connection  string
		method      string
		path        string
		operationID string
		pathParams  map[string]string
		want        string
		why         string
	}{
		{
			name: "operation id names the endpoint", connection: "acme-crm",
			method: "GET", path: "/v1/orders", operationID: "listOrders",
			want: "api:acme-crm:listOrders",
			why:  "the same operation against two upstreams is two endpoints, so the connection is part of the target",
		},
		{
			name: "path addressing keeps the request line", connection: "acme-crm",
			method: "GET", path: "/v1/orders",
			want: "api:acme-crm:GET /v1/orders",
		},
		{
			name: "no endpoint named", connection: "acme-crm",
			want: "",
			why:  "a call that named no endpoint has no target",
		},
		{
			name: "template resolved by its path params", connection: "platform-admin",
			operationID: "POST /admin/scripts/{id}/versions/{version}/approve",
			pathParams:  map[string]string{"id": "script-a", "version": "3"},
			want:        "api:platform-admin:POST /admin/scripts/script-a/versions/3/approve",
			why:         "approving one script and approving another must not be the same target (#1352)",
		},
		{
			name: "same template, different resource", connection: "platform-admin",
			operationID: "POST /admin/scripts/{id}/versions/{version}/approve",
			pathParams:  map[string]string{"id": "script-b", "version": "3"},
			want:        "api:platform-admin:POST /admin/scripts/script-b/versions/3/approve",
		},
		{
			name: "unresolved slot yields no target", connection: "platform-admin",
			operationID: "POST /admin/scripts/{id}/versions/{version}/approve",
			pathParams:  map[string]string{"id": "script-a"},
			want:        "",
			why:         "a target with a hole in it identifies nothing, so it must not be comparable",
		},
		{
			name: "empty slot value yields no target", connection: "platform-admin",
			operationID: "GET /admin/scripts/{id}", pathParams: map[string]string{"id": "  "},
			want: "",
		},
		{
			name: "named operation carries its resolved resource", connection: "acme-crm",
			operationID: "approveScriptVersion",
			pathParams:  map[string]string{"version": "3", "id": "script-a"},
			want:        `api:acme-crm:approveScriptVersion({"id":"script-a","version":"3"})`,
			why:         "a spec that declares operationIds still has to distinguish which resource was addressed",
		},
		{
			name: "path template addressed directly is not a resource", connection: "acme-crm",
			method: "GET", path: "/v1/orders/{id}",
			want: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := APITarget(c.connection, c.method, c.path, c.operationID, c.pathParams)
			if got != c.want {
				t.Errorf("APITarget = %q, want %q (%s)", got, c.want, c.why)
			}
		})
	}
}

// TestAPITargetOrdersUnresolvedParams proves the rendering of the parameters no
// slot consumed does not depend on Go's map iteration order: two calls carrying
// the same values have to compare equal, which is the whole point of a target.
// It also proves the rendering is unambiguous: a value holding the separators a
// flat encoding would use must not be able to impersonate a different set.
func TestAPITargetOrdersUnresolvedParams(t *testing.T) {
	t.Parallel()

	const want = `api:acme:op({"a":"1","b":"2","c":"3"})`
	for range 20 {
		got := APITarget("acme", "POST", "", "op", map[string]string{"c": "3", "a": "1", "b": "2"})
		if got != want {
			t.Fatalf("APITarget = %q, want %q", got, want)
		}
	}

	// One parameter whose value spells out another pairing must not collide
	// with the pairing itself.
	spoofed := APITarget("acme", "POST", "", "op", map[string]string{"a": `1","b":"2`})
	honest := APITarget("acme", "POST", "", "op", map[string]string{"a": "1", "b": "2"})
	if spoofed == honest {
		t.Errorf("two different parameter sets rendered the same target: %q", honest)
	}
}
