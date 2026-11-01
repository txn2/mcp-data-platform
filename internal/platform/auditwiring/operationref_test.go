package auditwiring

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// gatewayStub is an api gateway toolkit serving the named connections and
// matching one method and path to an operation id.
type gatewayStub struct{ conns []string }

func (gatewayStub) Kind() string                          { return "api" }
func (gatewayStub) Name() string                          { return "api" }
func (gatewayStub) Connection() string                    { return "" }
func (gatewayStub) RegisterTools(*mcp.Server)             {}
func (gatewayStub) Tools() []string                       { return nil }
func (gatewayStub) SetSemanticProvider(semantic.Provider) {}
func (gatewayStub) SetQueryProvider(query.Provider)       {}
func (gatewayStub) Close() error                          { return nil }

func (g gatewayStub) ListConnections() []toolkit.ConnectionDetail {
	out := make([]toolkit.ConnectionDetail, 0, len(g.conns))
	for _, c := range g.conns {
		out = append(out, toolkit.ConnectionDetail{Name: c})
	}
	return out
}

func (gatewayStub) ResolveOperationID(_ context.Context, connection, method, path string) string {
	if connection == "crm" && method == "GET" && path == "/v1/orders/7" {
		return "getOrder"
	}
	return ""
}

type toolkitList []registry.Toolkit

func (l toolkitList) All() []registry.Toolkit { return l }

// TestOperationRefsNameTheCatalogDataset is #2384: an api call's operation
// reference is the dataset the catalog scan names the operation by, from the
// operation id the call named or the one its method and path matched; a
// connection no toolkit serves, or a call matching no operation, names none.
func TestOperationRefsNameTheCatalogDataset(t *testing.T) {
	if operationRefs(nil) != nil {
		t.Fatal("without toolkits there is no builder")
	}
	ref := operationRefs(toolkitList{gatewayStub{conns: []string{"crm"}}})
	ctx := context.Background()
	for _, tc := range []struct {
		name, connection, method, path, operationID, want string
	}{
		{"named operation", "crm", "", "", "listOrders", "mcp:dataset:(api,crm,listOrders)"},
		{"matched by path", "crm", "GET", "/v1/orders/7", "", "mcp:dataset:(api,crm,getOrder)"},
		{"matches nothing", "crm", "GET", "/v1/unknown", "", ""},
		{"unknown connection", "gone", "", "", "listOrders", ""},
		{"nothing addressed", "crm", "", "", "", ""},
	} {
		if got := ref(ctx, tc.connection, tc.method, tc.path, tc.operationID); got != tc.want {
			t.Errorf("%s: ref = %q, want %q", tc.name, got, tc.want)
		}
	}
}
