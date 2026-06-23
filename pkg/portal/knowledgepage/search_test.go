package knowledgepage

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearch_Lexical(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresStore{db: db}

	cols := append([]string{
		"id", "slug", "title", "summary", "body", "tags",
		"created_by", "created_email", "updated_by", "current_version",
		"created_at", "updated_at", "deleted_at",
	}, "lex_rank")
	mock.ExpectQuery("FROM portal_knowledge_pages").
		WithArgs("revenue").
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow("kp1", "", "Revenue model", "", "how revenue works", []byte(`[]`),
				"", "", "", 1, time.Now(), time.Now(), nil, 0.42))

	out, err := store.Search(context.Background(), SearchQuery{QueryText: "revenue", Limit: 10})
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "kp1", out[0].Page.ID)
	assert.InDelta(t, 0.42, out[0].Score, 0.0001)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSearch_Hybrid(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresStore{db: db}

	cols := append([]string{
		"id", "slug", "title", "summary", "body", "tags",
		"created_by", "created_email", "updated_by", "current_version",
		"created_at", "updated_at", "deleted_at",
	}, "vec_score", "lex_match")
	mock.ExpectQuery("UNION ALL").
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow("kp1", "", "Revenue model", "", "body", []byte(`[]`),
				"", "", "", 1, time.Now(), time.Now(), nil, 0.9, true))

	out, err := store.Search(context.Background(), SearchQuery{
		QueryText: "revenue", Embedding: []float32{0.1, 0.2, 0.3}, Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "kp1", out[0].Page.ID)
	// Hybrid fused score combines semantic + lexical; must be in (0,1].
	assert.Greater(t, out[0].Score, 0.0)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSearch_Errors(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := &postgresStore{db: db}

	mock.ExpectQuery("FROM portal_knowledge_pages").WillReturnError(errBoom)
	_, err = store.Search(context.Background(), SearchQuery{QueryText: "x"})
	assert.Error(t, err)

	mock.ExpectQuery("UNION ALL").WillReturnError(errBoom)
	_, err = store.Search(context.Background(), SearchQuery{QueryText: "x", Embedding: []float32{0.1}})
	assert.Error(t, err)
}

func TestSearchQuery_EffectiveLimit(t *testing.T) {
	assert.Equal(t, DefaultSearchLimit, SearchQuery{}.EffectiveLimit())
	assert.Equal(t, 5, SearchQuery{Limit: 5}.EffectiveLimit())
}
