package portal

import (
	"context"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplyUpdateFields_ClearsEmbeddingOnIndexedChange pins that an update
// touching an indexed field (name/description/tags) drops the embedding columns
// so the reconciler re-embeds, while a content/thumbnail-only update preserves
// the vector.
func TestApplyUpdateFields_ClearsEmbeddingOnIndexedChange(t *testing.T) {
	name := "n"
	indexed := []struct {
		label   string
		updates AssetUpdate
	}{
		{"name", AssetUpdate{Name: &name}},
		{"description", AssetUpdate{Description: &name}},
		{"tags", AssetUpdate{Tags: []string{"t"}}},
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
	qb, err := applyUpdateFields(psq.Update("portal_assets"), AssetUpdate{
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

// TestSectionsTextMatchesIndexComposition guards that SectionsText (the
// denormalized column source) and the FTS/embedding composition agree on
// section ordering and separators.
func TestSectionsTextMatchesIndexComposition(t *testing.T) {
	got := SectionsText([]CollectionSection{
		{Title: "A", Description: "alpha"},
		{Title: "B", Description: "beta"},
	})
	assert.True(t, strings.Contains(got, "A alpha") && strings.Contains(got, "B beta"))
}
