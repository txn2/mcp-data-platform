package memorylayer

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// dummyDB returns a non-connecting *sql.DB. New builds the store wrapper and the
// toolkit without touching the database, so a real connection is never needed;
// the staleness watcher (the only component that queries) is not started in
// these tests unless explicitly gated on, and even then its first tick is far in
// the future.
func dummyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", "postgres://localhost:5432/test?sslmode=disable")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// stubSemantic is a non-nil semantic.Provider used only to satisfy the staleness
// watcher's precondition; its methods are never called because the watcher's
// first tick is 15 minutes out and the test stops it immediately.
type stubSemantic struct{ semantic.Provider }

func TestNew_NilDBIsNoop(t *testing.T) {
	h, err := New(nil, nil, Config{ToolkitName: "default"})
	require.NoError(t, err)
	assert.Nil(t, h, "no database → nil handle (memory disabled)")

	// Every accessor is safe on the nil handle and returns the disabled zero.
	assert.Nil(t, h.MemoryStore())
	assert.Nil(t, h.EmbeddingProvider())
	assert.Nil(t, h.Toolkit())
	assert.Nil(t, h.MemoryProvider())
	assert.Nil(t, h.IndexProducer())
	assert.Nil(t, h.RecallOnEmbed())
	raw := embedding.NewNoopProvider(4)
	assert.Same(t, raw, h.Background(raw), "with no layer there is no gate to wrap in")
	h.Start() // no panic
	h.Stop()  // no panic
}

func TestNew_AssemblesLayer(t *testing.T) {
	h, err := New(dummyDB(t), nil, Config{
		ToolkitName:       "default",
		EmbeddingProvider: providerOllama,
		Ollama:            embedding.OllamaConfig{URL: "http://localhost:11434", Model: "nomic-embed-text"},
	})
	require.NoError(t, err)
	require.NotNil(t, h)

	assert.NotNil(t, h.MemoryStore(), "store built from the db")
	assert.NotNil(t, h.Toolkit(), "toolkit exposed for registration")
	assert.NotNil(t, h.MemoryProvider(), "enrichment adapter exposed")
	require.NotNil(t, h.EmbeddingProvider())
	assert.Equal(t, embedding.KindOllama, h.EmbeddingProvider().Kind(),
		"provider 'ollama' selects the Ollama embedder")
	assert.Nil(t, h.stalenessWatcher, "watcher stays off with no semantic provider")
}

func TestNew_EmbedderSelection(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		wantKind string
	}{
		{"ollama selected", providerOllama, embedding.KindOllama},
		{"empty falls back to noop", "", embedding.KindNoop},
		{"unknown falls back to noop", "openai", embedding.KindNoop},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, err := New(dummyDB(t), nil, Config{ToolkitName: "default", EmbeddingProvider: tc.provider})
			require.NoError(t, err)
			require.NotNil(t, h.EmbeddingProvider())
			assert.Equal(t, tc.wantKind, h.EmbeddingProvider().Kind())
		})
	}
}

func TestNew_StalenessWatcherGating(t *testing.T) {
	t.Run("constructed when enabled and semantic provider present", func(t *testing.T) {
		h, err := New(dummyDB(t), stubSemantic{}, Config{
			ToolkitName:      "default",
			StalenessEnabled: true,
			Staleness:        memory.StalenessConfig{BatchSize: 10},
		})
		require.NoError(t, err)
		require.NotNil(t, h.stalenessWatcher, "watcher built when enabled + semantic provider")
		// Two-phase: Start launches the goroutine, Stop winds it down without
		// blocking (the first tick is far in the future).
		h.Start()
		h.Stop()
	})

	t.Run("disabled when staleness off", func(t *testing.T) {
		h, err := New(dummyDB(t), stubSemantic{}, Config{ToolkitName: "default", StalenessEnabled: false})
		require.NoError(t, err)
		assert.Nil(t, h.stalenessWatcher)
		h.Start() // no-op, no panic (no watcher constructed)
		h.Stop()  // no-op, no panic
	})

	t.Run("disabled when no semantic provider even if enabled", func(t *testing.T) {
		h, err := New(dummyDB(t), nil, Config{ToolkitName: "default", StalenessEnabled: true})
		require.NoError(t, err)
		assert.Nil(t, h.stalenessWatcher, "no semantic provider → no watcher")
	})
}

// TestNew_WiresTheCaptureEmbedPath pins the #1987/#1988 wiring the platform
// reads off the handle: the memory write-path producer for the index queue to
// bind, the recall hook the memory consumer calls after it writes a vector,
// and both sides of the embed gate around the layer's embedder.
func TestNew_WiresTheCaptureEmbedPath(t *testing.T) {
	h, err := New(dummyDB(t), nil, Config{
		ToolkitName:       "default",
		EmbeddingProvider: providerOllama,
		Ollama:            embedding.OllamaConfig{URL: "http://localhost:11434", Model: "nomic-embed-text"},
	})
	require.NoError(t, err)

	require.NotNil(t, h.IndexProducer())
	assert.Equal(t, "memory", h.IndexProducer().Kind())
	assert.NotNil(t, h.RecallOnEmbed())

	interactive := h.EmbeddingProvider()
	assert.True(t, embedding.IsConfigured(interactive))
	assert.Equal(t, "nomic-embed-text", embedding.ModelName(interactive), "the gate forwards the model")
	worker := embedding.NewOllamaProvider(embedding.OllamaConfig{Model: "nomic-embed-text"})
	background := h.Background(worker)
	assert.NotSame(t, worker, background, "the worker's provider is wrapped in the gate")
	assert.Equal(t, "nomic-embed-text", embedding.ModelName(background))
}
