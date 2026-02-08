package mcpapps

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const resTestMaxRows = 500

// assertCSPField checks that a CSP config map contains a key.
func assertCSPField(t *testing.T, csp map[string]any, key string) {
	t.Helper()
	if _, ok := csp[key]; !ok {
		t.Errorf("Expected %s in csp", key)
	}
}

func TestCreateResourceHandler(t *testing.T) {
	testdata := testdataDir(t)
	app := &AppDefinition{
		Name:        "test-app",
		ResourceURI: regTestResourceURI,
		ToolNames:   []string{"test_tool"},
		AssetsPath:  testdata,
		EntryPoint:  regTestEntryPoint,
	}

	handler := createResourceHandler(app)

	t.Run("serves entry point", func(t *testing.T) {
		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{
				URI: regTestResourceURI,
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
	testdata := testdataDir(t)
	type testConfig struct {
		ChartCDN string `json:"chartCDN"`
		MaxRows  int    `json:"maxRows"`
	}

	app := &AppDefinition{
		Name:        "test-app",
		ResourceURI: regTestResourceURI,
		ToolNames:   []string{"test_tool"},
		AssetsPath:  testdata,
		EntryPoint:  regTestEntryPoint,
		Config: testConfig{
			ChartCDN: "https://cdn.example.com/chart.js",
			MaxRows:  resTestMaxRows,
		},
	}

	handler := createResourceHandler(app)

	req := &mcp.ReadResourceRequest{
		Params: &mcp.ReadResourceParams{
			URI: regTestResourceURI,
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
			requestURI: regTestResourceURI,
			baseURI:    regTestResourceURI,
			want:       "",
		},
		{
			name:       "with file path",
			requestURI: "ui://test-app/style.css",
			baseURI:    regTestResourceURI,
			want:       "/style.css",
		},
		{
			name:       "with nested path",
			requestURI: "ui://test-app/assets/images/logo.png",
			baseURI:    regTestResourceURI,
			want:       "/assets/images/logo.png",
		},
		{
			name:       "non-matching base",
			requestURI: "ui://other-app/file.txt",
			baseURI:    regTestResourceURI,
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
		idx := bytes.Index(result, []byte(`<script id="app-config"`))
		headIdx := bytes.Index(result, []byte(`</head>`))
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
		if !bytes.Equal(result, content) {
			t.Error("Content should be unchanged with nil config")
		}
	})

	t.Run("handles missing head tag", func(t *testing.T) {
		content := []byte(`<html><body>No head</body></html>`)
		config := map[string]string{"key": "value"}

		result := injectConfig(content, config)

		// Should return unchanged when no </head> found
		if !bytes.Equal(result, content) {
			t.Error("Content should be unchanged when no </head> tag")
		}
	})

	t.Run("handles marshal error", func(t *testing.T) {
		content := []byte(`<html><head></head></html>`)
		// channels cannot be marshaled to JSON
		config := make(chan int)

		result := injectConfig(content, config)

		// Should return unchanged on marshal error
		if !bytes.Equal(result, content) {
			t.Error("Content should be unchanged on marshal error")
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

func TestRegisterResources(t *testing.T) {
	testdata := testdataDir(t)
	registry := NewRegistry()
	app := &AppDefinition{
		Name:        "test-app",
		ResourceURI: regTestResourceURI,
		ToolNames:   []string{"test_tool"},
		AssetsPath:  testdata,
		EntryPoint:  regTestEntryPoint,
	}
	_ = registry.Register(app)

	impl := &mcp.Implementation{Name: "test", Version: "1.0"}
	server := mcp.NewServer(impl, nil)
	registry.RegisterResources(server)

	// Verify resource was registered by checking the server has resources
	// The server's internal state is not directly accessible, but we can verify
	// the function runs without panic
}

func TestBuildResourceMeta(t *testing.T) {
	t.Run("returns nil when not entry point", func(t *testing.T) {
		app := &AppDefinition{
			EntryPoint: regTestEntryPoint,
			CSP:        &CSPConfig{},
		}
		meta := buildResourceMeta("other.html", app)
		if meta != nil {
			t.Error("Expected nil meta for non-entry point")
		}
	})

	t.Run("returns nil when CSP is nil", func(t *testing.T) {
		app := &AppDefinition{
			EntryPoint: regTestEntryPoint,
			CSP:        nil,
		}
		meta := buildResourceMeta(regTestEntryPoint, app)
		if meta != nil {
			t.Error("Expected nil meta when CSP is nil")
		}
	})

	t.Run("includes CSP domains", func(t *testing.T) {
		app := &AppDefinition{
			EntryPoint: regTestEntryPoint,
			CSP: &CSPConfig{
				ResourceDomains: []string{"https://cdn.example.com"},
				ConnectDomains:  []string{"https://api.example.com"},
				FrameDomains:    []string{"https://frame.example.com"},
			},
		}
		meta := buildResourceMeta(regTestEntryPoint, app)
		if meta == nil {
			t.Fatal("Expected non-nil meta")
		}

		ui, ok := meta["ui"].(map[string]any)
		if !ok {
			t.Fatal("Expected ui map in meta")
		}

		csp, ok := ui["csp"].(map[string]any)
		if !ok {
			t.Fatal("Expected csp map in ui")
		}

		assertCSPField(t, csp, "resourceDomains")
		assertCSPField(t, csp, "connectDomains")
		assertCSPField(t, csp, "frameDomains")
	})

	t.Run("includes permissions", func(t *testing.T) {
		app := &AppDefinition{
			EntryPoint: regTestEntryPoint,
			CSP: &CSPConfig{
				Permissions: &PermissionsConfig{
					ClipboardWrite: &struct{}{},
				},
			},
		}
		meta := buildResourceMeta(regTestEntryPoint, app)
		if meta == nil {
			t.Fatal("Expected non-nil meta")
		}

		ui, ok := meta["ui"].(map[string]any)
		if !ok {
			t.Fatal("Expected ui map in meta")
		}

		if _, ok := ui["permissions"]; !ok {
			t.Error("Expected permissions in ui")
		}
	})
}

func TestBuildCSPMeta(t *testing.T) {
	t.Run("empty CSP returns empty map", func(t *testing.T) {
		csp := &CSPConfig{}
		meta := buildCSPMeta(csp)
		if len(meta) != 0 {
			t.Errorf("Expected empty map, got %v", meta)
		}
	})

	t.Run("includes all domain types", func(t *testing.T) {
		csp := &CSPConfig{
			ResourceDomains: []string{"https://cdn.example.com"},
			ConnectDomains:  []string{"https://api.example.com"},
			FrameDomains:    []string{"https://frame.example.com"},
		}
		meta := buildCSPMeta(csp)

		if len(meta) != 3 {
			t.Errorf("Expected 3 entries, got %d", len(meta))
		}

		if _, ok := meta["resourceDomains"]; !ok {
			t.Error("Expected resourceDomains")
		}
		if _, ok := meta["connectDomains"]; !ok {
			t.Error("Expected connectDomains")
		}
		if _, ok := meta["frameDomains"]; !ok {
			t.Error("Expected frameDomains")
		}
	})

	t.Run("only includes non-empty domains", func(t *testing.T) {
		csp := &CSPConfig{
			ResourceDomains: []string{"https://cdn.example.com"},
		}
		meta := buildCSPMeta(csp)

		if len(meta) != 1 {
			t.Errorf("Expected 1 entry, got %d", len(meta))
		}

		if _, ok := meta["resourceDomains"]; !ok {
			t.Error("Expected resourceDomains")
		}
	})
}

func TestBuildResourceContents(t *testing.T) {
	t.Run("text content", func(t *testing.T) {
		content := buildResourceContents("ui://test", "text/html", []byte("<html></html>"), nil)
		if content.Text != "<html></html>" {
			t.Errorf("Expected text content, got %q", content.Text)
		}
		if len(content.Blob) != 0 {
			t.Error("Expected empty blob for text content")
		}
	})

	t.Run("binary content", func(t *testing.T) {
		content := buildResourceContents("ui://test", "image/png", []byte{0x89, 0x50, 0x4E, 0x47}, nil)
		if len(content.Blob) == 0 {
			t.Error("Expected blob for binary content")
		}
		if content.Text != "" {
			t.Error("Expected empty text for binary content")
		}
	})
}

func TestResolveMIMEType(t *testing.T) {
	t.Run("entry point uses MCP App MIME type", func(t *testing.T) {
		got := resolveMIMEType(regTestEntryPoint, regTestEntryPoint)
		if got != mcpAppMIMEType {
			t.Errorf("Expected %q, got %q", mcpAppMIMEType, got)
		}
	})

	t.Run("non-entry point uses file MIME type", func(t *testing.T) {
		got := resolveMIMEType("style.css", regTestEntryPoint)
		if got != "text/css; charset=utf-8" {
			t.Errorf("Expected CSS MIME type, got %q", got)
		}
	})
}

func TestResolveFilename(t *testing.T) {
	app := &AppDefinition{
		ResourceURI: regTestResourceURI,
		EntryPoint:  regTestEntryPoint,
	}

	t.Run("returns entry point for base URI", func(t *testing.T) {
		got := resolveFilename(regTestResourceURI, app)
		if got != regTestEntryPoint {
			t.Errorf("Expected index.html, got %q", got)
		}
	})

	t.Run("returns specific file for path", func(t *testing.T) {
		got := resolveFilename("ui://test-app/style.css", app)
		if got != "style.css" {
			t.Errorf("Expected style.css, got %q", got)
		}
	})
}

func TestReadAsset(t *testing.T) {
	testdata := testdataDir(t)
	app := &AppDefinition{
		AssetsPath: testdata,
		EntryPoint: regTestEntryPoint,
	}

	t.Run("reads existing file", func(t *testing.T) {
		content, err := readAsset(app, regTestEntryPoint)
		if err != nil {
			t.Fatalf("readAsset returned error: %v", err)
		}
		if len(content) == 0 {
			t.Error("Content should not be empty")
		}
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		_, err := readAsset(app, "nonexistent.html")
		if !errors.Is(err, ErrAssetNotFound) {
			t.Errorf("Expected ErrAssetNotFound, got %v", err)
		}
	})

	t.Run("prevents path traversal", func(t *testing.T) {
		_, err := readAsset(app, "../errors.go")
		if !errors.Is(err, ErrPathTraversal) {
			t.Errorf("Expected ErrPathTraversal, got %v", err)
		}
	})

	t.Run("prevents nested traversal", func(t *testing.T) {
		// Try accessing file with nested path traversal
		_, err := readAsset(app, "subdir/../../errors.go")
		if !errors.Is(err, ErrPathTraversal) {
			t.Errorf("Expected ErrPathTraversal, got %v", err)
		}
	})
}
