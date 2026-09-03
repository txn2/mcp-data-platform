package agentinstructions

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/configstore"
)

// Store is the customized agent-instruction layer as its writers need it: read
// the current text, write a new one.
//
// It is declared here rather than imported so this package does not depend on
// the toolkit that consumes it -- the knowledge toolkit imports this package for
// the layer's byte bound, and importing it back would be a cycle. The two
// declarations are structurally identical, so a Store satisfies
// knowledge.InstructionsStore and a nil Store converts to a nil one.
type Store interface {
	AgentInstructions(ctx context.Context) (string, error)
	SetAgentInstructions(ctx context.Context, value, author string) error
}

// configModeDatabase is the config-store mode that can hold a write. A file
// store answers reads from the YAML and refuses every Set (ErrReadOnly), so a
// deployment on one has nowhere to record a promotion.
const configModeDatabase = "database"

// Layer adapts the platform's config plumbing onto the knowledge toolkit's
// InstructionsStore: the customized agent-instruction layer read as the value
// every session sees, and written as the database override that value resolves
// from.
type Layer struct {
	store    configstore.Store
	defaults map[string]string
	key      string
}

// New returns the customized agent-instruction layer as a writable store, or
// nil when store cannot hold a write. A nil return leaves the apply_knowledge
// agent_instructions sink and its rollback unavailable, so a promotion is
// refused with the alternative named rather than reporting a success nothing
// recorded.
//
// defaults are the file-config values a key falls back to (Platform's
// FileDefaults), and key is the config key the layer lives under
// (platform.ConfigKeyServerAgentInstructions). Both are passed in so this
// package stays free of pkg/platform, which composes it.
func New(store configstore.Store, defaults map[string]string, key string) Store {
	if store == nil || store.Mode() != configModeDatabase {
		return nil
	}
	return Layer{store: store, defaults: defaults, key: key}
}

// AgentInstructions returns the effective customized instruction text: the
// stored override when a row exists, otherwise the file-config value, so a
// deployment whose instructions still come from YAML reads its own text and a
// promotion edits that rather than replacing it with one section.
//
// A lookup failure is an error rather than a fallback. The read path treats a
// failed lookup as an absent row on purpose (a database outage must not blank
// out an agent's instructions), but a read-modify-write cannot: falling back
// here would overwrite a stored value with the file value plus the new section.
func (l Layer) AgentInstructions(ctx context.Context) (string, error) {
	entry, err := l.store.Get(ctx, l.key)
	switch {
	case err == nil:
		return entry.Value, nil
	case errors.Is(err, configstore.ErrNotFound):
		return l.defaults[l.key], nil
	default:
		return "", fmt.Errorf("reading config entry %s: %w", l.key, err)
	}
}

// SetAgentInstructions stores the customized instruction text as the database
// override, recording author as its writer. The layer's byte bound is enforced
// here as well as at each writer, so no path can store a value the composed
// instructions would then carry into every session.
func (l Layer) SetAgentInstructions(ctx context.Context, value, author string) error {
	if err := CheckCustomizedSize(value); err != nil {
		return err
	}
	if err := l.store.Set(ctx, l.key, value, author); err != nil {
		return fmt.Errorf("writing config entry %s: %w", l.key, err)
	}
	return nil
}
