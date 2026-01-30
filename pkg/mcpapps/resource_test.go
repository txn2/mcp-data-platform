package mcpapps

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCreateResourceHandler(t *testing.T) {
	app := &AppDefinition{
		Name:        "test-app",
		ResourceURI: "ui://test-app",
		ToolNames:   []string{"test_tool"},
		Assets:      testAssets,
		AssetsRoot:  "testdata",
		EntryPoint:  "index.html",
	}

	handler := createResourceHandler(app)

	t.Run("serves entry point", func(t *testing.T) {
		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{
				URI: "ui://test-app",
			},
		}

		result, err := handler(context.Background(), req)
		if err != nil {
			t.Fatalf("Handler returned error: %v", err)
		}

		if len(result.Contents) != 1 {
			t.Fatalf("Expected 1 content, got %d", len(result.Contents))
		}

		content := result.Contents[0]
		// Entry point HTML should use MCP App profile MIME type
		if content.MIMEType != mcpAppMIMEType {
			t.Errorf("MIME type = %q, want %q", content.MIMEType, mcpAppMIMEType)
		}

		if !strings.Contains(content.Text, "<html>") {
			t.Error("Content should contain HTML")
		}
	})

	t.Run("serves specific file", func(t *testing.T) {
		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{
				URI: "ui://test-app/style.css",
			},
		}

		result, err := handler(context.Background(), req)
		if err != nil {
			t.Fatalf("Handler returned error: %v", err)
		}

		content := result.Contents[0]
		if content.MIMEType != "text/css; charset=utf-8" {
			t.Errorf("MIME type = %q, want %q", content.MIMEType, "text/css; charset=utf-8")
		}

		if !strings.Contains(content.Text, "color") {
			t.Error("Content should contain CSS")
		}
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{
				URI: "ui://test-app/nonexistent.txt",
			},
		}

		_, err := handler(context.Background(), req)
		if err == nil {
			t.Error("Expected error for missing file")
		}
	})
}

func TestCreateResourceHandler_ConfigInjection(t *testing.T) {
	type testConfig struct {
		ChartCDN string `json:"chartCDN"`
		MaxRows  int    `json:"maxRows"`
	}

	app := &AppDefinition{
		Name:        "test-app",
		ResourceURI: "ui://test-app",
		ToolNames:   []string{"test_tool"},
		Assets:      testAssets,
		AssetsRoot:  "testdata",
		EntryPoint:  "index.html",
		Config: testConfig{
			ChartCDN: "https://cdn.example.com/chart.js",
			MaxRows:  500,
		},
	}

	handler := createResourceHandler(app)

	req := &mcp.ReadResourceRequest{
		Params: &mcp.ReadResourceParams{
			URI: "ui://test-app",
		},
	}

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}

	content := result.Contents[0].Text

	// Check that config is injected
	if !strings.Contains(content, "app-config") {
		t.Error("Config script tag should be present")
	}

	if !strings.Contains(content, "https://cdn.example.com/chart.js") {
		t.Error("Config values should be injected")
	}

	if !strings.Contains(content, "500") {
		t.Error("MaxRows should be in injected config")
	}
}

func TestExtractPath(t *testing.T) {
	tests := []struct {
		name       string
		requestURI string
		baseURI    string
		want       string
	}{
		{
			name:       "base URI only",
			requestURI: "ui://test-app",
			baseURI:    "ui://test-app",
			want:       "",
		},
		{
			name:       "with file path",
			requestURI: "ui://test-app/style.css",
			baseURI:    "ui://test-app",
			want:       "/style.css",
		},
		{
			name:       "with nested path",
			requestURI: "ui://test-app/assets/images/logo.png",
			baseURI:    "ui://test-app",
			want:       "/assets/images/logo.png",
		},
		{
			name:       "non-matching base",
			requestURI: "ui://other-app/file.txt",
			baseURI:    "ui://test-app",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPath(tt.requestURI, tt.baseURI)
			if got != tt.want {
				t.Errorf("extractPath(%q, %q) = %q, want %q", tt.requestURI, tt.baseURI, got, tt.want)
			}
		})
	}
}

func TestInjectConfig(t *testing.T) {
	t.Run("injects before </head>", func(t *testing.T) {
		content := []byte(`<!DOCTYPE html><html><head><title>Test</title></head><body></body></html>`)
		config := map[string]string{"key": "value"}

		result := injectConfig(content, config)

		if !strings.Contains(string(result), `<script id="app-config"`) {
			t.Error("Config script should be injected")
		}

		// Verify it's before </head>
		idx := strings.Index(string(result), `<script id="app-config"`)
		headIdx := strings.Index(string(result), `</head>`)
		if idx > headIdx {
			t.Error("Config should be injected before </head>")
		}
	})

	t.Run("replaces existing app-config", func(t *testing.T) {
		content := []byte(`<html><head><script id="app-config" type="application/json">{}</script></head></html>`)
		config := map[string]string{"newkey": "newvalue"}

		result := injectConfig(content, config)

		if strings.Count(string(result), `app-config`) != 1 {
			t.Error("Should have exactly one app-config script")
		}

		if !strings.Contains(string(result), "newkey") {
			t.Error("New config should be present")
		}
	})

	t.Run("handles nil config", func(t *testing.T) {
		content := []byte(`<html><head></head></html>`)
		result := injectConfig(content, nil)

		// Should return unchanged
		if string(result) != string(content) {
			t.Error("Content should be unchanged with nil config")
		}
	})
}

func TestIsBinaryMIME(t *testing.T) {
	tests := []struct {
		mimeType string
		want     bool
	}{
		{"text/html", false},
		{"text/html; charset=utf-8", false},
		{"text/css", false},
		{"text/plain", false},
		{"application/javascript", false},
		{"application/javascript; charset=utf-8", false},
		{"application/json", false},
		{"application/xml", false},
		{"image/svg+xml", false},
		{"image/png", true},
		{"image/jpeg", true},
		{"image/gif", true},
		{"font/woff2", true},
		{"application/octet-stream", true},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			got := isBinaryMIME(tt.mimeType)
			if got != tt.want {
				t.Errorf("isBinaryMIME(%q) = %v, want %v", tt.mimeType, got, tt.want)
			}
		})
	}
}
