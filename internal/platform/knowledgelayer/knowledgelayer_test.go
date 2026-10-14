package knowledgelayer

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/platform/catalogstore"
	"github.com/txn2/mcp-data-platform/internal/platform/knowledgepageindex"
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
	assert.Nil(t, h.Catalog())
}

func TestNew_InsightStoreSelection(t *testing.T) {
	t.Run("a supplied memory store is the one used", func(t *testing.T) {
		h, err := New(dummyDB(t), stubMemoryStore{}, nil, Config{ToolkitName: "default"})
		require.NoError(t, err)
		require.NotNil(t, h)
		require.NotNil(t, h.InsightStore())
		_, ok := h.InsightStore().(knowledgekit.SearchableInsightStore)
		assert.True(t, ok, "memory-backed adapter is a SearchableInsightStore")
	})

	// A caller with no memory store gets one built over the database rather
	// than a store for knowledge_insights, which migration 000031 dropped
	// (#1517). Turning the memory toolkit off must not take knowledge capture
	// down with it.
	t.Run("no memory store still selects the memory-backed adapter", func(t *testing.T) {
		h, err := New(dummyDB(t), nil, nil, Config{ToolkitName: "default"})
		require.NoError(t, err)
		require.NotNil(t, h)
		require.NotNil(t, h.InsightStore())
		_, ok := h.InsightStore().(knowledgekit.SearchableInsightStore)
		assert.True(t, ok, "the memory-backed adapter is a SearchableInsightStore")
	})
}

func TestNew_ApplyGating(t *testing.T) {
	t.Run("apply disabled leaves changeset store and catalog nil", func(t *testing.T) {
		h, err := New(dummyDB(t), nil, nil, Config{ToolkitName: "default", ApplyEnabled: false, Catalog: catalogstore.New(dummyDB(t))})
		require.NoError(t, err)
		require.NotNil(t, h)
		assert.NotNil(t, h.Toolkit(), "toolkit is always built")
		assert.Nil(t, h.ChangesetStore(), "no changeset store when apply is disabled")
		assert.Nil(t, h.Catalog(), "no catalog when apply is disabled")
	})

	t.Run("apply enabled without a catalog builds the changeset store", func(t *testing.T) {
		h, err := New(dummyDB(t), nil, nil, Config{ToolkitName: "default", ApplyEnabled: true})
		require.NoError(t, err)
		require.NotNil(t, h)
		assert.NotNil(t, h.ChangesetStore(), "changeset store built when apply is enabled")
		assert.Nil(t, h.Catalog())
	})

	t.Run("apply enabled with a catalog hands it to the toolkit", func(t *testing.T) {
		store := catalogstore.New(dummyDB(t))
		h, err := New(dummyDB(t), nil, nil, Config{ToolkitName: "default", ApplyEnabled: true, Catalog: store})
		require.NoError(t, err)
		require.NotNil(t, h)
		assert.Same(t, store, h.Catalog())
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

// TestPageIndexProducer covers the write-path index-job seam the queue binds
// (#1256): the apply path's page writer carries a producer for the
// knowledge-pages kind, and there is nothing to bind when apply is off (no page
// writer) or when no layer was built at all.
func TestPageIndexProducer(t *testing.T) {
	h, err := New(dummyDB(t), nil, nil, Config{ToolkitName: "default", ApplyEnabled: true})
	require.NoError(t, err)
	require.NotNil(t, h.PageIndexProducer())
	assert.Equal(t, knowledgepageindex.SourceKind, h.PageIndexProducer().Kind())

	off, err := New(dummyDB(t), nil, nil, Config{ToolkitName: "default"})
	require.NoError(t, err)
	assert.Nil(t, off.PageIndexProducer(), "no page writer when apply is disabled")

	var nilHandle *Handle
	assert.Nil(t, nilHandle.PageIndexProducer())
}
