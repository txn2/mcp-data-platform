package platform

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sync"

	"github.com/txn2/mcp-data-platform/pkg/configstore"
)

// overrideStore is the read half of configstore.Store that Config needs to
// resolve database-backed overrides. Config depends on this narrowed shape
// rather than the full store so it cannot write, and so a test double does
// not have to implement Set/Delete/Changelog. Unexported deliberately:
// callers pass any store satisfying it without naming it, and the platform's
// public surface gains a behavior, not a type.
type overrideStore interface {
	Get(ctx context.Context, key string) (*configstore.Entry, error)
	List(ctx context.Context) ([]configstore.Entry, error)
}

// overrideBinding holds the bound store behind its own lock, keeping Config
// free of a mutex so it stays copyable.
type overrideBinding struct {
	mu    sync.RWMutex
	store overrideStore
}

// BindOverrideStore installs the store consulted on every read of a
// database-overridable setting. Passing nil unbinds it, which makes every
// such read resolve to the file config.
//
// The store is the authority: nothing is copied into Config at bind time and
// nothing is written back to it later, so two processes sharing one database
// resolve the same value without any cross-process notification. Must be
// called during setup, before the server begins handling requests.
func (c *Config) BindOverrideStore(s overrideStore) {
	if c.overrides == nil {
		c.overrides = &overrideBinding{}
	}
	c.overrides.mu.Lock()
	defer c.overrides.mu.Unlock()
	c.overrides.store = s
}

// boundOverrides returns the bound store, or nil when none is bound.
func (c *Config) boundOverrides() overrideStore {
	if c.overrides == nil {
		return nil
	}
	c.overrides.mu.RLock()
	defer c.overrides.mu.RUnlock()
	return c.overrides.store
}

// EffectiveCopy returns a copy of the config with every database-overridable
// field replaced by the value in force for ctx. It exists for the admin
// surfaces that serialize the whole config: without it they would render the
// file values and silently omit the operator's stored overrides.
//
// The copy is shallow apart from the fields it replaces, so callers must treat
// it as read-only.
func (c *Config) EffectiveCopy(ctx context.Context) *Config {
	out := *c
	out.Server.Description = c.ServerDescription(ctx)
	out.Server.AgentInstructions = c.ServerAgentInstructions(ctx)
	out.Tools.Deny = c.ToolsDenySnapshot(ctx)
	out.Tools.DescriptionOverrides = c.ToolDescriptionOverridesSnapshot(ctx)
	return &out
}

// resolve returns the stored override for key. found is false when no store is
// bound, no row exists, or the lookup fails.
//
// A failed lookup is deliberately indistinguishable from an absent row: both
// fall back to the operator's file config. A database outage must not blank out
// agent instructions or drop a deny pattern, so the failure mode is "the YAML
// the operator shipped", never "empty".
func (c *Config) resolve(ctx context.Context, key string) (value string, found bool) {
	store := c.boundOverrides()
	if store == nil {
		return "", false
	}
	entry, err := store.Get(ctx, key)
	if err != nil {
		if !errors.Is(err, configstore.ErrNotFound) {
			slog.WarnContext(ctx, "config override lookup failed; falling back to file config",
				"key", key, "error", err)
		}
		return "", false
	}
	return entry.Value, true
}

// ServerDescription returns the effective server description: the stored
// override when one exists, otherwise the file-config value.
func (c *Config) ServerDescription(ctx context.Context) string {
	if v, ok := c.resolve(ctx, ConfigKeyServerDescription); ok {
		return v
	}
	return c.Server.Description
}

// ServerAgentInstructions returns the effective agent instructions: the stored
// override when one exists, otherwise the file-config value.
func (c *Config) ServerAgentInstructions(ctx context.Context) string {
	if v, ok := c.resolve(ctx, ConfigKeyServerAgentInstructions); ok {
		return v
	}
	return c.Server.AgentInstructions
}

// ToolsDenySnapshot returns the effective platform-wide tool deny patterns.
// Callers may mutate the returned slice.
//
// A stored row wins over the file config even when it decodes to an empty
// list: that is an operator who deliberately cleared the deny list, which is
// not the same as having no override at all. A malformed row is ignored in
// favor of the file config, so a corrupt value cannot silently un-hide tools
// the YAML denies.
func (c *Config) ToolsDenySnapshot(ctx context.Context) []string {
	if v, ok := c.resolve(ctx, ConfigKeyToolsDeny); ok {
		deny, err := parseToolsDenyValue(v)
		if err == nil {
			return deny
		}
		slog.WarnContext(ctx, "ignoring malformed tools.deny override; falling back to file config",
			"error", err)
	}
	if len(c.Tools.Deny) == 0 {
		return nil
	}
	return slices.Clone(c.Tools.Deny)
}

// ToolsAllowSnapshot returns a copy of tools.allow. Allow is file-only: there
// is no config_entries key for it, so this is a plain copy of the loaded YAML.
func (c *Config) ToolsAllowSnapshot() []string {
	if len(c.Tools.Allow) == 0 {
		return nil
	}
	return slices.Clone(c.Tools.Allow)
}

// ToolDescriptionOverridesSnapshot returns the effective per-tool description
// overrides: the file-config map with every stored tool.<name>.description row
// layered on top. Callers may mutate the returned map.
//
// A stored row with an empty value removes the entry, reverting that tool to
// whatever description it would otherwise carry.
func (c *Config) ToolDescriptionOverridesSnapshot(ctx context.Context) map[string]string {
	merged := maps.Clone(c.Tools.DescriptionOverrides)

	store := c.boundOverrides()
	if store == nil {
		return merged
	}
	entries, err := store.List(ctx)
	if err != nil {
		slog.WarnContext(ctx, "config override list failed; falling back to file config", "error", err)
		return merged
	}

	for _, e := range entries {
		name, ok := toolDescriptionKey(e.Key)
		if !ok {
			continue
		}
		if e.Value == "" {
			delete(merged, name)
			continue
		}
		if merged == nil {
			merged = make(map[string]string)
		}
		merged[name] = e.Value
	}
	return merged
}
