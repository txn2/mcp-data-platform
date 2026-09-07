package graphql

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// relayPage renders one page of a Relay connection over the namespaced
// fixture's product query.
func relayPage(rows []string, cursor string, hasNext bool) string {
	edges := make([]string, 0, len(rows))
	for _, r := range rows {
		edges = append(edges, fmt.Sprintf(`{"node":{"_id":%q,"name":%q},"cursor":%q}`, r, r, r))
	}
	return fmt.Sprintf(
		`{"data":{"masterData":{"product":{"query":{"edges":[%s],"pageInfo":{"hasNextPage":%t,"endCursor":%q},"totalCount":9}}}}}`,
		strings.Join(edges, ","), hasNext, cursor)
}

const pagedDocument = `query P($after: String) { masterData { product { query(after: $after) { edges { node { _id name } cursor } pageInfo { hasNextPage endCursor } totalCount } } } }`

func TestPaginateWalksARelayConnectionByDefault(t *testing.T) {
	u := newUpstream(t)
	u.respond = func(_ graphQLRequest, callNo int) (int, string) {
		switch callNo {
		case 1:
			return http.StatusOK, relayPage([]string{"a", "b"}, "c1", true)
		case 2:
			return http.StatusOK, relayPage([]string{"c"}, "c2", false)
		default:
			t.Errorf("the walk asked for a page past the end (call %d)", callNo)
			return http.StatusOK, relayPage(nil, "", false)
		}
	}
	tk := newToolkit(t, u, "namespaced", nil)

	out := callQuery(t, tk, QueryInput{
		Connection: "gql", Query: pagedDocument,
		Paginate: &PaginateInput{
			Items:          "masterData.product.query.edges",
			CursorVariable: "after",
		},
	})

	if out.Pagination == nil {
		t.Fatal("no pagination report")
	}
	if out.Pagination.PagesFetched != 2 || out.Pagination.ItemsMerged != 3 {
		t.Errorf("report = %+v", out.Pagination)
	}
	if out.Pagination.StoppedBy != StoppedByEnd || out.Pagination.NextCursor != "" {
		t.Errorf("report = %+v; the upstream said there was no next page", out.Pagination)
	}
	// The caller reads one page's shape holding every row.
	var body struct {
		MasterData struct {
			Product struct {
				Query struct {
					Edges      []json.RawMessage `json:"edges"`
					TotalCount int               `json:"totalCount"`
				} `json:"query"`
			} `json:"product"`
		} `json:"masterData"`
	}
	if err := json.Unmarshal(out.Data, &body); err != nil {
		t.Fatalf("merged data: %v\n%s", err, out.Data)
	}
	if len(body.MasterData.Product.Query.Edges) != 3 {
		t.Errorf("merged edges = %d", len(body.MasterData.Product.Query.Edges))
	}
	if body.MasterData.Product.Query.TotalCount != 9 {
		t.Error("the rest of the page's shape was not preserved")
	}
	// The cursor goes back as a variable, so the document is unchanged.
	calls := u.calls()
	if calls[0].Variables["after"] != nil {
		t.Errorf("the first page carried a cursor: %v", calls[0].Variables)
	}
	if calls[1].Variables["after"] != "c1" {
		t.Errorf("the second page's cursor = %v", calls[1].Variables["after"])
	}
	if calls[1].Query != calls[0].Query {
		t.Error("the document changed between pages")
	}
}

func TestPaginateFollowsANamedCursorPathForANonRelayUpstream(t *testing.T) {
	// A scroll-style API: no pageInfo, a bare next cursor beside the
	// results, and the end signaled by that cursor going away.
	u := newUpstream(t)
	u.respond = func(_ graphQLRequest, callNo int) (int, string) {
		if callNo == 1 {
			return http.StatusOK, `{"data":{"search":{"total":2,"results":[{"__typename":"Dataset","urn":"a"}],"nextScrollId":"s1"}}}`
		}
		return http.StatusOK, `{"data":{"search":{"total":2,"results":[{"__typename":"Dataset","urn":"b"}],"nextScrollId":null}}}`
	}
	tk := newToolkit(t, u, "flat", nil)

	out := callQuery(t, tk, QueryInput{
		Connection: "gql",
		Query:      `query S($input: SearchInput!) { search(input: $input) { total results { __typename ... on Dataset { urn } } nextScrollId } }`,
		Variables:  jsonRaw(t, map[string]any{"input": map[string]any{"query": "x"}}),
		Paginate: &PaginateInput{
			Items:          "search.results",
			CursorVariable: "scrollId",
			NextCursorPath: "search.nextScrollId",
		},
	})

	if out.Pagination.PagesFetched != 2 || out.Pagination.ItemsMerged != 2 {
		t.Errorf("report = %+v", out.Pagination)
	}
	if u.calls()[1].Variables["scrollId"] != "s1" {
		t.Errorf("the cursor was not fed back: %v", u.calls()[1].Variables)
	}
}

func TestPaginateStopsAtItsPageBoundAndHandsBackTheCursor(t *testing.T) {
	u := newUpstream(t)
	u.respond = func(_ graphQLRequest, callNo int) (int, string) {
		return http.StatusOK, relayPage([]string{fmt.Sprint(callNo)}, fmt.Sprintf("c%d", callNo), true)
	}
	tk := newToolkit(t, u, "namespaced", nil)

	out := callQuery(t, tk, QueryInput{
		Connection: "gql", Query: pagedDocument,
		Paginate: &PaginateInput{
			Items: "masterData.product.query.edges", CursorVariable: "after", MaxPages: 2,
		},
	})

	if out.Pagination.PagesFetched != 2 || out.Pagination.StoppedBy != StoppedByMaxPages {
		t.Fatalf("report = %+v", out.Pagination)
	}
	if out.Pagination.NextCursor != "c2" {
		t.Errorf("next cursor = %q; the walk must be continuable", out.Pagination.NextCursor)
	}
}

func TestPaginateStopsOnAPageTheUpstreamRefused(t *testing.T) {
	u := newUpstream(t)
	u.respond = func(_ graphQLRequest, callNo int) (int, string) {
		if callNo == 1 {
			return http.StatusOK, relayPage([]string{"a"}, "c1", true)
		}
		return http.StatusOK, `{"errors":[{"message":"rate limited"}]}`
	}
	tk := newToolkit(t, u, "namespaced", nil)

	out := callQuery(t, tk, QueryInput{
		Connection: "gql", Query: pagedDocument,
		Paginate: &PaginateInput{Items: "masterData.product.query.edges", CursorVariable: "after"},
	})

	if out.Pagination.StoppedBy != StoppedByError {
		t.Errorf("report = %+v", out.Pagination)
	}
	if !out.UpstreamError || len(out.Errors) != 1 {
		t.Errorf("the refusal was not reported: %+v", out)
	}
}

func TestPaginateRefusesAWalkItCannotPerform(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(relayPage([]string{"a"}, "", false))
	tk := newToolkit(t, u, "namespaced", nil)
	cases := []struct {
		name string
		in   PaginateInput
		want string
	}{
		{"no items path", PaginateInput{CursorVariable: "after"}, "paginate.items is required"},
		{"no cursor variable", PaginateInput{Items: "a.b"}, "paginate.cursor_variable is required"},
		{"absurd page bound", PaginateInput{Items: "a.b", CursorVariable: "after", MaxPages: 99999}, "max_pages"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := c.in
			msg := refuseQuery(t, tk, QueryInput{Connection: "gql", Query: pagedDocument, Paginate: &in})
			if !strings.Contains(msg, c.want) {
				t.Errorf("msg = %q; want %q", msg, c.want)
			}
		})
	}
}

func TestPaginateRefusesAPathThatIsNotWhereItSays(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", nil)
	cases := []struct {
		name, body, items, want string
	}{
		{"no data at all", `{"data":null}`, "masterData.product.query.edges", "no data to paginate"},
		{"a missing segment", relayPage(nil, "", false), "masterData.absent.edges", "not in the response data"},
		{"a scalar in the middle", `{"data":{"masterData":"text"}}`, "masterData.product.edges", "not an object"},
		{"the path is not an array", relayPage(nil, "", false), "masterData.product.query.pageInfo", "not an array"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u.respond = answer(c.body)
			msg := refuseQuery(t, tk, QueryInput{
				Connection: "gql", Query: pagedDocument,
				Paginate: &PaginateInput{Items: c.items, CursorVariable: "after"},
			})
			if !strings.Contains(msg, c.want) {
				t.Errorf("msg = %q; want %q", msg, c.want)
			}
		})
	}
}

func TestPaginateStopsWhenTheUpstreamSaysThereIsNoNextPage(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"hasNextPage is false", relayPage([]string{"a"}, "c1", false)},
		{"the cursor is null", relayPage([]string{"a"}, "", true)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := newUpstream(t)
			u.respond = answer(c.body)
			tk := newToolkit(t, u, "namespaced", nil)
			out := callQuery(t, tk, QueryInput{
				Connection: "gql", Query: pagedDocument,
				Paginate: &PaginateInput{Items: "masterData.product.query.edges", CursorVariable: "after"},
			})
			if out.Pagination.PagesFetched != 1 || out.Pagination.StoppedBy != StoppedByEnd {
				t.Errorf("report = %+v", out.Pagination)
			}
		})
	}
}

func TestPaginateDoesNotMutateTheCallersVariables(t *testing.T) {
	original := map[string]any{"after": "seed"}
	copied := copyVars(original)
	copied["after"] = "changed"
	if original["after"] != "seed" {
		t.Error("the walk wrote through to the caller's variables")
	}
}

func TestSplitPathIgnoresStrayDots(t *testing.T) {
	if got := splitPath(" a . . b "); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("splitPath = %v", got)
	}
	if got := splitPath(""); got != nil {
		t.Errorf("splitPath(\"\") = %v", got)
	}
}

// TestPaginateDoesNotReportAStaleCursorWhenTheWalkFinishedAtItsBound is the
// case the two stopping conditions coincide on: the last page the bound
// allowed was also the last page there was. Reporting max_pages with the
// cursor of a page already fetched would send a caller round again for rows
// they already have.
func TestPaginateDoesNotReportAStaleCursorWhenTheWalkFinishedAtItsBound(t *testing.T) {
	u := newUpstream(t)
	u.respond = func(_ graphQLRequest, callNo int) (int, string) {
		if callNo == 1 {
			return http.StatusOK, relayPage([]string{"a"}, "c1", true)
		}
		return http.StatusOK, relayPage([]string{"b"}, "", false)
	}
	tk := newToolkit(t, u, "namespaced", nil)

	out := callQuery(t, tk, QueryInput{
		Connection: "gql", Query: pagedDocument,
		Paginate: &PaginateInput{
			Items: "masterData.product.query.edges", CursorVariable: "after", MaxPages: 2,
		},
	})

	if out.Pagination.StoppedBy != StoppedByEnd {
		t.Errorf("stopped_by = %q; the upstream said there was no next page", out.Pagination.StoppedBy)
	}
	if out.Pagination.NextCursor != "" {
		t.Errorf("next_cursor = %q; there is no next page to continue from", out.Pagination.NextCursor)
	}
	if out.Pagination.ItemsMerged != 2 {
		t.Errorf("items_merged = %d", out.Pagination.ItemsMerged)
	}
}
