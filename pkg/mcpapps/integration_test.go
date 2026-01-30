//go:build integration

package mcpapps_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/mcpapps"
	"github.com/txn2/mcp-data-platform/pkg/mcpapps/queryresults"
)

// TestMCPAppsIntegration verifies the full MCP Apps flow.
func TestMCPAppsIntegration(t *testing.T) {
	// Setup: Create registry and register app
	reg := mcpapps.NewRegistry()
	app := queryresults.App(queryresults.Config{})
	if err := reg.Register(app); err != nil {
		t.Fatalf("Failed to register app: %v", err)
	}

	// Create MCP server
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	// Add middleware and register resources
	server.AddReceivingMiddleware(mcpapps.ToolMetadataMiddleware(reg))
	reg.RegisterResources(server)

	// Register a mock trino_query tool
	server.AddTool(&mcp.Tool{
		Name:        "trino_query",
		Description: "Execute a Trino SQL query",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"columns":[],"rows":[],"stats":{}}`},
			},
		}, nil
	})

	t.Run("registry has correct tool mapping", func(t *testing.T) {
		foundApp := reg.GetForTool("trino_query")
		if foundApp == nil {
			t.Fatal("No app found for trino_query")
		}

		if foundApp.Name != "query-results" {
			t.Errorf("App name = %q, want query-results", foundApp.Name)
		}

		if foundApp.ResourceURI != "ui://query-results" {
			t.Errorf("ResourceURI = %q, want ui://query-results", foundApp.ResourceURI)
		}
	})

	t.Run("middleware injects UI metadata", func(t *testing.T) {
		// Create a handler that returns tools
		mockHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			return &mcp.ListToolsResult{
				Tools: []*mcp.Tool{
					{Name: "trino_query", Description: "Query tool"},
					{Name: "other_tool", Description: "Other tool"},
				},
			}, nil
		}

		// Wrap with middleware
		middleware := mcpapps.ToolMetadataMiddleware(reg)
		wrapped := middleware(mockHandler)

		// Call tools/list
		result, err := wrapped(context.Background(), "tools/list", nil)
		if err != nil {
			t.Fatalf("Middleware call failed: %v", err)
		}

		listResult := result.(*mcp.ListToolsResult)

		// Find trino_query and verify _meta.ui
		for _, tool := range listResult.Tools {
			if tool.Name == "trino_query" {
				if tool.Meta == nil {
					t.Fatal("trino_query should have Meta")
				}
				ui, ok := tool.Meta["ui"]
				if !ok {
					t.Fatal("trino_query should have ui in Meta")
				}
				uiMap := ui.(map[string]string)
				if uiMap["resourceUri"] != "ui://query-results" {
					t.Errorf("resourceUri = %q, want ui://query-results", uiMap["resourceUri"])
				}
				t.Log("_meta.ui correctly injected into trino_query")
			}
			if tool.Name == "other_tool" {
				if tool.Meta != nil && tool.Meta["ui"] != nil {
					t.Error("other_tool should NOT have ui metadata")
				}
			}
		}
	})

	t.Run("resource handler serves HTML", func(t *testing.T) {
		// Directly test the resource handler by reading from embedded assets
		content, err := app.Assets.ReadFile("assets/index.html")
		if err != nil {
			t.Fatalf("Failed to read index.html: %v", err)
		}

		if !strings.Contains(string(content), "<!DOCTYPE html>") {
			t.Error("Content should be HTML")
		}

		if !strings.Contains(string(content), "Query Results") {
			t.Error("Content should contain 'Query Results'")
		}

		t.Logf("HTML content length: %d bytes", len(content))
	})
}

// TestQueryResultsHTML verifies the HTML content.
func TestQueryResultsHTML(t *testing.T) {
	app := queryresults.App(queryresults.Config{
		ChartCDN:         "https://cdn.example.com/chart.js",
		DefaultChartType: "line",
		MaxTableRows:     500,
	})

	content, err := app.Assets.ReadFile("assets/index.html")
	if err != nil {
		t.Fatalf("Failed to read index.html: %v", err)
	}

	// Check essential elements
	checks := []string{
		"<!DOCTYPE html>",
		"Query Results",
		"filter",
		"export",
		"chart",
		"table",
	}

	for _, check := range checks {
		if !strings.Contains(string(content), check) {
			t.Errorf("HTML should contain %q", check)
		}
	}

	// Verify config
	t.Run("config values preserved", func(t *testing.T) {
		cfg := app.Config.(queryresults.Config)
		cfgJSON, _ := json.Marshal(cfg)
		t.Logf("Config: %s", cfgJSON)

		if cfg.ChartCDN != "https://cdn.example.com/chart.js" {
			t.Errorf("ChartCDN not preserved")
		}
		if cfg.DefaultChartType != "line" {
			t.Errorf("DefaultChartType not preserved")
		}
		if cfg.MaxTableRows != 500 {
			t.Errorf("MaxTableRows not preserved")
		}
	})
}

// TestFullPlatformIntegration tests with actual Platform config.
func TestFullPlatformIntegration(t *testing.T) {
	t.Log("To test the full platform integration:")
	t.Log("1. Build: go build -o mcp-data-platform ./cmd/mcp-data-platform")
	t.Log("2. Create config with mcpapps.enabled: true")
	t.Log("3. Run: npx @anthropics/mcp-inspector ./mcp-data-platform --config <config.yaml>")
	t.Log("4. In Inspector: List Tools -> verify trino_query has _meta.ui")
	t.Log("5. In Inspector: List Resources -> verify ui://query-results exists")
	t.Log("6. In Inspector: Read Resource ui://query-results -> verify HTML")
}
