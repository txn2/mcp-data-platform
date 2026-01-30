package mcpapps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
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
		// Determine which file to serve
		filename := app.EntryPoint

		// Check if a specific file is requested via query params or path
		// The URI format is: ui://app-name or ui://app-name/path/to/file
		requestedPath := extractPath(req.Params.URI, app.ResourceURI)
		if requestedPath != "" && requestedPath != "/" {
			filename = strings.TrimPrefix(requestedPath, "/")
		}

		// Read the file from embedded assets
		content, err := readAsset(app, filename)
		if err != nil {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}

		// If it's the entry point HTML, inject config
		if filename == app.EntryPoint && app.Config != nil {
			content = injectConfig(content, app.Config)
		}

		mimeType := MIMEType(filename)

		// Use MCP App profile MIME type for entry point HTML
		if filename == app.EntryPoint {
			mimeType = mcpAppMIMEType
		}

		// For binary content, use Blob; for text, use Text
		var resourceContents *mcp.ResourceContents
		if isBinaryMIME(mimeType) {
			resourceContents = &mcp.ResourceContents{
				URI:      req.Params.URI,
				MIMEType: mimeType,
				Blob:     content,
			}
		} else {
			resourceContents = &mcp.ResourceContents{
				URI:      req.Params.URI,
				MIMEType: mimeType,
				Text:     string(content),
			}
		}

		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{resourceContents},
		}, nil
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

// readAsset reads a file from the app's embedded assets.
func readAsset(app *AppDefinition, filename string) ([]byte, error) {
	// Build the full path within the embedded FS
	filepath := filename
	if app.AssetsRoot != "" {
		filepath = path.Join(app.AssetsRoot, filename)
	}

	// Read from embedded filesystem
	content, err := fs.ReadFile(app.Assets, filepath)
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
