//go:build integration

package resource

// Real-Postgres round-trip test for the resource store. resource.Insert binds
// pq.Array(r.Tags) into the `tags TEXT[] NOT NULL` column unconditionally, so a
// Resource with a nil Tags slice (the Go zero value) would bind SQL NULL and be
// rejected with error 23502 — the exact defect that shipped prompt creation
// broken. sqlmock cannot catch this; this test does.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

func TestResourceStore_Insert_RealDB_NilTags(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	r := Resource{
		ID:          "res_realdb_1",
		Scope:       ScopeGlobal,
		Category:    "runbooks",
		Filename:    "etl.md",
		DisplayName: "ETL Runbook",
		Description: "Round-trip test resource.",
		MIMEType:    "text/markdown",
		SizeBytes:   123,
		S3Key:       "resources/res_realdb_1/etl.md",
		URI:         "mcp://global/runbooks/etl.md",
		UploaderSub: "sub-1",
		// Tags intentionally nil — pq.Array(nil) would bind NULL into tags TEXT[] NOT NULL.
	}
	require.NoError(t, store.Insert(ctx, r), "insert resource with nil tags")

	got, err := store.Get(ctx, "res_realdb_1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "res_realdb_1", got.ID)
	assert.Equal(t, ScopeGlobal, got.Scope)
	assert.NotNil(t, got.Tags)
	assert.Empty(t, got.Tags)
}

func TestResourceStore_Insert_RealDB_WithTags(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	r := Resource{
		ID: "res_realdb_2", Scope: ScopeGlobal, Category: "runbooks",
		Filename: "f.md", DisplayName: "F", Description: "d", MIMEType: "text/markdown",
		SizeBytes: 1, S3Key: "k", URI: "mcp://global/runbooks/f.md", UploaderSub: "sub-2",
		Tags: []string{"a", "b"},
	}
	require.NoError(t, store.Insert(ctx, r))
	got, err := store.Get(ctx, "res_realdb_2")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.ElementsMatch(t, []string{"a", "b"}, got.Tags)
}
