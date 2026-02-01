package mcpapps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RegisterResources registers all app resources with the MCP server.
// Each app's UI is served as an MCP resource at its configured ResourceURI.
func (r *Registry) RegisterResources(server *mcp.Server) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, app := range r.apps {
		registerAppResource(server, app)
	}
}

// MCP Apps profile MIME type per specification.
const mcpAppMIMEType = "text/html;profile=mcp-app"

// registerAppResource registers a single app's resource with the MCP server.
func registerAppResource(server *mcp.Server, app *AppDefinition) {
	resource := &mcp.Resource{
		URI:         app.ResourceURI,
		Name:        app.Name,
		Description: fmt.Sprintf("Interactive UI for %s", app.Name),
		MIMEType:    mcpAppMIMEType,
	}

	handler := createResourceHandler(app)
	server.AddResource(resource, handler)
}

// createResourceHandler creates a ResourceHandler for an app.
func createResourceHandler(app *AppDefinition) mcp.ResourceHandler {
	return func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		filename := resolveFilename(req.Params.URI, app)

		content, err := readAsset(app, filename)
		if err != nil {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}

		// Inject config for entry point
		if filename == app.EntryPoint && app.Config != nil {
			content = injectConfig(content, app.Config)
		}

		mimeType := resolveMIMEType(filename, app.EntryPoint)
		meta := buildResourceMeta(filename, app)

		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				buildResourceContents(req.Params.URI, mimeType, content, meta),
			},
		}, nil
	}
}

// resolveFilename determines the filename to serve based on the request.
func resolveFilename(requestURI string, app *AppDefinition) string {
	filename := app.EntryPoint
	requestedPath := extractPath(requestURI, app.ResourceURI)
	if requestedPath != "" && requestedPath != "/" {
		filename = strings.TrimPrefix(requestedPath, "/")
	}
	return filename
}

// resolveMIMEType returns the MIME type for a file, using MCP App profile for entry points.
func resolveMIMEType(filename, entryPoint string) string {
	if filename == entryPoint {
		return mcpAppMIMEType
	}
	return MIMEType(filename)
}

// buildResourceMeta builds the resource metadata including CSP and permissions.
func buildResourceMeta(filename string, app *AppDefinition) mcp.Meta {
	if filename != app.EntryPoint || app.CSP == nil {
		return nil
	}

	uiMeta := map[string]any{}

	// Build CSP domains
	cspMeta := buildCSPMeta(app.CSP)
	if len(cspMeta) > 0 {
		uiMeta["csp"] = cspMeta
	}

	// Add permissions at ui level (per MCP Apps spec)
	if app.CSP.Permissions != nil {
		uiMeta["permissions"] = app.CSP.Permissions
	}

	return mcp.Meta{"ui": uiMeta}
}

// buildCSPMeta builds the CSP domains portion of metadata.
func buildCSPMeta(csp *CSPConfig) map[string]any {
	cspMeta := map[string]any{}
	if len(csp.ResourceDomains) > 0 {
		cspMeta["resourceDomains"] = csp.ResourceDomains
	}
	if len(csp.ConnectDomains) > 0 {
		cspMeta["connectDomains"] = csp.ConnectDomains
	}
	if len(csp.FrameDomains) > 0 {
		cspMeta["frameDomains"] = csp.FrameDomains
	}
	return cspMeta
}

// buildResourceContents creates the appropriate ResourceContents based on MIME type.
func buildResourceContents(uri, mimeType string, content []byte, meta mcp.Meta) *mcp.ResourceContents {
	if isBinaryMIME(mimeType) {
		return &mcp.ResourceContents{
			URI:      uri,
			MIMEType: mimeType,
			Blob:     content,
			Meta:     meta,
		}
	}
	return &mcp.ResourceContents{
		URI:      uri,
		MIMEType: mimeType,
		Text:     string(content),
		Meta:     meta,
	}
}

// extractPath extracts the file path from a resource URI.
// For example: "ui://query-results/style.css" with base "ui://query-results"
// returns "/style.css".
func extractPath(requestURI, baseURI string) string {
	if !strings.HasPrefix(requestURI, baseURI) {
		return ""
	}
	return strings.TrimPrefix(requestURI, baseURI)
}

// readAsset reads a file from the app's filesystem assets with path traversal protection.
func readAsset(app *AppDefinition, filename string) ([]byte, error) {
	// Build the full path
	fullPath := filepath.Join(app.AssetsPath, filename)

	// Get absolute paths for comparison
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return nil, ErrAssetNotFound
	}
	basePath, err := filepath.Abs(app.AssetsPath)
	if err != nil {
		return nil, ErrAssetNotFound
	}

	// Security: prevent path traversal by ensuring resolved path stays within base
	if !strings.HasPrefix(absPath, basePath+string(filepath.Separator)) && absPath != basePath {
		return nil, ErrPathTraversal
	}

	// Read from filesystem
	// #nosec G304 -- path is validated above to prevent traversal
	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil, ErrAssetNotFound
	}

	return content, nil
}

// injectConfig injects app configuration as JSON into the HTML.
// It looks for a <script id="app-config"> tag and replaces its content,
// or appends it before </head> if not found.
// If config is nil, the content is returned unchanged.
func injectConfig(content []byte, config any) []byte {
	if config == nil {
		return content
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		return content
	}

	configScript := fmt.Sprintf(`<script id="app-config" type="application/json">%s</script>`, configJSON)

	// Check if there's an existing app-config script to replace
	existingTag := []byte(`<script id="app-config"`)
	if idx := bytes.Index(content, existingTag); idx != -1 {
		// Find the closing </script> tag
		endIdx := bytes.Index(content[idx:], []byte(`</script>`))
		if endIdx != -1 {
			// Replace the existing script
			// Use explicit buffer to avoid slice aliasing issues with append
			endIdx += idx + len("</script>")
			var buf bytes.Buffer
			buf.Write(content[:idx])
			buf.WriteString(configScript)
			buf.Write(content[endIdx:])
			return buf.Bytes()
		}
	}

	// No existing tag, insert before </head>
	headClose := []byte(`</head>`)
	if idx := bytes.Index(content, headClose); idx != -1 {
		var buf bytes.Buffer
		buf.Write(content[:idx])
		buf.WriteString(configScript + "\n")
		buf.Write(content[idx:])
		return buf.Bytes()
	}

	// No </head> found, return as-is
	return content
}

// isBinaryMIME returns true if the MIME type indicates binary content.
func isBinaryMIME(mimeType string) bool {
	// Strip charset and other parameters
	if idx := strings.Index(mimeType, ";"); idx != -1 {
		mimeType = mimeType[:idx]
	}
	mimeType = strings.TrimSpace(mimeType)

	switch {
	case strings.HasPrefix(mimeType, "text/"):
		return false
	case strings.HasPrefix(mimeType, "application/json"):
		return false
	case strings.HasPrefix(mimeType, "application/javascript"):
		return false
	case strings.HasPrefix(mimeType, "application/xml"):
		return false
	case mimeType == "image/svg+xml":
		return false
	default:
		return true
	}
}
