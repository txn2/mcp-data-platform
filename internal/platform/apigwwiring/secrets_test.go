package apigwwiring

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// TestWiredSecretsFillARequestThroughTheRealTool is #2051 assembled: the
// store wired onto the live toolkit, a real api_invoke_endpoint call over
// MCP naming {{secret:portal_password}}, the value read from the database
// row, received by the upstream, and redacted from the echo that comes back.
func TestWiredSecretsFillARequestThroughTheRealTool(t *testing.T) {
	got := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- string(body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"received": string(body)})
	}))
	t.Cleanup(upstream.Close)

	cfg, err := apigatewaykit.ParseConfig(map[string]any{"base_url": upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnectionName = "grid"
	tk := apigatewaykit.NewMulti(apigatewaykit.MultiConfig{DefaultName: "api", Instances: map[string]apigatewaykit.Config{"grid": cfg}})
	reg := registry.NewRegistry()
	if err := reg.Register(tk); err != nil {
		t.Fatal(err)
	}

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now()
	mock.ExpectQuery(`, value FROM gateway_secrets WHERE name`).WithArgs("portal_password").WillReturnRows(
		sqlmock.NewRows([]string{"name", "description", "allow_connections", "allow_personas", "created_by", "updated_by", "created_at", "updated_at", "value"}).
			AddRow("portal_password", "", "{grid}", "{}", "", "", now, now, "hunter22-pw"))

	Secrets(reg, nil, nil, "admin") // no database: nothing is wired
	Secrets(reg, db, nil, "admin")

	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	tk.RegisterTools(server)
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: apigatewaykit.ToolInvokeEndpoint, Arguments: map[string]any{
		"connection": "grid", "method": "POST", "path": "/session/1/element/2/value",
		"body": map[string]any{"text": "{{secret:portal_password}}"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	text, _ := json.Marshal(res)
	if res.IsError {
		t.Fatalf("the call failed: %s", text)
	}
	if received := <-got; received != `{"text":"hunter22-pw"}` {
		t.Errorf("the upstream received %q", received)
	}
	if strings.Contains(string(text), "hunter22-pw") || !strings.Contains(string(text), "[REDACTED:portal_password]") {
		t.Errorf("the result carries the value or no redaction: %s", text)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}
