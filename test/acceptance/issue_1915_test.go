//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"testing"
)

// Issue #1915: api_invoke_endpoint cut a JSON response past the context
// budget to a byte prefix: invalid JSON ending partway through a record, no
// count of what was kept, and one way to the rest, api_export, which writes.
// A read-only count across a production deployment's insights and memory
// records could not be completed.
//
// A list is now cut on whole items. The result stays valid JSON, reports
// body_items {shown, total}, and where the operation declares paging
// parameters names next_arguments: the api_invoke_endpoint call that reads
// the items after the ones shown, before api_export. graphql_query past the
// budget returns what fits, cut on the same rule.
//
// Every criterion runs against the running platform and the upstreams the dev
// stack runs: the api-test fixture's three paginated list endpoints (cursor,
// Link header with page/per_page, OData $top/$skip), the platform's own admin
// memory list through the built-in platform-admin connection, and DataHub's
// GMS at /api/graphql for the GraphQL kind.
//
// Wire forms: api_invoke_endpoint's query_params values are untyped, so every
// paging value is sent as a number and as a string. graphql_query's variables
// is typed ["object", "string"] and is sent in both forms.

const (
	issue1915Budget  = 32 * 1024
	issue1915Total   = 2500
	issue1915Page    = 1000
	issue1915Purpose = "Acceptance #1915: a list past the context budget is cut on items and read on inline."
)

// issue1915Style is one fixture paging style: the path, the key the items
// are under, the first request's query, and how a caller steps past a page
// that was not cut, by the API's own convention, stopping where the API
// signals the list's end.
type issue1915Style struct {
	path  string
	items string
	first func(form string) map[string]any
	step  func(query map[string]any, body map[string]any, n int) (map[string]any, bool)
}

func issue1915Value(form string, n int) any {
	if form == "string" {
		return fmt.Sprint(n)
	}
	return n
}

func issue1915Int(v any) int {
	var n int
	_, _ = fmt.Sscan(fmt.Sprint(v), &n)
	return n
}

func issue1915With(q map[string]any, k string, v any) map[string]any {
	out := map[string]any{}
	for key, val := range q {
		out[key] = val
	}
	out[k] = v
	return out
}

var issue1915Styles = map[string]issue1915Style{
	"cursor": {
		path: "/v1/pagination/cursor", items: "items",
		first: func(form string) map[string]any {
			return map[string]any{"limit": issue1915Value(form, issue1915Page), "total": issue1915Value(form, issue1915Total)}
		},
		step: func(q, body map[string]any, _ int) (map[string]any, bool) {
			cursor, _ := body["next_cursor"].(string)
			return issue1915With(q, "cursor", cursor), cursor != ""
		},
	},
	"page": {
		path: "/v1/pagination/link", items: "items",
		first: func(form string) map[string]any {
			return map[string]any{"page": issue1915Value(form, 1), "per_page": issue1915Value(form, issue1915Page), "total": issue1915Value(form, issue1915Total)}
		},
		step: func(q, body map[string]any, _ int) (map[string]any, bool) {
			more := issue1915Int(body["page"])*issue1915Int(body["per_page"]) < issue1915Int(body["total"])
			return issue1915With(q, "page", issue1915Int(q["page"])+1), more
		},
	},
	"odata": {
		path: "/v1/pagination/odata", items: "value",
		first: func(form string) map[string]any {
			return map[string]any{"$top": issue1915Value(form, issue1915Page), "$skip": issue1915Value(form, 0), "total": issue1915Value(form, issue1915Total)}
		},
		step: func(q, body map[string]any, n int) (map[string]any, bool) {
			_, more := body["@odata.nextLink"]
			return issue1915With(q, "$skip", issue1915Int(q["$skip"])+n), more
		},
	},
}

// issue1915AssetTotal is how many portal assets the caller holds, read
// before and after a read-only walk to show it wrote none.
func issue1915AssetTotal(t *testing.T, c *client) float64 {
	t.Helper()
	status, body := c.rest(http.MethodGet, "/api/v1/portal/assets?limit=1", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/portal/assets: HTTP %d %v", status, body)
	}
	return number(t, body, "total")
}

// issue1915Invoke calls api_invoke_endpoint as a model's call arrives and
// returns the decoded result, having checked the text the client receives is
// inside the budget.
func issue1915Invoke(t *testing.T, c *client, args map[string]any) map[string]any {
	t.Helper()
	args["purpose"] = issue1915Purpose
	res, text, err := c.callRaw("api_invoke_endpoint", args)
	if err != nil || res.IsError {
		t.Fatalf("api_invoke_endpoint: %v %s", err, text)
	}
	if len(text) > issue1915Budget {
		t.Fatalf("the result is %d characters; want it inside the %d budget", len(text), issue1915Budget)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("the result is not JSON: %v", err)
	}
	return out
}

// issue1915AssertCut checks a cut result's contract: the items shown are
// counted against the response's, and the hint names the read-only way on
// before api_export.
func issue1915AssertCut(t *testing.T, out map[string]any, shown int) {
	t.Helper()
	items, _ := out["body_items"].(map[string]any)
	if items == nil || issue1915Int(items["shown"]) != shown || issue1915Int(items["total"]) <= shown {
		t.Fatalf("body_items = %v with %d items in the body; want the items shown of more", out["body_items"], shown)
	}
	hint, _ := out["hint"].(string)
	next, export := strings.Index(hint, "next_arguments"), strings.Index(hint, "api_export")
	if next < 0 || export < 0 || next > export {
		t.Errorf("hint = %q; want next_arguments named before api_export", hint)
	}
	if out["pagination"] != nil {
		t.Errorf("pagination = %v on a cut; want the response's signal, which skips the items cut, dropped", out["pagination"])
	}
	if _, ok := out["export_arguments"].(map[string]any); !ok {
		t.Errorf("export_arguments = %v; want the api_export call offered after", out["export_arguments"])
	}
}

// TestIssue1915_EachPagingStyleReadsTheWholeListInline: for each paging
// style the fixture serves, following next_arguments from every cut, and the
// API's own paging past every page that was not cut, reads each of the
// list's items exactly once, in order, and writes no asset.
func TestIssue1915_EachPagingStyleReadsTheWholeListInline(t *testing.T) {
	for name, style := range issue1915Styles {
		for _, form := range []string{"number", "string"} {
			t.Run(name+"/values_as_"+form, func(t *testing.T) {
				c := connect(t)
				before := issue1915AssetTotal(t, c)
				args := map[string]any{"connection": issue1587FixtureConn, "method": "GET", "path": style.path, "query_params": style.first(form)}
				var seen, repeat []int
				cuts := 0
				for range 50 {
					out := issue1915Invoke(t, c, args)
					body, _ := out["body"].(map[string]any)
					list, ok := body[style.items].([]any)
					if !ok {
						t.Fatalf("body = %.300v; want valid JSON carrying the %s list", out["body"], style.items)
					}
					ids := make([]int, 0, len(list))
					for _, it := range list {
						item, _ := it.(map[string]any)
						ids = append(ids, issue1915Int(item["id"]))
					}
					if repeat != nil {
						// A cursor cut's next request asks for the same page
						// again, as many items at a time as were shown: its
						// items are the ones already read, so they replace
						// rather than extend them.
						if fmt.Sprint(ids[:min(len(repeat), len(ids))]) != fmt.Sprint(repeat) {
							t.Fatalf("the repeated page holds %v; want the %d items shown", ids, len(repeat))
						}
						seen = seen[:len(seen)-len(repeat)]
						repeat = nil
					}
					seen = append(seen, ids...)
					if next, ok := out["next_arguments"].(map[string]any); ok {
						cuts++
						issue1915AssertCut(t, out, len(list))
						if name == "cursor" {
							repeat = ids
						}
						args = next
						continue
					}
					if truncated, _ := out["body_truncated"].(bool); truncated {
						t.Fatalf("a cut with no next_arguments: %v", out["hint"])
					}
					q, _ := args["query_params"].(map[string]any)
					nextQuery, more := style.step(q, body, len(list))
					if !more {
						break
					}
					args = issue1915With(args, "query_params", nextQuery)
				}
				if cuts == 0 {
					t.Fatal("no page was cut; the case needs pages past the budget")
				}
				if len(seen) != issue1915Total {
					t.Fatalf("read %d items; want each of the %d once", len(seen), issue1915Total)
				}
				for i, id := range seen {
					if id != i {
						t.Fatalf("item %d is %d; want the list in order with no gap or repeat", i, id)
					}
				}
				if after := issue1915AssetTotal(t, c); after != before {
					t.Errorf("assets went from %v to %v; a read-only walk wrote one", before, after)
				}
			})
		}
	}
}

// issue1915Words is the vocabulary the seeded records are drawn from. Each
// record is its own random draw, so no two restate one another and the
// capture path's recall-first supersede leaves every one standing.
var issue1915Words = strings.Fields(`ledger invoice pallet carrier freight tariff quota rebate vendor
	warehouse forklift manifest customs broker container dock shipment parcel route depot courier
	margin revenue forecast budget accrual payroll audit variance ledger reconcile fiscal quarter
	cohort churn retention funnel campaign segment persona channel referral loyalty voucher coupon
	sensor firmware telemetry latency uptime outage incident rollback canary replica shard index
	glacier estuary canyon plateau tundra monsoon savanna archipelago fjord delta lagoon meadow
	violin sonata tempo chorus ballad rhythm melody harmony overture cadence refrain crescendo
	enzyme protein genome mitosis neuron synapse antibody vaccine pathogen catalyst isotope polymer`)

func issue1915Content(r *rand.Rand, i int) string {
	words := make([]string, 0, 130)
	for range 130 {
		words = append(words, issue1915Words[r.Intn(len(issue1915Words))])
	}
	return fmt.Sprintf("Acceptance 1915 record %d: %s.", i, strings.Join(words, " "))
}

// TestIssue1915_TheAdminMemoryListReadsOnInline is the ticket's production
// case: an administrator counting memory records through the platform-admin
// connection, a list past the budget, read to its end without api_export.
// The pages read inline are compared with the whole list the admin REST
// route returns, which no budget applies to. The records are not filtered by
// status: the capture path supersedes a record another one restates, and
// the comparison holds whatever the status.
func TestIssue1915_TheAdminMemoryListReadsOnInline(t *testing.T) {
	owner, admin := connectAs(t, devOwnerAPIKey), connect(t)
	r := rand.New(rand.NewSource(1915))
	for i := range 45 {
		issue1926Capture(t, owner, issue1915Content(r, i))
	}
	var whole []string
	for offset := 0; ; offset += 100 {
		status, body := admin.rest(http.MethodGet, fmt.Sprintf("%s?created_by=%s&limit=100&offset=%d", issue1926Route, devOwnerEmail, offset), http.NoBody)
		if status != http.StatusOK {
			t.Fatalf("admin REST list: HTTP %d %v", status, body)
		}
		data, _ := body["data"].([]any)
		for _, r := range data {
			rec, _ := r.(map[string]any)
			whole = append(whole, fmt.Sprint(rec["id"]))
		}
		if len(data) < 100 {
			break
		}
	}
	if len(whole) < 45 {
		t.Fatalf("the admin REST list holds %d records; want at least the 45 written", len(whole))
	}

	for _, form := range []string{"number", "string"} {
		t.Run("limit_as_"+form, func(t *testing.T) {
			admin := connect(t)
			before := issue1915AssetTotal(t, admin)
			args := map[string]any{
				"connection": issue1926AdminConn, "method": "GET", "path": issue1926Route,
				"query_params": map[string]any{"created_by": devOwnerEmail, "limit": issue1915Value(form, 100)},
			}
			var read []string
			cuts := 0
			for range 50 {
				out := issue1915Invoke(t, admin, args)
				body, _ := out["body"].(map[string]any)
				data, ok := body["data"].([]any)
				if !ok {
					t.Fatalf("body = %.300v; want valid JSON carrying the data list", out["body"])
				}
				for _, r := range data {
					rec, _ := r.(map[string]any)
					read = append(read, fmt.Sprint(rec["id"]))
				}
				if next, ok := out["next_arguments"].(map[string]any); ok {
					cuts++
					issue1915AssertCut(t, out, len(data))
					args = next
					continue
				}
				q, _ := args["query_params"].(map[string]any)
				offset := issue1915Int(q["offset"]) + len(data)
				if len(data) == 0 || offset >= issue1915Int(body["total"]) {
					break
				}
				args = issue1915With(args, "query_params", issue1915With(q, "offset", offset))
			}
			if cuts == 0 {
				t.Fatal("no page was cut; the case needs a list past the budget")
			}
			if strings.Join(read, ",") != strings.Join(whole, ",") {
				t.Errorf("read inline %d records; want the %d the admin list holds, in order", len(read), len(whole))
			}
			if after := issue1915AssetTotal(t, admin); after != before {
				t.Errorf("assets went from %v to %v; a read-only walk wrote one", before, after)
			}
		})
	}
}

// issue1915SearchDocument is one catalog search whose answer is one list,
// searchAcrossEntities.searchResults. Each result repeats its urn under
// issue1915URNAliases aliases, which puts the answer past the budget on the
// few entities a local DataHub holds without adding a second list.
func issue1915SearchDocument() string {
	var b strings.Builder
	b.WriteString(`query Acc1915Search { searchAcrossEntities(input: {query: "*", start: 0, count: 100}) { searchResults { entity { type`)
	for i := range issue1915URNAliases {
		fmt.Fprintf(&b, " u%02d: urn", i)
	}
	b.WriteString(" } } } }")
	return b.String()
}

const issue1915URNAliases = 20

// TestIssue1915_GraphQLQueryReturnsWhatFits: graphql_query past the budget
// returns the list's leading items that fit, as valid JSON, instead of
// withholding the data.
func TestIssue1915_GraphQLQueryReturnsWhatFits(t *testing.T) {
	requireGraphQLUpstream(t)
	name := issue1277Connect(t, connect(t), "budget-1915", nil)
	for form, variables := range map[string]any{"object": map[string]any{}, "string": "{}"} {
		t.Run("variables_as_"+form, func(t *testing.T) {
			c := connect(t)
			res, text, err := c.callRaw(issue1277QueryTool, map[string]any{
				"connection": name, "query": issue1915SearchDocument(), "variables": variables, "purpose": issue1915Purpose,
			})
			if err != nil || res.IsError {
				t.Fatalf("graphql_query: %v %s", err, text)
			}
			if len(text) > issue1915Budget {
				t.Fatalf("the result is %d characters; want it inside the %d budget", len(text), issue1915Budget)
			}
			var out struct {
				Data struct {
					Search struct {
						Results []map[string]any `json:"searchResults"`
					} `json:"searchAcrossEntities"`
				} `json:"data"`
				DataTruncated bool `json:"data_truncated"`
				DataItems     *struct {
					Path  string `json:"path"`
					Shown int    `json:"shown"`
					Total int    `json:"total"`
				} `json:"data_items"`
				ExportArguments map[string]any `json:"export_arguments"`
				Note            string         `json:"note"`
			}
			if err := json.Unmarshal([]byte(text), &out); err != nil {
				t.Fatalf("result is not JSON: %v", err)
			}
			if !out.DataTruncated || out.DataItems == nil {
				t.Fatalf("data_truncated=%v data_items=%v; want the list cut", out.DataTruncated, out.DataItems)
			}
			results := out.Data.Search.Results
			if len(results) == 0 || len(results) != out.DataItems.Shown || out.DataItems.Total <= len(results) || out.DataItems.Path != "searchAcrossEntities.searchResults" {
				t.Errorf("data_items = %+v with %d results shown; want the leading results of more, at searchAcrossEntities.searchResults", *out.DataItems, len(results))
			}
			for i, r := range results {
				entity, _ := r["entity"].(map[string]any)
				if entity["type"] == nil || entity[fmt.Sprintf("u%02d", issue1915URNAliases-1)] == nil {
					t.Fatalf("result %d = %v; want whole items", i, r)
				}
			}
			if out.ExportArguments["connection"] != name || !strings.Contains(out.Note, "graphql_export") {
				t.Errorf("export_arguments=%v note=%q; want the graphql_export call offered", out.ExportArguments, out.Note)
			}
		})
	}
}
