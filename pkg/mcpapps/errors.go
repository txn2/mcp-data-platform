package mcpapps

import "errors"

// Validation errors for AppDefinition.
var (
	// ErrMissingName is returned when AppDefinition.Name is empty.
	ErrMissingName = errors.New("app name is required")

	// ErrMissingResourceURI is returned when AppDefinition.ResourceURI is empty.
	ErrMissingResourceURI = errors.New("resource URI is required")

	// ErrMissingToolNames is returned when AppDefinition.ToolNames is empty.
	ErrMissingToolNames = errors.New("at least one tool name is required")

	// ErrMissingEntryPoint is returned when AppDefinition.EntryPoint is empty.
	ErrMissingEntryPoint = errors.New("entry point is required")

	// ErrMissingAssetsPath is returned when AppDefinition.AssetsPath is empty.
	ErrMissingAssetsPath = errors.New("assets_path is required")

	// ErrAssetsPathNotAbsolute is returned when AssetsPath is not an absolute path.
	ErrAssetsPathNotAbsolute = errors.New("assets_path must be absolute")

	// ErrEntryPointNotFound is returned when the entry point file doesn't exist.
	ErrEntryPointNotFound = errors.New("entry point not found")

	// ErrPathTraversal is returned when a path traversal attack is detected.
	ErrPathTraversal = errors.New("path traversal detected")

	// ErrAppAlreadyRegistered is returned when trying to register an app
	// with a name that's already registered.
	ErrAppAlreadyRegistered = errors.New("app already registered")

	// ErrAppNotFound is returned when looking up an app that doesn't exist.
	ErrAppNotFound = errors.New("app not found")

	// ErrAssetNotFound is returned when a requested asset doesn't exist.
	ErrAssetNotFound = errors.New("asset not found")
)
