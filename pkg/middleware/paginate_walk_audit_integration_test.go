package middleware_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	apigateway "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// TestPaginateWalk_OneCallOneAuditRowCarryingThePageCount is the issue
// #1535 acceptance through the real chain: a live mcp.Server with the
// tool-call and audit middleware and the real api-gateway toolkit, one
// api_invoke_endpoint call with a paginate block against a three-page
// cursor upstream. The one call yields the merged array, and the ONE
// audit row it produces carries pages_fetched and stopped_by under
// parameters.result beside the paginate block it was called with.
func TestPaginateWalk_OneCallOneAuditRowCarryingThePageCount(t *testing.T) {
	const pages = 3
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if c := r.URL.Query().Get("cursor"); c != "" {
			page, _ = strconv.Atoi(c)
		}
		body := map[string]any{"data": []map[string]any{{"id": page}}}
		if page < pages {
			body["next_cursor"] = strconv.Itoa(page + 1)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer upstream.Close()

	tk := apigateway.New("api")
	if err := tk.AddConnection("vendor", map[string]any{"base_url": upstream.URL}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "walk-audit", Version: "v0"}, nil)
	tk.RegisterTools(server)

	logger := &recordingAuditLogger{}
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(logger))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&fakeAuthn{user: &middleware.UserInfo{UserID: "u1", Email: "u1@example.com", Roles: []string{"analyst"}}},
		&fakeAuthz{persona: "analyst"},
		&fakeLookup{kind: "api", name: "api", conn: "vendor"},
		middleware.ToolCallConfig{AdminPersona: "admin"},
	))

	ctx := context.Background()
	sess := mustConnect(ctx, t, server)
	defer func() { _ = sess.Close() }()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name: apigateway.ToolInvokeEndpoint,
		Arguments: map[string]any{
			"connection": "vendor", "method": "GET", "path": "/v1/items",
			"paginate": map[string]any{"items": "data", "cursor_param": "cursor"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("walk returned an error result: %v", res.Content)
	}
	var out struct {
		Body         []map[string]any `json:"body"`
		PagesFetched int              `json:"pages_fetched"`
		StoppedBy    string           `json:"stopped_by"`
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T", res.Content[0])
	}
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(out.Body) != pages || out.PagesFetched != pages || out.StoppedBy != "end" {
		t.Fatalf("result = %+v; want %d merged items over %d pages", out, pages, pages)
	}

	ev, ok := waitForAuditEvent(logger, apigateway.ToolInvokeEndpoint, time.Second)
	if !ok {
		t.Fatalf("no audit event for the walk")
	}
	if n := len(logger.Events()); n != 1 {
		t.Fatalf("audit rows = %d; a walk is one call and one row", n)
	}
	facts, _ := ev.Parameters["result"].(map[string]any)
	if fmt.Sprint(facts["pages_fetched"]) != strconv.Itoa(pages) || facts["stopped_by"] != "end" {
		t.Errorf("parameters.result = %v; want pages_fetched %d, stopped_by end", facts, pages)
	}
	if _, has := ev.Parameters["paginate"]; !has {
		t.Errorf("parameters lack the paginate block the call was made with: %v", ev.Parameters)
	}
	if !ev.Success {
		t.Errorf("audit row reports failure: %q", ev.ErrorMessage)
	}
}
