// Package toolargs owns the platform-facade seam for the arguments the PLATFORM
// puts on a tool rather than the toolkit: the session handle (#792) and the
// call's purpose (#1317). Both are added to a tool's advertised input schema by
// a tools/list decorator, taken back off the request before the handler runs,
// and configured by their own YAML block, so they share a home.
//
// It lives here rather than in pkg/platform because the facade is frozen at its
// structural budgets and a new middleware must not grow it (see #756/#894/#1076).
// The facade keeps the config fields and the lines that register the middleware;
// the shapes and the construction live here. Both config types are aliased back
// (platform.SessionHandlesConfig, platform.PurposeConfig), so an operator's YAML
// and a library caller's Go address them unchanged.
package toolargs

import (
	"time"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// enabled reads a default-on *bool: nil means enabled, and only an explicit
// false turns the feature off.
func enabled(b *bool) bool { return b == nil || *b }

// DefaultSessionHandleTTL is the lifetime of an explicit session handle when
// sessions.handles.ttl is unset.
const DefaultSessionHandleTTL = 8 * time.Hour

// SessionHandles configures explicit session handles (issue #792): the
// platform_info-minted session_id that the model passes back on every tool
// call, replacing reliance on the transport-level Mcp-Session-Id header.
type SessionHandles struct {
	// Enabled activates handle minting, schema advertisement, and validation.
	// Default on (nil = enabled); set enabled: false for byte-identical legacy
	// transport-session behavior.
	Enabled *bool `yaml:"enabled"`

	// TTL is the handle lifetime, refreshed on use. Defaults to 8h.
	TTL time.Duration `yaml:"ttl"`

	// Require means a gated caller must have an established session, not that a
	// handle must be threaded on every call. A call carrying a valid handle uses
	// it; a call without one adopts the caller's own most-recently-active
	// session, resolved from their authenticated identity, so an MCP App's
	// sandboxed calls (which cannot thread the handle) are scoped rather than
	// refused (issue #1040). Only a caller with no session at all is refused with
	// SESSION_REQUIRED, which preserves the platform_info-first requirement (#800)
	// for genuinely fresh agents. Default on (nil = required); set require: false
	// to disable the requirement entirely, where a handle-less call falls back to
	// the transport session.
	Require *bool `yaml:"require"`
}

// IsEnabled reports whether explicit session handles are enabled, defaulting to
// true when not explicitly set.
func (c SessionHandles) IsEnabled() bool { return enabled(c.Enabled) }

// IsRequired reports whether a valid platform_info-minted handle is required on
// every gated tool call, defaulting to true when not explicitly set.
func (c SessionHandles) IsRequired() bool { return enabled(c.Require) }

// HandleTTL returns the configured handle lifetime, or the 8h default when
// unset or non-positive.
func (c SessionHandles) HandleTTL() time.Duration {
	if c.TTL > 0 {
		return c.TTL
	}
	return DefaultSessionHandleTTL
}

// Purpose configures the purpose argument: the one sentence the agent states
// about the wider task a data-access call serves, advertised on the gated tools'
// input schemas, taken off the request before the tool sees it, and recorded on
// the audit row.
type Purpose struct {
	// Enabled activates advertisement, stripping, and recording. Default on
	// (nil = enabled); set enabled: false to remove the argument entirely.
	Enabled *bool `yaml:"enabled"`

	// Require refuses a gated call that states no purpose, with
	// PURPOSE_REQUIRED. Default on (nil = required); set require: false to
	// record a purpose whenever one is stated but never refuse a call.
	//
	// The refusal only ever reaches a caller that threaded an explicit session
	// handle on the same call, which is the platform's proof that the caller can
	// thread a platform-injected argument at all. See
	// middleware.PurposeResolver.resolve.
	Require *bool `yaml:"require"`

	// Tools overrides the gated tool set. Entries are tool-name globs
	// (filepath.Match semantics, e.g. "datahub_get_*") plus
	// "kind:<toolkit-kind>" entries that gate every tool a toolkit of that kind
	// serves — the default set uses "kind:mcp" to cover every tool an MCP
	// gateway connection proxies, whose names are chosen upstream. An empty list
	// means the default set (middleware.DefaultPurposeTools).
	//
	// The platform OWNS the purpose argument name on a gated tool: it advertises
	// it and strips it before the handler runs. A deployment whose upstream MCP
	// server defines a purpose parameter of its own removes "kind:mcp" (or lists
	// the tools it wants gated by name) so that tool keeps its own argument.
	Tools []string `yaml:"tools"`
}

// IsEnabled reports whether the purpose argument is active, defaulting to true
// when not explicitly set.
func (c Purpose) IsEnabled() bool { return enabled(c.Enabled) }

// IsRequired reports whether a gated call must state a purpose, defaulting to
// true when not explicitly set.
func (c Purpose) IsRequired() bool { return enabled(c.Require) }

// BuildPurposeResolver constructs the purpose resolver, or returns nil when the
// argument is disabled — a valid no-op for both the tool-call middleware that
// enforces it and the tools/list decorator that advertises it.
//
// lookup resolves a proxied tool's toolkit kind, which is how the default set's
// "kind:mcp" entry reaches every tool an MCP gateway connection serves without
// naming any of them.
func BuildPurposeResolver(cfg Purpose, lookup middleware.ToolkitLookup) *middleware.PurposeResolver {
	if !cfg.IsEnabled() {
		return nil
	}
	return middleware.NewPurposeResolver(middleware.PurposeConfig{
		Enabled: true,
		Require: cfg.IsRequired(),
		Tools:   cfg.Tools,
		Lookup:  lookup,
	})
}
