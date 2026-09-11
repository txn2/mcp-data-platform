package graphql

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// auditMeta reads the verdict a result carries for the audit middleware.
func auditMeta(t *testing.T, res *mcp.CallToolResult) (outcome, message string) {
	t.Helper()
	if res.Meta == nil {
		t.Fatal("the result carries no _meta; the audit middleware would record the call as a success")
	}
	outcome, _ = res.Meta[observability.MetaAuditOutcome].(string)
	message, _ = res.Meta[observability.MetaAuditOutcomeMessage].(string)
	return outcome, message
}

// TestAnErrorsArrayInAnHTTP200IsAuditedAsAFailure is #1678: the result
// already said upstream_error, and the audit row said success. The
// verdict on _meta is what the audit middleware, and through it the
// call catalog, classify on.
func TestAnErrorsArrayInAnHTTP200IsAuditedAsAFailure(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":null,"errors":[{"message":"Cannot query field \"population\" on type \"Continent\""},{"message":"second"}]}`)
	tk := newToolkit(t, u, "flat", nil)

	res, _, err := tk.handleQuery(context.Background(), nil, QueryInput{Connection: "gql", Query: datasetDocument})
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	if res.IsError {
		t.Fatal("a proxied upstream failure is not a tool error; the gateway's wire contract keeps IsError for the platform's own failures")
	}
	outcome, message := auditMeta(t, res)
	if outcome != observability.OutcomeUpstreamError {
		t.Errorf("audit_outcome = %q; want %q", outcome, observability.OutcomeUpstreamError)
	}
	if want := `Cannot query field "population" on type "Continent" (and 1 more)`; message != want {
		t.Errorf("audit_outcome_message = %q; want %q", message, want)
	}
}

// TestANon2xxIsAuditedByItsStatusClass keeps the categories the api
// gateway's rows use for a status-line failure, naming the upstream's
// own error when the body carried one and the status text otherwise.
func TestANon2xxIsAuditedByItsStatusClass(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		body        string
		wantOutcome string
		wantMessage string
	}{
		{"502 html page", http.StatusBadGateway, "<html>gateway timeout</html>", observability.OutcomeUpstream5xx, "Bad Gateway"},
		{"401 with a GraphQL body", http.StatusUnauthorized, `{"errors":[{"message":"token expired"}]}`, observability.OutcomeUpstream4xx, "token expired"},
		{"429 with an empty body", http.StatusTooManyRequests, "", observability.OutcomeUpstream4xx, "Too Many Requests"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := newUpstream(t)
			u.respond = func(graphQLRequest, int) (int, string) { return tc.status, tc.body }
			tk := newToolkit(t, u, "flat", nil)
			res, _, err := tk.handleQuery(context.Background(), nil, QueryInput{Connection: "gql", Query: datasetDocument})
			if err != nil {
				t.Fatalf("handleQuery: %v", err)
			}
			outcome, message := auditMeta(t, res)
			if outcome != tc.wantOutcome || message != tc.wantMessage {
				t.Errorf("verdict = (%q, %q); want (%q, %q)", outcome, message, tc.wantOutcome, tc.wantMessage)
			}
		})
	}
}

// TestASuccessfulCallIsStampedOK: every row carries a category, as the
// gateway's do, and ok is the one the middleware does not override on.
func TestASuccessfulCallIsStampedOK(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":{"urn":"u1","name":"orders"}}}`)
	tk := newToolkit(t, u, "flat", nil)
	res, _, err := tk.handleQuery(context.Background(), nil, QueryInput{Connection: "gql", Query: datasetDocument})
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	outcome, message := auditMeta(t, res)
	if outcome != observability.OutcomeOK || message != "" {
		t.Errorf("verdict = (%q, %q); want (ok, \"\")", outcome, message)
	}
}

// TestAWalkHaltedByAnErrorsPageIsAuditedAsAFailure: a page the upstream
// refused ends the walk with that page's verdict, and the one call is
// recorded on it.
func TestAWalkHaltedByAnErrorsPageIsAuditedAsAFailure(t *testing.T) {
	u := newUpstream(t)
	u.respond = func(_ graphQLRequest, callNo int) (int, string) {
		if callNo == 1 {
			return http.StatusOK, relayPage([]string{"a"}, "c1", true)
		}
		return http.StatusOK, `{"errors":[{"message":"cursor expired"}]}`
	}
	tk := newToolkit(t, u, "namespaced", nil)
	res, _, err := tk.handleQuery(context.Background(), nil, QueryInput{
		Connection: "gql", Query: pagedDocument,
		Paginate: &PaginateInput{Items: "masterData.product.query.edges", CursorVariable: "after"},
	})
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	outcome, message := auditMeta(t, res)
	if outcome != observability.OutcomeUpstreamError || message != "cursor expired" {
		t.Errorf("verdict = (%q, %q); want (upstream_error, cursor expired)", outcome, message)
	}
}

// TestExportIsAuditedOnTheSameVerdict: graphql_export runs the same
// document through the same send, and api_export is audited on its
// upstream's answer, so an export whose result is an errors array is a
// failed call in the catalog too.
func TestExportIsAuditedOnTheSameVerdict(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":null,"errors":[{"message":"denied"}]}`)
	tk, _, _ := exportToolkit(t, u, "flat")
	res, _, err := tk.handleExport(context.Background(), nil, exportInput{
		Connection: "gql", Query: datasetDocument, Name: "denied.json",
	})
	if err != nil {
		t.Fatalf("handleExport: %v", err)
	}
	outcome, message := auditMeta(t, res)
	if outcome != observability.OutcomeUpstreamError || message != "denied" {
		t.Errorf("verdict = (%q, %q); want (upstream_error, denied)", outcome, message)
	}
	var out exportOutput
	decodeResult(t, res, &out)
	if !out.UpstreamError || out.Status != http.StatusOK {
		t.Errorf("out = %+v; the export must say what the audit row says", out)
	}
	if out.AssetID == "" {
		t.Error("the errors were not written to an asset; a caller who exported a failure should be able to read it")
	}
}

func TestClassifyUpstreamBoundsAndNamesTheMessage(t *testing.T) {
	long := strings.Repeat("x", maxAuditMessageChars+10)
	cases := []struct {
		name   string
		status int
		errs   []Error
		failed bool
		want   auditVerdict
	}{
		{"not failed ignores errors", 200, []Error{{Message: "ignored"}}, false, auditVerdict{outcome: "ok"}},
		{
			"long message is cut", 200,
			[]Error{{Message: long}},
			true,
			auditVerdict{outcome: "upstream_error", message: strings.Repeat("x", maxAuditMessageChars) + "..."},
		},
		{
			"empty message is named", 200,
			[]Error{{}},
			true,
			auditVerdict{outcome: "upstream_error", message: "the endpoint reported an error without a message"},
		},
		{"unnamed status", 299, nil, true, auditVerdict{outcome: "upstream_error", message: "HTTP 299"}},
		{"redirect answer", http.StatusFound, nil, true, auditVerdict{outcome: "upstream_error", message: "Found"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyUpstream(tc.status, tc.errs, tc.failed); got != tc.want {
				t.Errorf("classifyUpstream = %+v; want %+v", got, tc.want)
			}
		})
	}
}
