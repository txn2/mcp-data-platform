package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/configstore"
)

// failingOverrideStore stands in for a database that is down or unreachable.
type failingOverrideStore struct{ err error }

func (s failingOverrideStore) Get(context.Context, string) (*configstore.Entry, error) {
	return nil, s.err
}

func (s failingOverrideStore) List(context.Context) ([]configstore.Entry, error) {
	return nil, s.err
}

func boundConfig(t *testing.T, entries map[string]string) *Config {
	t.Helper()
	store := newMutableOverrideStore()
	for k, v := range entries {
		store.set(k, v)
	}
	cfg := &Config{}
	cfg.BindOverrideStore(store)
	return cfg
}

func TestServerDescription_Resolution(t *testing.T) {
	ctx := context.Background()

	t.Run("no store bound returns file config", func(t *testing.T) {
		cfg := &Config{}
		cfg.Server.Description = "from yaml"
		assert.Equal(t, "from yaml", cfg.ServerDescription(ctx))
	})

	t.Run("stored override wins over file config", func(t *testing.T) {
		cfg := boundConfig(t, map[string]string{ConfigKeyServerDescription: "from db"})
		cfg.Server.Description = "from yaml"
		assert.Equal(t, "from db", cfg.ServerDescription(ctx))
	})

	t.Run("absent row falls back to file config", func(t *testing.T) {
		cfg := boundConfig(t, nil)
		cfg.Server.Description = "from yaml"
		assert.Equal(t, "from yaml", cfg.ServerDescription(ctx))
	})

	t.Run("stored empty string wins", func(t *testing.T) {
		// An operator who saves an empty description means it. Falling back
		// to the YAML here would make the field impossible to clear.
		cfg := boundConfig(t, map[string]string{ConfigKeyServerDescription: ""})
		cfg.Server.Description = "from yaml"
		assert.Empty(t, cfg.ServerDescription(ctx))
	})

	t.Run("store failure falls back to file config", func(t *testing.T) {
		cfg := &Config{}
		cfg.Server.Description = "from yaml"
		cfg.BindOverrideStore(failingOverrideStore{err: errors.New("db down")})
		assert.Equal(t, "from yaml", cfg.ServerDescription(ctx))
	})

	t.Run("unbinding restores file config", func(t *testing.T) {
		cfg := boundConfig(t, map[string]string{ConfigKeyServerDescription: "from db"})
		cfg.Server.Description = "from yaml"
		require.Equal(t, "from db", cfg.ServerDescription(ctx))
		cfg.BindOverrideStore(nil)
		assert.Equal(t, "from yaml", cfg.ServerDescription(ctx))
	})
}

func TestServerAgentInstructions_Resolution(t *testing.T) {
	ctx := context.Background()

	t.Run("stored override wins", func(t *testing.T) {
		cfg := boundConfig(t, map[string]string{ConfigKeyServerAgentInstructions: "db guidance"})
		cfg.Server.AgentInstructions = "yaml guidance"
		assert.Equal(t, "db guidance", cfg.ServerAgentInstructions(ctx))
	})

	t.Run("store failure keeps operator guidance", func(t *testing.T) {
		// The failure that matters: agent instructions carry the operator's
		// data-safety rules. A database blip must not serve a session with
		// none of them.
		cfg := &Config{}
		cfg.Server.AgentInstructions = "never return credentials"
		cfg.BindOverrideStore(failingOverrideStore{err: errors.New("db down")})
		assert.Equal(t, "never return credentials", cfg.ServerAgentInstructions(ctx))
	})
}

func TestToolsDenySnapshot_Resolution(t *testing.T) {
	ctx := context.Background()

	t.Run("no store bound returns file config copy", func(t *testing.T) {
		cfg := &Config{}
		cfg.Tools.Deny = []string{"trino_admin_kill"}
		got := cfg.ToolsDenySnapshot(ctx)
		assert.Equal(t, []string{"trino_admin_kill"}, got)

		got[0] = "mutated"
		assert.Equal(t, []string{"trino_admin_kill"}, cfg.Tools.Deny, "caller must not alias live config")
	})

	t.Run("stored override wins", func(t *testing.T) {
		cfg := boundConfig(t, map[string]string{ConfigKeyToolsDeny: `["s3_delete_object"]`})
		cfg.Tools.Deny = []string{"trino_admin_kill"}
		assert.Equal(t, []string{"s3_delete_object"}, cfg.ToolsDenySnapshot(ctx))
	})

	t.Run("stored empty array clears the file deny list", func(t *testing.T) {
		cfg := boundConfig(t, map[string]string{ConfigKeyToolsDeny: `[]`})
		cfg.Tools.Deny = []string{"trino_admin_kill"}
		assert.Empty(t, cfg.ToolsDenySnapshot(ctx))
	})

	t.Run("malformed row keeps the file deny list", func(t *testing.T) {
		// A corrupt row must not silently un-hide tools the YAML denies.
		cfg := boundConfig(t, map[string]string{ConfigKeyToolsDeny: "not json"})
		cfg.Tools.Deny = []string{"trino_admin_kill"}
		assert.Equal(t, []string{"trino_admin_kill"}, cfg.ToolsDenySnapshot(ctx))
	})

	t.Run("store failure keeps the file deny list", func(t *testing.T) {
		cfg := &Config{}
		cfg.Tools.Deny = []string{"trino_admin_kill"}
		cfg.BindOverrideStore(failingOverrideStore{err: errors.New("db down")})
		assert.Equal(t, []string{"trino_admin_kill"}, cfg.ToolsDenySnapshot(ctx))
	})
}

func TestToolDescriptionOverridesSnapshot_Resolution(t *testing.T) {
	ctx := context.Background()

	t.Run("no store bound returns file config copy", func(t *testing.T) {
		cfg := &Config{}
		cfg.Tools.DescriptionOverrides = map[string]string{"trino_query": "from yaml"}
		got := cfg.ToolDescriptionOverridesSnapshot(ctx)
		assert.Equal(t, "from yaml", got["trino_query"])

		got["trino_query"] = "mutated"
		assert.Equal(t, "from yaml", cfg.Tools.DescriptionOverrides["trino_query"],
			"caller must not alias live config")
	})

	t.Run("stored rows layer over file config", func(t *testing.T) {
		cfg := boundConfig(t, map[string]string{
			"tool.trino_query.description":     "from db",
			"tool.s3_list_objects.description": "also from db",
			ConfigKeyServerDescription:         "unrelated key is ignored",
		})
		cfg.Tools.DescriptionOverrides = map[string]string{
			"trino_query":    "from yaml",
			"datahub_search": "yaml only",
		}
		got := cfg.ToolDescriptionOverridesSnapshot(ctx)
		assert.Equal(t, "from db", got["trino_query"])
		assert.Equal(t, "also from db", got["s3_list_objects"])
		assert.Equal(t, "yaml only", got["datahub_search"])
		assert.NotContains(t, got, "server")
	})

	t.Run("stored empty value removes the override", func(t *testing.T) {
		cfg := boundConfig(t, map[string]string{"tool.trino_query.description": ""})
		cfg.Tools.DescriptionOverrides = map[string]string{"trino_query": "from yaml"}
		assert.NotContains(t, cfg.ToolDescriptionOverridesSnapshot(ctx), "trino_query")
	})

	t.Run("populates from stored rows with no file overrides", func(t *testing.T) {
		cfg := boundConfig(t, map[string]string{"tool.trino_query.description": "from db"})
		assert.Equal(t, "from db", cfg.ToolDescriptionOverridesSnapshot(ctx)["trino_query"])
	})

	t.Run("store failure keeps the file overrides", func(t *testing.T) {
		cfg := &Config{}
		cfg.Tools.DescriptionOverrides = map[string]string{"trino_query": "from yaml"}
		cfg.BindOverrideStore(failingOverrideStore{err: errors.New("db down")})
		assert.Equal(t, "from yaml", cfg.ToolDescriptionOverridesSnapshot(ctx)["trino_query"])
	})
}

// TestOverrides_SharedStoreAcrossConfigs is the regression test for #1106.
//
// Two Config values stand in for two replicas of the same deployment: separate
// processes, separate memory, one shared store. A write that lands in the store
// (as an admin PUT served by replica A does) must be visible to replica B on
// its very next read, with no notification between them and no restart.
func TestOverrides_SharedStoreAcrossConfigs(t *testing.T) {
	ctx := context.Background()
	shared := newMutableOverrideStore()

	replicaA := &Config{}
	replicaA.Server.Description = "file default"
	replicaA.BindOverrideStore(shared)

	replicaB := &Config{}
	replicaB.Server.Description = "file default"
	replicaB.BindOverrideStore(shared)

	require.Equal(t, "file default", replicaA.ServerDescription(ctx))
	require.Equal(t, "file default", replicaB.ServerDescription(ctx))

	// Admin PUT served by replica A.
	shared.set(ConfigKeyServerDescription, "operator authored")

	assert.Equal(t, "operator authored", replicaA.ServerDescription(ctx))
	assert.Equal(t, "operator authored", replicaB.ServerDescription(ctx),
		"replica that did not serve the write must still see it")

	// Same for the other overridable keys.
	shared.set(ConfigKeyServerAgentInstructions, "operator guidance")
	shared.set(ConfigKeyToolsDeny, `["s3_delete_object"]`)
	shared.set("tool.trino_query.description", "operator description")

	assert.Equal(t, "operator guidance", replicaB.ServerAgentInstructions(ctx))
	assert.Equal(t, []string{"s3_delete_object"}, replicaB.ToolsDenySnapshot(ctx))
	assert.Equal(t, "operator description",
		replicaB.ToolDescriptionOverridesSnapshot(ctx)["trino_query"])
}

func TestEffectiveCopy(t *testing.T) {
	ctx := context.Background()

	cfg := boundConfig(t, map[string]string{
		ConfigKeyServerDescription:       "stored description",
		ConfigKeyServerAgentInstructions: "stored guidance",
		ConfigKeyToolsDeny:               `["s3_delete_object"]`,
		"tool.trino_query.description":   "stored tool description",
	})
	cfg.Server.Name = "unchanged"
	cfg.Server.Description = "file description"
	cfg.Server.AgentInstructions = "file guidance"
	cfg.Tools.Deny = []string{"file_denied"}

	got := cfg.EffectiveCopy(ctx)

	assert.Equal(t, "stored description", got.Server.Description)
	assert.Equal(t, "stored guidance", got.Server.AgentInstructions)
	assert.Equal(t, []string{"s3_delete_object"}, got.Tools.Deny)
	assert.Equal(t, "stored tool description", got.Tools.DescriptionOverrides["trino_query"])
	assert.Equal(t, "unchanged", got.Server.Name, "non-overridable fields are carried through")

	// The source config keeps its file values: the copy must not write back.
	assert.Equal(t, "file description", cfg.Server.Description)
	assert.Equal(t, []string{"file_denied"}, cfg.Tools.Deny)
}
