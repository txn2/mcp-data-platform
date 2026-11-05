package apigateway

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/upstreamwarn"
)

// #2485/#2486: a call is filed under its operation id, or under its method and
// template when the spec gives the operation none, never the concrete path.
func TestOperationKey(t *testing.T) {
	tk := newResolverTestToolkit(t, "crm", "/api")
	c := tk.connections["crm"]
	ops, _ := buildOperationIndex(c.specs["users"].doc, "users", "/api")
	c.operations = ops
	c.cfg.CatalogID = "users-catalog"
	ctx := context.Background()

	for _, tc := range []struct{ method, path, opID, want string }{
		{"", "", "getUser", "getUser"},
		{"", "", "acc-sekret-not-an-operation", ""}, // a caller's string the catalog does not declare
		{"get", "/api/users/42", "", "getUser"},
		{"GET", "/api/widgets", "", "GET /widgets"}, // the spec's path, as a change report names it
		{"DELETE", "/api/nothing", "", ""},
	} {
		if got := tk.OperationKey(ctx, "crm", tc.method, tc.path, tc.opID); got != tc.want {
			t.Errorf("%s %s %q: got %q, want %q", tc.method, tc.path, tc.opID, got, tc.want)
		}
	}
	if got := tk.OperationKey(ctx, "missing", "GET", "/api/users", ""); got != "" {
		t.Errorf("an unknown connection: %q", got)
	}
	if got := tk.CatalogOf(ctx, "crm"); got != "users-catalog" {
		t.Errorf("CatalogOf: %q", got)
	}
	if got := tk.CatalogOf(ctx, "missing"); got != "" {
		t.Errorf("CatalogOf an unknown connection: %q", got)
	}
	marked := withOperation(ctx, c, InvokeInput{Method: "GET", Path: "/api/users/7"}, "")
	if got := upstreamwarn.Operation(marked); got != "getUser" {
		t.Errorf("the request is marked %q", got)
	}
}
