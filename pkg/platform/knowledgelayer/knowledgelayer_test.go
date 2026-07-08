package knowledgelayer

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// dummyDB returns a non-connecting *sql.DB. New builds the store wrappers and
// toolkit without touching the database, so a real connection is never needed.
func dummyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", "postgres://localhost:5432/test?sslmode=disable")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// stubMemoryStore is a non-nil memory.Store used only to select the
// memory-backed insight adapter; its methods are never called in these tests.
type stubMemoryStore struct{ memory.Store }

func TestNew_NilDBIsNoop(t *testing.T) {
	h, err := New(nil, nil, nil, Config{ToolkitName: "default"})
	require.NoError(t, err)
	assert.Nil(t, h, "no database → nil handle (knowledge disabled)")

	// Every accessor is safe on the nil handle and returns the disabled zero.
	assert.Nil(t, h.InsightStore())
	assert.Nil(t, h.ChangesetStore())
	assert.Nil(t, h.Toolkit())
	assert.Nil(t, h.DataHubWriter())
}

func TestNew_InsightStoreSelection(t *testing.T) {
	t.Run("memory store selects the searchable adapter", func(t *testing.T) {
		h, err := New(dummyDB(t), stubMemoryStore{}, nil, Config{ToolkitName: "default"})
		require.NoError(t, err)
		require.NotNil(t, h)
		require.NotNil(t, h.InsightStore())
		_, ok := h.InsightStore().(knowledgekit.SearchableInsightStore)
		assert.True(t, ok, "memory-backed adapter is a SearchableInsightStore")
	})

	t.Run("no memory store falls back to the postgres store", func(t *testing.T) {
		h, err := New(dummyDB(t), nil, nil, Config{ToolkitName: "default"})
		require.NoError(t, err)
		require.NotNil(t, h)
		require.NotNil(t, h.InsightStore())
		_, ok := h.InsightStore().(knowledgekit.SearchableInsightStore)
		assert.False(t, ok, "the legacy postgres store is not searchable")
	})
}

func TestNew_ApplyGating(t *testing.T) {
	t.Run("apply disabled leaves changeset store and writer nil", func(t *testing.T) {
		h, err := New(dummyDB(t), nil, nil, Config{ToolkitName: "default", ApplyEnabled: false})
		require.NoError(t, err)
		require.NotNil(t, h)
		assert.NotNil(t, h.Toolkit(), "toolkit is always built")
		assert.Nil(t, h.ChangesetStore(), "no changeset store when apply is disabled")
		assert.Nil(t, h.DataHubWriter(), "no writer when apply is disabled")
	})

	t.Run("apply enabled builds changeset store and noop writer without a connection", func(t *testing.T) {
		h, err := New(dummyDB(t), nil, nil, Config{ToolkitName: "default", ApplyEnabled: true})
		require.NoError(t, err)
		require.NotNil(t, h)
		assert.NotNil(t, h.ChangesetStore(), "changeset store built when apply is enabled")
		require.NotNil(t, h.DataHubWriter())
		_, ok := h.DataHubWriter().(*knowledgekit.NoopDataHubWriter)
		assert.True(t, ok, "nil DataHub config selects the noop writer")
	})

	t.Run("apply enabled with an invalid connection fails construction", func(t *testing.T) {
		_, err := New(dummyDB(t), nil, nil, Config{
			ToolkitName:            "default",
			ApplyEnabled:           true,
			ApplyDataHubConnection: "primary",
			DataHub:                &DataHubConfig{URL: "", Token: ""},
		})
		require.Error(t, err, "an unbuildable DataHub client propagates as an error")
	})

	t.Run("apply enabled with a connection builds the real writer", func(t *testing.T) {
		h, err := New(dummyDB(t), nil, nil, Config{
			ToolkitName:            "default",
			ApplyEnabled:           true,
			ApplyDataHubConnection: "primary",
			DataHub:                &DataHubConfig{URL: "http://datahub:8080", Token: "test-token"},
		})
		require.NoError(t, err)
		require.NotNil(t, h)
		require.NotNil(t, h.DataHubWriter())
		_, ok := h.DataHubWriter().(*knowledgekit.DataHubClientWriter)
		assert.True(t, ok, "a resolved DataHub config selects the real client writer")
	})
}

func TestBuildDataHubWriter(t *testing.T) {
	t.Run("nil config yields the noop writer", func(t *testing.T) {
		w, err := buildDataHubWriter("nonexistent", nil)
		require.NoError(t, err)
		_, ok := w.(*knowledgekit.NoopDataHubWriter)
		assert.True(t, ok, "expected NoopDataHubWriter when connection not found")
	})

	t.Run("valid config yields the client writer", func(t *testing.T) {
		w, err := buildDataHubWriter("primary", &DataHubConfig{URL: "http://datahub:8080", Token: "test-token"})
		require.NoError(t, err)
		_, ok := w.(*knowledgekit.DataHubClientWriter)
		assert.True(t, ok, "expected DataHubClientWriter for a valid connection")
	})

	t.Run("empty url yields an error", func(t *testing.T) {
		_, err := buildDataHubWriter("primary", &DataHubConfig{URL: "", Token: ""})
		require.Error(t, err, "expected error for invalid datahub config")
	})
}

func TestNewFromInsightStore_InjectedStore(t *testing.T) {
	// The seam that lets callers inject their own insight store without a
	// database (apply disabled, so db/embedding are unused).
	store := knowledgekit.NewNoopStore()
	h, err := NewFromInsightStore(nil, store, nil, Config{ToolkitName: "default"})
	require.NoError(t, err)
	require.NotNil(t, h)
	assert.Equal(t, store, h.InsightStore(), "the injected store is exposed verbatim")
	assert.NotNil(t, h.Toolkit())
}

func TestNewFromInsightStore_ApplyRequiresDB(t *testing.T) {
	// Enabling apply without a database is a construction error: the changeset
	// and page stores are Postgres-backed, so a nil db would otherwise build
	// stores that only fail at query time.
	_, err := NewFromInsightStore(nil, knowledgekit.NewNoopStore(), nil, Config{
		ToolkitName:  "default",
		ApplyEnabled: true,
	})
	require.Error(t, err, "apply enabled with a nil db must fail construction")
}
