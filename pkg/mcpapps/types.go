// Package mcpapps provides MCP Apps support for interactive UI components.
//
// MCP Apps allow tool responses to include interactive UI elements that hosts
// can render for enhanced user experiences. This package implements the
// infrastructure for registering apps, injecting metadata into tool responses,
// and serving embedded HTML/JS/CSS assets.
package mcpapps

import "embed"

// AppDefinition defines an MCP App that provides interactive UI for tool results.
type AppDefinition struct {
	// Name is the unique identifier for this app (e.g., "query-results").
	Name string

	// ResourceURI is the MCP resource URI that serves this app's UI
	// (e.g., "ui://query-results").
	ResourceURI string

	// ToolNames is the list of tool names this app enhances.
	// When any of these tools are called, the response will include
	// _meta.ui metadata pointing to this app.
	ToolNames []string

	// Assets contains the embedded filesystem with HTML/JS/CSS files.
	Assets embed.FS

	// AssetsRoot is the root directory within Assets to serve from
	// (e.g., "assets").
	AssetsRoot string

	// EntryPoint is the main HTML file within AssetsRoot (e.g., "index.html").
	EntryPoint string

	// Config holds app-specific configuration that will be injected
	// into the HTML as JSON. This can be used to configure CDN URLs,
	// feature flags, or other runtime parameters.
	Config any
}

// UIMetadata represents the _meta.ui field injected into tool definitions.
// MCP Apps-compatible hosts use this to associate interactive UIs with tools.
type UIMetadata struct {
	// ResourceURI points to the MCP resource that serves the app UI.
	ResourceURI string `json:"resourceUri"`
}

// ToolMeta represents the _meta field for a tool definition.
type ToolMeta struct {
	// UI contains MCP Apps UI metadata.
	UI *UIMetadata `json:"ui,omitempty"`
}

// Validate checks that the AppDefinition has all required fields.
func (a *AppDefinition) Validate() error {
	if a.Name == "" {
		return ErrMissingName
	}
	if a.ResourceURI == "" {
		return ErrMissingResourceURI
	}
	if len(a.ToolNames) == 0 {
		return ErrMissingToolNames
	}
	if a.EntryPoint == "" {
		return ErrMissingEntryPoint
	}
	return nil
}
