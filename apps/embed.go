// Package apps provides the embedded default MCP App assets shipped with the binary.
package apps

import "embed"

// PlatformInfo is the embedded filesystem for the platform-info app.
//
//go:embed platform-info/index.html
var PlatformInfo embed.FS
