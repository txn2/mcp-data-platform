//go:build integration

package callindex

// Real-Postgres round-trip test for the call-record embedding write path.
//
// indexjobs.TextHash returns a raw SHA-256, and raw digest bytes are not a
// UTF-8 string: PostgreSQL rejects a NUL outright and rejects any other invalid
// sequence on the encoding check. 000107 declared embedding_text_hash TEXT, so
// UpsertVectors failed on every write and the calls kind never indexed a single
// record (#1365). sqlmock cannot see that -- it rubber-stamps the parameter --
// so this exercises the write against the real column type.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

func TestCallIndexStore_UpsertVectors_RealDB_RoundTrip(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()

	records := callrecord.NewPostgresStore(db, callrecord.Config{})
	require.NoError(t, records.Insert(ctx, callrecord.Record{
		EventID:   "evt_callindex_realdb_1",
		Kind:      "sql",
		ToolName:  "trino_query",
		Statement: "SELECT 1",
		Purpose:   "prove the embedding write path survives a raw digest",
		Success:   true,
	}))

	var id string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM call_records WHERE event_id = $1`, "evt_callindex_realdb_1").Scan(&id))

	store := NewStore(db)

	text, err := store.GetText(ctx, id)
	require.NoError(t, err)
	require.NotEmpty(t, text)

	// The hash the worker actually writes: 32 raw octets, which is what a
	// text column cannot hold. Any digest works as a regression probe --
	// this one is derived from the record's own embed text, as planVectors
	// derives it.
	hash := indexjobs.TextHash(text)
	require.Len(t, hash, 32)

	require.NoError(t, store.UpsertVectors(ctx, id, []indexjobs.Vector{{
		ItemID:    id,
		Embedding: make([]float32, 768),
		Model:     "nomic-embed-text",
		TextHash:  hash,
	}}), "upsert a raw SHA-256 into embedding_text_hash")

	got, err := store.ListVectors(ctx, id)
	require.NoError(t, err)
	require.Contains(t, got, id)
	// The digest must survive the round trip byte for byte: the worker
	// dedups on bytes.Equal against exactly this value, so a lossy column
	// would re-embed the record on every pass even once the write landed.
	assert.Equal(t, hash, got[id].TextHash)
	assert.Equal(t, "nomic-embed-text", got[id].Model)
	assert.Equal(t, 768, got[id].Dim)

	// With a vector persisted the record is no longer a gap for the model
	// that produced it, which is what full coverage is made of.
	gaps, err := store.FindGaps(ctx, "nomic-embed-text")
	require.NoError(t, err)
	assert.NotContains(t, gaps, id)

	indexed, expected, err := store.Coverage(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, indexed)
	assert.Equal(t, 1, expected)
}
