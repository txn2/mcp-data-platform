// Package mcpapps provides MCP Apps support for interactive UI components.
//
// MCP Apps allow tool responses to include interactive UI elements that hosts
// can render for enhanced user experiences. This package implements the
// infrastructure for registering apps, injecting metadata into tool responses,
// and serving filesystem-based HTML/JS/CSS assets.
package mcpapps

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

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

	// AssetsPath is the absolute filesystem path to the directory
	// containing the app's HTML/JS/CSS files. Optional when Content is set.
	AssetsPath string

	// Content is an optional embedded filesystem. When set, assets are served
	// from this FS instead of AssetsPath. Path traversal protection is handled
	// by fs.FS. Either AssetsPath or Content must be provided.
	Content fs.FS

	// EntryPoint is the main HTML file within AssetsPath (e.g., "index.html").
	EntryPoint string

	// Config holds app-specific configuration that will be injected
	// into the HTML as JSON. This can be used to configure CDN URLs,
	// feature flags, or other runtime parameters.
	Config any

	// CSP defines Content Security Policy requirements for the app.
	// The host uses this to enforce appropriate CSP headers.
	CSP *CSPConfig
}

// CSPConfig defines Content Security Policy requirements for an MCP App.
type CSPConfig struct {
	// ResourceDomains lists origins for static resources (scripts, images, styles, fonts).
	// Maps to CSP script-src, img-src, style-src, font-src, media-src directives.
	ResourceDomains []string `json:"resourceDomains,omitempty"`

	// ConnectDomains lists origins for network requests (fetch/XHR/WebSocket).
	// Maps to CSP connect-src directive.
	ConnectDomains []string `json:"connectDomains,omitempty"`

	// FrameDomains lists origins for nested iframes.
	// Maps to CSP frame-src directive.
	FrameDomains []string `json:"frameDomains,omitempty"`

	// Permissions lists browser capabilities the app needs.
	// Hosts MAY honor these by setting appropriate iframe allow attributes.
	Permissions *PermissionsConfig `json:"permissions,omitempty"`
}

// PermissionsConfig defines browser permissions requested by an MCP App.
type PermissionsConfig struct {
	// ClipboardWrite requests write access to the clipboard.
	ClipboardWrite *struct{} `json:"clipboardWrite,omitempty"`
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
	if a.AssetsPath == "" && a.Content == nil {
		return ErrMissingAssetsPath
	}
	if a.EntryPoint == "" {
		return ErrMissingEntryPoint
	}
	return nil
}

// ValidateAssets verifies that the assets path exists and contains the entry point.
// This should be called after Validate to ensure the filesystem is ready.
// When Content is set, filesystem checks are skipped.
func (a *AppDefinition) ValidateAssets() error {
	if a.Content != nil {
		return nil
	}

	if !filepath.IsAbs(a.AssetsPath) {
		return ErrAssetsPathNotAbsolute
	}

	entryPath := filepath.Join(a.AssetsPath, a.EntryPoint)
	if _, err := os.Stat(entryPath); err != nil {
		if os.IsNotExist(err) {
			return ErrEntryPointNotFound
		}
		return fmt.Errorf("checking entry point %s: %w", entryPath, err)
	}

	return nil
}
