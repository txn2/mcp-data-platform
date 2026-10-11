// Package servercaps builds the capabilities the platform's MCP server
// declares, explicitly rather than by the SDK's inference, so the server's
// contract is visible in one place. It was taken out of the platform facade
// for that package's size budget; the facade decides what is on and this
// package states what that declares.
package servercaps

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/mcptask"
)

// Input is what the capabilities are decided by.
type Input struct {
	// Resources is true when resource templates, managed resources or custom
	// resources are configured.
	Resources bool
	// Prompts is true when prompts are configured or the knowledge layer
	// serves them.
	Prompts bool
	// Tasks is true where the MCP Tasks extension is offered (#2009).
	Tasks bool
}

// Build returns the capabilities in describes.
func Build(in Input) *mcp.ServerCapabilities {
	caps := &mcp.ServerCapabilities{
		// Tools are always available — every platform deployment has at least platform_info.
		Tools: &mcp.ToolCapabilities{ListChanged: true},
		// Logging is always available for client logging support, through
		// the deprecation window SEP-2577 keeps it working for.
		Logging: &mcp.LoggingCapabilities{}, //nolint:staticcheck // still served to clients that use it
	}

	// Resources are available when templates or managed resources are enabled.
	if in.Resources {
		caps.Resources = &mcp.ResourceCapabilities{ListChanged: true}
	}

	// Prompts are available when configured. ListChanged is always true when
	// prompts are advertised: prompts are DB-backed and runtime-mutable
	// (create/update/delete/approve/promote via manage_prompt and the admin
	// API), and the platform emits notifications/prompts/list_changed on every
	// such write (see the prompt layer's notifying store), so the capability is
	// honest in both directions (#927).
	if in.Prompts {
		caps.Prompts = &mcp.PromptCapabilities{ListChanged: true}
	}

	// Completions serve argument autocompletion for prompts and resource
	// templates, so the capability is advertised exactly when there is something
	// to complete. The platform wires the CompletionHandler on the same
	// condition (a non-nil caps.Completions), so the declared capability and
	// the implementation stay in lockstep (#928).
	if in.Prompts || in.Resources {
		caps.Completions = &mcp.CompletionCapabilities{}
	}

	// The Tasks extension, wherever its tasks can be kept (#2009).
	if in.Tasks {
		caps.AddExtension(mcptask.ExtensionID, map[string]any{})
	}
	return caps
}
