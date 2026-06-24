package knowledgepage

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_ListPagesReferencing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectQuery("FROM knowledge_page_entity_refs r.*JOIN portal_knowledge_pages.*r.asset_id = ").
		WithArgs("asset-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "slug", "title"}).
			AddRow("kp1", "fiscal", "Fiscal Calendar").
			AddRow("kp2", nil, "Revenue"))

	pages, err := store.ListPagesReferencing(context.Background(),
		EntityRef{TargetType: RefTargetAsset, AssetID: "asset-1"})
	require.NoError(t, err)
	require.Len(t, pages, 2)
	assert.Equal(t, "Fiscal Calendar", pages[0].Title)
	assert.Equal(t, "fiscal", pages[0].Slug)
	assert.Equal(t, "", pages[1].Slug) // NULL slug
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_ListPagesReferencing_Connection(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectQuery("connection_kind = .* AND r.connection_name = ").
		WithArgs("trino", "warehouse").
		WillReturnRows(sqlmock.NewRows([]string{"id", "slug", "title"}).AddRow("kp1", "p", "P"))

	pages, err := store.ListPagesReferencing(context.Background(),
		EntityRef{TargetType: RefTargetConnection, ConnectionKind: "trino", ConnectionName: "warehouse"})
	require.NoError(t, err)
	require.Len(t, pages, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReverseLookupFilter(t *testing.T) {
	cases := []struct {
		ref   EntityRef
		where string
		args  []any
	}{
		{EntityRef{TargetType: RefTargetAsset, AssetID: "a"}, "r.asset_id = $1", []any{"a"}},
		{EntityRef{TargetType: RefTargetPrompt, PromptID: "p"}, "r.prompt_id = $1", []any{"p"}},
		{EntityRef{TargetType: RefTargetCollection, CollectionID: "c"}, "r.collection_id = $1", []any{"c"}},
		{EntityRef{TargetType: RefTargetKnowledgePage, RefPageID: "kp"}, "r.ref_page_id = $1", []any{"kp"}},
		{EntityRef{TargetType: RefTargetConnection, ConnectionKind: "k", ConnectionName: "n"}, "r.connection_kind = $1 AND r.connection_name = $2", []any{"k", "n"}},
		{EntityRef{TargetType: RefTargetDataHub, EntityURN: "u"}, "r.entity_urn = $1", []any{"u"}},
		{EntityRef{TargetType: "bogus"}, "", nil},
	}
	for _, c := range cases {
		where, args := reverseLookupFilter(c.ref)
		assert.Equal(t, c.where, where, c.ref.TargetType)
		assert.Equal(t, c.args, args, c.ref.TargetType)
	}
}

func TestStore_ListPagesReferencing_UnknownTypeAndError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	// Unknown target type makes no query and returns no pages.
	pages, err := store.ListPagesReferencing(context.Background(), EntityRef{TargetType: "bogus"})
	require.NoError(t, err)
	assert.Nil(t, pages)

	// A query error propagates.
	mock.ExpectQuery("entity_urn = ").WithArgs("urn:li:dataset:x").WillReturnError(errBoom)
	_, err = store.ListPagesReferencing(context.Background(),
		EntityRef{TargetType: RefTargetDataHub, EntityURN: "urn:li:dataset:x"})
	require.Error(t, err)
}
