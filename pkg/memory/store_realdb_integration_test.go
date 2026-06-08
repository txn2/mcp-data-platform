//go:build integration

package memory

// Real-Postgres round-trip test for the memory store. The write path marshals
// slice/map fields to JSONB (nil tolerated as JSON null) and only binds the
// embedding columns when an embedding is present, so a minimal record with no
// embedding must insert and read back cleanly against the real schema.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

func TestMemoryStore_Insert_RealDB_RoundTrip(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	rec := Record{
		ID:        "mem_realdb_1",
		Content:   "The transactions table is partitioned by transaction_date.",
		Dimension: "knowledge",
		Category:  "business_context",
		Source:    "user",
		// EntityURNs/RelatedColumns/Metadata left nil (marshaled to JSON null/[]),
		// Embedding left nil (embedding columns omitted from the INSERT).
	}
	require.NoError(t, store.Insert(ctx, rec), "insert memory record with no embedding")

	got, err := store.Get(ctx, "mem_realdb_1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "mem_realdb_1", got.ID)
	assert.Equal(t, rec.Content, got.Content)
	assert.Equal(t, "knowledge", got.Dimension)
}
