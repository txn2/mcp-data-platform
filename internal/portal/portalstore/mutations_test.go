package portalstore

import (
	"context"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// TestApplyUpdateFields_ClearsEmbeddingOnIndexedChange pins that an update
// touching an indexed field (name/description/tags) drops the embedding columns
// so the reconciler re-embeds, while a content/thumbnail-only update preserves
// the vector.
func TestApplyUpdateFields_ClearsEmbeddingOnIndexedChange(t *testing.T) {
	name := "n"
	indexed := []struct {
		label   string
		updates portaldomain.AssetUpdate
	}{
		{"name", portaldomain.AssetUpdate{Name: &name}},
		{"description", portaldomain.AssetUpdate{Description: &name}},
		{"tags", portaldomain.AssetUpdate{Tags: []string{"t"}}},
	}
	for _, tc := range indexed {
		t.Run(tc.label, func(t *testing.T) {
			qb, err := applyUpdateFields(psq.Update("portal_assets"), tc.updates)
			require.NoError(t, err)
			sql, _, err := qb.ToSql()
			require.NoError(t, err)
			assert.Contains(t, sql, "embedding", "indexed-field update must clear the embedding")
			assert.Contains(t, sql, "embedding_text_hash")
		})
	}
}

func TestApplyUpdateFields_PreservesEmbeddingOnContentOnly(t *testing.T) {
	thumb := "thumb/key.png"
	qb, err := applyUpdateFields(psq.Update("portal_assets"), portaldomain.AssetUpdate{
		ContentType:    "text/csv",
		S3Key:          "new/key",
		SizeBytes:      99,
		HasContent:     true,
		ThumbnailS3Key: &thumb,
	})
	require.NoError(t, err)
	sql, _, err := qb.ToSql()
	require.NoError(t, err)
	assert.NotContains(t, sql, "embedding", "a content/thumbnail-only update must not touch the embedding")
}

// TestCollectionUpdate_ClearsEmbedding pins that the collection rename path drops
// the embedding (name + description feed CollectionIndexText).
func TestCollectionUpdate_ClearsEmbedding(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresCollectionStore(db)

	mock.ExpectExec("UPDATE portal_collections.*embedding = NULL").
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, store.Update(context.Background(), "c-1", "New Name", "desc"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSectionsTextMatchesIndexComposition guards that portaldomain.SectionsText (the
// denormalized column source) and the FTS/embedding composition agree on
// section ordering and separators.
func TestSectionsTextMatchesIndexComposition(t *testing.T) {
	got := portaldomain.SectionsText([]portaldomain.CollectionSection{
		{Title: "A", Description: "alpha"},
		{Title: "B", Description: "beta"},
	})
	assert.True(t, strings.Contains(got, "A alpha") && strings.Contains(got, "B beta"))
}

// TestApplyUpdateFields_MaxVersions covers the three states of the retention
// override: unset leaves the column alone, a value writes it, and a clear sets
// it back to NULL so the asset inherits the deployment default again (#1421).
func TestApplyUpdateFields_MaxVersions(t *testing.T) {
	name := "n"

	t.Run("unset leaves the column alone", func(t *testing.T) {
		qb, err := applyUpdateFields(psq.Update("portal_assets"), portaldomain.AssetUpdate{Name: &name})
		require.NoError(t, err)
		stmt, _, err := qb.ToSql()
		require.NoError(t, err)
		assert.NotContains(t, stmt, "max_versions")
	})

	t.Run("a value is written, zero included", func(t *testing.T) {
		for _, n := range []int{0, 1, 250} {
			keep := n
			qb, err := applyUpdateFields(psq.Update("portal_assets"),
				portaldomain.AssetUpdate{MaxVersions: &keep})
			require.NoError(t, err, "a retention change alone is a complete update")
			stmt, args, err := qb.ToSql()
			require.NoError(t, err)
			assert.Contains(t, stmt, "max_versions")
			assert.Contains(t, args, n)
		}
	})

	t.Run("a clear sets the column to NULL", func(t *testing.T) {
		qb, err := applyUpdateFields(psq.Update("portal_assets"),
			portaldomain.AssetUpdate{ClearMaxVersions: true})
		require.NoError(t, err)
		stmt, args, err := qb.ToSql()
		require.NoError(t, err)
		assert.Contains(t, stmt, "max_versions")
		assert.Equal(t, []any{nil}, args)
	})

	t.Run("a clear wins over a value", func(t *testing.T) {
		keep := 5
		qb, err := applyUpdateFields(psq.Update("portal_assets"),
			portaldomain.AssetUpdate{MaxVersions: &keep, ClearMaxVersions: true})
		require.NoError(t, err)
		_, args, err := qb.ToSql()
		require.NoError(t, err)
		assert.Equal(t, []any{nil}, args, "inheriting is the state a caller can always get back out of")
	})

	t.Run("a retention change leaves the embedding intact", func(t *testing.T) {
		keep := 10
		qb, err := applyUpdateFields(psq.Update("portal_assets"),
			portaldomain.AssetUpdate{MaxVersions: &keep})
		require.NoError(t, err)
		stmt, _, err := qb.ToSql()
		require.NoError(t, err)
		assert.NotContains(t, stmt, "embedding", "retention is not indexed text")
	})
}
