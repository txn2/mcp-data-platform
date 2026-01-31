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

	// ErrAppAlreadyRegistered is returned when trying to register an app
	// with a name that's already registered.
	ErrAppAlreadyRegistered = errors.New("app already registered")

	// ErrAppNotFound is returned when looking up an app that doesn't exist.
	ErrAppNotFound = errors.New("app not found")

	// ErrAssetNotFound is returned when a requested asset doesn't exist.
	ErrAssetNotFound = errors.New("asset not found")
)
