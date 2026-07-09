package promptlayer

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

func TestNew_StoreSelection(t *testing.T) {
	t.Run("explicit store is used", func(t *testing.T) {
		store := newMockPromptStore()
		h := New(Config{Store: store, Registry: registry.NewRegistry()})
		require.NotNil(t, h)
		assert.Same(t, store, h.Store())
	})

	t.Run("no store and no db leaves store nil", func(t *testing.T) {
		h := New(Config{Registry: registry.NewRegistry()})
		require.NotNil(t, h, "handle is always non-nil so static prompts still register")
		assert.Nil(t, h.Store())
	})
}

func TestNew_LoadPrompts(t *testing.T) {
	// An empty prompts directory loads nothing and does not error.
	h := New(Config{PromptsDir: t.TempDir(), Registry: registry.NewRegistry()})
	assert.NoError(t, h.LoadPrompts())
}

func TestSetEmbedderAndShareStore(t *testing.T) {
	h := New(Config{Store: newMockPromptStore(), Registry: registry.NewRegistry()})

	// Before binding, a shared-prompt lookup finds no share lister.
	share := &stubShareLister{}
	h.SetShareStore(share)
	assert.Same(t, share, h.shareStore)

	emb := &stubEmbedder{}
	h.SetEmbedder(emb)
	assert.Same(t, emb, h.embedder)
}

func TestStore_NilHandle(t *testing.T) {
	var h *Handle
	assert.Nil(t, h.Store())
}

func TestPromptCreator(t *testing.T) {
	t.Run("nil when no store", func(t *testing.T) {
		h := New(Config{Registry: registry.NewRegistry()})
		assert.Nil(t, h.PromptCreator())
	})

	t.Run("adapter creates and registers", func(t *testing.T) {
		store := newMockPromptStore()
		h := New(Config{Store: store, Registry: registry.NewRegistry()})
		pc := h.PromptCreator()
		require.NotNil(t, pc)

		pr := &prompt.Prompt{Name: "new-prompt", Scope: prompt.ScopeGlobal, Content: "c"}
		require.NoError(t, pc.Create(context.Background(), pr))
		assert.Contains(t, store.prompts, "new-prompt", "Create persisted through the store")

		pc.RegisterRuntimePrompt(pr)
		infos := h.AllPromptInfos()
		found := false
		for _, i := range infos {
			if i.Name == "new-prompt" {
				found = true
			}
		}
		assert.True(t, found, "RegisterRuntimePrompt tracked the prompt metadata")
	})

	t.Run("adapter surfaces store create error", func(t *testing.T) {
		store := newMockPromptStore()
		store.createErr = assert.AnError
		h := New(Config{Store: store, Registry: registry.NewRegistry()})
		err := h.PromptCreator().Create(context.Background(), &prompt.Prompt{Name: "x"})
		assert.Error(t, err)
	})
}

// A nil Handle degrades every entry point to a safe no-op / zero value, matching
// the documented contract, so a caller that never built the layer never panics.
func TestNilHandle_IsSafe(t *testing.T) {
	var h *Handle
	ctx := context.Background()

	assert.Nil(t, h.Store())
	assert.Nil(t, h.PromptCreator())
	assert.Nil(t, h.AllPromptInfos())
	assert.Nil(t, h.ListVisible(ctx, "a@x", nil))
	_, ok := h.GetByName(ctx, "a@x", nil, "global-x", nil)
	assert.False(t, ok)

	// Mutators and registration are no-ops (must not panic).
	assert.NotPanics(t, func() {
		h.RegisterRuntimePrompt(&prompt.Prompt{Name: "g", Scope: prompt.ScopeGlobal})
		h.UnregisterRuntimePrompt("g")
		server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
		h.RegisterTool(server)
		h.RegisterPlatformPrompts(server)
	})
}

// stubEmbedder is a minimal non-noop embedding provider for setter coverage.
type stubEmbedder struct{}

func (*stubEmbedder) Embed(context.Context, string) ([]float32, error) { return []float32{1}, nil }
func (*stubEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	return make([][]float32, len(texts)), nil
}
func (*stubEmbedder) Dimension() int { return 1 }
func (*stubEmbedder) Kind() string   { return "stub" }
