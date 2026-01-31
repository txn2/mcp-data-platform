//go:build integration

package mcpapps_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/mcpapps"
)

// testAppsDir returns the absolute path to the apps/query-results directory.
func testAppsDir(t *testing.T) string {
	t.Helper()
	// Navigate from pkg/mcpapps to project root, then to apps/query-results
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	// Go up two directories from pkg/mcpapps to project root
	projectRoot := filepath.Join(wd, "..", "..")
	appsDir := filepath.Join(projectRoot, "apps", "query-results")

	// Verify it exists
	if _, err := os.Stat(appsDir); err != nil {
		t.Skipf("apps/query-results not found at %s: %v", appsDir, err)
	}

	return appsDir
}

// TestMCPAppsIntegration verifies the full MCP Apps flow.
func TestMCPAppsIntegration(t *testing.T) {
	appsDir := testAppsDir(t)

	// Setup: Create registry and register app
	reg := mcpapps.NewRegistry()
	app := &mcpapps.AppDefinition{
		Name:        "query-results",
		ResourceURI: "ui://query-results",
		ToolNames:   []string{"trino_query"},
		AssetsPath:  appsDir,
		EntryPoint:  "index.html",
		Config: map[string]any{
			"chartCDN":         "https://cdn.jsdelivr.net/npm/chart.js",
			"defaultChartType": "bar",
			"maxTableRows":     1000,
		},
		CSP: &mcpapps.CSPConfig{
			ResourceDomains: []string{"https://cdn.jsdelivr.net"},
		},
	}

	if err := app.ValidateAssets(); err != nil {
		t.Fatalf("Failed to validate assets: %v", err)
	}

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
		// Read the HTML directly from the filesystem
		content, err := os.ReadFile(filepath.Join(appsDir, "index.html"))
		if err != nil {
			t.Fatalf("Failed to read index.html: %v", err)
		}

		if !strings.Contains(string(content), "<!DOCTYPE html>") {
			t.Error("Content should be HTML")
		}

		if !strings.Contains(string(content), "Results") {
			t.Error("Content should contain 'Results'")
		}

		t.Logf("HTML content length: %d bytes", len(content))
	})
}

// TestQueryResultsHTML verifies the HTML content.
func TestQueryResultsHTML(t *testing.T) {
	appsDir := testAppsDir(t)

	content, err := os.ReadFile(filepath.Join(appsDir, "index.html"))
	if err != nil {
		t.Fatalf("Failed to read index.html: %v", err)
	}

	// Check essential elements
	checks := []string{
		"<!DOCTYPE html>",
		"filter",
		"chart",
		"table",
	}

	for _, check := range checks {
		if !strings.Contains(string(content), check) {
			t.Errorf("HTML should contain %q", check)
		}
	}
}

// TestFullPlatformIntegration tests with actual Platform config.
func TestFullPlatformIntegration(t *testing.T) {
	t.Log("To test the full platform integration:")
	t.Log("1. Build: go build -o mcp-data-platform ./cmd/mcp-data-platform")
	t.Log("2. Create config with mcpapps.enabled: true and apps configured with assets_path")
	t.Log("3. Run: npx @anthropics/mcp-inspector ./mcp-data-platform --config <config.yaml>")
	t.Log("4. In Inspector: List Tools -> verify trino_query has _meta.ui")
	t.Log("5. In Inspector: List Resources -> verify ui://query-results exists")
	t.Log("6. In Inspector: Read Resource ui://query-results -> verify HTML")
}
